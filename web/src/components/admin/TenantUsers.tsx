// SPDX-License-Identifier: AGPL-3.0-or-later
import { KeyRound, Link, LogIn, Send } from "lucide-react";

import { Alert, Button } from "antd";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { lazy, Suspense, useRef, useState } from "react";
import { useLang, useT } from "../../lib/i18n";
import { listPeople, resendVerification, updatePerson } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import { personLabel, statusTone } from "../../lib/manage/people";
import { queryKey } from "../../lib/query";
import { sessionChanged, session } from "../../lib/session";
import { useStore } from "../../lib/store";
import type { Person } from "../../lib/manage/types";
import AdminPassword from "./AdminPassword";
import ShowOnce from "./ShowOnce";
import { useOnSessionChange } from "./hooks";
import StatusTag from "./StatusTag";
import { more } from "./adminStyles";
import Listing, { type ListAction, type ListMenuEntry } from "../files/Listing";
import { useAdminText } from "../../lib/manage/adminText";
import LastSignIn from "./LastSignIn";

// The dialogs load on first use: a first load of the tenant's people does not need them. Remove is the owner's People
// page's own dialog: a confirmation, then, when the person's My files hold files, the choice of what happens to them.
const ResetPassword = lazy(() => import("./ResetPassword"));
const SignInAs = lazy(() => import("./SignInAs"));
const RemoveDialog = lazy(() => import("../manage/RemoveDialog"));

// A secret shown once: a link, with the id and title of its dialog.
interface OnceLink {
  id: string;
  url: string;
  title: string;
}

// TenantUsers: the tenant's people. An admin does not manage a tenant's members day to day. It can resend the invitation of
// a person who has not verified yet, reset a person's password, disable or enable a person, remove a person from the account, and (when admin.tenant_access
// is on) sign in as the person. The actions are offered only as far as the installation allows; the server checks each one
// again. One action runs at a time; while it runs, the row's actions stay where they are, disabled.
export default function TenantUsers({ account }: { account: string }) {
  const { t } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const inFlight = useRef(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [link, setLink] = useState<OnceLink | null>(null);
  const [resetFor, setResetFor] = useState<Person | null>(null);
  const [signInFor, setSignInFor] = useState<Person | null>(null);
  const [linkFor, setLinkFor] = useState<Person | null>(null);
  const [removing, setRemoving] = useState<Person | null>(null);
  const adminText = useAdminText();
  // tenant_access and mail_configured are sent to an installation admin's own session only.
  const me = useStore(session);
  const tenantAccess = me?.tenantAccess === true;
  const mailConfigured = me?.mailConfigured === true;
  useOnSessionChange(() => {
    setLink(null);
    setResetFor(null);
    setSignInFor(null);
    setLinkFor(null);
    setRemoving(null);
  });
  const q = useInfiniteQuery({
    queryKey: queryKey("tenant-users", account),
    queryFn: ({ pageParam }) => listPeople(account, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor || undefined,
    refetchOnMount: "always",
  });
  const people: Person[] = q.data?.pages.flatMap((p) => p.items) ?? [];
  const firstFailed = q.isError && q.data === undefined;

  // act runs one change on a row: one at a time, and an answer for a person who has gone is not shown.
  const act = async (p: Person, fn: (changed: () => boolean) => Promise<void>) => {
    if (inFlight.current) return;
    inFlight.current = true;
    const changed = sessionChanged();
    setBusy(p.id);
    setActionError(null);
    setNotice(null);
    try {
      await fn(changed);
    } catch (err) {
      if (changed()) return;
      setActionError(failureText(err, lang));
    } finally {
      inFlight.current = false;
      setBusy(null);
    }
  };

  const resend = (p: Person) =>
    act(p, async (changed) => {
      const reply = await resendVerification(account, p.id, "email");
      if (changed()) return;
      setNotice(reply.verification?.sent === false ? t("people.mailNotSent") : t("people.mailSent"));
    });

  // Disable and Enable go through the route the owner's People page uses (PATCH status); the list is read again after.
  const changeStatus = (p: Person) =>
    act(p, async (changed) => {
      await updatePerson(account, p.id, { status: p.status === "disabled" ? "active" : "disabled" });
      if (changed()) return;
      await qc.invalidateQueries({ queryKey: queryKey("tenant-users", account) });
    });

  // newLink asks the admin's password, then makes the link. The password goes with the request only; a refusal reaches the
  // prompt, which stays open (decision 84).
  const newLink = async (p: Person, adminPassword: string) => {
    const changed = sessionChanged();
    const reply = await resendVerification(account, p.id, "link", adminPassword);
    if (changed()) return;
    setLinkFor(null);
    if (reply.verification?.url) {
      setLink({ id: "owner-link", url: reply.verification.url, title: t("tenant.users.linkTitle", { name: personLabel(p) }) });
    } else {
      setNotice(t("people.mailSent"));
    }
  };

  return (
    <div>
      {q.isError && <Alert type="error" showIcon role="alert" message={failureText(q.error, lang)} style={{ marginBlockEnd: 12 }} />}
      {actionError !== null && <Alert type="error" showIcon role="alert" message={actionError} style={{ marginBlockEnd: 12 }} />}
      {notice !== null && <Alert type="success" showIcon role="status" message={notice} style={{ marginBlockEnd: 12 }} />}

      {!firstFailed && q.isPending && <p role="status">{t("app.loading")}</p>}
      {/* The people table (the approved mockup): Name with the email, Role, Status, Last sign-in and the row's
          actions. The first three actions show inline on a desktop and the rest in the ⋮ menu; a phone has the ⋮ menu alone.
          Each action shows only where it applies. */}
      {!firstFailed && q.data !== undefined && (
        <Listing
          label={t("tenant.users.title")}
          items={people}
          rowLabel={personLabel}
          moreLabel={(p) => t("tenants.actionsFor", { name: personLabel(p) })}
          columns={[
            {
              key: "name",
              title: t("files.col.name"),
              render: (p) => (
                <span style={{ minWidth: 0 }}>
                  <span>
                    <bdi>{personLabel(p)}</bdi>
                  </span>
                  <span style={{ display: "block", fontSize: 13, overflowWrap: "anywhere", color: "var(--ant-color-text-secondary)" }}>
                    <bdi>{p.email}</bdi>
                  </span>
                </span>
              ),
            },
            { key: "role", title: t("people.detail.role"), secondary: true, render: (p) => t(`people.role.${p.role}`) },
            {
              key: "status",
              title: t("people.detail.status"),
              width: 120,
              render: (p) => <StatusTag tone={statusTone(p.status)}>{t(`people.status.${p.status}`)}</StatusTag>,
            },
            { key: "last", title: t("people.lastSignIn"), secondary: true, width: 160, render: (p) => <LastSignIn at={p.last_sign_in_at} /> },
          ]}
          actions={(p) => {
            const pending = p.status === "pending";
            const ownerPending = p.role === "owner" && pending;
            const canReset = mailConfigured || tenantAccess;
            const canSignIn = tenantAccess && p.status === "active";
            // A pending person's invitation goes again by email, with mail. A new link to an admin's session is only for a
            // pending owner, and only with tenant access (decision 41): the server refuses the rest.
            const canResend = pending && mailConfigured;
            const canNewLink = ownerPending && tenantAccess;
            const busyRow = busy === p.id;
            const acts: (ListAction | false)[] = [
              canResend && { key: "resend", label: `${t("tenant.users.resend")}: ${personLabel(p)}`, icon: <Send className="icon-dir" size={18} aria-hidden="true" focusable="false" />, run: () => void resend(p) },
              canNewLink && { key: "newlink", label: `${t("tenant.users.newLink")}: ${personLabel(p)}`, icon: <Link size={18} aria-hidden="true" focusable="false" />, run: () => setLinkFor(p) },
              canSignIn && { key: "signin", label: `${t("tenant.users.signInAs")}: ${personLabel(p)}`, icon: <LogIn className="icon-dir" size={18} aria-hidden="true" focusable="false" />, run: () => setSignInFor(p) },
              canReset && { key: "reset", label: `${t("tenant.users.reset")}: ${personLabel(p)}`, icon: <KeyRound size={18} aria-hidden="true" focusable="false" />, run: () => setResetFor(p) },
            ];
            // A row with a change on its way keeps its actions where they are, disabled.
            return acts.flatMap((a) => (a ? [{ ...a, disabled: busyRow }] : []));
          }}
          menu={(p): ListMenuEntry[] => [
            // Disable and Enable (the mockup's ⋮): a pending person has not verified, so there is nothing to disable yet.
            ...(p.status === "pending"
              ? []
              : [
                  {
                    key: "status",
                    label: p.status === "disabled" ? t("people.enable") : t("people.disable"),
                    danger: p.status !== "disabled",
                    disabled: busy === p.id,
                    onSelect: () => void changeStatus(p),
                  },
                ]),
            // Remove from account (the mockup's ⋮), for anyone, a pending person too. Its words are the admin
            // catalogue part's, which the page loads with its empty table; until they arrive, the People page's "Remove…".
            {
              key: "remove",
              label: adminText ? t("admin.removeFromAccount") : t("people.remove.open"),
              danger: true,
              disabled: busy === p.id,
              onSelect: () => {
                setActionError(null);
                setNotice(null);
                setRemoving(p);
              },
            },
          ]}
          // An admin page adds no one to a tenant, so its empty table has no action.
          empty={{ title: t("tenant.users.empty"), description: adminText ? t("admin.empty.users") : undefined }}
        />
      )}
      {!firstFailed && q.hasNextPage && (
        <div style={more}>
          <Button disabled={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
            {t("common.loadMore")}
          </Button>
        </div>
      )}

      <Suspense fallback={null}>
        {resetFor !== null && (
          <ResetPassword
            accountID={account}
            person={resetFor}
            mailConfigured={mailConfigured}
            tenantAccess={tenantAccess}
            onClose={() => setResetFor(null)}
            onDone={(text) => {
              setResetFor(null);
              setActionError(null);
              setNotice(text);
            }}
            onLink={(url) => {
              setLink({ id: "reset-link", url, title: t("tenant.users.resetLinkTitle", { name: personLabel(resetFor) }) });
              setResetFor(null);
            }}
          />
        )}
        {signInFor !== null && <SignInAs accountID={account} person={signInFor} onClose={() => setSignInFor(null)} />}
        {removing !== null && (
          <RemoveDialog
            account={account}
            person={removing}
            candidates={people}
            onclose={() => setRemoving(null)}
            ondone={() => {
              setNotice(t("people.removed", { name: personLabel(removing) }));
              setRemoving(null);
              void qc.invalidateQueries({ queryKey: queryKey("tenant-users", account) });
            }}
          />
        )}
      </Suspense>
      {linkFor !== null && (
        <AdminPassword
          id="owner-link-admin"
          title={t("admin.confirm.title")}
          what={t("tenant.users.linkPasswordWhat", { name: personLabel(linkFor) })}
          submitLabel={t("admin.confirm.submit")}
          onConfirm={(adminPassword) => newLink(linkFor, adminPassword)}
          onClose={() => setLinkFor(null)}
        />
      )}

      {link !== null && (
        <ShowOnce
          id={link.id}
          title={link.title}
          label={t("tenant.new.linkLabel")}
          value={link.url}
          warning={t("tenant.new.linkWarning")}
          onClosed={() => setLink(null)}
        />
      )}
    </div>
  );
}
