// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { accentPlan, contrast, luminance, resolveBrand, textOn, validAccent } from './brand';
import { DARK, LIGHT, SIDEBAR } from '../theme/tokens';

describe('brand colours', () => {
  it('has the WCAG extremes', () => {
    expect(luminance('#000000')).toBe(0);
    expect(luminance('#FFFFFF')).toBeCloseTo(1, 6);
    expect(contrast('#000000', '#FFFFFF')).toBeCloseTo(21, 6);
  });

  it('puts white or black on an accent, whichever reads better', () => {
    expect(textOn('#2563EB')).toBe('#FFFFFF');
    expect(textOn('#FDE68A')).toBe('#000000');
  });

  it('accepts only #rrggbb', () => {
    expect(validAccent('#2563eb')).toBe(true);
    expect(validAccent('2563eb')).toBe(false);
    expect(validAccent('#2563e')).toBe(false);
    expect(validAccent('red')).toBe(false);
  });
});

describe('accentPlan: each theme on its own', () => {
  it('passes in both themes', () => {
    expect(accentPlan('#0F766E')).toEqual({ light: true, dark: true });
  });

  it('fails only in light for a pale accent', () => {
    expect(accentPlan('#FDE68A')).toEqual({ light: false, dark: true });
  });

  it('fails only in dark for a deep accent', () => {
    expect(accentPlan('#1E3A8A')).toEqual({ light: true, dark: false });
  });
});

describe('resolveBrand', () => {
  const base = { name: 'Acme', logo_url: null };

  it('keeps the defaults when the accent is the default', () => {
    const r = resolveBrand({ ...base, accent: '#053D8E' });
    expect(r.light.accent).toBe(LIGHT.accent);
    expect(r.dark.accentFill).toBe(DARK.accentFill);
    expect(r.sidebar.bg).toBe(SIDEBAR.bg);
  });

  it('falls back to the default for an invalid accent', () => {
    expect(resolveBrand({ ...base, accent: 'red' }).accent).toBe('#053D8E');
  });

  it('derives the family from a custom accent in both themes that pass', () => {
    const r = resolveBrand({ ...base, accent: '#0F766E' });
    expect(r.applies).toEqual({ light: true, dark: true });
    expect(r.light.accent).toBe('#0F766E');
    expect(r.light.accentFill).toBe('#0F766E');
    expect(r.light.accentText).toBe('#0F766E');
    expect(r.light.accentFillHover).not.toBe('#0F766E');
    expect(r.sidebar.bg).toBe('#0F766E');
    expect(textOn('#0F766E')).toBe(r.sidebar.text);
  });

  it('keeps the default in a theme where the accent fails the gate', () => {
    const r = resolveBrand({ ...base, accent: '#FDE68A' });
    expect(r.applies).toEqual({ light: false, dark: true });
    expect(r.light.accent).toBe(LIGHT.accent);
    expect(r.dark.accent).not.toBe(DARK.accent);
    expect(r.sidebar.bg).toBe(SIDEBAR.bg);
  });

  it('lets explicit tokens win over the derived family', () => {
    const r = resolveBrand({
      ...base,
      accent: '#0F766E',
      palette: { light: { accent_fill: '#123456' } },
      sidebar: { bg: '#010203', on_active: '#ABCDEF' },
    });
    expect(r.light.accentFill).toBe('#123456');
    expect(r.light.accentText).toBe('#0F766E');
    expect(r.sidebar.bg).toBe('#010203');
    expect(r.sidebar.onActive).toBe('#ABCDEF');
  });

  it('ignores a malformed explicit token', () => {
    const r = resolveBrand({ ...base, accent: '#053D8E', palette: { dark: { accent: 'blue' } } });
    expect(r.dark.accent).toBe(DARK.accent);
  });
});

// L5: the derived accent text is the text colour of the accent on the card, so it must read at 4.5:1,
// not only the 3:1 the accent gate asks of the accent itself.
describe('derived accent text reads at 4.5:1 on the card', () => {
  it('holds for light accents too', () => {
    let checked = 0;
    for (const accent of ['#0891B2', '#0284C7', '#0D9488', '#15803D', '#CA8A04', '#2563EB', '#053D8E', '#7C3AED']) {
      const r = resolveBrand({ name: 'x', accent, logo_url: null, palette: { light: {}, dark: {} }, sidebar: {} } as never);
      if (r.applies.light) {
        checked++;
        expect(contrast(r.light.accentText, LIGHT.card), accent).toBeGreaterThanOrEqual(4.5);
      }
      if (r.applies.dark) {
        checked++;
        expect(contrast(r.dark.accentText, DARK.card), accent).toBeGreaterThanOrEqual(4.5);
      }
    }
    expect(checked).toBeGreaterThan(8);
  });
});

