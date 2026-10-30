// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  availableOf,
  heldBytes,
  limitTextFor,
  mergePage,
  meterLevel,
  pageQuery,
  parseAccountLimit,
  parseUserLimit,
  sortByUse,
} from './logic';

describe('heldBytes and meterLevel', () => {
  it('counts files and what uploads reserve', () => {
    expect(heldBytes({ used_bytes: 700, reserved_bytes: 250 })).toBe(950);
    expect(heldBytes({ used_bytes: 0, reserved_bytes: 0 })).toBe(0);
  });

  it('has no level without a limit', () => {
    expect(meterLevel(10_000, null)).toBe('none');
    expect(meterLevel(10_000, 0)).toBe('none');
  });

  it('warns at 80% and is in danger at 95%, as the meter does', () => {
    expect(meterLevel(79, 100)).toBe('ok');
    expect(meterLevel(80, 100)).toBe('warning');
    expect(meterLevel(94, 100)).toBe('warning');
    expect(meterLevel(95, 100)).toBe('danger');
    expect(meterLevel(500, 100)).toBe('danger');
  });
});

describe('availableOf', () => {
  it('is null with no limit and never negative', () => {
    expect(availableOf(5, null)).toBeNull();
    expect(availableOf(5, 0)).toBeNull();
    expect(availableOf(30, 100)).toBe(70);
    expect(availableOf(130, 100)).toBe(0);
  });
});

describe('parseUserLimit', () => {
  it('reads empty as the account default, 0 as no limit, and an amount in its unit', () => {
    expect(parseUserLimit({ amount: '', unit: 'GB' })).toEqual({ ok: true, value: null });
    expect(parseUserLimit({ amount: ' ', unit: 'TB' })).toEqual({ ok: true, value: null });
    expect(parseUserLimit({ amount: '0', unit: 'GB' })).toEqual({ ok: true, value: 0 });
    expect(parseUserLimit({ amount: '50', unit: 'GB' })).toEqual({ ok: true, value: 50_000_000_000 });
    expect(parseUserLimit({ amount: '1.5', unit: 'TB' })).toEqual({ ok: true, value: 1_500_000_000_000 });
  });

  it('refuses what is not a limit', () => {
    for (const bad of ['-1', '1e3', 'abc', '1.234', '.5', '00']) {
      expect(parseUserLimit({ amount: bad, unit: 'GB' }), bad).toEqual({ ok: false });
    }
  });
});

describe('parseAccountLimit', () => {
  it('reads empty and 0 both as no default limit', () => {
    expect(parseAccountLimit({ amount: '', unit: 'GB' })).toEqual({ ok: true, value: 0 });
    expect(parseAccountLimit({ amount: '0', unit: 'TB' })).toEqual({ ok: true, value: 0 });
    expect(parseAccountLimit({ amount: '2.25', unit: 'TB' })).toEqual({ ok: true, value: 2_250_000_000_000 });
  });

  it('refuses a bad amount', () => {
    expect(parseAccountLimit({ amount: 'x', unit: 'GB' })).toEqual({ ok: false });
  });
});

describe('limitTextFor', () => {
  it('fills the field in the unit the limit reads back in', () => {
    expect(limitTextFor(50_000_000_000)).toEqual({ amount: '50', unit: 'GB' });
    expect(limitTextFor(1_500_000_000_000)).toEqual({ amount: '1.5', unit: 'TB' });
  });

  it('leaves the field empty for the default and for no limit', () => {
    expect(limitTextFor(null).amount).toBe('');
    expect(limitTextFor(0).amount).toBe('');
  });
});

describe('sortByUse', () => {
  it('puts the most held first and breaks ties by ID', () => {
    const rows = [
      { id: 'usr_b', used_bytes: 10, reserved_bytes: 0 },
      { id: 'usr_a', used_bytes: 5, reserved_bytes: 5 },
      { id: 'usr_c', used_bytes: 99, reserved_bytes: 0 },
      { id: 'usr_d', used_bytes: 1, reserved_bytes: 0 },
    ];
    expect(sortByUse(rows).map((r) => r.id)).toEqual(['usr_c', 'usr_a', 'usr_b', 'usr_d']);
    expect(rows[0].id).toBe('usr_b');
  });
});

describe('mergePage and pageQuery', () => {
  it('keeps the rows already shown and drops repeats from an overlapping page', () => {
    const shown = [{ id: 'a' }, { id: 'b' }];
    expect(mergePage(shown, [{ id: 'b' }, { id: 'c' }]).map((x) => x.id)).toEqual(['a', 'b', 'c']);
  });

  it('builds the limit and cursor, encoding the cursor', () => {
    expect(pageQuery({ limit: 20 })).toBe('?limit=20');
    expect(pageQuery({ limit: 20, cursor: 'a+b/c=' })).toBe('?limit=20&cursor=a%2Bb%2Fc%3D');
    expect(pageQuery({ extra: { folder: 'fld_1', none: undefined } })).toBe('?folder=fld_1');
    expect(pageQuery({})).toBe('');
  });
});
