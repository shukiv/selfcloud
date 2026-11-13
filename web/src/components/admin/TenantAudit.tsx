// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button, Select } from "antd";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Download } from "lucide-react";
import { useState } from "react";
import { useLang, useT } from "../../lib/i18n";
import { auditLog } from "../../lib/manage/api";
import { AUDIT_ACTIONS } from "../../lib/manage/actions";
import { auditRange, downloadAuditExport, rangeInvalid, type AuditRange } from "../../lib/manage/auditRange";
import { failureText } from "../../lib/manage/failure";
import { loadNames, NO_NAMES } from "../../lib/manage/names";
import { queryKey } from "../../lib/query";
import { type AuditEntry, type Names } from "../../lib/audit/sentence";
import IconButton from "../IconButton";
import Listing from "../files/Listing";
import { auditColumns, auditSaid } from "../manage/auditColumns";
import { dateField, dateInput, fieldError, more, sectionTitle, toolbar } from "./adminStyles";
import { useAdminText } from "../../lib/manage/adminText";

type Entry = AuditEntry & { id: string; time: string };

// TenantAudit: the tenant's audit log, newest first, with a filter by action. The entries about a personal folder are
// returned or withheld by the server exactly as it answers them. Names are read so a sentence can name people; without
// them each sentence reads with its generic words.
export default function TenantAudit({ account }: { account: string }) {
  const { t } = useT();
  const lang = useLang();
  const [action, setAction] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [range, setRange] = useState<AuditRange>({});
  const invalid = rangeInvalid(from, to);
  const chooseFrom = (next: string) => {
    setFrom(next);
    if (!rangeInvalid(next, to)) setRange(auditRange(next, to));
  };
  const chooseTo = (next: string) => {
    setTo(next);
    if (!rangeInvalid(from, next)) setRange(auditRange(from, next));
  };
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
  const said = (e: Entry) => auditSaid(e, names, lang);

  return (
    <div>
      <div style={toolbar}>
        <h2 style={sectionTitle}>{t("tenant.audit.title")}</h2>
        <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 8 }}>
          <div style={dateField}>
            <label htmlFor="tenant-audit-from">{t("audit.from")}</label>
            <input
              id="tenant-audit-from"
              type="date"
              value={from}
              onChange={(e) => chooseFrom(e.target.value)}
              aria-invalid={invalid || undefined}
              aria-describedby={invalid ? "tenant-audit-range-error" : undefined}
              style={dateInput}
            />
          </div>
          <div style={dateField}>
            <label htmlFor="tenant-audit-to">{t("audit.to")}</label>
            <input
              id="tenant-audit-to"
              type="date"
              value={to}
              onChange={(e) => chooseTo(e.target.value)}
              aria-invalid={invalid || undefined}
              aria-describedby={invalid ? "tenant-audit-range-error" : undefined}
              style={dateInput}
            />
          </div>
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
          {loaded && (
            <IconButton
              label={t("audit.export")}
              icon={<Download size={18} />}
              disabled={invalid}
              onClick={() => downloadAuditExport(account, { ...range, action: action || undefined })}
            />
          )}
          {invalid && (
            <p id="tenant-audit-range-error" role="alert" style={fieldError}>
              {t("audit.rangeInvalid")}
            </p>
          )}
        </div>
      </div>

      {q.isError && <Alert type="error" showIcon role="alert" message={failureText(q.error, lang)} style={{ marginBlockEnd: 12 }} />}
      {!firstFailed && q.isPending && <p role="status">{t("app.loading")}</p>}
      {/* Newest first, so the When column says so with aria-sort and no button. The event column is the
          sentence and its code, named Action as the filter above it is. */}
      {!firstFailed && q.data !== undefined && (
        <Listing
          label={t("tenant.audit.title")}
          items={entries}
          rowLabel={said}
          columns={auditColumns(t, lang, said, { whenSecondary: false })}
          // The audit log has no create action.
          empty={{ title: t("audit.empty"), description: adminText ? t("admin.empty.audit") : undefined }}
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
