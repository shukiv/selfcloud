// SPDX-License-Identifier: AGPL-3.0-or-later

import type { ReactNode } from "react";
import { useT, useLang } from "../../lib/i18n";
import { formatBytes } from "../../lib/files/format";
import { heldBytes, sidebarMeterLevel, storagePercent, type MeStorage } from "../../lib/storage/logic";
import type { Sidebar } from "../../theme/tokens";

// The bar's colours on the sidebar's blue (decision 66). The accent is the sidebar's own yellow; the warning and the error
// colours keep 3:1 against the track and the sidebar. The track is dark: a yellow fill keeps 3:1 against it.
const WARNING = "#FFA940";
const DANGER = "#FF6B6B";
const TRACK = "rgba(0, 0, 0, 0.35)";

// storageTitle is the row's text: "Storage (61% full)", or "Storage" with no limit to be full of.
export function storageTitle(s: MeStorage | null, t: (key: string, params?: Record<string, string>) => string): string {
  const percent = storagePercent(s);
  return percent === null ? t("nav.storage") : t("nav.storageFull", { percent: String(percent) });
}

// SidebarStorage is the Storage entry of the sidebar: a row like the others, then a slim bar and the figures. With no limit it
// is the row, and the line says what is used, with no bar. A null read (not yet known) shows the row alone. selected says the
// row is the current page's: it is filled with the active colour and its text takes the on-active colour (decision 72).
// The figures, the track and the fill are the same whether it is selected or not.
export default function SidebarStorage({ my, sidebar, selected = false }: { my: MeStorage | null; sidebar: Sidebar; selected?: boolean }): ReactNode {
  const { t } = useT();
  const lang = useLang();
  const held = my ? heldBytes(my) : 0;
  const quota = my?.quota_bytes ?? null;
  const level = sidebarMeterLevel(held, quota);
  const has = quota !== null && quota > 0;
  // The bar and the text take the same figure (storagePercent), so 99.6% reads 99% and the bar is not full.
  const width = has ? (storagePercent(my) ?? 0) : 0;
  const colour = level === "danger" ? DANGER : level === "warning" ? WARNING : sidebar.active;
  // A selected row is filled with the active colour (decision 72), so its text and figures take the on-active colour.
  const ink = sidebar.onActive;
  const line = has
    ? t("usage.ofLimit", { used: formatBytes(held, lang), limit: formatBytes(quota, lang) })
    : t("nav.storageUsed", { used: formatBytes(held, lang) });
  return (
    <span className="shell-storage" style={{ display: "grid", gap: 4, minInlineSize: 0, color: selected ? ink : sidebar.text }}>
      <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{storageTitle(my, t)}</span>
      {my !== null && (
        <>
          {has && (
            <span
              aria-hidden="true"
              data-testid="sidebar-meter"
              data-level={level}
              style={{
                display: "block",
                blockSize: 6,
                borderRadius: 3,
                background: TRACK,
                overflow: "hidden",
                boxShadow: `inset 0 0 0 1px ${sidebar.caption}`,
              }}
            >
              <span style={{ display: "block", blockSize: "100%", inlineSize: `${width}%`, background: colour }} />
            </span>
          )}
          <span style={{ fontSize: 12, color: selected ? sidebar.onActive : sidebar.caption, fontVariantNumeric: "tabular-nums", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {line}
          </span>
        </>
      )}
    </span>
  );
}
