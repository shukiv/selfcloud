// SPDX-License-Identifier: AGPL-3.0-or-later

import type { CSSProperties, ReactNode } from "react";
import { Tooltip } from "antd";
import { HardDrive } from "lucide-react";
import { Link } from "react-router-dom";
import { useT, useLang } from "../../lib/i18n";
import { formatBytes } from "../../lib/files/format";
import { heldBytes, sidebarMeterLevel, storagePercent, type MeStorage } from "../../lib/storage/logic";
import type { Sidebar } from "../../theme/tokens";
import { currentMark } from "../../theme/currentMark";

// The bar's colours on the sidebar's card (decisions 66 and 171). The accent is the sidebar's own yellow; the warning and the
// error colours keep 3:1 against the track. The track is the meter's own dark (decision 66): the fill is compared with the
// unfilled track, so it keeps 3:1 against it in every brand.
const WARNING = "#FFA940";
const DANGER = "#FF6B6B";
const TRACK = "rgba(0, 0, 0, 0.35)";

// storageTitle is the folded icon's name and tooltip: "Storage (61% full)", or "Storage" with no limit to be full of.
export function storageTitle(s: MeStorage | null, t: (key: string, params?: Record<string, string>) => string): string {
  const percent = storagePercent(s);
  return percent === null ? t("nav.storage") : t("nav.storageFull", { percent: String(percent) });
}

// cardHover is the card's hover tint (decision 171): the card's white with a little more of it, so the hover lightens the card
// without leaving the sidebar's own colours. A card colour that is not the white tint keeps itself.
function cardHover(card: string): string {
  const m = /^rgba\(255, 255, 255, ([0-9.]+)\)$/.exec(card);
  if (!m) return card;
  return `rgba(255, 255, 255, ${Math.min(1, Math.round((Number(m[1]) + 0.06) * 100) / 100)})`;
}

// SidebarStorage is the storage card pinned at the bottom of the sidebar (decision 171): the caption, a slim bar and the figures,
// one link to the Storage page. With no limit it is the caption and the line that says what is used, with no bar. A null read (not
// yet known) shows the caption alone. selected says the Storage page is the current place: the card's border takes the sidebar's
// mark colour (currentMark), and the figures keep their colours — the mark is the border, not a fill. onNavigate closes the phone
// drawer when the card opens the page.
export default function SidebarStorage({ my, sidebar, selected = false, onNavigate }: { my: MeStorage | null; sidebar: Sidebar; selected?: boolean; onNavigate?: () => void }): ReactNode {
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
    <Link
      to="/storage"
      className="shell-storage-card"
      data-testid="storage-card"
      aria-current={selected ? "page" : undefined}
      onClick={onNavigate}
      style={{
        display: "grid",
        gap: 6,
        minInlineSize: 0,
        marginBlockStart: 8,
        padding: 12,
        borderRadius: 4,
        border: `1px solid ${selected ? currentMark(sidebar).bar : sidebar.cardBorder}`,
        color: sidebar.text,
        textDecoration: "none",
        // The background and the hover tint are read by the stylesheet (index.css) from these variables, so the hover lightens
        // the card without an inline background winning over it.
        "--sc-card-bg": sidebar.card,
        "--sc-card-hover": cardHover(sidebar.card),
      } as CSSProperties}
    >
      <span className="shell-storage-caption" style={{ display: "block", fontSize: 12, color: sidebar.caption, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
        {t("nav.storage")}
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
          <span className="shell-storage-line" style={{ display: "block", fontSize: 12, color: sidebar.text, fontVariantNumeric: "tabular-nums", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {line}
          </span>
        </>
      )}
    </Link>
  );
}

// StorageRail is the folded rail's storage icon link (decision 171): one HardDrive icon at the bottom of the rail, named by the
// storage title, which its tooltip shows. On the Storage page it is the current place: aria-current page, and the stylesheet fills
// it with the active colour as the rail's menu rows are (decision 72). onNavigate closes the drawer (the rail is a wide window's).
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
        <HardDrive size={18} strokeWidth={1.75} aria-hidden="true" focusable="false" />
      </Link>
    </Tooltip>
  );
}
