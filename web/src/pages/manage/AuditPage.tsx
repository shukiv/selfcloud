// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { Alert, Button, Select } from "antd";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useLang, useT } from "../../lib/i18n";
import { useAdminText } from "../../lib/manage/adminText";
import { auditLog } from "../../lib/manage/api";
import { AUDIT_ACTIONS } from "../../lib/manage/actions";
import { failureText } from "../../lib/manage/failure";
import { loadNames, NO_NAMES } from "../../lib/manage/names";
import { queryKey } from "../../lib/query";
import { auditColumns, auditSaid, type AuditRow } from "../../components/manage/auditColumns";
import Listing from "../../components/files/Listing";
import { OwnerOnly, useAccountID, useManageTitle } from "../../components/manage/OwnerPage";

// The audit log: what changed in the account, newest first. The server hides an entry about a personal folder from
// everyone but its user, and this page shows the list exactly as the server answers it.
function AuditView() {
  const { t } = useT();
  const adminText = useAdminText();
  const lang = useLang();
  const account = useAccountID();
  useManageTitle("manage.audit.title");
  const [action, setAction] = useState("");

  const q = useInfiniteQuery({
    queryKey: queryKey("manage", account, "audit", action),
    queryFn: ({ pageParam }) => auditLog(account, { action: action || undefined, cursor: pageParam }),
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
  const said = (e: AuditRow) => auditSaid(e, lookup, lang);

  return (
    <div>
      <div style={{ display: "flex", flexWrap: "wrap", justifyContent: "space-between", alignItems: "center", gap: 8, marginBlockEnd: 16 }}>
        <h1 className="page-title" style={{ fontSize: 20, fontWeight: 600, margin: 0 }}>
          {t("manage.audit.title")}
        </h1>
        <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 8 }}>
          <label htmlFor="audit-action">{t("audit.filter")}</label>
          <Select
            id="audit-action"
            value={action}
            onChange={(v: string) => setAction(v)}
            style={{ minWidth: 220 }}
            options={[{ value: "", label: t("audit.allActions") }, ...AUDIT_ACTIONS.map((a) => ({ value: a, label: a }))]}
          />
        </div>
      </div>

      {namesPartial && (
        <p role="note" style={{ margin: "0 0 12px", color: "var(--ant-color-text-secondary)" }}>
          {t("audit.namesPartial")}
        </p>
      )}
      {failure !== null && <Alert type={loaded ? "warning" : "error"} showIcon role="alert" message={failure} style={{ marginBottom: 12 }} />}

      {!loaded && q.isPending && account !== "" && <p role="status">{t("app.loading")}</p>}

      {/* Newest first: the When column says so with aria-sort and no button (decision 122). The table keeps its headers when empty. */}
      {loaded && (
        <Listing
          label={t("manage.audit.title")}
          items={entries}
          rowLabel={said}
          columns={auditColumns(t, lang, said)}
          // The audit log has no create action.
          empty={{ title: t("audit.empty"), description: adminText ? t("admin.empty.audit") : undefined }}
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
