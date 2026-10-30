// SPDX-License-Identifier: AGPL-3.0-or-later

import { generate } from '@ant-design/colors';
import { writable } from './store';
import { DARK, DEFAULT_ACCENT, LIGHT, SIDEBAR, type Palette } from '../theme/tokens';

// The sidebar of a resolved brand is a fresh object, so its colours can be replaced.
export type SidebarColours = Record<keyof typeof SIDEBAR, string>;

// The brand the server describes in /api/v1/info. Every colour is #rrggbb. A missing key means the
// default; an explicit token wins over a colour derived from the accent.
export interface TokenWire {
  accent?: string;
  accent_fill?: string;
  accent_fill_hover?: string;
  accent_fill_active?: string;
  accent_text?: string;
  accent_soft?: string;
  accent_border?: string;
}

export interface SidebarWire {
  bg?: string;
  text?: string;
  caption?: string;
  active?: string;
  on_active?: string;
}

export interface Brand {
  name: string;
  accent: string;
  logo_url: string | null;
  palette?: { light?: TokenWire; dark?: TokenWire };
  sidebar?: SidebarWire;
}

// The pages' backgrounds, which the accent must reach in each theme.
const MIN_ACCENT_CONTRAST = 3;

export { DEFAULT_ACCENT };

// The minimum password length the server accepts, read from /api/v1/info. 8 until it is read.
export const passwordMinLength = writable(8);

export const brand = writable<Brand>({ name: 'Selfcloud', accent: DEFAULT_ACCENT, logo_url: null });

function rgb(hex: string): [number, number, number] {
  const n = parseInt(hex.slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

function toHex(c: [number, number, number]): string {
  return '#' + c.map((v) => Math.round(v).toString(16).padStart(2, '0')).join('').toUpperCase();
}

// mix blends b into a: t is 0 for a, 1 for b.
function mix(a: string, b: string, t: number): string {
  const x = rgb(a);
  const y = rgb(b);
  return toHex([0, 1, 2].map((i) => x[i] + (y[i] - x[i]) * t) as [number, number, number]);
}

// luminance is the WCAG relative luminance of a #rrggbb colour.
export function luminance(hex: string): number {
  const [r, g, b] = rgb(hex).map((v) => {
    const x = v / 255;
    return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

// contrast is the WCAG contrast ratio of two colours.
export function contrast(a: string, b: string): number {
  const x = luminance(a);
  const y = luminance(b);
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}

// textOn is black or white, whichever has the higher contrast. It always reaches 4.5:1.
export function textOn(hex: string): string {
  return contrast(hex, '#FFFFFF') >= contrast(hex, '#000000') ? '#FFFFFF' : '#000000';
}

// readable moves the colour toward black on a light background, or toward white on a dark one, until
// it reaches min contrast against that background. A colour that already reaches it is kept.
export function readable(hex: string, background: string, min: number): string {
  const target = luminance(background) > 0.5 ? '#000000' : '#FFFFFF';
  let out = hex;
  for (let t = 0; t <= 1 && contrast(out, background) < min; t += 0.02) out = mix(hex, target, t);
  return out;
}

// accentPlan says, for each theme, whether the accent may replace the default there: it must reach
// the page background of that theme.
export function accentPlan(accent: string): { light: boolean; dark: boolean } {
  return {
    light: contrast(accent, LIGHT.page) >= MIN_ACCENT_CONTRAST,
    dark: contrast(accent, DARK.page) >= MIN_ACCENT_CONTRAST,
  };
}

// safeLogo keeps a logo URL only when the page may put it in an image: the same-origin path the server sends (a single
// slash, no backslash), an http(s) URL, or a PNG, JPEG, GIF, WebP or SVG data URL. Anything else gives no logo.
export function safeLogo(url: string | null | undefined): string | null {
  if (typeof url !== 'string' || url === '') return null;
  if (/^\/(?![/\\])[^\\\s]*$/.test(url)) return url;
  if (/^https?:\/\/[^\s\\]+$/i.test(url)) return url;
  if (/^data:image\/(png|jpe?g|gif|webp|svg\+xml);base64,[A-Za-z0-9+/=]+$/i.test(url)) return url;
  return null;
}

export function validAccent(hex: string): boolean {
  return /^#[0-9a-fA-F]{6}$/.test(hex);
}

// Token names of the wire form, and of the palette.
const TOKEN_KEYS: [keyof TokenWire, keyof Palette][] = [
  ['accent', 'accent'],
  ['accent_fill', 'accentFill'],
  ['accent_fill_hover', 'accentFillHover'],
  ['accent_fill_active', 'accentFillActive'],
  ['accent_text', 'accentText'],
  ['accent_soft', 'accentSoft'],
  ['accent_border', 'accentBorder'],
];

// derivedFamily builds the accent family of one theme from the accent, against that theme's card.
// Hover and active come from the colour ramp; the soft and border tints are the accent blended into
// the card, because the ramp's pale steps turn grey for dark blues.
function derivedFamily(accent: string, mode: 'light' | 'dark'): Partial<Palette> {
  if (mode === 'light') {
    const ramp = generate(accent);
    return {
      accent,
      accentFill: accent,
      // The text in the accent colour reads at 4.5:1 on the card; the accent itself needs only 3:1.
      accentText: readable(accent, LIGHT.card, 4.5),
      accentHover: ramp[6],
      accentFillHover: ramp[6],
      accentActive: ramp[7],
      accentFillActive: ramp[7],
      accentSoft: mix(accent, LIGHT.card, 0.92),
      accentSoftHover: mix(accent, LIGHT.card, 0.88),
      accentBorder: mix(accent, LIGHT.card, 0.75),
      accentBorderHover: mix(accent, LIGHT.card, 0.55),
    };
  }
  const ramp = generate(accent, { theme: 'dark', backgroundColor: DARK.card });
  return {
    accent: ramp[8],
    accentText: readable(ramp[9], DARK.card, 4.5),
    accentHover: ramp[9],
    accentActive: ramp[7],
    accentFill: ramp[6],
    accentFillHover: ramp[7],
    accentFillActive: ramp[5],
    accentSoft: mix(accent, DARK.card, 0.86),
    accentSoftHover: mix(accent, DARK.card, 0.8),
    accentBorder: mix(accent, DARK.card, 0.7),
    accentBorderHover: ramp[8],
  };
}

export interface ResolvedBrand {
  name: string;
  accent: string;
  logo_url: string | null;
  light: Palette;
  dark: Palette;
  sidebar: SidebarColours;
  // applies says, for each theme, whether the accent family replaced the default there.
  applies: { light: boolean; dark: boolean };
}

// resolveBrand merges the defaults, the accent-derived family and the explicit tokens. An invalid
// accent falls back to the default. A theme where the accent fails the contrast gate keeps its
// defaults, and so does the sidebar's blue when the light theme fails.
export function resolveBrand(b: Brand): ResolvedBrand {
  const accent = validAccent(b.accent) ? b.accent.toUpperCase() : DEFAULT_ACCENT;
  const plan = accentPlan(accent);
  const light: Palette = { ...LIGHT };
  const dark: Palette = { ...DARK };
  const sidebar: SidebarColours = { ...SIDEBAR };
  const custom = accent !== DEFAULT_ACCENT;

  if (custom && plan.light) Object.assign(light, derivedFamily(accent, 'light'));
  if (custom && plan.dark) Object.assign(dark, derivedFamily(accent, 'dark'));
  if (custom && plan.light) {
    sidebar.bg = light.accentFill;
    sidebar.text = textOn(sidebar.bg);
    sidebar.onActive = textOn(sidebar.active);
  }

  for (const [mode, target] of [
    ['light', light],
    ['dark', dark],
  ] as const) {
    const tokens = b.palette?.[mode];
    if (!tokens) continue;
    for (const [wire, key] of TOKEN_KEYS) {
      const v = tokens[wire];
      if (typeof v === 'string' && validAccent(v)) target[key] = v.toUpperCase();
    }
  }

  const wire = b.sidebar ?? {};
  const sidebarKeys: [keyof SidebarWire, keyof SidebarColours][] = [
    ['bg', 'bg'],
    ['text', 'text'],
    ['caption', 'caption'],
    ['active', 'active'],
    ['on_active', 'onActive'],
  ];
  for (const [w, key] of sidebarKeys) {
    const v = wire[w];
    if (typeof v === 'string' && validAccent(v)) sidebar[key] = v.toUpperCase();
  }

  return {
    name: b.name,
    accent,
    logo_url: safeLogo(b.logo_url),
    light,
    dark,
    sidebar,
    applies: plan,
  };
}

// applyBrand stores the brand and sets the page title. Colours are not written to the page: the
// interface reads them from resolveBrand.
export function applyBrand(b: Brand): void {
  const accent = validAccent(b.accent) ? b.accent.toUpperCase() : DEFAULT_ACCENT;
  brand.set({ ...b, accent });
  document.title = b.name;
}
