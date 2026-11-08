// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { brand, resolveBrand } from './brand';

// The artwork flag (decision 109): the server says the installation shows the SelfCloud artwork; the page reads it, and
// does not compare names.
describe('the SelfCloud artwork flag', () => {
  it('passes the server flag through, and is off when the server does not set it', () => {
    expect(resolveBrand({ name: 'SelfCloud', accent: '#053D8E', logo_url: '/brand/logo', artwork: true }).artwork).toBe(true);
    expect(resolveBrand({ name: 'Acme Drive', accent: '#053D8E', logo_url: null }).artwork).toBe(false);
    expect(resolveBrand({ name: 'Acme Drive', accent: '#053D8E', logo_url: null, artwork: false }).artwork).toBe(false);
  });

  it('defaults the name to SelfCloud until the server answers', () => {
    let name = '';
    brand.subscribe((b) => (name = b.name))();
    expect(name).toBe('SelfCloud');
  });
});
