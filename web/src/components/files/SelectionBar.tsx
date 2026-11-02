// SPDX-License-Identifier: AGPL-3.0-or-later

import type { ReactNode } from "react";
import { theme as antdTheme } from "antd";
import { X } from "lucide-react";
import IconButton from "../IconButton";
import { panelH, PANEL_INSET } from "../../lib/files/layout";

export type SelectionAction = { key: string; label: string; icon: ReactNode; enabled: boolean; run: () => void };

// SelectionBar is the bar that takes the toolbar's row while something is selected (decision 68, the owner's model): a
// rounded pill with no borders. The clear icon, then the count, then the action icons, started right after the count, then a
// spacer, then More (the rest of the actions, in a menu the caller gives).
export default function SelectionBar({
  label,
  countText,
  clearLabel,
  onClear,
  actions,
  moreMenu,
  coarse,
}: {
  label: string;
  countText: string;
  clearLabel: string;
  onClear: () => void;
  actions: SelectionAction[];
  moreMenu?: ReactNode;
  coarse: boolean;
}) {
  const { token } = antdTheme.useToken();
  return (
    <div
      role="group"
      aria-label={label}
      data-testid="selection-panel"
      style={{
        display: "flex",
        alignItems: "center",
        gap: 4,
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
      <span aria-live="polite" title={countText} style={{ flex: "none", maxWidth: "40%", padding: "0 8px", fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
        {countText}
      </span>
      {actions.map((a) => (
        <IconButton key={a.key} label={a.label} icon={a.icon} disabled={!a.enabled} onClick={a.run} />
      ))}
      <span data-slot="spacer" style={{ flex: "1 1 auto", minWidth: 0 }} />
      {moreMenu}
    </div>
  );
}
