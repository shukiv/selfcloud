// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button } from "antd";
import { useInfiniteQuery } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { useLang, useT } from "../../lib/i18n";
import { listPeople, resendVerification } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import { personLabel, statusTone } from "../../lib/manage/people";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import type { Person } from "../../lib/manage/types";
import ShowOnce from "./ShowOnce";
import { useOnSessionChange } from "./hooks";
import StatusTag from "./StatusTag";
import { list, more, muted, row, rowMain, rowTitle } from "./adminStyles";

// TenantUsers: the tenant's people, read only. An admin does not manage a tenant's members day to day; it can resend the
// invitation of an owner who has not verified yet, by email or as a new link shown once.
export default function TenantUsers({ account }: { account: string }) {
  const { t } = useT();
  const lang = useLang();
  const inFlight = useRef(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [link, setLink] = useState<{ url: string; who: string } | null>(null);
  useOnSessionChange(() => setLink(null));
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
        setLink({ url: reply.verification.url, who: personLabel(p) });
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
          {people.map((p) => (
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
              {p.role === "owner" && p.status === "pending" && (
                <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
                  <Button size="small" disabled={busy === p.id} onClick={() => void resend(p, "email")}>
                    {t("tenant.users.resend")}
                    <span className="sr-only">: {personLabel(p)}</span>
                  </Button>
                  <Button size="small" disabled={busy === p.id} onClick={() => void resend(p, "link")}>
                    {t("tenant.users.newLink")}
                    <span className="sr-only">: {personLabel(p)}</span>
                  </Button>
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      {!firstFailed && q.hasNextPage && (
        <div style={more}>
          <Button disabled={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
            {t("common.loadMore")}
          </Button>
        </div>
      )}

      {link !== null && (
        <ShowOnce
          id="owner-link"
          title={t("tenant.users.linkTitle", { name: link.who })}
          label={t("tenant.new.linkLabel")}
          value={link.url}
          warning={t("tenant.new.linkWarning")}
          onClosed={() => setLink(null)}
        />
      )}
    </div>
  );
}
