// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button, Input, Modal, Radio } from "antd";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { useLang, useT } from "../../lib/i18n";
import { brand } from "../../lib/brand";
import { useStore } from "../../lib/store";
import { isAdmin, session, sessionChanged } from "../../lib/session";
import { queryKey } from "../../lib/query";
import { createAdmin, deleteAdmin, listAdmins } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import { isLastActiveAdmin } from "../../lib/manage/admin";
import { personLabel, statusTone } from "../../lib/manage/people";
import type { Person, PersonReply } from "../../lib/manage/types";
import NotFoundPage from "../NotFoundPage";
import Confirm from "../../components/Confirm";
import ShowOnce from "../../components/admin/ShowOnce";
import StatusTag from "../../components/admin/StatusTag";
import { field, form as formStyle, foot, list, more, muted, pageTitle, row, rowMain, rowTitle, toolbar } from "../../components/admin/adminStyles";
import { useGuard, useOnSessionChange } from "../../components/admin/hooks";

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

// AdminsPage lists the people who run this installation, for an installation admin only. The last active admin cannot be
// removed: the page disables the removal and says why, and the server refuses it too.
export default function AdminsPage() {
  const user = useStore(session)?.user;
  return isAdmin(user) ? <AdminsView /> : <NotFoundPage />;
}

function AdminsView() {
  const { t } = useT();
  const lang = useLang();
  const brandName = useStore(brand).name;
  const qc = useQueryClient();
  const flight = useGuard();
  const [notice, setNotice] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [how, setHow] = useState<"email" | "link">("email");
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [removing, setRemoving] = useState<Person | null>(null);
  const [removeBusy, setRemoveBusy] = useState(false);
  const [removeError, setRemoveError] = useState<string | null>(null);
  const [secret, setSecret] = useState<{ url: string; who: string } | null>(null);
  const adminsKey = queryKey("admins");

  // The form and the secret belong to the person who opened them: a change of person clears both.
  useOnSessionChange(() => {
    setSecret(null);
    setCreating(false);
  });

  useEffect(() => {
    document.title = `${t("admin.admins.title")} · ${brandName}`;
  }, [t, brandName]);

  const q = useInfiniteQuery({
    queryKey: adminsKey,
    queryFn: ({ pageParam }) => listAdmins(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor || undefined,
    refetchOnMount: "always",
  });
  const admins: Person[] = q.data?.pages.flatMap((p) => p.items) ?? [];
  const firstFailed = q.isError && q.data === undefined;
  const refresh = () => qc.invalidateQueries({ queryKey: adminsKey });

  const openNew = () => {
    setFormError(null);
    setCreating(true);
  };

  const add = (e: FormEvent) => {
    e.preventDefault();
    const address = email.trim();
    if (!EMAIL.test(address)) return;
    void flight(async () => {
      const changed = sessionChanged();
      setBusy(true);
      setFormError(null);
      try {
        const reply: PersonReply = await createAdmin({ email: address, display_name: name.trim(), verification: how });
        if (changed()) return;
        setCreating(false);
        setEmail("");
        setName("");
        if (reply.verification?.url) {
          setSecret({ url: reply.verification.url, who: personLabel(reply) });
        } else {
          setNotice(reply.verification?.sent === false ? t("people.mailNotSent") : t("admins.added"));
        }
        void refresh();
      } catch (err) {
        if (changed()) return;
        setFormError(failureText(err, lang));
      } finally {
        setBusy(false);
      }
    });
  };

  const confirmRemove = () => {
    const target = removing;
    if (!target) return Promise.resolve();
    return flight(async () => {
      const changed = sessionChanged();
      setRemoveBusy(true);
      setRemoveError(null);
      try {
        await deleteAdmin(target.id);
        if (changed()) return;
        setNotice(t("admins.removed", { name: personLabel(target) }));
        setRemoving(null);
        void refresh();
      } catch (err) {
        if (changed()) return;
        setRemoveError(failureText(err, lang));
      } finally {
        setRemoveBusy(false);
      }
    });
  };

  const emailOk = EMAIL.test(email.trim());

  return (
    <div>
      <div style={toolbar}>
        <h1 style={{ ...pageTitle, margin: 0 }}>{t("admin.admins.title")}</h1>
        <Button type="primary" onClick={openNew}>
          {t("admins.new")}
        </Button>
      </div>

      {notice !== null && <Alert type="success" showIcon role="status" message={notice} style={{ marginBlockEnd: 12 }} />}
      {q.isError && <Alert type="error" showIcon role="alert" message={failureText(q.error, lang)} style={{ marginBlockEnd: 12 }} />}

      {!firstFailed && q.isPending && <p role="status">{t("app.loading")}</p>}
      {/* The list stays on screen when a refetch fails: react-query keeps the data, so the error goes above it. */}
      {!firstFailed && q.data !== undefined && (
        <ul style={list} aria-label={t("admin.admins.title")}>
          {admins.map((a) => {
            const last = isLastActiveAdmin(admins, a.id);
            return (
              <li key={a.id} style={row}>
                <div style={rowMain}>
                  <p style={rowTitle}>
                    <bdi>{personLabel(a)}</bdi>
                  </p>
                  <p style={muted}>
                    <bdi>{a.email}</bdi>
                  </p>
                  {last && (
                    <p id={`last-admin-${a.id}`} style={muted}>
                      {t("admins.lastHint")}
                    </p>
                  )}
                </div>
                <div>
                  <StatusTag tone={statusTone(a.status)}>{t(`people.status.${a.status}`)}</StatusTag>
                </div>
                <div>
                  <Button
                    size="small"
                    danger
                    disabled={last}
                    aria-describedby={last ? `last-admin-${a.id}` : undefined}
                    title={last ? t("admins.lastHint") : undefined}
                    onClick={() => {
                      setRemoveError(null);
                      setRemoving(a);
                    }}
                  >
                    {t("admins.remove")}
                    <span className="sr-only">: {personLabel(a)}</span>
                  </Button>
                </div>
              </li>
            );
          })}
        </ul>
      )}
      {!firstFailed && q.hasNextPage && (
        <div style={more}>
          <Button disabled={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
            {t("common.loadMore")}
          </Button>
        </div>
      )}

      {creating && (
        <Modal
          open
          title={t("admins.new.title")}
          width={560}
          footer={null}
          maskClosable={false}
          keyboard={!busy}
          closable={!busy}
          onCancel={busy ? undefined : () => setCreating(false)}
        >
          <form style={formStyle} onSubmit={add} noValidate>
            <div style={field}>
              <label htmlFor="admin-email">{t("admins.new.email")}</label>
              <Input id="admin-email" type="email" autoComplete="off" inputMode="email" value={email} onChange={(e) => setEmail(e.target.value)} autoFocus />
            </div>
            <div style={field}>
              <label htmlFor="admin-name">{t("admins.new.name")}</label>
              <Input id="admin-name" autoComplete="off" value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            <fieldset style={{ display: "grid", gap: 8, margin: 0, padding: 0, border: 0 }}>
              <legend>{t("tenant.new.how")}</legend>
              <Radio.Group
                name="admin-how"
                value={how}
                onChange={(e) => setHow(e.target.value as "email" | "link")}
                options={[
                  { value: "email", label: t("tenant.new.howEmail") },
                  { value: "link", label: t("tenant.new.howLink") },
                ]}
              />
            </fieldset>
            {formError !== null && <Alert type="error" showIcon role="alert" message={formError} />}
            <div style={foot}>
              <Button disabled={busy} onClick={() => setCreating(false)}>{t("common.cancel")}</Button>
              <Button type="primary" htmlType="submit" loading={busy} disabled={busy || !emailOk}>
                {t("admins.new.send")}
              </Button>
            </div>
          </form>
        </Modal>
      )}

      {secret !== null && (
        <ShowOnce
          id="admin-link"
          title={t("admins.linkTitle", { name: secret.who })}
          label={t("tenant.new.linkLabel")}
          value={secret.url}
          warning={t("tenant.new.linkWarning")}
          onClosed={() => setSecret(null)}
        />
      )}

      {removing !== null && (
        <Confirm
          id="remove-admin"
          title={t("admins.removeTitle", { name: personLabel(removing) })}
          message={t("admins.removeMessage")}
          action={t("admins.remove")}
          danger
          error={removeError}
          busy={removeBusy}
          onconfirm={() => void confirmRemove()}
          onclose={() => setRemoving(null)}
        />
      )}
    </div>
  );
}
