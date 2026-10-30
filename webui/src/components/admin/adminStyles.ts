// SPDX-License-Identifier: AGPL-3.0-or-later

import type { CSSProperties } from "react";

// The installation admin pages share these layout rules. Logical properties only: the page reads right to left in Hebrew.
export const pageTitle: CSSProperties = { fontSize: 20, fontWeight: 600, margin: "0 0 16px" };
export const toolbar: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  alignItems: "center",
  justifyContent: "space-between",
  gap: 12,
  marginBlockEnd: 16,
};
export const sectionTitle: CSSProperties = { margin: 0, fontSize: 16, fontWeight: 600 };
export const list: CSSProperties = { listStyle: "none", margin: 0, padding: 0, display: "grid", gap: 8 };
export const row: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  alignItems: "center",
  gap: 12,
  padding: 12,
  border: "1px solid var(--ant-color-border)",
  borderRadius: 4,
};
export const rowMain: CSSProperties = { flex: "1 1 220px", minWidth: 0, overflowWrap: "anywhere" };
export const rowTitle: CSSProperties = { margin: 0, fontWeight: 600 };
export const muted: CSSProperties = { margin: 0, fontSize: 14, color: "var(--ant-color-text-secondary)" };
export const more: CSSProperties = { display: "flex", justifyContent: "center", marginBlockStart: 16 };
export const form: CSSProperties = { display: "grid", gap: 16 };
export const field: CSSProperties = { display: "grid", gap: 4 };
export const fieldset: CSSProperties = { display: "grid", gap: 8, margin: 0, padding: 0, border: 0 };
export const foot: CSSProperties = { display: "flex", flexWrap: "wrap", gap: 8, justifyContent: "flex-end" };
export const mono: CSSProperties = { fontFamily: "ui-monospace, monospace", overflowWrap: "anywhere", margin: 0 };
