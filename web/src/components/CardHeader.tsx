// SPDX-License-Identifier: AGPL-3.0-or-later

import type { ReactNode } from "react";
import { usePalette } from "../theme/theme";

// The icon and title of a card. The icon takes its colour from the card's tone, so a glyph and the
// card it sits in always agree; a tone other than the accent says something about the card's state.
export type CardTone = "accent" | "success" | "warning" | "danger";

export function CardHeader({ icon, title, tone = "accent" }: { icon: ReactNode; title: ReactNode; tone?: CardTone }) {
  const p = usePalette();
  const color = { accent: p.accent, success: p.success, warning: p.warning, danger: p.danger }[tone];
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "4px 0" }}>
      <span style={{ display: "flex", flexShrink: 0, color }}>{icon}</span>
      <span style={{ fontSize: 17, fontWeight: 600 }}>{title}</span>
    </div>
  );
}
