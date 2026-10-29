// SPDX-License-Identifier: AGPL-3.0-or-later

import { writable } from 'svelte/store';

export interface Brand {
  name: string;
  accent: string;
  logo_url: string | null;
}

export const DEFAULT_ACCENT = '#2563EB';
// The page backgrounds of the two themes (the --color-background tokens of MASTER.md).
const LIGHT_BACKGROUND = '#F8FAFC';
const DARK_BACKGROUND = '#0B1120';
// The accent must reach this against the page background, in each theme on its own.
const MIN_ACCENT_CONTRAST = 3;

// The minimum password length the server accepts, read from /api/v1/info. 8 until it is read.
export const passwordMinLength = writable(8);

export const brand = writable<Brand>({ name: 'Selfcloud', accent: DEFAULT_ACCENT, logo_url: null });

function rgb(hex: string): [number, number, number] {
  const n = parseInt(hex.slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
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

// accentPlan says, for each theme, whether the accent may replace the default primary there.
export function accentPlan(accent: string): { light: boolean; dark: boolean } {
  return {
    light: contrast(accent, LIGHT_BACKGROUND) >= MIN_ACCENT_CONTRAST,
    dark: contrast(accent, DARK_BACKGROUND) >= MIN_ACCENT_CONTRAST,
  };
}

export function validAccent(hex: string): boolean {
  return /^#[0-9a-fA-F]{6}$/.test(hex);
}

function setOrUnset(style: CSSStyleDeclaration, name: string, value: string | null): void {
  if (value === null) style.removeProperty(name);
  else style.setProperty(name, value);
}

// applyBrand sets the page title and, in each theme where the accent passes, the accent. A theme where
// it fails keeps the default primary. The colours go through the CSS object model, not a style element,
// so the strict content policy holds.
export function applyBrand(b: Brand): void {
  const accent = validAccent(b.accent) ? b.accent.toUpperCase() : DEFAULT_ACCENT;
  brand.set({ ...b, accent });
  document.title = b.name;
  const style = document.documentElement.style;
  const plan = accentPlan(accent);
  setOrUnset(style, '--brand-primary', plan.light ? accent : null);
  setOrUnset(style, '--brand-on-primary', plan.light ? textOn(accent) : null);
  setOrUnset(style, '--brand-primary-dark', plan.dark ? accent : null);
  setOrUnset(style, '--brand-on-primary-dark', plan.dark ? textOn(accent) : null);
}
