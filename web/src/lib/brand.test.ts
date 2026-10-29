// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { accentPlan, contrast, luminance, textOn, validAccent } from './brand';

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
