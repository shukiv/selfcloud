// SPDX-License-Identifier: AGPL-3.0-or-later

import type { ReactNode } from "react";
import { useT, useLang } from "../../lib/i18n";
import { formatBytes } from "../../lib/files/format";
import { heldBytes, sidebarMeterLevel, storagePercent, type MeStorage } from "../../lib/storage/logic";
import type { Sidebar } from "../../theme/tokens";

// The bar's colours on the sidebar's blue (decision 66). The accent is the sidebar's own yellow; the warning and the error
// colours keep 3:1 against the track and the sidebar, and the track is dark, so the bar reads in light and in dark alike.
const WARNING = "#FFA940";
const DANGER = "#FF6B6B";
const TRACK = "rgba(0, 0, 0, 0.35)";

// On the selected row (the yellow pill) the on-active colour is the text, the figures at 75% of it (5.9:1 on the yellow), and the
// track is it at 80% (6.7:1 on the yellow), with a 1px ring of it outside the track, so the bar's edge reads on the yellow and
// the yellow fill is outlined. The accent fill stays yellow: against that track it is 6.7:1, and the warning and danger fills are
// 5.3:1 and 3.7:1.
const LINE_ON_PILL = 0.75;
const TRACK_ON_PILL = 0.8;

// withAlpha is the hex colour of a token at an alpha, as an rgba() string (the token stays the one source of the colour).
function withAlpha(hex: string, alpha: number): string {
  const n = parseInt(hex.slice(1), 16);
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`;
}

// storageTitle is the row's text: "Storage (61% full)", or "Storage" with no limit to be full of.
export function storageTitle(s: MeStorage | null, t: (key: string, params?: Record<string, string>) => string): string {
  const percent = storagePercent(s);
  return percent === null ? t("nav.storage") : t("nav.storageFull", { percent: String(percent) });
}

// SidebarStorage is the Storage entry of the sidebar: a row like the others, then a slim bar and the figures. With no limit it
// is the row, and the line says what is used, with no bar. A null read (not yet known) shows the row alone. selected says the
// row is the current page's (the yellow pill): its text and track then take the on-active colours.
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
  const line = has
    ? t("usage.ofLimit", { used: formatBytes(held, lang), limit: formatBytes(quota, lang) })
    : t("nav.storageUsed", { used: formatBytes(held, lang) });
  return (
    <span className="shell-storage" style={{ display: "grid", gap: 4, minInlineSize: 0, color: selected ? sidebar.onActive : sidebar.text }}>
      <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: selected ? sidebar.onActive : undefined }}>{storageTitle(my, t)}</span>
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
                background: selected ? withAlpha(sidebar.onActive, TRACK_ON_PILL) : TRACK,
                overflow: "hidden",
                // On the yellow the ring is outside the track: an inset ring would sit under the yellow fill, which is the row's own colour.
                boxShadow: selected ? `0 0 0 1px ${sidebar.onActive}` : `inset 0 0 0 1px ${sidebar.caption}`,
              }}
            >
              <span style={{ display: "block", blockSize: "100%", inlineSize: `${width}%`, background: colour }} />
            </span>
          )}
          <span style={{ fontSize: 12, color: selected ? withAlpha(sidebar.onActive, LINE_ON_PILL) : sidebar.caption, fontVariantNumeric: "tabular-nums", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {line}
          </span>
        </>
      )}
    </span>
  );
}
