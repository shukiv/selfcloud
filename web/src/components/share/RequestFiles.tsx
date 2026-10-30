// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Input, Switch } from "antd";
import { useLang, useT, translate, translatePlural, type Lang } from "../../lib/i18n";
import { ApiError } from "../../lib/apiError";
import { useStore } from "../../lib/store";
import { passwordMinLength } from "../../lib/brand";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import type { Node, Role } from "../../lib/files/types";
import { createDropLink, deleteShareLink, listShareLinks, resetShareLink, updateDropLink } from "../../lib/sharing/api";
import { ownDrop, type ShareLink } from "../../lib/sharing/links";
import { dateInputValue, maxExpiryDays } from "../../lib/sharing/limits";
import {
  dropCreateBody,
  dropDateLimits,
  dropDefaultDate,
  dropFormOf,
  dropPatchBody,
  dropProblem,
  dropState,
  type DropForm,
  type DropProblem,
} from "../../lib/sharing/drop";

// RequestFiles is the Request files part of the share dialog, for a directory. A drop link lets anyone send files into
// the directory and see nothing else. The parent shows it only where the account allows links that anyone can open and
// the caller manages the directory (canRequestFiles). The server checks every change again. The secret of the link is
// shown once, after it is made or reset.
export interface RequestFilesProps {
  node: Node;
  cap: Role | null;
  onchanged: () => void;
}

function blankForm(now: Date, maxDays: number): DropForm {
  return {
    date: dropDefaultDate(now, maxDays),
    password: "",
    passwordChanged: false,
    title: "",
    message: "",
    askName: false,
    notify: false,
  };
}

// dropErrorText says what a refused change means, by its code and reason. The server's English text is never shown.
function dropErrorText(err: unknown, lang: Lang, maxDays: number): string {
  if (err instanceof ApiError) {
    const reason = err.details.reason;
    if (err.code === "forbidden" && reason === "public_links_off") return translate(lang, "share.err.publicOff");
    if (err.code === "forbidden" && reason === "links_off") return translate(lang, "share.err.linksOff");
    if (err.code === "forbidden" && reason === "manager_required") return translate(lang, "share.err.anyoneManager");
    if (err.code === "forbidden") return translate(lang, "share.err.forbidden");
    if (err.code === "conflict" && reason === "exists") return translate(lang, "share.err.exists");
    if (err.code === "conflict" && reason === "members_only") return translate(lang, "share.err.linkMembersOnly");
    if (err.code === "conflict") return translate(lang, "share.err.membersOnly");
    if (err.code === "not_found") return translate(lang, "share.err.gone");
    if (err.code === "invalid_request" && err.details.max_expiry_seconds !== undefined) {
      return translatePlural(lang, "share.err.maxExpiry", maxDays);
    }
    if (err.code === "invalid_request") return translate(lang, "share.err.invalid");
    if (err.code === "rate_limited") return translate(lang, "share.err.limited");
    if (err.status === 0) return translate(lang, "error.network");
  }
  return translate(lang, "error.generic");
}

export default function RequestFiles({ node, cap, onchanged }: RequestFilesProps) {
  const { t } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const maxDays = useStore(maxExpiryDays);
  const minPassword = useStore(passwordMinLength);
  const key = queryKey("links", node.id);

  const linksQ = useQuery({
    queryKey: key,
    queryFn: () => listShareLinks(node.id),
    refetchOnMount: "always",
  });
  const drop: ShareLink | null = linksQ.data ? ownDrop(linksQ.data.items, node.id) : null;
  const manager = cap === "manager";
  const limits = dropDateLimits(new Date(), maxDays);

  // The form follows the link. It is set from the link when the link arrives or changes, and by the person's typing
  // in between. A new link (after turning on) starts from the defaults.
  const [form, setForm] = useState<DropForm>(() => blankForm(new Date(), maxDays));
  const [formFor, setFormFor] = useState<string | null>(null);
  if (linksQ.data && formFor !== (drop?.id ?? "")) {
    setFormFor(drop?.id ?? "");
    setForm(drop ? dropFormOf(drop, (iso) => dateInputValue(new Date(iso))) : blankForm(new Date(), maxDays));
  }

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [madeURL, setMadeURL] = useState<string | null>(null);

  const problemText = (p: DropProblem): string => {
    switch (p) {
      case "date":
        return t("drop.err.date");
      case "title_long":
        return t("drop.err.title");
      case "message_long":
        return t("drop.err.message");
      case "password_short":
        return t("share.err.passwordShort", { count: minPassword });
      case "password_long":
        return t("share.err.passwordLong");
    }
  };

  // run performs one change and shows its refusal. Only one change runs at a time. A change that ends after the session
  // has changed is dropped.
  async function run(change: () => Promise<void>): Promise<void> {
    if (busy) return;
    const stale = sessionChanged();
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await change();
      if (stale()) return;
      await qc.invalidateQueries({ queryKey: key });
      onchanged();
    } catch (err) {
      if (stale()) return;
      setError(dropErrorText(err, lang, maxDays));
    } finally {
      setBusy(false);
    }
  }

  // turnOn makes the drop link with the form's values.
  async function turnOn(): Promise<void> {
    const now = new Date();
    const problem = dropProblem(form, now, maxDays, minPassword);
    if (problem !== null) {
      setError(problemText(problem));
      return;
    }
    const body = dropCreateBody(form, now, maxDays, minPassword);
    if (body === null) {
      setError(t("drop.err.date"));
      return;
    }
    await run(async () => {
      const made = await createDropLink(node.id, body);
      setMadeURL(made.url);
      setNotice(t("drop.notice.on"));
    });
  }

  // turnOff removes the drop link at once. Its URL stops working, and files already sent are kept.
  async function turnOff(): Promise<void> {
    if (!drop) return;
    const existing = drop;
    await run(async () => {
      await deleteShareLink(existing.id);
      setMadeURL(null);
      setForm(blankForm(new Date(), maxDays));
      setNotice(t("drop.notice.off"));
    });
  }

  // save changes the drop link's end, text, switches and password.
  async function save(): Promise<void> {
    if (!drop) return;
    const now = new Date();
    const problem = dropProblem(form, now, maxDays, minPassword);
    if (problem !== null) {
      setError(problemText(problem));
      return;
    }
    const body = dropPatchBody(form, now, maxDays, minPassword);
    if (body === null) {
      setError(t("drop.err.date"));
      return;
    }
    const existing = drop;
    await run(async () => {
      await updateDropLink(existing.id, body);
      setForm((f) => ({ ...f, password: "", passwordChanged: false }));
      setNotice(t("share.general.saved"));
    });
  }

  // renew gives the drop link a new secret. The old URL stops working at once.
  async function renew(): Promise<void> {
    if (!drop) return;
    const existing = drop;
    await run(async () => {
      const made = await resetShareLink(existing.id);
      setMadeURL(made.url);
      setNotice(t("share.general.renewed"));
    });
  }

  async function copy(): Promise<void> {
    if (madeURL === null) return;
    try {
      await navigator.clipboard.writeText(madeURL);
      setNotice(t("share.general.copied"));
    } catch {
      setNotice(t("share.general.copyFailed"));
    }
  }

  const onToggle = (on: boolean) => {
    if (busy || !manager) return;
    if (on) void turnOn();
    else void turnOff();
  };

  return (
    <section aria-labelledby="share-request-files" className="request" style={{ display: "grid", gap: 12 }}>
      <h3 id="share-request-files" style={{ margin: 0, fontSize: 14, fontWeight: 600 }}>
        {t("drop.title")}
      </h3>
      <p className="hint" style={{ margin: 0, fontSize: 13 }}>{t("drop.hint")}</p>
      {error && <Alert type="error" showIcon role="alert" message={error} />}
      {notice && (
        <p className="hint" role="status" style={{ margin: 0, fontSize: 13 }}>
          {notice}
        </p>
      )}
      {linksQ.isError && !linksQ.data ? (
        // A failed first load shows the failure and no form: the switch and the form would act on a link not read.
        <Alert type="error" showIcon role="alert" message={dropErrorText(linksQ.error, lang, maxDays)} />
      ) : linksQ.isPending ? (
        <p role="status" style={{ margin: 0, color: "var(--ant-color-text-secondary)" }}>
          {t("share.loading")}
        </p>
      ) : (
        <>
          <label style={{ display: "flex", alignItems: "center", gap: 8, minHeight: 44 }}>
            <Switch checked={drop !== null} disabled={busy || !manager} onChange={onToggle} />
            <span>{t("drop.toggle")}</span>
          </label>

          {drop !== null && (
            <form
              style={{ display: "grid", gap: 12 }}
              onSubmit={(e) => {
                e.preventDefault();
                void save();
              }}
            >
              {dropState(drop) === "paused" && (
                <p style={{ margin: 0 }}>
                  <strong>{t("share.general.paused")}</strong> <span className="hint">{t("share.general.pausedHint")}</span>
                </p>
              )}
              <div style={{ display: "grid", gap: 8 }}>
                <label htmlFor="drop-expires">{t("drop.ends")}</label>
                <Input
                  id="drop-expires"
                  type="date"
                  value={form.date}
                  onChange={(e) => setForm((f) => ({ ...f, date: e.target.value }))}
                  min={limits.earliest}
                  max={limits.latest}
                  required
                  disabled={busy || !manager}
                />
              </div>
              <div style={{ display: "grid", gap: 8 }}>
                <label htmlFor="drop-title">{t("drop.titleField")}</label>
                <Input
                  id="drop-title"
                  value={form.title}
                  maxLength={400}
                  onChange={(e) => setForm((f) => ({ ...f, title: e.target.value }))}
                  disabled={busy || !manager}
                />
                <p className="hint" style={{ margin: 0, fontSize: 13 }}>{t("drop.titleHint")}</p>
              </div>
              <div style={{ display: "grid", gap: 8 }}>
                <label htmlFor="drop-message">{t("drop.message")}</label>
                <Input.TextArea
                  id="drop-message"
                  rows={3}
                  value={form.message}
                  onChange={(e) => setForm((f) => ({ ...f, message: e.target.value }))}
                  disabled={busy || !manager}
                />
              </div>
              <label style={{ display: "flex", alignItems: "center", gap: 8, minHeight: 44 }}>
                <Switch
                  checked={form.askName}
                  disabled={busy || !manager}
                  onChange={(on) => setForm((f) => ({ ...f, askName: on }))}
                />
                <span>{t("drop.askName")}</span>
              </label>
              <label style={{ display: "flex", alignItems: "center", gap: 8, minHeight: 44 }}>
                <Switch
                  checked={form.notify}
                  disabled={busy || !manager}
                  onChange={(on) => setForm((f) => ({ ...f, notify: on }))}
                />
                <span>{t("drop.notify")}</span>
              </label>
              <div style={{ display: "grid", gap: 8 }}>
                <p className="hint" style={{ margin: 0, fontSize: 13 }}>
                  {drop.password_set ? t("share.general.passwordSet") : t("share.general.noPassword")}
                </p>
                <label htmlFor="drop-password">
                  {t(drop.password_set ? "share.general.passwordChange" : "share.general.passwordAdd")}
                </label>
                <Input.Password
                  id="drop-password"
                  autoComplete="new-password"
                  value={form.password}
                  disabled={busy || !manager}
                  onChange={(e) => setForm((f) => ({ ...f, password: e.target.value, passwordChanged: true }))}
                />
                <p className="hint" style={{ margin: 0, fontSize: 13 }}>{t("share.general.passwordHint", { count: minPassword })}</p>
              </div>
              {manager && (
                <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
                  <Button disabled={busy} onClick={() => void renew()}>
                    {t("share.general.new")}
                  </Button>
                  <Button type="primary" htmlType="submit" disabled={busy}>
                    {t("drop.save")}
                  </Button>
                </div>
              )}
            </form>
          )}

          {madeURL !== null && (
            <div className="made" style={{ display: "grid", gap: 8 }}>
              <p className="hint" style={{ margin: 0, fontSize: 13 }}>{t("share.general.copyNow")}</p>
              <div style={{ display: "flex", gap: 8 }}>
                <Input readOnly value={madeURL} aria-label={t("share.general.urlLabel")} onFocus={(e) => e.currentTarget.select()} />
                <Button type="primary" onClick={() => void copy()}>
                  {t("share.general.copy")}
                </Button>
              </div>
            </div>
          )}
        </>
      )}
    </section>
  );
}
