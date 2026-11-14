// SPDX-License-Identifier: AGPL-3.0-or-later

import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { theme as antdTheme } from "antd";
import { X } from "lucide-react";
import IconButton from "../IconButton";
import { panelH, panelFixedW, panelSlotW, PANEL_INSET, PANEL_GAP, COUNT_MIN_W } from "../../lib/files/layout";

export type SelectionAction = { key: string; label: string; icon: ReactNode; enabled: boolean; run: () => void };

// SelectionBar is the bar that takes the toolbar's row while something is selected (decision 68, the owner's model): a
// rounded pill with no borders. The clear icon, then the count, then the action icons, started right after the count, then a
// spacer, then More (the rest of the actions, in a menu the caller gives).
//
// The bar shows as many of its ordered actions as fit its own width (decision 185): shown = floor((width - fixed) / slot),
// never fewer than 0. The width is the bar's own border box, read by a ResizeObserver on its root, so the count comes from the
// room the bar has, not from what it draws: hiding an icon cannot widen the bar, and the measure cannot flicker. The slot is the
// icon button plus one gap (44 + 4 on a finger, 40 + 4 otherwise); the fixed part is Clear + the count's minimum + More + the
// padding + the three gaps between those four (layout.ts). The actions that do not fit are given to overflow, which draws them
// at the top of More, in order; without an overflow the bar keeps every icon, so no action is ever unreachable. A window that
// cannot measure (jsdom has no ResizeObserver) is taken as wide enough for every icon.
export default function SelectionBar({
  label,
  countText,
  clearLabel,
  onClear,
  actions,
  moreMenu,
  overflow,
  coarse,
}: {
  label: string;
  countText: string;
  clearLabel: string;
  onClear: () => void;
  actions: SelectionAction[];
  moreMenu?: ReactNode;
  overflow?: (hidden: SelectionAction[]) => ReactNode;
  coarse: boolean;
}) {
  const { token } = antdTheme.useToken();
  const rootRef = useRef<HTMLDivElement>(null);
  // null is "not measured": every action keeps its icon until the observer reports the bar's width.
  const [width, setWidth] = useState<number | null>(null);
  useLayoutEffect(() => {
    const el = rootRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver((entries) => {
      const entry = entries[0];
      if (!entry) return;
      const box = entry.borderBoxSize?.[0];
      setWidth(box ? box.inlineSize : entry.contentRect.width + 2 * PANEL_INSET);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const shown =
    width === null || overflow === undefined
      ? actions.length
      : Math.max(0, Math.min(actions.length, Math.floor((width - panelFixedW(coarse)) / panelSlotW(coarse))));
  const hidden = actions.slice(shown);
  return (
    <div
      ref={rootRef}
      role="group"
      aria-label={label}
      data-testid="selection-panel"
      style={{
        display: "flex",
        alignItems: "center",
        gap: PANEL_GAP,
        minWidth: 0,
        flex: "1 1 auto",
        boxSizing: "border-box",
        height: panelH(coarse),
        paddingBlock: PANEL_INSET,
        paddingInline: PANEL_INSET,
        borderRadius: 999,
        background: token.colorFillSecondary,
      }}
    >
      <IconButton data-testid="selection-clear" label={clearLabel} icon={<X size={16} aria-hidden="true" focusable="false" />} onClick={onClear} />
      <span aria-live="polite" title={countText} style={{ flex: "none", minWidth: COUNT_MIN_W, maxWidth: "40%", boxSizing: "border-box", padding: "0 8px", fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
        {countText}
      </span>
      {actions.slice(0, shown).map((a) => (
        <IconButton key={a.key} label={a.label} icon={a.icon} disabled={!a.enabled} onClick={a.run} />
      ))}
      <span data-slot="spacer" style={{ flex: "1 1 auto", minWidth: 0 }} />
      {hidden.length > 0 && overflow !== undefined ? overflow(hidden) : moreMenu}
    </div>
  );
}
