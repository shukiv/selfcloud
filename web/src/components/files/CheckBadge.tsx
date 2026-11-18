// SPDX-License-Identifier: AGPL-3.0-or-later

import { Check } from "lucide-react";
import type { CSSProperties } from "react";
import { SIDEBAR } from "../../theme/tokens";
import { useModeOrLight } from "../../theme/theme";

// CheckBadge is the yellow round check that marks a selected item, drawn over its icon. The ring is the selected row's own
// background, so the badge reads as a cut-out of it; in the light theme a thin dark rim keeps the yellow edge against a pale row.
// pos places the badge on its icon: the bottom-end corner of a row's mark, the top-start of a tile's preview.
export default function CheckBadge({ size, ring, pos }: { size: number; ring: string; pos: CSSProperties }) {
  const dark = useModeOrLight() === "dark";
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
