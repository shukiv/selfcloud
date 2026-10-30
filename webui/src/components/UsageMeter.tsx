// SPDX-License-Identifier: AGPL-3.0-or-later

import { useT, useLang } from "../lib/i18n";
import { formatBytes } from "../lib/files/format";

// UsageMeter shows how much of a quota is used. It turns warning at 80% and danger at 95%, and the text says so too.
// A null quota is no limit: the meter then shows the size with no bar.
export default function UsageMeter({ used, quota }: { used: number; quota: number | null }) {
  const { t } = useT();
  const lang = useLang();
  const has = quota !== null && quota > 0;
  const share = has ? Math.min(1, used / quota) : 0;
  const level = !has ? "none" : share >= 0.95 ? "danger" : share >= 0.8 ? "warning" : "ok";
  const text = has
    ? t("usage.ofLimit", { used: formatBytes(used, lang), limit: formatBytes(quota, lang) })
    : t("usage.noLimit", { used: formatBytes(used, lang) });
  const state = level === "danger" ? t("usage.danger") : level === "warning" ? t("usage.warning") : "";
  const color = level === "danger" ? "var(--ant-color-error)" : level === "warning" ? "var(--ant-color-warning)" : "var(--ant-color-primary)";
  return (
    <div style={{ display: "grid", gap: 8 }}>
      <p style={{ margin: 0, display: "flex", flexWrap: "wrap", gap: 8, justifyContent: "space-between", fontVariantNumeric: "tabular-nums" }}>
        <span>{text}</span>
        {state !== "" && <span style={{ fontWeight: 600, color }}>{state}</span>}
      </p>
      {has && (
        <div
          role="meter"
          aria-valuemin={0}
          aria-valuemax={quota}
          aria-valuenow={Math.min(used, quota)}
          aria-label={t("usage.label")}
          style={{ blockSize: 8, borderRadius: 999, overflow: "hidden", background: "var(--ant-color-fill-quaternary)" }}
        >
          <span style={{ display: "block", blockSize: "100%", inlineSize: `${Math.round(share * 100)}%`, background: color }} />
        </div>
      )}
    </div>
  );
}
