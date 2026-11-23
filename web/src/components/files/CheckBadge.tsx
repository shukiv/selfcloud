// SPDX-License-Identifier: AGPL-3.0-or-later

import { Check } from "lucide-react";
import { theme as antdTheme } from "antd";
import type { CSSProperties } from "react";
import { SIDEBAR } from "../../theme/tokens";
import { useModeOrLight } from "../../theme/theme";

// CheckBadge is the yellow round check that marks a selected item, drawn over its icon. The ring is the selected row's own
// background, so the badge reads as a cut-out of it; in the light theme a thin dark rim keeps the yellow edge against a pale row.
// pos places the badge on its icon: the bottom-end corner of a row's mark, the top-start of a tile's preview.
// With empty, it draws the unselected item's mark instead: an empty outline circle in the header select-all mark's "none" style
// (the same border colour and width), filled with the item's own background (the container colour, not the ring, which is the
// selected item's tint) so it stays visible on a thumbnail. It shows only while anything is selected, in exactly the place and
// size the check would have.
export default function CheckBadge({ size, ring, pos, empty }: { size: number; ring: string; pos: CSSProperties; empty?: boolean }) {
  const dark = useModeOrLight() === "dark";
  const { token } = antdTheme.useToken();
  if (empty) {
    return (
      <span
        className="empty-circle"
        aria-hidden="true"
        style={{
          position: "absolute",
          ...pos,
          display: "inline-flex",
          width: size,
          height: size,
          borderRadius: "50%",
          boxSizing: "border-box",
          background: token.colorBgContainer,
          border: `2px solid ${token.colorTextSecondary}`,
          pointerEvents: "none",
        }}
      />
    );
  }
  return (
    <span
      className="check-badge"
      aria-hidden="true"
      style={{
        position: "absolute",
        ...pos,
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        width: size,
        height: size,
        borderRadius: "50%",
        background: SIDEBAR.active,
        color: SIDEBAR.onActive,
        boxShadow: dark ? `0 0 0 2px ${ring}` : `0 0 0 2px ${ring}, 0 0 0 3px rgba(13, 33, 66, 0.55)`,
        pointerEvents: "none",
      }}
    >
      <Check size={Math.round(size * 0.62)} strokeWidth={3} aria-hidden="true" focusable="false" />
    </span>
  );
}