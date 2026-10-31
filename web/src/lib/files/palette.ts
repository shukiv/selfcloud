// SPDX-License-Identifier: AGPL-3.0-or-later

// The folder colours: the twelve names the server stores (internal/store/nodes.go, Colors). A colour is one of these
// names, never a free value. Each name's shade for each theme is a CSS variable, --colour-<name>, set in index.css, so
// the same name paints in light and in dark mode, and the contrast is checked there (palette.test.ts).
export const COLOURS = [
  "red",
  "orange",
  "yellow",
  "green",
  "teal",
  "blue",
  "indigo",
  "purple",
  "pink",
  "brown",
  "slate",
  "grey",
] as const;

export type Colour = (typeof COLOURS)[number];

// isColour reports whether v is a palette name. Anything else, including a hex value, is not a colour.
export function isColour(v: unknown): v is Colour {
  return typeof v === "string" && (COLOURS as readonly string[]).includes(v);
}

// colourVar is the CSS value that paints a palette name in the current theme.
export function colourVar(c: Colour): string {
  return `var(--colour-${c})`;
}
