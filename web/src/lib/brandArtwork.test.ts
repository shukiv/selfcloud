// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { brand, DEFAULT_BRAND, resolveBrand } from './brand';

// The artwork flag (decision 109): the server says the installation shows the SelfCloud artwork; the page reads it, and
// does not compare names.
describe('the SelfCloud artwork flag', () => {
  it('passes the server flag through, and is off when the server does not set it', () => {
    expect(resolveBrand({ name: 'SelfCloud', accent: '#053D8E', logo_url: '/brand/logo', artwork: true }).artwork).toBe(true);
    expect(resolveBrand({ name: 'Acme Drive', accent: '#053D8E', logo_url: null }).artwork).toBe(false);
    expect(resolveBrand({ name: 'Acme Drive', accent: '#053D8E', logo_url: null, artwork: false }).artwork).toBe(false);
  });

  // Nothing is named until the server's answer is applied; the default brand is only the fallback when the server is unreachable.
  it('names nothing until the server answers, and falls back to the default brand', () => {
    let name = 'unset';
    brand.subscribe((b) => (name = b.name))();
    expect(name).toBe('');
    expect(DEFAULT_BRAND.name).toBe('SelfCloud');
    expect(DEFAULT_BRAND.artwork).toBe(true);
  });
});
