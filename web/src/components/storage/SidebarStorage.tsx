// SPDX-License-Identifier: AGPL-3.0-or-later

import type { ReactNode } from "react";
import { Tooltip, theme } from "antd";
import { Cloud } from "lucide-react";
import { Link } from "react-router-dom";
import { useT, useLang } from "../../lib/i18n";
import { formatBytes } from "../../lib/files/format";
import { heldBytes, sidebarMeterLevel, storagePercent, type MeStorage } from "../../lib/storage/logic";
import { RADIUS, type Sidebar } from "../../theme/tokens";

// The bar's colours on the sidebar's block (decisions 66, 171 and 185). The accent is the sidebar's own yellow; the warning and the
// error colours keep 3:1 against the track. The track is the meter's own dark (decision 66): the fill is compared with the
// unfilled track, so it keeps 3:1 against it in every brand. On the Storage page the block is filled with the active colour, and
// the normal fill takes the on-active ink instead, so it keeps 3:1 against the track on the fill too.
const WARNING = "#FFA940";
const DANGER = "#FF6B6B";
const TRACK = "rgba(0, 0, 0, 0.35)";

// storageTitle is the block's title and the folded icon's name and tooltip: "Storage (61% full)", or "Storage" with no limit to be
// full of.
export function storageTitle(s: MeStorage | null, t: (key: string, params?: Record<string, string>) => string): string {
  const percent = storagePercent(s);
  return percent === null ? t("nav.storage") : t("nav.storageFull", { percent: String(percent) });
}

// SidebarStorage is the storage block pinned at the bottom of the sidebar (decision 181's pin, decision 185's look): a sidebar
// row, not a card. It takes the rows' inline margin, radius and hover tint, with no border and no background of its own. Line 1 is
// a Cloud icon in the rows' icon column and the storage title in the rows' label column; line 2 the bar, only with a limit, from the
// icon's start edge to a menu row's padding short of the block's end edge; line 3 the figures in the caption colour. With no limit there
// is no bar. A null read (not yet known) shows the title alone. selected says the Storage page is the current place: the stylesheet fills the block
// with the active colour as the selected menu row is, and the title, the figures and the bar's outline and normal fill take the
// on-active ink. onNavigate closes the phone drawer when the block opens the page.
export default function SidebarStorage({ my, sidebar, inline, selected = false, onNavigate }: { my: MeStorage | null; sidebar: Sidebar; inline: number; selected?: boolean; onNavigate?: () => void }): ReactNode {
  const { t } = useT();
  const lang = useLang();
  const { token } = theme.useToken();
  const held = my ? heldBytes(my) : 0;
  const quota = my?.quota_bytes ?? null;
  const level = sidebarMeterLevel(held, quota);
  const has = quota !== null && quota > 0;
  // The bar and the text take the same figure (storagePercent), so 99.6% reads 99% and the bar is not full.
  const width = has ? (storagePercent(my) ?? 0) : 0;
  const colour = level === "danger" ? DANGER : level === "warning" ? WARNING : selected ? sidebar.onActive : sidebar.active;
  const line = has
    ? t("usage.ofLimit", { used: formatBytes(held, lang), limit: formatBytes(quota, lang) })
    : t("nav.storageUsed", { used: formatBytes(held, lang) });
  return (
    <Link
      to="/storage"
      className="shell-storage-card"
      data-testid="storage-card"
      aria-current={selected ? "page" : undefined}
      onClick={onNavigate}
      style={{
        display: "grid",
        gap: 4,
        minInlineSize: 0,
        marginInline: inline,
        marginBlockStart: 8,
        paddingBlock: 8,
        // The content starts in the rows' icon column (the shell sets --sc-icon-col from the block's own start edge), so the icon,
        // the bar and the figures all start where the rows' icons start.
        paddingInlineStart: "var(--sc-icon-col, 24px)",
        // The content ends one menu-row inline padding short of the block's end edge (antd's Menu uses token.padding for its rows'
        // itemPaddingInline), so the bar, the title and the figures do not run into the block's end edge and its hover/active fill.
        paddingInlineEnd: token.padding,
        borderRadius: RADIUS,
        color: selected ? sidebar.onActive : sidebar.text,
        textDecoration: "none",
        // The background, the hover tint and the current place's fill are read by the stylesheet (index.css) from the sidebar's
        // variables, so no inline background wins over them.
      }}
    >
      <span style={{ display: "flex", alignItems: "center", gap: 10, minInlineSize: 0 }}>
        <Cloud size={18} strokeWidth={1.75} aria-hidden="true" focusable="false" style={{ flex: "none" }} />
        <span className="shell-storage-title" style={{ fontSize: 14, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {storageTitle(my, t)}
        </span>
      </span>
      {my !== null && (
        <>
          {has && (
            <span
              aria-hidden="true"
              data-testid="sidebar-meter"
              data-level={level}
              style={{
                display: "block",
                blockSize: 4,
                borderRadius: 2,
                background: TRACK,
                overflow: "hidden",
                boxShadow: `inset 0 0 0 1px ${selected ? sidebar.onActive : sidebar.caption}`,
              }}
            >
              <span style={{ display: "block", blockSize: "100%", inlineSize: `${width}%`, background: colour }} />
            </span>
          )}
          <span className="shell-storage-line" style={{ display: "block", fontSize: 12, color: selected ? sidebar.onActive : sidebar.caption, fontVariantNumeric: "tabular-nums", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {line}
          </span>
        </>
      )}
    </Link>
  );
}

// StorageRail is the folded rail's storage icon link (decision 181): one Cloud icon at the bottom of the rail (decision 185), named
// by the storage title, which its tooltip shows. On the Storage page it is the current place: aria-current page, and the stylesheet
// fills it with the active colour as the rail's menu rows are (decision 72). onNavigate closes the drawer (the rail is a wide window's).
export function StorageRail({ my, sidebar, selected = false, onNavigate }: { my: MeStorage | null; sidebar: Sidebar; selected?: boolean; onNavigate?: () => void }): ReactNode {
  const { t } = useT();
  const title = storageTitle(my, t);
  return (
    <Tooltip title={title} placement="right" trigger={["hover", "focus"]}>
      <Link
        to="/storage"
        className="shell-storage-rail"
        data-testid="storage-rail"
        aria-label={title}
        aria-current={selected ? "page" : undefined}
        onClick={onNavigate}
        style={{ color: sidebar.text }}
      >
        <Cloud size={18} strokeWidth={1.75} aria-hidden="true" focusable="false" />
      </Link>
    </Tooltip>
  );
}
