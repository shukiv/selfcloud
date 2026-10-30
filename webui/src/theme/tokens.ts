// SPDX-License-Identifier: AGPL-3.0-or-later

// The colour and shape tokens of the default look. Every colour the interface paints comes from
// here, or from a brand override merged over it (lib/brand.ts). The accent is the brand blue; the
// sidebar is that blue in both modes, with the active page on a yellow pill.

// Two values per accent in dark mode: accentFill is what white text sits on, accentText is
// accent-coloured text on the dark card.
export const LIGHT = {
  page: "#EDF0F6",
  card: "#FFFFFF",
  line: "#DFE6F0",
  line2: "#CAD8EE",
  text: "#0D2142",
  text2: "#34445E",
  muted: "#5A6B85",
  // Hints only: 3.7:1 on white.
  faint: "#75869F",
  accent: "#053D8E",
  accentFill: "#053D8E",
  accentFillHover: "#032D6C",
  accentFillActive: "#021D47",
  accentText: "#053D8E",
  accentHover: "#032D6C",
  accentActive: "#021D47",
  accentSoft: "#EBF0F6",
  accentSoftHover: "#E3EBF6",
  accentBorder: "#CAD8EE",
  accentBorderHover: "#A9C1E6",
  teal: "#3D8A9F",
  tealText: "#2C6A7B",
  tealSoft: "#E8F3F6",
  tealBorder: "#BDDCE5",
  // The semantic colours are for glyphs, bars and fills. Text in them takes the -Text step.
  success: "#059669",
  successText: "#047857",
  successSoft: "#E2F7F0",
  successBorder: "#ABE7D3",
  warning: "#F59E0B",
  warningText: "#B45309",
  warningSoft: "#FFF6D1",
  warningBorder: "#FFE88C",
  danger: "#E11D48",
  dangerText: "#BE123C",
  dangerSoft: "#FDEDF0",
  dangerBorder: "#F6BBC8",
  shadow: "0 22px 60px -22px rgba(5, 61, 142, 0.34)",
  tile: "#EDF0F6",
} as const;

export type Palette = Record<keyof typeof LIGHT, string>;

export const DARK: Palette = {
  page: "#071531",
  card: "#0D2142",
  line: "#1C2E4B",
  line2: "#34445E",
  text: "#FFFFFF",
  text2: "#C3CFDF",
  muted: "#96A6BD",
  faint: "#75869F",
  accent: "#7097D0",
  accentFill: "#2A5AA6",
  accentFillHover: "#446EB1",
  accentFillActive: "#244D8D",
  accentText: "#A9C1E6",
  accentHover: "#A9C1E6",
  accentActive: "#5C7FB4",
  accentSoft: "#1B3256",
  accentSoftHover: "#21395E",
  accentBorder: "#34445E",
  accentBorderHover: "#7097D0",
  teal: "#5AA8BD",
  tealText: "#A5CFDB",
  tealSoft: "#183453",
  tealBorder: "#28506D",
  success: "#34D399",
  successText: "#34D399",
  successSoft: "#123A4E",
  successBorder: "#1B5F60",
  warning: "#F59E0B",
  warningText: "#FECC00",
  warningSoft: "#2A363A",
  warningBorder: "#615D2B",
  danger: "#FB7185",
  dangerText: "#FB7185",
  dangerSoft: "#2E2C4B",
  dangerBorder: "#603D59",
  shadow: "0 22px 60px -22px rgba(0, 0, 0, 0.55)",
  tile: "#1C2E4B",
};

export const PALETTE: Record<"light" | "dark", Palette> = { light: LIGHT, dark: DARK };

// The sidebar: brand blue in both modes. The active page sits on the yellow pill.
export const SIDEBAR = {
  bg: "#053D8E",
  text: "#FFFFFF",
  caption: "#A9C1E6",
  hover: "rgba(255, 255, 255, 0.10)",
  card: "rgba(255, 255, 255, 0.08)",
  cardBorder: "rgba(255, 255, 255, 0.14)",
  active: "#FECC00",
  onActive: "#0D2142",
} as const;

export type Sidebar = Record<keyof typeof SIDEBAR, string>;

// Every corner is the same radius, the logo's: 4px.
export const RADIUS = 4;

// The default accent, the brand blue. An installation whose accent equals it has no override.
export const DEFAULT_ACCENT = "#053D8E";
