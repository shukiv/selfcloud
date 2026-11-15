// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { Alert, Button, Select, Skeleton } from "antd";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useLang, useT } from "../../lib/i18n";
import { useAT } from "../../lib/audit/i18n";
import { useAdminText } from "../../lib/manage/adminText";
import { auditLog } from "../../lib/manage/api";
import { AUDIT_ACTIONS } from "../../lib/manage/actions";
import type { AuditRange } from "../../lib/manage/auditRange";
import { failureText } from "../../lib/manage/failure";
import { loadNames, NO_NAMES } from "../../lib/manage/names";
import { queryKey } from "../../lib/query";
import { auditColumns, auditSaid, type AuditRow } from "../../components/manage/auditColumns";
import AuditRangeBar from "../../components/manage/AuditRangeBar";
import AuditTextFailure from "../../components/manage/AuditTextFailure";
import Listing from "../../components/files/Listing";
import { OwnerOnly, useAccountID, useManageTitle } from "../../components/manage/OwnerPage";

// The audit log: what changed in the account, newest first. The server hides an entry about a personal folder from
// everyone but its user, and this page shows the list exactly as the server answers it.
function AuditView() {
  const { t } = useT();
  const { ready: auditReady, failed: auditFailed, t: at } = useAT();
  const adminText = useAdminText();
  const lang = useLang();
  const account = useAccountID();
  useManageTitle("manage.audit.title");
  const [action, setAction] = useState("");
  const [range, setRange] = useState<AuditRange>({});

  const q = useInfiniteQuery({
    queryKey: queryKey("manage", account, "audit", action, range.since, range.until),
    queryFn: ({ pageParam }) => auditLog(account, { action: action || undefined, since: range.since, until: range.until, cursor: pageParam }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: account !== "",
    refetchOnMount: "always",
  });
  // The names only make the sentences readable. When they cannot load, the page says so and the sentences use generic words.
  const names = useQuery({
    queryKey: queryKey("manage", account, "names"),
    queryFn: () => loadNames(account),
    enabled: account !== "",
    refetchOnMount: "always",
  });
  const lookup = names.data?.names ?? NO_NAMES;
  const namesPartial = names.isError || (names.data !== undefined && !names.data.complete);

  const entries: AuditRow[] = q.data?.pages.flatMap((p) => p.items) ?? [];
  const failure = q.isError ? failureText(q.error, lang) : null;
  const loaded = q.data !== undefined;
  const said = (e: AuditRow) => auditSaid(e, lookup, t, at);

  // The sentences' catalogue did not load: the main catalogue says so, and Retry asks again (as the media page does).
  if (auditFailed) return <AuditTextFailure />;

  return (
    <div>
      <div style={{ display: "flex", flexWrap: "wrap", justifyContent: "space-between", alignItems: "center", gap: 8, marginBlockEnd: 16 }}>
        <h1 className="page-title" style={{ fontSize: 20, fontWeight: 600, margin: 0 }}>
          {t("manage.audit.title")}
        </h1>
        <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 8 }}>
          <label htmlFor="audit-action">{auditReady ? at("audit.filter") : null}</label>
          <Select
            id="audit-action"
            value={action}
            onChange={(v: string) => setAction(v)}
            style={{ minWidth: 220 }}
            // The options wait for the catalogue: the first one's word is its.
            options={auditReady ? [{ value: "", label: at("audit.allActions") }, ...AUDIT_ACTIONS.map((a) => ({ value: a, label: a }))] : []}
          />
          <AuditRangeBar account={account} action={action} idPrefix="audit" loaded={loaded} onRange={setRange} />
        </div>
      </div>

      {namesPartial && (
        <p role="note" style={{ margin: "0 0 12px", color: "var(--ant-color-text-secondary)" }}>
          {at("audit.namesPartial")}
        </p>
      )}
      {failure !== null && <Alert type={loaded ? "warning" : "error"} showIcon role="alert" message={failure} style={{ marginBottom: 12 }} />}

      {/* The sentences wait for their catalogue as the entries wait for the server: one skeleton for both, and the
          table only when both are there. Newest first: the When column says so with aria-sort and no button (decision 122). */}
      {!(q.isError && !loaded) && (!auditReady || (!loaded && q.isPending && account !== "")) && (
        <div role="status" aria-busy="true">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton active paragraph={{ rows: 4 }} />
        </div>
      )}

      {/* The table keeps its headers when empty. */}
      {auditReady && loaded && (
        <Listing
          label={t("manage.audit.title")}
          items={entries}
          rowLabel={said}
          columns={auditColumns(at, lang, said, { whenSecondary: true })}
          // The audit log has no create action.
          empty={{ title: at("audit.empty"), description: adminText ? t("admin.empty.audit") : undefined }}
        />
      )}

      {q.hasNextPage && (
        <div style={{ display: "flex", justifyContent: "center", marginBlockStart: 16 }}>
          <Button disabled={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
            {t("common.loadMore")}
          </Button>
        </div>
      )}
    </div>
  );
}

export default function AuditPage() {
  return (
    <OwnerOnly section="audit">
      <AuditView />
    </OwnerOnly>
  );
}
