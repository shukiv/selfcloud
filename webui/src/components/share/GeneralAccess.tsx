// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Input, Radio, Select } from "antd";
import { useLang, useT, translate, translatePlural, type Lang } from "../../lib/i18n";
import { ApiError } from "../../lib/apiError";
import { session } from "../../lib/session";
import { useStore } from "../../lib/store";
import { passwordMinLength } from "../../lib/brand";
import { go } from "../../lib/router";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import type { Node, Role } from "../../lib/files/types";
import {
  createShareLink,
  deleteShareLink,
  listShareLinks,
  resetShareLink,
  updateShareLink,
} from "../../lib/sharing/api";
import { inheritedLinks, linkRoleOptions, ownLink, type ShareLink } from "../../lib/sharing/links";
import { dateInputValue, expiryFromInputCapped, expiryLimit, maxExpiryDays } from "../../lib/sharing/limits";
import { useNativeChange } from "./useNativeChange";
import {
  anyoneExpiresAt,
  defaultAnyoneEnd,
  latestAnyoneEnd,
  passwordProblem,
} from "../../lib/sharing/anyone";

// GeneralAccess is the "General access" part of the share dialog: whether the item has a link that the account's
// members can open (or anyone, where the account allows it), and the link's role, end date and secret. The server
// checks every change; this only shows what the caller may do. A link never gives manager. The secret is shown once:
// the URL of a link made or reset just now is kept in this component only, and never stored or sent back.
type Choice = "restricted" | "organisation" | "anyone";

// linkErrorText says what a refused change means, by its code and reason. The server's English text is never shown.
export function linkErrorText(err: unknown, lang: Lang, maxDays: number): string {
  if (err instanceof ApiError) {
    const reason = err.details.reason;
    if (err.code === "forbidden" && reason === "links_off") return translate(lang, "share.err.linksOff");
    if (err.code === "forbidden" && reason === "public_links_off") return translate(lang, "share.err.publicOff");
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

export interface GeneralAccessProps {
  node: Node;
  cap: Role | null;
  onchanged: () => void;
  onopen: () => void;
}

export default function GeneralAccess({ node, cap, onchanged, onopen }: GeneralAccessProps) {
  const { t } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const me = useStore(session);
  const maxDays = useStore(maxExpiryDays);
  const minPassword = useStore(passwordMinLength);
  const key = queryKey("links", node.id);

  const linksQ = useQuery({
    queryKey: key,
    queryFn: () => listShareLinks(node.id),
    refetchOnMount: "always",
  });
  const links: ShareLink[] = linksQ.data?.items ?? [];

  const [choice, setChoice] = useState<Choice>("restricted");
  const [newRole, setNewRole] = useState<Role>("viewer");
  const [newExpires, setNewExpires] = useState("");
  const [anyoneDate, setAnyoneDate] = useState(() => dateInputValue(defaultAnyoneEnd(new Date(), maxDays)));
  const [anyonePassword, setAnyonePassword] = useState("");
  const [editPassword, setEditPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  // madeURL is the URL of a link made or reset just now. It is shown once, so the person copies it now.
  const [madeURL, setMadeURL] = useState<string | null>(null);
  const chosenFirst = useRef(false);

  const own = ownLink(links, node.id);
  const above = inheritedLinks(links, node.id);
  const roles = linkRoleOptions(cap);
  const canChange = cap !== null;
  const account = me?.account?.name ?? "";
  const latest = expiryLimit(maxDays, new Date());
  const latestDate = latest ? dateInputValue(latest) : undefined;
  const earliestDate = dateInputValue(new Date(Date.now() + 24 * 60 * 60 * 1000));
  // An anyone link is offered only where the account allows it and the caller manages the item.
  const canAnyone = me?.publicLinks === true && cap === "manager";
  const anyoneLatest = dateInputValue(latestAnyoneEnd(new Date(), maxDays));
  const anyoneEarliest = dateInputValue(new Date(Date.now() + 24 * 60 * 60 * 1000));

  // The first answer sets the choice the item already has. Later answers leave the person's choice alone.
  useEffect(() => {
    if (!linksQ.data || chosenFirst.current) return;
    chosenFirst.current = true;
    const mine = ownLink(linksQ.data.items, node.id);
    setChoice(mine ? (mine.audience === "anyone" ? "anyone" : "organisation") : "restricted");
  }, [linksQ.data, node.id]);

  // A session change clears the secret shown here: it belongs to the person who made the link.
  useEffect(() => {
    setMadeURL(null);
  }, [me?.csrf]);

  const refresh = () => qc.invalidateQueries({ queryKey: key });

  // The two date inputs that change a link commit on the native change event, as Svelte does, not on each keystroke.
  const anyoneEndsRef = useNativeChange<HTMLDivElement>((v) => void changeAnyoneEnds(v));
  const expiresRef = useNativeChange<HTMLDivElement>((v) => void changeExpires(v));

  // run performs one change and shows its refusal. Only one change runs at a time. A change that finishes after the
  // session has ended is dropped: it was made for the person who has gone.
  async function run(change: () => Promise<void>): Promise<void> {
    if (busy) return;
    const stale = sessionChanged();
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await change();
      if (stale()) return;
      await refresh();
      onchanged();
    } catch (err) {
      if (stale()) return;
      setError(linkErrorText(err, lang, maxDays));
    } finally {
      setBusy(false);
    }
  }

  // choose switches between Restricted and the account's link. Restricted removes the link, at once; the organisation
  // and anyone choices show the form that makes it. A link that exists keeps its secret when its audience changes.
  async function choose(next: Choice): Promise<void> {
    if (busy || !canChange) return;
    if (next === "organisation" || next === "anyone") {
      setChoice(next);
      const existing = own;
      if (existing && existing.audience !== next) {
        if (next === "anyone" && !canAnyone) return;
        await run(async () => {
          await updateShareLink(existing.id, { audience: next });
          setNotice(t("share.general.saved"));
        });
      }
      return;
    }
    const existing = own;
    if (!existing) {
      setChoice("restricted");
      return;
    }
    await run(async () => {
      await deleteShareLink(existing.id);
      setChoice("restricted");
      setMadeURL(null);
      setNotice(t("share.general.removed"));
    });
  }

  async function create(e: FormEvent): Promise<void> {
    e.preventDefault();
    if (choice === "anyone") return createAnyone();
    const expires = newExpires === "" ? null : expiryFromInputCapped(newExpires, new Date(), maxDays);
    if (expires === null && newExpires !== "") {
      setError(t("share.err.invalid"));
      return;
    }
    await run(async () => {
      const made = await createShareLink(node.id, {
        audience: "organisation",
        role: newRole,
        ...(expires ? { expires_at: expires } : {}),
      });
      setMadeURL(made.url);
      setNotice(t("share.general.created"));
    });
  }

  // createAnyone makes the link that anyone can open. The end date is required; the server would take none.
  async function createAnyone(): Promise<void> {
    const at = anyoneExpiresAt(anyoneDate, new Date(), maxDays);
    if (at === null) {
      setError(t("share.err.date"));
      return;
    }
    const problem = passwordProblem(anyonePassword, minPassword);
    if (problem !== null) {
      setError(t(problem === "short" ? "share.err.passwordShort" : "share.err.passwordLong", { count: minPassword }));
      return;
    }
    const password = anyonePassword;
    await run(async () => {
      const made = await createShareLink(node.id, {
        audience: "anyone",
        expires_at: at,
        ...(password !== "" ? { password } : {}),
      });
      setAnyonePassword("");
      setMadeURL(made.url);
      setNotice(t("share.general.created"));
    });
  }

  // changeAnyoneEnds moves the end date of an anyone link. Clearing it is not offered.
  async function changeAnyoneEnds(value: string): Promise<void> {
    const existing = own;
    if (!existing) return;
    const at = anyoneExpiresAt(value, new Date(), maxDays);
    if (at === null) {
      setError(t("share.err.date"));
      return;
    }
    await run(async () => {
      await updateShareLink(existing.id, { expires_at: at });
      setNotice(t("share.general.saved"));
    });
  }

  // changeAnyonePassword sets a new password on an anyone link, or removes it when given "". The password is sent
  // once and never kept after the change.
  async function changeAnyonePassword(password: string): Promise<void> {
    const existing = own;
    if (!existing) return;
    const problem = password === "" ? null : passwordProblem(password, minPassword);
    if (problem !== null) {
      setError(t(problem === "short" ? "share.err.passwordShort" : "share.err.passwordLong", { count: minPassword }));
      return;
    }
    await run(async () => {
      await updateShareLink(existing.id, { password });
      setEditPassword("");
      setNotice(t("share.general.saved"));
    });
  }

  async function changeRole(role: Role): Promise<void> {
    const existing = own;
    if (!existing) return;
    await run(async () => {
      await updateShareLink(existing.id, { role });
      setNotice(t("share.general.saved"));
    });
  }

  async function changeExpires(value: string): Promise<void> {
    const existing = own;
    if (!existing) return;
    const expires = value === "" ? null : expiryFromInputCapped(value, new Date(), maxDays);
    if (value !== "" && expires === null) {
      setError(t("share.err.invalid"));
      return;
    }
    await run(async () => {
      await updateShareLink(existing.id, { expires_at: expires });
      setNotice(t("share.general.saved"));
    });
  }

  // renew gives the link a new secret. The old URL stops working at once.
  async function renew(): Promise<void> {
    const existing = own;
    if (!existing) return;
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

  const openNode = (nodeID: string) => {
    go(`/n/${nodeID}`);
    onopen();
  };

  const roleSelect = (id: string, value: Role, onChange: (r: Role) => void, disabled: boolean) => (
    <Select
      id={id}
      value={value}
      virtual={false}
      disabled={disabled}
      onChange={(v: Role) => onChange(v)}
      options={roles.map((r) => ({ value: r, label: t(`role.${r}`) }))}
      style={{ width: "100%" }}
    />
  );

  return (
    <section aria-labelledby="share-general" className="general" style={{ display: "grid", gap: 12 }}>
      <h3 id="share-general" style={{ margin: 0, fontSize: 14, fontWeight: 600 }}>
        {t("share.general")}
      </h3>
      {error && <Alert type="error" showIcon role="alert" message={error} />}
      {notice && (
        <p className="hint" role="status" style={{ margin: 0, fontSize: 13 }}>
          {notice}
        </p>
      )}
      {linksQ.isError && !linksQ.data ? (
        // A failed first load shows the failure and no choices: a choice made on an unknown link would not be saved as
        // the person thinks. A failed refetch of a list already shown keeps the list.
        <Alert type="error" showIcon role="alert" message={linkErrorText(linksQ.error, lang, maxDays)} />
      ) : linksQ.isPending ? (
        <p role="status" style={{ margin: 0, color: "var(--ant-color-text-secondary)" }}>
          {t("share.loading")}
        </p>
      ) : (
        <>
          <Radio.Group
            name="share-general-access"
            value={choice}
            disabled={busy || !canChange}
            onChange={(e) => void choose(e.target.value as Choice)}
            style={{ display: "grid", gap: 8 }}
          >
            <Radio value="restricted">{t("share.general.restricted")}</Radio>
            <Radio value="organisation">{t("share.general.organisation", { account })}</Radio>
            {(canAnyone || own?.audience === "anyone") && (
              <Radio value="anyone" disabled={!canAnyone && own?.audience !== "anyone"}>
                {t("share.general.anyone")}
              </Radio>
            )}
          </Radio.Group>
          {choice === "anyone" && <p className="hint" style={{ margin: 0, fontSize: 13 }}>{t("share.general.anyoneHint")}</p>}

          {own && own.audience === "anyone" ? (
            <div style={{ display: "grid", gap: 12 }}>
              {own.paused && (
                <p style={{ margin: 0 }}>
                  <strong>{t("share.general.paused")}</strong> <span className="hint">{t("share.general.pausedHint")}</span>
                </p>
              )}
              <p className="hint" style={{ margin: 0, fontSize: 13 }}>{t("share.general.viewerOnly")}</p>
              <div ref={anyoneEndsRef} style={{ display: "grid", gap: 8 }}>
                <label htmlFor="share-general-expires">{t("share.general.endsRequired")}</label>
                <Input
                  key={`anyone-${own.expires_at ?? ""}`}
                  id="share-general-expires"
                  type="date"
                  defaultValue={own.expires_at ? dateInputValue(new Date(own.expires_at)) : ""}
                  min={anyoneEarliest}
                  max={anyoneLatest}
                  disabled={busy || !canChange}
                />
              </div>
              <div style={{ display: "grid", gap: 8 }}>
                <p className="hint" style={{ margin: 0, fontSize: 13 }}>
                  {own.password_set ? t("share.general.passwordSet") : t("share.general.noPassword")}
                </p>
                <label htmlFor="share-general-password-edit">
                  {t(own.password_set ? "share.general.passwordChange" : "share.general.passwordAdd")}
                </label>
                <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
                  <Input.Password
                    id="share-general-password-edit"
                    autoComplete="new-password"
                    value={editPassword}
                    onChange={(e) => setEditPassword(e.target.value)}
                    disabled={busy || !canChange}
                    style={{ flex: "1 1 200px" }}
                  />
                  <Button disabled={busy || !canChange || editPassword === ""} onClick={() => void changeAnyonePassword(editPassword)}>
                    {t("share.general.passwordSave")}
                  </Button>
                  {own.password_set && (
                    <Button disabled={busy || !canChange} onClick={() => void changeAnyonePassword("")}>
                      {t("share.general.passwordRemove")}
                    </Button>
                  )}
                </div>
                <p className="hint" style={{ margin: 0, fontSize: 13 }}>{t("share.general.passwordHint", { count: minPassword })}</p>
              </div>
              {canChange && (
                <div style={{ display: "flex", justifyContent: "flex-end" }}>
                  <Button disabled={busy} onClick={() => void renew()}>
                    {t("share.general.new")}
                  </Button>
                </div>
              )}
            </div>
          ) : own ? (
            <div style={{ display: "grid", gap: 12 }}>
              {own.paused && (
                <p style={{ margin: 0 }}>
                  <strong>{t("share.general.paused")}</strong> <span className="hint">{t("share.general.pausedHint")}</span>
                </p>
              )}
              <div style={{ display: "grid", gap: 12, gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))" }}>
                <div style={{ display: "grid", gap: 8 }}>
                  <label htmlFor="share-general-role">{t("share.general.role")}</label>
                  {roleSelect("share-general-role", own.role as Role, (r) => void changeRole(r), busy || !canChange)}
                </div>
                <div ref={expiresRef} style={{ display: "grid", gap: 8 }}>
                  <label htmlFor="share-general-expires">{t("share.general.expires")}</label>
                  <Input
                    key={`expires-${own.expires_at ?? ""}`}
                    id="share-general-expires"
                    type="date"
                    defaultValue={own.expires_at ? dateInputValue(new Date(own.expires_at)) : ""}
                    min={earliestDate}
                    max={latestDate}
                    disabled={busy || !canChange}
                  />
                </div>
              </div>
              {canChange && (
                <div style={{ display: "flex", justifyContent: "flex-end" }}>
                  <Button disabled={busy} onClick={() => void renew()}>
                    {t("share.general.new")}
                  </Button>
                </div>
              )}
            </div>
          ) : choice === "anyone" && canChange ? (
            <form onSubmit={create} style={{ display: "grid", gap: 12 }}>
              <p className="hint" style={{ margin: 0, fontSize: 13 }}>{t("share.general.viewerOnly")}</p>
              <div style={{ display: "grid", gap: 12, gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))" }}>
                <div style={{ display: "grid", gap: 8 }}>
                  <label htmlFor="share-anyone-expires">{t("share.general.endsRequired")}</label>
                  <Input
                    id="share-anyone-expires"
                    type="date"
                    value={anyoneDate}
                    onChange={(e) => setAnyoneDate(e.target.value)}
                    min={anyoneEarliest}
                    max={anyoneLatest}
                    required
                    disabled={busy}
                  />
                </div>
                <div style={{ display: "grid", gap: 8 }}>
                  <label htmlFor="share-anyone-password">{t("share.general.password")}</label>
                  <Input.Password
                    id="share-anyone-password"
                    autoComplete="new-password"
                    value={anyonePassword}
                    onChange={(e) => setAnyonePassword(e.target.value)}
                    disabled={busy}
                  />
                  <p className="hint" style={{ margin: 0, fontSize: 13 }}>{t("share.general.passwordHint", { count: minPassword })}</p>
                </div>
              </div>
              <div style={{ display: "flex", justifyContent: "flex-end" }}>
                <Button type="primary" htmlType="submit" disabled={busy}>
                  {t("share.general.create")}
                </Button>
              </div>
            </form>
          ) : choice === "organisation" && canChange ? (
            <form onSubmit={create} style={{ display: "grid", gap: 12 }}>
              <div style={{ display: "grid", gap: 12, gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))" }}>
                <div style={{ display: "grid", gap: 8 }}>
                  <label htmlFor="share-general-new-role">{t("share.general.role")}</label>
                  {roleSelect("share-general-new-role", newRole, setNewRole, busy)}
                </div>
                <div style={{ display: "grid", gap: 8 }}>
                  <label htmlFor="share-general-new-expires">{t("share.general.expires")}</label>
                  <Input
                    id="share-general-new-expires"
                    type="date"
                    value={newExpires}
                    onChange={(e) => setNewExpires(e.target.value)}
                    min={earliestDate}
                    max={latestDate}
                    disabled={busy}
                  />
                </div>
              </div>
              <div style={{ display: "flex", justifyContent: "flex-end" }}>
                <Button type="primary" htmlType="submit" disabled={busy}>
                  {t("share.general.create")}
                </Button>
              </div>
            </form>
          ) : null}

          {madeURL !== null && (
            <div className="made" style={{ display: "grid", gap: 8 }}>
              <p className="hint" style={{ margin: 0, fontSize: 13 }}>{t("share.general.copyNow")}</p>
              <div style={{ display: "flex", gap: 8 }}>
                <Input
                  readOnly
                  value={madeURL}
                  aria-label={t("share.general.urlLabel")}
                  onFocus={(e) => e.currentTarget.select()}
                />
                <Button type="primary" onClick={() => void copy()}>
                  {t("share.general.copy")}
                </Button>
              </div>
            </div>
          )}

          {above.map((l) => (
            <div key={l.id} style={{ display: "flex", flexWrap: "wrap", justifyContent: "space-between", alignItems: "center", gap: 8 }}>
              <span>{t("share.general.inherited", { role: t(`role.${l.role}`) })}</span>
              <Button onClick={() => openNode(l.node_id)}>{t("share.openParent")}</Button>
            </div>
          ))}
        </>
      )}
    </section>
  );
}
