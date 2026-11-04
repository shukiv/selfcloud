// SPDX-License-Identifier: AGPL-3.0-or-later
import { KeyRound, Link, LogIn, Send } from "lucide-react";
import IconButton from "../IconButton";

import { Alert, Button } from "antd";
import { useInfiniteQuery } from "@tanstack/react-query";
import { lazy, Suspense, useRef, useState } from "react";
import { useLang, useT } from "../../lib/i18n";
import { listPeople, resendVerification } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import { personLabel, statusTone } from "../../lib/manage/people";
import { queryKey } from "../../lib/query";
import { sessionChanged, session } from "../../lib/session";
import { useStore } from "../../lib/store";
import type { Person } from "../../lib/manage/types";
import ShowOnce from "./ShowOnce";
import { useOnSessionChange } from "./hooks";
import StatusTag from "./StatusTag";
import { list, more, muted, row, rowMain, rowTitle } from "./adminStyles";

// The two dialogs load on first use: a first load of the tenant's people does not need them.
const ResetPassword = lazy(() => import("./ResetPassword"));
const SignInAs = lazy(() => import("./SignInAs"));

// A secret shown once: a link, with the id and title of its dialog.
interface OnceLink {
  id: string;
  url: string;
  title: string;
}

// TenantUsers: the tenant's people. An admin does not manage a tenant's members day to day. It can resend the invitation of
// an owner who has not verified yet, reset a person's password, and (when admin.tenant_access is on) sign in as the person.
// The reset and sign-in actions are offered only as far as the installation allows; the server checks each one again.
export default function TenantUsers({ account }: { account: string }) {
  const { t } = useT();
  const lang = useLang();
  const inFlight = useRef(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [link, setLink] = useState<OnceLink | null>(null);
  const [resetFor, setResetFor] = useState<Person | null>(null);
  const [signInFor, setSignInFor] = useState<Person | null>(null);
  // tenant_access and mail_configured are sent to an installation admin's own session only.
  const me = useStore(session);
  const tenantAccess = me?.tenantAccess === true;
  const mailConfigured = me?.mailConfigured === true;
  useOnSessionChange(() => {
    setLink(null);
    setResetFor(null);
    setSignInFor(null);
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

  const resend = async (p: Person, method: "email" | "link") => {
    if (inFlight.current) return;
    inFlight.current = true;
    const changed = sessionChanged();
    setBusy(p.id);
    setActionError(null);
    setNotice(null);
    try {
      const reply = await resendVerification(account, p.id, method);
      if (changed()) return;
      if (method === "link" && reply.verification?.url) {
        setLink({ id: "owner-link", url: reply.verification.url, title: t("tenant.users.linkTitle", { name: personLabel(p) }) });
      } else {
        setNotice(reply.verification?.sent === false ? t("people.mailNotSent") : t("people.mailSent"));
      }
    } catch (err) {
      if (changed()) return;
      setActionError(failureText(err, lang));
    } finally {
      inFlight.current = false;
      setBusy(null);
    }
  };

  return (
    <div>
      {q.isError && <Alert type="error" showIcon role="alert" message={failureText(q.error, lang)} style={{ marginBlockEnd: 12 }} />}
      {actionError !== null && <Alert type="error" showIcon role="alert" message={actionError} style={{ marginBlockEnd: 12 }} />}
      {notice !== null && <Alert type="success" showIcon role="status" message={notice} style={{ marginBlockEnd: 12 }} />}

      {!firstFailed && q.isPending && <p role="status">{t("app.loading")}</p>}
      {q.isSuccess && people.length === 0 && (
        <p role="status" style={muted}>
          {t("tenant.users.empty")}
        </p>
      )}
      {!firstFailed && people.length > 0 && (
        <ul style={list} aria-label={t("tenant.users.title")}>
          {people.map((p) => {
            const ownerPending = p.role === "owner" && p.status === "pending";
            const canReset = mailConfigured || tenantAccess;
            const canSignIn = tenantAccess && p.status === "active";
            return (
              <li key={p.id} style={row}>
                <div style={rowMain}>
                  <p style={rowTitle}>
                    <bdi>{personLabel(p)}</bdi>
                  </p>
                  <p style={muted}>
                    <bdi>{p.email}</bdi>
                  </p>
                </div>
                <div>{t(`people.role.${p.role}`)}</div>
                <div>
                  <StatusTag tone={statusTone(p.status)}>{t(`people.status.${p.status}`)}</StatusTag>
                </div>
                {(ownerPending || canReset || canSignIn) && (
                  <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
                    {ownerPending && (
                      <>
                        <IconButton label={`${t("tenant.users.resend")}: ${personLabel(p)}`} tip={t("tenant.users.resend")} disabled={busy === p.id} onClick={() => void resend(p, "email")} icon={<Send className="icon-dir" size={18} aria-hidden="true" focusable="false" />} />
                        <IconButton label={`${t("tenant.users.newLink")}: ${personLabel(p)}`} tip={t("tenant.users.newLink")} disabled={busy === p.id} onClick={() => void resend(p, "link")} icon={<Link size={18} aria-hidden="true" focusable="false" />} />
                      </>
                    )}
                    {canReset && (
                      <IconButton label={`${t("tenant.users.reset")}: ${personLabel(p)}`} tip={t("tenant.users.reset")} disabled={busy === p.id} onClick={() => setResetFor(p)} icon={<KeyRound size={18} aria-hidden="true" focusable="false" />} />
                    )}
                    {canSignIn && (
                      <IconButton label={`${t("tenant.users.signInAs")}: ${personLabel(p)}`} tip={t("tenant.users.signInAs")} disabled={busy === p.id} onClick={() => setSignInFor(p)} icon={<LogIn className="icon-dir" size={18} aria-hidden="true" focusable="false" />} />
                    )}
                  </div>
                )}
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
      </Suspense>

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
