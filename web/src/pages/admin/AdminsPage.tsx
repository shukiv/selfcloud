// SPDX-License-Identifier: AGPL-3.0-or-later
import { UserMinus } from "lucide-react";
import { ItemList, type ItemColumn } from "../../components/files/Listing";
import EmptyState from "../../components/EmptyState";

import { App, Alert, Button, Input, Modal, Tag } from "antd";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { useLang, useT } from "../../lib/i18n";
import { brand } from "../../lib/brand";
import { useStore } from "../../lib/store";
import { isAdmin, session, sessionChanged } from "../../lib/session";
import { queryKey } from "../../lib/query";
import { createAdmin, deleteAdmin, listAdmins } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import { useFlight } from "../../lib/flight";
import { personLabel, statusTone } from "../../lib/manage/people";
import type { Person, PersonReply } from "../../lib/manage/types";
import NotFoundPage from "../NotFoundPage";
import Confirm from "../../components/Confirm";
import StatusTag from "../../components/admin/StatusTag";
import { field, form as formStyle, foot, more, pageTitle, toolbar } from "../../components/admin/adminStyles";
import { useGuard, useOnSessionChange } from "../../components/admin/hooks";

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

// AdminsPage lists the people who run this installation, for an installation admin only. The last active admin cannot be
// removed: the server refuses it, and its refusal is shown in the confirmation.
export default function AdminsPage() {
  const user = useStore(session)?.user;
  return isAdmin(user) ? <AdminsView /> : <NotFoundPage />;
}

function AdminsView() {
  const { t } = useT();
  const lang = useLang();
  const brandName = useStore(brand).name;
  const qc = useQueryClient();
  const guard = useGuard();
  const request = useFlight();
  const { message } = App.useApp();
  // mail_configured is sent to an installation admin's own session. A new admin can only be invited by email: the server
  // refuses a link to an admin's session for anyone but a pending owner (decision 41).
  const mailConfigured = useStore(session)?.mailConfigured === true;
  const [notice, setNotice] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [removing, setRemoving] = useState<Person | null>(null);
  const [removeBusy, setRemoveBusy] = useState(false);
  const [removeError, setRemoveError] = useState<string | null>(null);
  const adminsKey = queryKey("admins");

  // The form belongs to the person who opened it: a change of person closes it.
  useOnSessionChange(() => {
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
    if (!EMAIL.test(address) || !mailConfigured) return;
    void guard(async () => {
      const changed = sessionChanged();
      setBusy(true);
      setFormError(null);
      try {
        const reply: PersonReply = await createAdmin({ email: address, display_name: name.trim(), verification: "email" }, request.start());
        if (changed()) return;
        setCreating(false);
        setEmail("");
        setName("");
        setNotice(reply.verification?.sent === false ? t("people.mailNotSent") : t("admins.added"));
        void refresh();
      } catch (err) {
        if (changed()) return;
        setFormError(failureText(err, lang));
      } finally {
        request.end();
        setBusy(false);
      }
    });
  };

  // cancelForm and cancelRemove close their dialog at once, busy or not. A request on its way is aborted, and the page says
  // the admin may or may not have been added or removed.
  // A cancelled write may have gone through, so the list is read again and shows it.
  const cancelWrite = () => {
    if (!request.abort()) return false;
    void message.warning(t("common.cancelledMaybe"));
    void refresh();
    return true;
  };
  const cancelForm = () => {
    cancelWrite();
    setCreating(false);
  };
  const cancelRemove = () => {
    cancelWrite();
    setRemoving(null);
  };

  // The server decides who is the last active admin: it counts every admin and the admin keys that do not expire, and
  // its refusal (reason last_admin) is shown in the confirmation. The page sees only the pages loaded so far.
  const confirmRemove = () => {
    const target = removing;
    if (!target) return Promise.resolve();
    return guard(async () => {
      const changed = sessionChanged();
      setRemoveBusy(true);
      setRemoveError(null);
      try {
        await deleteAdmin(target.id, request.start());
        if (changed()) return;
        setNotice(t("admins.removed", { name: personLabel(target) }));
        setRemoving(null);
        void refresh();
      } catch (err) {
        if (changed()) return;
        setRemoveError(failureText(err, lang));
      } finally {
        request.end();
        setRemoveBusy(false);
      }
    });
  };

  const emailOk = EMAIL.test(email.trim());
  // The signed-in admin's own row says so and has no Remove icon: an admin cannot remove themselves here (design-tl 3.1).
  const selfID = useStore(session)?.user?.id;
  const columns: ItemColumn<Person>[] = [
    {
      key: "name",
      title: t("files.col.name"),
      render: (a) => (
        <span style={{ minWidth: 0 }}>
          <span>
            <bdi>{personLabel(a)}</bdi>
            {a.id === selfID && (
              <Tag style={{ marginInlineStart: 8 }}>{t("share.you")}</Tag>
            )}
          </span>
          <span style={{ display: "block", fontSize: 13, overflowWrap: "anywhere", color: "var(--ant-color-text-secondary)" }}>
            <bdi>{a.email}</bdi>
          </span>
        </span>
      ),
    },
    {
      key: "status",
      title: t("people.detail.status"),
      width: 120,
      render: (a) => <StatusTag tone={statusTone(a.status)}>{t(`people.status.${a.status}`)}</StatusTag>,
    },
  ];

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
        <ItemList
          label={t("admin.admins.title")}
          items={admins}
          rowLabel={personLabel}
          moreLabel={(a) => t("tenants.actionsFor", { name: personLabel(a) })}
          columns={columns}
          actions={(a) =>
            a.id === selfID
              ? []
              : [
                  {
                    key: "remove",
                    label: `${t("admins.remove")}: ${personLabel(a)}`,
                    icon: <UserMinus size={18} aria-hidden="true" focusable="false" />,
                    enabled: true,
                    run: () => {
                      setRemoveError(null);
                      setRemoving(a);
                    },
                  },
                ]
          }
          empty={<EmptyState role="status" text={t("admins.empty")} />}
        />
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
          onCancel={cancelForm}
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
              <legend>{t("admins.new.how")}</legend>
              {mailConfigured ? (
                <p style={{ margin: 0 }}>{t("tenant.new.howEmail")}</p>
              ) : (
                <Alert type="info" showIcon role="status" message={t("admins.new.noMail")} />
              )}
            </fieldset>
            {formError !== null && <Alert type="error" showIcon role="alert" message={formError} />}
            <div style={foot}>
              <Button onClick={cancelForm}>{t("common.cancel")}</Button>
              <Button type="primary" htmlType="submit" loading={busy} disabled={busy || !emailOk || !mailConfigured}>
                {t("admins.new.send")}
              </Button>
            </div>
          </form>
        </Modal>
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
          oncancel={cancelRemove}
        />
      )}
    </div>
  );
}
