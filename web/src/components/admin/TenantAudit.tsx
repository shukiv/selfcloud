// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button, Select } from "antd";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useLang, useT } from "../../lib/i18n";
import { auditLog } from "../../lib/manage/api";
import { AUDIT_ACTIONS } from "../../lib/manage/actions";
import { failureText } from "../../lib/manage/failure";
import { loadNames, NO_NAMES } from "../../lib/manage/names";
import { queryKey } from "../../lib/query";
import { type AuditEntry, type Names } from "../../lib/audit/sentence";
import AuditItem from "../manage/AuditItem";
import { list, more, muted, sectionTitle, toolbar } from "./adminStyles";

type Entry = AuditEntry & { id: string; time: string };

// TenantAudit: the tenant's audit log, newest first, with a filter by action. The entries about a personal folder are
// returned or withheld by the server exactly as it answers them. Names are read so a sentence can name people; without
// them each sentence reads with its generic words.
export default function TenantAudit({ account }: { account: string }) {
  const { t } = useT();
  const lang = useLang();
  const [action, setAction] = useState("");
  const namesQ = useQuery({
    queryKey: queryKey("tenant-names", account),
    queryFn: async () => (await loadNames(account)).names,
    retry: false,
  });
  const names: Names = namesQ.data ?? NO_NAMES;
  const q = useInfiniteQuery({
    queryKey: queryKey("tenant-audit", account, action),
    queryFn: ({ pageParam }) => auditLog(account, { action: action || undefined, cursor: pageParam }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor || undefined,
    refetchOnMount: "always",
  });
  const entries: Entry[] = q.data?.pages.flatMap((p) => p.items) ?? [];
  const firstFailed = q.isError && q.data === undefined;

  return (
    <div>
      <div style={toolbar}>
        <h2 style={sectionTitle}>{t("tenant.audit.title")}</h2>
        <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 8 }}>
          <label htmlFor="tenant-audit-action">{t("audit.filter")}</label>
          <Select
            id="tenant-audit-action"
            value={action}
            onChange={(v: string) => setAction(v)}
            // Every action is in the list: the virtual list would draw only the first few.
            virtual={false}
            style={{ minWidth: 200, maxInlineSize: "100%" }}
            options={[{ value: "", label: t("audit.allActions") }, ...AUDIT_ACTIONS.map((a) => ({ value: a, label: a }))]}
          />
        </div>
      </div>

      {q.isError && <Alert type="error" showIcon role="alert" message={failureText(q.error, lang)} style={{ marginBlockEnd: 12 }} />}
      {!firstFailed && q.isPending && <p role="status">{t("app.loading")}</p>}
      {q.isSuccess && entries.length === 0 && (
        <p role="status" style={muted}>
          {t("audit.empty")}
        </p>
      )}
      {!firstFailed && entries.length > 0 && (
        <ul style={list} aria-label={t("tenant.audit.title")}>
          {entries.map((entry) => (
            <AuditItem key={entry.id} entry={entry} names={names} />
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
    </div>
  );
}
