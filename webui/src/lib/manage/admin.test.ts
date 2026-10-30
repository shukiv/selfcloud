// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { expiryFor, isLastActiveAdmin, nextTab, tabFrom, tenantQuery, tenantSearch } from './admin';

describe('tenantQuery and tenantSearch', () => {
  it('reads the list address, with the defaults for anything else', () => {
    expect(tenantQuery('?q=%20acme%20&status=suspended')).toEqual({ q: 'acme', status: 'suspended' });
    expect(tenantQuery('?status=deleting')).toEqual({ q: '', status: 'all' });
    expect(tenantQuery('')).toEqual({ q: '', status: 'all' });
    expect(tenantQuery(`?q=${'x'.repeat(300)}`).q).toHaveLength(200);
  });

  it('writes the address back, leaving the defaults out', () => {
    expect(tenantSearch('', 'all')).toBe('');
    expect(tenantSearch(' acme ', 'active')).toBe('?q=acme&status=active');
    expect(tenantQuery(tenantSearch('a&b', 'suspended'))).toEqual({ q: 'a&b', status: 'suspended' });
  });
});

describe('expiryFor', () => {
  const now = new Date('2026-01-01T00:00:00Z');
  it('never ends for "never", and otherwise adds whole days', () => {
    expect(expiryFor('never', now)).toBeUndefined();
    expect(expiryFor('30', now)).toBe('2026-01-31T00:00:00.000Z');
    expect(expiryFor('365', now)).toBe('2027-01-01T00:00:00.000Z');
  });
});

describe('tabs', () => {
  it('reads ?tab= and falls back to the overview', () => {
    expect(tabFrom('?tab=keys')).toBe('keys');
    expect(tabFrom('?tab=nope')).toBe('overview');
    expect(tabFrom('')).toBe('overview');
  });

  it('moves with the arrow keys, wrapping, and with Home and End', () => {
    expect(nextTab('overview', 'ArrowRight')).toBe('users');
    expect(nextTab('audit', 'ArrowRight')).toBe('overview');
    expect(nextTab('overview', 'ArrowLeft')).toBe('audit');
    expect(nextTab('users', 'End')).toBe('audit');
    expect(nextTab('users', 'Home')).toBe('overview');
    expect(nextTab('users', 'Enter')).toBeNull();
  });
});

describe('isLastActiveAdmin', () => {
  it('is true only for the one active admin', () => {
    const admins = [
      { id: 'usr_a', status: 'active' },
      { id: 'usr_b', status: 'pending' },
    ];
    expect(isLastActiveAdmin(admins, 'usr_a')).toBe(true);
    expect(isLastActiveAdmin(admins, 'usr_b')).toBe(false);
    expect(isLastActiveAdmin([...admins, { id: 'usr_c', status: 'active' }], 'usr_a')).toBe(false);
  });
});
