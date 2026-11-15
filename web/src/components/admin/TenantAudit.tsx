// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button, Select, Skeleton } from "antd";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useLang, useT } from "../../lib/i18n";
import { useAT } from "../../lib/audit/i18n";
import { auditLog } from "../../lib/manage/api";
import { AUDIT_ACTIONS } from "../../lib/manage/actions";
import type { AuditRange } from "../../lib/manage/auditRange";
import { failureText } from "../../lib/manage/failure";
import { loadNames, NO_NAMES } from "../../lib/manage/names";
import { queryKey } from "../../lib/query";
import { type AuditEntry, type Names } from "../../lib/audit/sentence";
import Listing from "../files/Listing";
import { auditColumns, auditSaid } from "../manage/auditColumns";
import AuditRangeBar from "../manage/AuditRangeBar";
import AuditTextFailure from "../manage/AuditTextFailure";
import { more, sectionTitle, toolbar } from "./adminStyles";
import { useAdminText } from "../../lib/manage/adminText";

type Entry = AuditEntry & { id: string; time: string };

// TenantAudit: the tenant's audit log, newest first, with a filter by action. The entries about a personal folder are
// returned or withheld by the server exactly as it answers them. Names are read so a sentence can name people; without
// them each sentence reads with its generic words.
export default function TenantAudit({ account }: { account: string }) {
  const { t } = useT();
  const { ready: auditReady, failed: auditFailed, t: at } = useAT();
  const lang = useLang();
  const [action, setAction] = useState("");
  const [range, setRange] = useState<AuditRange>({});
  const adminText = useAdminText();
  const namesQ = useQuery({
    queryKey: queryKey("tenant-names", account),
    queryFn: async () => (await loadNames(account)).names,
    retry: false,
  });
  const names: Names = namesQ.data ?? NO_NAMES;
  const q = useInfiniteQuery({
    queryKey: queryKey("tenant-audit", account, action, range.since, range.until),
    queryFn: ({ pageParam }) => auditLog(account, { action: action || undefined, since: range.since, until: range.until, cursor: pageParam }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor || undefined,
    refetchOnMount: "always",
  });
  const entries: Entry[] = q.data?.pages.flatMap((p) => p.items) ?? [];
  const firstFailed = q.isError && q.data === undefined;
  const loaded = q.data !== undefined;
  // An entry's sentence, as text: the Event cell shows it, and a screen reader hears the row by it.
  const said = (e: Entry) => auditSaid(e, names, t, at);

  // The sentences' catalogue did not load: the main catalogue says so, and Retry asks again (as the media page does).
  if (auditFailed) return <AuditTextFailure />;

  return (
    <div>
      <div style={toolbar}>
        <h2 style={sectionTitle}>{t("tenant.audit.title")}</h2>
        <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 8 }}>
          <label htmlFor="tenant-audit-action">{auditReady ? at("audit.filter") : null}</label>
          <Select
            id="tenant-audit-action"
            value={action}
            onChange={(v: string) => setAction(v)}
            // Every action is in the list: the virtual list would draw only the first few.
            virtual={false}
            style={{ minWidth: 200, maxInlineSize: "100%" }}
            // The options wait for the catalogue: the first one's word is its.
            options={auditReady ? [{ value: "", label: at("audit.allActions") }, ...AUDIT_ACTIONS.map((a) => ({ value: a, label: a }))] : []}
          />
          <AuditRangeBar account={account} action={action} idPrefix="tenant-audit" loaded={loaded} onRange={setRange} />
        </div>
      </div>

      {q.isError && <Alert type="error" showIcon role="alert" message={failureText(q.error, lang)} style={{ marginBlockEnd: 12 }} />}
      {/* The sentences wait for their catalogue as the entries wait for the server: one skeleton for both. */}
      {!firstFailed && (!auditReady || q.isPending) && (
        <div role="status" aria-busy="true">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton active paragraph={{ rows: 4 }} />
        </div>
      )}
      {/* Newest first, so the When column says so with aria-sort and no button. The event column is the
          sentence and its code, named Action as the filter above it is. */}
      {!firstFailed && auditReady && q.data !== undefined && (
        <Listing
          label={t("tenant.audit.title")}
          items={entries}
          rowLabel={said}
          columns={auditColumns(at, lang, said, { whenSecondary: false })}
          // The audit log has no create action.
          empty={{ title: at("audit.empty"), description: adminText ? t("admin.empty.audit") : undefined }}
        />
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
