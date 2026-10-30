// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Checkbox, Input, Modal, Select } from "antd";
import { useLang, useT, translate, translatePlural, type Lang } from "../../lib/i18n";
import { ApiError } from "../../lib/apiError";
import { session, sessionChanged } from "../../lib/session";
import { useStore } from "../../lib/store";
import { queryKey } from "../../lib/query";
import { atLeast } from "../../lib/files/roles";
import { formatDate } from "../../lib/files/format";
import { statOrNull } from "../../lib/files/api";
import { go } from "../../lib/router";
import type { DirectoryEntry, FolderAccess, Node, Permission, Role } from "../../lib/files/types";
import {
  listPermissions,
  removePermission,
  setPermission,
  transferOwnership,
  updateNodeFlags,
} from "../../lib/sharing/api";
import { grantableRoles, shareCap } from "../../lib/sharing/roles";
import { ancestorIDs, canRemoveHere, originOf, type Origin } from "../../lib/sharing/inherited";
import { dateInputValue, expiryFromInputCapped, expiryLimit, maxExpiryDays } from "../../lib/sharing/limits";
import { canRequestFiles } from "../../lib/sharing/drop";
import PeopleCombobox from "./PeopleCombobox";
import GeneralAccess from "./GeneralAccess";
import RequestFiles from "./RequestFiles";

// ShareDialog shows who has access to an item, and changes it as far as the caller may. The server checks every change;
// the dialog only shows what the caller's role allows. A person below manager sees only its own rows (the server's
// rule), and says so. The dialog closes on Escape, its close button, or the parent's own action; a click on the mask
// never answers it, and a change of session closes it, so the next person never sees this one's state.
export interface ShareDialogProps {
  node: Node;
  space: FolderAccess | null;
  onclose: () => void;
  onchanged: () => void;
}

// shareErrorText says what a refused change means, by its code. The server's English text is never shown.
export function shareErrorText(err: unknown, lang: Lang, maxDays: number): string {
  if (err instanceof ApiError) {
    if (err.code === "forbidden") return translate(lang, "share.err.forbidden");
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

// noticeFor says why no mail went out, when the server says so. A share that was made is never undone for it.
export function noticeFor(reason: string | undefined, lang: Lang): string | null {
  switch (reason) {
    case undefined:
    case "not_requested":
      return null;
    case "too_many_recipients":
      return translate(lang, "share.notice.tooMany");
    case "no_recipients":
      return translate(lang, "share.notice.noRecipients");
    case "recipient_not_active":
      return translate(lang, "share.notice.notActive");
    case "mail_budget":
      return translate(lang, "share.notice.budget");
    case "mail_failed":
      return translate(lang, "share.notice.failed");
    case "mail_not_configured":
      return translate(lang, "share.notice.notConfigured");
    default:
      return translate(lang, "share.notice.none");
  }
}

export default function ShareDialog({ node, space, onclose, onchanged }: ShareDialogProps) {
  const { t, tp } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const me = useStore(session);
  const maxDays = useStore(maxExpiryDays);
  const key = queryKey("permissions", node.id);

  const permsQ = useQuery({
    queryKey: key,
    queryFn: () => listPermissions(node.id),
    refetchOnMount: "always",
  });
  const perms: Permission[] = permsQ.data?.items ?? [];

  // Names of the ancestors the permissions sit on. An ancestor the caller cannot read has no name, and the row says so.
  const ancestors = ancestorIDs(perms);
  const namesQ = useQuery({
    queryKey: queryKey("ancestor-names", node.id, ancestors.join(",")),
    queryFn: async () => {
      const out: Record<string, string> = {};
      for (const id of ancestors) {
        const parent = await statOrNull(id);
        // A folder's root has an empty name: such an ancestor is named by its place, not by a blank.
        if (parent && parent.name !== "") out[id] = parent.name;
      }
      return out;
    },
    enabled: permsQ.isSuccess && ancestors.length > 0,
  });
  const names = useMemo(() => new Map(Object.entries(namesQ.data ?? {})), [namesQ.data]);

  const [notice, setNotice] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // The add form.
  const [picked, setPicked] = useState<DirectoryEntry | null>(null);
  const [newRole, setNewRole] = useState<Role>("viewer");
  const [expires, setExpires] = useState("");
  const [notify, setNotify] = useState(true);
  const [message, setMessage] = useState("");
  const [pickedInvalid, setPickedInvalid] = useState(false);

  // A removal waits for a confirmation.
  const [confirmRemove, setConfirmRemove] = useState<Permission | null>(null);

  // The advanced settings, read from the node and kept here while the dialog is open.
  const [editorsCanShare, setEditorsCanShare] = useState(node.editors_can_share);
  const [noDownload, setNoDownload] = useState(node.no_download);
  useEffect(() => {
    setEditorsCanShare(node.editors_can_share);
    setNoDownload(node.no_download);
  }, [node.editors_can_share, node.no_download]);

  // Ownership moves to another user.
  const [transferOpen, setTransferOpen] = useState(false);
  const [transferTo, setTransferTo] = useState<DirectoryEntry | null>(null);
  const [transferConfirm, setTransferConfirm] = useState(false);

  const manager = atLeast(node.role, "manager");
  const cap = shareCap({
    role: node.role,
    editorsCanShare: node.editors_can_share,
    managersOnly: space?.sharing_managers_only ?? false,
  });
  const roleOptions = grantableRoles(cap);
  const canShareHere = roleOptions.length > 0;
  const personal = space?.kind === "personal";
  const isOwner = node.owner_id !== undefined && node.owner_id === me?.user.id;
  const latest = expiryLimit(maxDays, new Date());
  const latestDate = latest ? dateInputValue(latest) : undefined;
  const earliestDate = dateInputValue(new Date(Date.now() + 24 * 60 * 60 * 1000));

  // A session change closes the dialog: its state belongs to the person who opened it.
  const changedSince = useMemo(() => sessionChanged(), []);
  useEffect(() => {
    if (changedSince()) onclose();
  }, [me, changedSince, onclose]);

  const refresh = () => qc.invalidateQueries({ queryKey: key });

  // run performs one change: it shows the reply, refreshes the list, and tells the parent. A change that ends after the
  // session has changed is dropped.
  async function run(change: () => Promise<string | null>): Promise<void> {
    if (busy) return;
    const stale = sessionChanged();
    setBusy(true);
    setFormError(null);
    try {
      const text = await change();
      if (stale()) return;
      setNotice(text);
      await refresh();
      onchanged();
    } catch (err) {
      if (stale()) return;
      setFormError(shareErrorText(err, lang, maxDays));
    } finally {
      setBusy(false);
    }
  }

  const subjectName = (p: Permission): string => {
    if (p.subject_name) return p.subject_name;
    if (p.subject_kind === "user" && p.subject_id === me?.user.id) return t("share.you");
    return p.subject_kind === "group" ? t("share.subject.group") : t("share.subject.person");
  };

  const originText = (p: Permission): string => {
    const o: Origin = originOf(p, names);
    if (o.kind === "own") return "";
    if (o.kind === "named") return t("share.from", { name: o.name });
    return t("share.fromParent");
  };

  async function changeRole(p: Permission, role: Role): Promise<void> {
    if (role === p.role) return;
    await run(async () => {
      await setPermission(node.id, p.subject_id, { role, notify: false });
      return t("share.changed", { name: subjectName(p), role: t(`role.${role}`) });
    });
  }

  async function remove(p: Permission): Promise<void> {
    setConfirmRemove(null);
    await run(async () => {
      await removePermission(node.id, p.subject_id);
      return t("share.removed", { name: subjectName(p) });
    });
  }

  async function add(e: FormEvent): Promise<void> {
    e.preventDefault();
    setFormError(null);
    setNotice(null);
    if (picked === null) {
      setPickedInvalid(true);
      setFormError(t("share.err.pick"));
      return;
    }
    setPickedInvalid(false);
    const expiresAt = expiryFromInputCapped(expires, new Date(), maxDays);
    if (expires !== "" && expiresAt === null) {
      setFormError(t("share.err.date"));
      return;
    }
    if (message.length > 500) {
      setFormError(t("share.err.message"));
      return;
    }
    const who = picked.name;
    const target = picked.id;
    await run(async () => {
      const reply = await setPermission(node.id, target, {
        role: newRole,
        notify,
        ...(expiresAt ? { expires_at: expiresAt } : {}),
        ...(message.trim() ? { message: message.trim() } : {}),
      });
      const mailNote = notify && reply.notified === false ? noticeFor((reply as { reason?: string }).reason, lang) : null;
      const done = t("share.done", { name: who, role: t(`role.${newRole}`) });
      setPicked(null);
      setExpires("");
      setMessage("");
      setNotify(true);
      return mailNote ? `${done} ${mailNote}` : done;
    });
  }

  async function setFlag(flag: "editors_can_share" | "no_download", on: boolean): Promise<void> {
    if (busy) return;
    const stale = sessionChanged();
    const before = flag === "editors_can_share" ? editorsCanShare : noDownload;
    const set = flag === "editors_can_share" ? setEditorsCanShare : setNoDownload;
    setFormError(null);
    setBusy(true);
    set(on);
    try {
      await updateNodeFlags(node.id, flag === "editors_can_share" ? { editors_can_share: on } : { no_download: on });
      if (stale()) return;
      setNotice(
        t(
          flag === "editors_can_share"
            ? on ? "share.flag.editorsOn" : "share.flag.editorsOff"
            : on ? "share.flag.noDownloadOn" : "share.flag.noDownloadOff",
        ),
      );
      onchanged();
    } catch (err) {
      if (stale()) return;
      set(before);
      setFormError(shareErrorText(err, lang, maxDays));
    } finally {
      setBusy(false);
    }
  }

  async function transfer(): Promise<void> {
    if (transferTo === null) return;
    const who = transferTo.name;
    const target = transferTo.id;
    const stale = sessionChanged();
    setBusy(true);
    setFormError(null);
    try {
      await transferOwnership(node.id, target);
      if (stale()) return;
      setTransferOpen(false);
      setTransferConfirm(false);
      setNotice(t("share.transferred", { name: who }));
      onchanged();
    } catch (err) {
      if (stale()) return;
      setFormError(shareErrorText(err, lang, maxDays));
    } finally {
      setBusy(false);
    }
  }

  const openParent = (p: Permission) => {
    if (p.inherited_from) {
      go(`/n/${p.inherited_from}`);
      onclose();
    }
  };

  const title = t("share.title", { name: node.name });
  const roleSelectOptions = roleOptions.map((r) => ({ value: r, label: t(`role.${r}`) }));

  return (
    <Modal
      open
      title={title}
      onCancel={onclose}
      maskClosable={false}
      footer={null}
      width={720}
      destroyOnClose
      className="share-dialog"
    >
      <div style={{ display: "grid", gap: 24, minWidth: 0 }}>
        <p className="live" role="status" aria-live="polite" style={{ margin: 0 }}>
          {notice ?? ""}
        </p>
        {formError && <Alert type="error" showIcon role="alert" message={formError} />}

        {!manager && (
          <p style={{ margin: 0, color: "var(--ant-color-text-secondary)" }}>
            {t("share.yourAccess", { role: t(`role.${node.role ?? "viewer"}`) })}
          </p>
        )}

        <GeneralAccess node={node} cap={cap} onchanged={onchanged} onopen={onclose} />

        {node.kind === "dir" && canRequestFiles(me?.publicLinks === true, cap) && (
          <RequestFiles node={node} cap={cap} onchanged={onchanged} />
        )}

        <section aria-labelledby="share-people">
          <h3 id="share-people" style={{ margin: "0 0 8px", fontSize: 16, fontWeight: 600 }}>
            {manager ? t("share.people") : t("share.yourRows")}
          </h3>
          {permsQ.isPending ? (
            <p role="status" style={{ margin: 0, color: "var(--ant-color-text-secondary)" }}>
              {t("share.loading")}
            </p>
          ) : permsQ.isError && perms.length === 0 ? (
            <Alert type="error" showIcon role="alert" message={shareErrorText(permsQ.error, lang, maxDays)} />
          ) : perms.length === 0 ? (
            <p style={{ margin: 0, color: "var(--ant-color-text-secondary)" }}>{t("share.none")}</p>
          ) : (
            <ul style={{ display: "grid", gap: 8, margin: 0, padding: 0, listStyle: "none" }}>
              {perms.map((p) => {
                const who = subjectName(p);
                const rowKey = p.subject_kind + p.subject_id + (p.inherited_from ?? "");
                const selfManager = p.subject_id === me?.user.id && p.role === "manager";
                return (
                  <li
                    key={rowKey}
                    style={{
                      display: "flex",
                      flexWrap: "wrap",
                      alignItems: "center",
                      justifyContent: "space-between",
                      gap: 8,
                      minHeight: 44,
                      paddingBlock: 8,
                      borderBlockEnd: "1px solid var(--ant-color-split)",
                    }}
                  >
                    <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 8, minWidth: 0 }}>
                      <span style={{ fontWeight: 600, overflowWrap: "anywhere" }}>
                        <bdi>{who}</bdi>
                      </span>
                      {p.subject_status === "pending" && <span className="badge">{t("share.badge.invited")}</span>}
                      {p.subject_kind === "group" && <span className="badge">{t("share.kind.group")}</span>}
                      {originText(p) && <span className="origin hint">{originText(p)}</span>}
                      {p.expires_at && (
                        <span className="origin hint">{t("share.endsOn", { date: formatDate(p.expires_at, lang) })}</span>
                      )}
                    </div>
                    <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 8 }}>
                      {manager && canRemoveHere(p) && !selfManager ? (
                        <>
                          <Select
                            id={`role-${p.subject_id}`}
                            aria-label={t("share.roleFor", { name: who })}
                            value={p.role}
                            virtual={false}
                            disabled={busy}
                            onChange={(v: Role) => void changeRole(p, v)}
                            style={{ minWidth: 140 }}
                            options={[
                              ...roleOptions.map((r) => ({ value: r, label: t(`role.${r}`) })),
                              ...(roleOptions.includes(p.role) ? [] : [{ value: p.role, label: t(`role.${p.role}`), disabled: true }]),
                            ]}
                          />
                          {confirmRemove?.subject_id === p.subject_id ? (
                            <span style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 8 }}>
                              <span>{t("share.removeAsk", { name: who })}</span>
                              <Button danger type="primary" onClick={() => void remove(p)} disabled={busy}>
                                {t("share.removeYes")}
                              </Button>
                              <Button onClick={() => setConfirmRemove(null)}>{t("common.cancel")}</Button>
                            </span>
                          ) : (
                            <Button onClick={() => setConfirmRemove(p)} disabled={busy}>
                              {t("share.remove")}
                            </Button>
                          )}
                        </>
                      ) : manager ? (
                        <>
                          <span className="badge">{t(`role.${p.role}`)}</span>
                          {p.inherited_from && <Button onClick={() => openParent(p)}>{t("share.openParent")}</Button>}
                        </>
                      ) : (
                        <span className="badge">{t(`role.${p.role}`)}</span>
                      )}
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
        </section>

        {canShareHere ? (
          <section aria-labelledby="share-add" style={{ display: "grid", gap: 12 }}>
            <h3 id="share-add" style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>
              {t("share.add")}
            </h3>
            <form onSubmit={add} style={{ display: "grid", gap: 12 }}>
              <PeopleCombobox
                id="share-who"
                label={t("share.who")}
                hint={t("share.whoHint")}
                invalid={pickedInvalid}
                errorId={pickedInvalid ? "share-who-error" : undefined}
                onpick={(entry) => {
                  setPicked(entry);
                  setPickedInvalid(false);
                }}
              />
              {pickedInvalid && (
                <p id="share-who-error" style={{ margin: 0, fontSize: 13, color: "var(--ant-color-error)" }}>
                  {t("share.err.pick")}
                </p>
              )}
              <div style={{ display: "grid", gap: 12, gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))" }}>
                <div style={{ display: "grid", gap: 8 }}>
                  <label htmlFor="share-role">{t("share.role")}</label>
                  <Select
                    id="share-role"
                    value={newRole}
                    virtual={false}
                    onChange={(v: Role) => setNewRole(v)}
                    options={roleSelectOptions}
                  />
                </div>
                <div style={{ display: "grid", gap: 8 }}>
                  <label htmlFor="share-expires">{t("share.expires")}</label>
                  <Input
                    id="share-expires"
                    type="date"
                    value={expires}
                    onChange={(e) => setExpires(e.target.value)}
                    min={earliestDate}
                    max={latestDate}
                    aria-describedby="share-expires-hint"
                  />
                  <p className="hint" id="share-expires-hint" style={{ margin: 0, fontSize: 13 }}>
                    {maxDays > 0 ? tp("share.expiresHintCap", maxDays) : t("share.expiresHint")}
                  </p>
                </div>
              </div>
              <div style={{ display: "grid", gap: 8 }}>
                <label htmlFor="share-message">{t("share.message")}</label>
                <Input.TextArea
                  id="share-message"
                  rows={2}
                  maxLength={500}
                  value={message}
                  onChange={(e) => setMessage(e.target.value)}
                  aria-describedby="share-message-hint"
                />
                <p className="hint" id="share-message-hint" style={{ margin: 0, fontSize: 13 }}>
                  {t("share.messageHint", { count: message.length })}
                </p>
              </div>
              <label style={{ display: "flex", alignItems: "center", gap: 8, minHeight: 44 }}>
                <Checkbox id="share-notify" checked={notify} onChange={(e) => setNotify(e.target.checked)}>
                  {t("share.notify")}
                </Checkbox>
              </label>
              <div style={{ display: "flex", justifyContent: "flex-end" }}>
                <Button type="primary" htmlType="submit" disabled={busy}>
                  {t("share.submit")}
                </Button>
              </div>
            </form>
          </section>
        ) : (
          !manager && <p style={{ margin: 0, color: "var(--ant-color-text-secondary)" }}>{t("share.cannotShare")}</p>
        )}

        {manager && (
          <section aria-labelledby="share-advanced" style={{ display: "grid", gap: 12, paddingBlockStart: 16, borderBlockStart: "1px solid var(--ant-color-split)" }}>
            <h3 id="share-advanced" style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>
              {t("share.advanced")}
            </h3>
            <Checkbox
              checked={editorsCanShare}
              disabled={busy}
              onChange={(e) => void setFlag("editors_can_share", e.target.checked)}
            >
              {t("share.editorsCanShare")}
            </Checkbox>
            <div style={{ display: "grid", gap: 4 }}>
              <Checkbox
                checked={noDownload}
                disabled={busy}
                aria-describedby="share-nodl-hint"
                onChange={(e) => void setFlag("no_download", e.target.checked)}
              >
                {t("share.noDownload")}
              </Checkbox>
              <p className="hint" id="share-nodl-hint" style={{ margin: 0, fontSize: 13 }}>
                {t("share.noDownloadHint")}
              </p>
            </div>

            {isOwner && (
              <div style={{ display: "grid", gap: 12 }}>
                {!transferOpen ? (
                  <Button onClick={() => setTransferOpen(true)}>{t("share.transfer")}</Button>
                ) : (
                  <>
                    <PeopleCombobox
                      id="share-transfer-to"
                      label={t("share.transferTo")}
                      hint={t("share.transferHint")}
                      onpick={(entry) => {
                        setTransferTo(entry?.kind === "user" ? entry : null);
                        setTransferConfirm(false);
                      }}
                    />
                    {transferTo && (
                      <>
                        <p className="hint" style={{ margin: 0, fontSize: 13 }}>
                          {t(personal ? "share.transferPersonal" : "share.transferTeam", { name: transferTo.name })}
                        </p>
                        {!transferConfirm ? (
                          <Button danger onClick={() => setTransferConfirm(true)}>
                            {t("share.transferGo")}
                          </Button>
                        ) : (
                          <>
                            <Alert type="error" showIcon role="alert" message={t("share.transferSure", { name: transferTo.name })} />
                            <Button danger type="primary" onClick={() => void transfer()} disabled={busy}>
                              {t("share.transferYes")}
                            </Button>
                          </>
                        )}
                      </>
                    )}
                  </>
                )}
              </div>
            )}
          </section>
        )}
      </div>
    </Modal>
  );
}
