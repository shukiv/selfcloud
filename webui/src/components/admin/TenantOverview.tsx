// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button, Checkbox, Input } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useLang, useT } from "../../lib/i18n";
import { formatBytes, formatDate, formatNumber } from "../../lib/files/format";
import { listSpaces, updateAccount, usage } from "../../lib/manage/api";
import { collectAll } from "../../lib/manage/paging";
import { failureText } from "../../lib/manage/failure";
import { limitBytes, splitLimit, type LimitUnit } from "../../lib/quota";
import { accountStorage } from "../../lib/storage/api";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import type { Account } from "../../lib/manage/types";
import PublicLinksSwitch from "../share/PublicLinksSwitch";
import UsageMeter from "../UsageMeter";
import { fieldset, foot, muted, form as formStyle } from "./adminStyles";
import { useGuard } from "./hooks";

// TenantOverview: the tenant's usage and counts, and its storage limit, which an admin changes here.
export default function TenantOverview({ account, onChanged }: { account: Account; onChanged: (a: Account) => void }) {
  const { t, tp } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const flight = useGuard();
  const overviewKey = queryKey("tenant-overview", account.id);
  const q = useQuery({
    queryKey: overviewKey,
    queryFn: async () => {
      const [u, spaces, st] = await Promise.all([
        usage(account.id),
        collectAll((c) => listSpaces(account.id, c)),
        accountStorage(account.id),
      ]);
      return { figures: u, spaceCount: spaces.items.length, storage: st };
    },
    refetchOnMount: "always",
  });
  const [editing, setEditing] = useState(false);
  const [noLimit, setNoLimit] = useState(false);
  const [amount, setAmount] = useState("");
  const [unit, setUnit] = useState<LimitUnit>("GB");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const startEdit = () => {
    setError(null);
    const none = account.quota_bytes === 0;
    setNoLimit(none);
    if (!none) {
      const s = splitLimit(account.quota_bytes);
      setAmount(s.amount);
      setUnit(s.unit);
    }
    setEditing(true);
  };

  const save = (e: FormEvent) => {
    e.preventDefault();
    let quota = 0;
    if (!noLimit) {
      const bytes = limitBytes(amount, unit);
      if (bytes === null) {
        setError(t("tenant.new.limitInvalid"));
        return;
      }
      quota = bytes;
    }
    void flight(async () => {
      const changed = sessionChanged();
      setBusy(true);
      setError(null);
      try {
        const updated = await updateAccount(account.id, { quota_bytes: quota });
        if (changed()) return;
        setEditing(false);
        onChanged(updated);
        void qc.invalidateQueries({ queryKey: overviewKey });
      } catch (err) {
        if (changed()) return;
        setError(failureText(err, lang));
      } finally {
        setBusy(false);
      }
    });
  };

  const d = q.data;
  return (
    <div style={{ display: "grid", gap: 16 }}>
      {q.isError && <Alert type="error" showIcon role="alert" message={failureText(q.error, lang)} />}
      {error !== null && <Alert type="error" showIcon role="alert" message={error} />}

      {d && (
        <section aria-labelledby="tenant-usage" style={{ display: "grid", gap: 12 }}>
          <h2 id="tenant-usage" style={{ fontSize: 16, fontWeight: 600, margin: 0 }}>
            {t("tenant.overview.usage")}
          </h2>
          <UsageMeter used={d.figures.used_bytes} quota={d.figures.quota_bytes} />
          <p style={muted}>{t("storage.reservedLine", { size: formatBytes(d.storage.account.reserved_bytes, lang) })}</p>
          <p style={muted}>
            {d.storage.account.user_quota_bytes === 0
              ? t("storage.defaultNone")
              : t("storage.defaultLine", { size: formatBytes(d.storage.account.user_quota_bytes, lang) })}
          </p>
          <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
            <p style={{ margin: 0 }}>
              {d.figures.quota_bytes === null
                ? t("tenant.overview.noLimit")
                : t("tenant.overview.limit", { size: formatBytes(d.figures.quota_bytes, lang) })}
            </p>
            {!editing && (
              <Button size="small" onClick={startEdit}>
                {t("tenant.overview.change")}
              </Button>
            )}
          </div>

          {editing && (
            <form style={formStyle} onSubmit={save} noValidate>
              <fieldset style={fieldset}>
                <div style={{ display: "flex", gap: 8 }}>
                  <Input
                    inputMode="decimal"
                    autoComplete="off"
                    value={amount}
                    disabled={noLimit}
                    aria-label={t("tenant.new.limitAmount")}
                    onChange={(e) => setAmount(e.target.value)}
                    style={{ flex: "1 1 auto", minWidth: 0 }}
                  />
                  <select
                    value={unit}
                    disabled={noLimit}
                    aria-label={t("tenant.new.limitUnit")}
                    onChange={(e) => setUnit(e.target.value as LimitUnit)}
                  >
                    <option value="GB">GB</option>
                    <option value="TB">TB</option>
                  </select>
                </div>
                <Checkbox checked={noLimit} onChange={(e) => setNoLimit(e.target.checked)}>
                  {t("tenant.new.noLimit")}
                </Checkbox>
              </fieldset>
              <p style={muted}>{t("tenant.overview.lowerHint")}</p>
              <div style={foot}>
                <Button onClick={() => setEditing(false)}>{t("common.cancel")}</Button>
                <Button type="primary" htmlType="submit" loading={busy} disabled={busy}>
                  {t("tenant.overview.save")}
                </Button>
              </div>
            </form>
          )}

          <dl style={{ display: "grid", gap: 12, gridTemplateColumns: "repeat(auto-fit, minmax(140px, 1fr))", margin: 0 }}>
            <div>
              <dt>{t("tenant.overview.people")}</dt>
              <dd style={{ margin: 0, fontSize: 20 }}>{formatNumber(d.figures.users_active, lang)}</dd>
              <dd style={muted}>{tp("tenant.overview.pending", d.figures.users_pending)}</dd>
            </div>
            <div>
              <dt>{t("tenant.overview.spaces")}</dt>
              <dd style={{ margin: 0, fontSize: 20 }}>{formatNumber(d.spaceCount, lang)}</dd>
            </div>
            <div>
              <dt>{t("tenant.overview.files")}</dt>
              <dd style={{ margin: 0, fontSize: 20 }}>{formatNumber(d.figures.files, lang)}</dd>
            </div>
          </dl>
        </section>
      )}

      <PublicLinksSwitch account={account.id} />

      <dl style={{ display: "grid", gap: 4, margin: 0 }}>
        <div>
          <dt>{t("tenant.overview.created")}</dt>
          <dd style={{ margin: 0 }}>
            <time dateTime={account.created_at}>{formatDate(account.created_at, lang)}</time>
          </dd>
        </div>
        <div>
          <dt>{t("tenant.overview.updated")}</dt>
          <dd style={{ margin: 0 }}>
            <time dateTime={account.updated_at}>{formatDate(account.updated_at, lang)}</time>
          </dd>
        </div>
      </dl>
    </div>
  );
}

