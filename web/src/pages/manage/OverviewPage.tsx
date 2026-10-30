// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert } from "antd";
import { useQuery } from "@tanstack/react-query";
import { useLang, useT } from "../../lib/i18n";
import { formatNumber } from "../../lib/files/format";
import { auditLog, listSpaces, usage } from "../../lib/manage/api";
import { collectAll } from "../../lib/manage/paging";
import { loadNames } from "../../lib/manage/names";
import { failureText } from "../../lib/manage/failure";
import { queryKey } from "../../lib/query";
import type { AuditEntry, Names } from "../../lib/audit/sentence";
import type { Usage } from "../../lib/manage/types";
import UsageMeter from "../../components/UsageMeter";
import AuditItem from "../../components/manage/AuditItem";
import PublicLinksSwitch from "../../components/share/PublicLinksSwitch";
import { OwnerOnly, useAccountID, useManageTitle } from "../../components/manage/OwnerPage";

// Overview: the account's usage and people, its recent activity, and the next steps while it is new.
interface Overview {
  usage: Usage;
  spaceCount: number;
  activity: Array<AuditEntry & { id: string; time: string }>;
  names: Names;
}

async function readOverview(account: string): Promise<Overview> {
  const [u, spaces, log, looked] = await Promise.all([
    usage(account),
    collectAll((c) => listSpaces(account, c)),
    auditLog(account, { limit: 10 }),
    loadNames(account),
  ]);
  return { usage: u, spaceCount: spaces.items.length, activity: log.items, names: looked.names };
}

function OverviewView() {
  const { t, tp } = useT();
  const lang = useLang();
  const account = useAccountID();
  useManageTitle("manage.overview.title");

  const q = useQuery({
    queryKey: queryKey("manage", account, "overview"),
    queryFn: () => readOverview(account),
    enabled: account !== "",
    refetchOnMount: "always",
  });
  const data = q.data;
  const people = data ? data.usage.users_active + data.usage.users_pending : 0;
  const fresh = data !== undefined && people <= 1 && data.spaceCount === 0;

  return (
    <div>
      <h1 className="page-title" style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>
        {t("manage.overview.title")}
      </h1>

      {q.isError && (
        <Alert type={data ? "warning" : "error"} showIcon role="alert" message={failureText(q.error, lang)} style={{ marginBottom: 12 }} />
      )}

      {q.isPending && account !== "" && <p role="status">{t("app.loading")}</p>}

      {data && (
        <>
          <section aria-labelledby="usage-title" style={{ marginBlockEnd: 24 }}>
            <h2 id="usage-title" style={{ fontSize: 16, fontWeight: 600, margin: "0 0 8px" }}>
              {t("manage.overview.usage")}
            </h2>
            <UsageMeter used={data.usage.used_bytes} quota={data.usage.quota_bytes} />
          </section>

          <dl style={{ display: "grid", gap: 12, gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))", margin: 0 }}>
            <div>
              <dt>{t("manage.overview.people")}</dt>
              <dd style={{ margin: 0, fontSize: 20 }}>{formatNumber(data.usage.users_active, lang)}</dd>
              <dd style={{ margin: 0, color: "var(--ant-color-text-secondary)" }}>
                {tp("manage.overview.pending", data.usage.users_pending)}
              </dd>
            </div>
            <div>
              <dt>{t("manage.overview.spaces")}</dt>
              <dd style={{ margin: 0, fontSize: 20 }}>{formatNumber(data.spaceCount, lang)}</dd>
            </div>
            <div>
              <dt>{t("manage.overview.files")}</dt>
              <dd style={{ margin: 0, fontSize: 20 }}>{formatNumber(data.usage.files, lang)}</dd>
            </div>
          </dl>

          {account !== "" && (
            <>
              <div style={{ marginBlockStart: 16 }}>
                <PublicLinksSwitch account={account} />
              </div>
              <p style={{ margin: "8px 0", fontSize: 14 }}>
                <a href="/manage/links">{t("links.openList")}</a>
              </p>
            </>
          )}

          {fresh && (
            <section aria-labelledby="next-title" style={{ marginBlockStart: 24 }}>
              <h2 id="next-title" style={{ fontSize: 16, fontWeight: 600, margin: "0 0 8px" }}>
                {t("manage.overview.next")}
              </h2>
              <p style={{ display: "flex", flexWrap: "wrap", gap: 8, margin: 0 }}>
                <a className="btn primary" href="/manage/people">{t("manage.overview.inviteFirst")}</a>
                <a className="btn secondary" href="/manage/spaces">{t("manage.overview.createSpace")}</a>
              </p>
            </section>
          )}

          <section aria-labelledby="activity-title" style={{ marginBlockStart: 24 }}>
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline", gap: 8 }}>
              <h2 id="activity-title" style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>
                {t("manage.overview.activity")}
              </h2>
              <a href="/manage/audit">{t("manage.overview.fullLog")}</a>
            </div>
            {data.activity.length === 0 ? (
              <p role="status" style={{ color: "var(--ant-color-text-secondary)" }}>{t("manage.overview.noActivity")}</p>
            ) : (
              <ul style={{ margin: 0, padding: 0, listStyle: "none" }}>
                {data.activity.map((entry) => (
                  <AuditItem key={entry.id} entry={entry} names={data.names} />
                ))}
              </ul>
            )}
          </section>
        </>
      )}
    </div>
  );
}

export default function OverviewPage() {
  return (
    <OwnerOnly section="overview">
      <OverviewView />
    </OwnerOnly>
  );
}
