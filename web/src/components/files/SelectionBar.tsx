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
// The bar shows as many of its actions as fit its own width (decision 201): shown = floor((width - fixed) / slot), never fewer
// than 0. The width is the bar's own border box, read by a ResizeObserver on its root, so the count comes from the room the bar
// has, not from what it draws: hiding an icon cannot widen the bar, and the measure cannot flicker. The slot is the icon button
// plus one gap (44 + 4 on a finger, 40 + 4 otherwise); the fixed part is Clear + the count's width + More + the padding + the
// three gaps between those four (layout.ts). The count shows its whole text (decision 242): the fit reserves its measured width
// (its scrollWidth, read when the text changes, with 48 px as the floor), so a long count moves an icon into More rather than
// cut the text. The count does not grow, and shrinks (ellipsis, its full text in the title) only when even Clear + the count +
// More do not fit the bar, so it cannot push the icons past the edge.
//
// Which actions leave the bar keeps decision 68's rule: the caller gives hideOrder, the order they go (Copy link first, then
// Move and Delete, Share and Download last). The icons that stay keep the display order, and the actions that do not fit are
// given to overflow, which draws them at the top of More in that same display order; without an overflow the bar keeps every
// icon, so no action is ever unreachable. A window that cannot measure (jsdom has no ResizeObserver) is taken as wide enough for
// every icon.
export default function SelectionBar({
  label,
  countText,
  clearLabel,
  onClear,
  actions,
  moreMenu,
  overflow,
  hideOrder,
  coarse,
}: {
  label: string;
  countText: string;
  clearLabel: string;
  onClear: () => void;
  actions: SelectionAction[];
  moreMenu?: ReactNode;
  overflow?: (hidden: SelectionAction[]) => ReactNode;
  hideOrder?: readonly string[];
  coarse: boolean;
}) {
  const { token } = antdTheme.useToken();
  const rootRef = useRef<HTMLDivElement>(null);
  const countRef = useRef<HTMLSpanElement>(null);
  // null is "not measured": every action keeps its icon until the bar's width is known.
  const [width, setWidth] = useState<number | null>(null);
  // The count's own width, reserved by the fit: its natural width once measured, COUNT_MIN_W until then.
  const [countW, setCountW] = useState(COUNT_MIN_W);
  useLayoutEffect(() => {
    const el = countRef.current;
    if (!el) return;
    // scrollWidth includes the padding and any cut-off part, so it is the count's natural width even when the span is held
    // narrow. A zero is not a measurement (jsdom does not lay out): the floor stays. The count's width does not depend on the
    // icons shown, so reserving it cannot start a measure loop.
    const natural = el.scrollWidth;
    if (natural > 0) setCountW(Math.max(COUNT_MIN_W, natural));
  }, [countText]);
  useLayoutEffect(() => {
    const el = rootRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    // The first fit is read before paint, so the bar never draws a wider set of icons than it keeps and then shrinks: its
    // row is the right height from the first frame, and the page cannot scroll under a tap taken next. The observer only
    // handles later width changes. A zero width is not a measurement (jsdom does not lay out), so it stays "not measured".
    const first = el.getBoundingClientRect().width;
    if (first > 0) setWidth(first);
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
      : Math.max(0, Math.min(actions.length, Math.floor((width - panelFixedW(coarse, countW)) / panelSlotW(coarse))));
  // Decision 68's rule: hideOrder names the actions as they leave the bar; the rest keep their icons. Both the icons that stay
  // and the hidden entries keep the display order. With no hideOrder the tail of the display order leaves first.
  const present = new Set(actions.map((a) => a.key));
  const leaveOrder = (hideOrder ?? [...actions.map((a) => a.key)].reverse()).filter((k) => present.has(k));
  const hiddenKeys = new Set(leaveOrder.slice(0, actions.length - shown));
  const hidden = actions.filter((a) => hiddenKeys.has(a.key));
  const visible = actions.filter((a) => !hiddenKeys.has(a.key));
  return (
    <div
      ref={rootRef}
      role="group"
      aria-label={label}
      data-testid="selection-panel"
      style={{
        display: "flex",
        // One row, always: the bar's block size never depends on how many icons fit, so drawing or hiding an icon cannot
        // change its height and scroll the page (decision 201).
        flexWrap: "nowrap",
        alignItems: "center",
        gap: PANEL_GAP,
        minWidth: 0,
        // The first draw keeps every icon until the width is measured below, and that measure lays the row out once with them
        // all. Without a clip the icons past the edge widen the page for that one layout: on a phone the layout viewport grows,
        // the page zooms out, and the window's scrollY is clamped down, which closes a menu opened next (the More tap). The clip
        // keeps that first layout inside the bar, so nothing is ever wider than the viewport. clip, not hidden: a focus on an
        // icon must not scroll the bar sideways.
        overflow: "clip",
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
      <span ref={countRef} aria-live="polite" title={countText} style={{ flex: "0 1 auto", minWidth: COUNT_MIN_W, boxSizing: "border-box", padding: "0 8px", fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
        {countText}
      </span>
      {visible.map((a) => (
        <IconButton key={a.key} label={a.label} icon={a.icon} disabled={!a.enabled} onClick={a.run} />
      ))}
      <span data-slot="spacer" style={{ flex: "1 1 auto", minWidth: 0 }} />
      {hidden.length > 0 && overflow !== undefined ? overflow(hidden) : moreMenu}
    </div>
  );
}
