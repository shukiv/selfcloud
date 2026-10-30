// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { limitBytes, splitLimit } from './quota';

describe('limitBytes', () => {
  it('reads decimal units', () => {
    expect(limitBytes('50', 'GB')).toBe(50_000_000_000);
    expect(limitBytes('1.5', 'TB')).toBe(1_500_000_000_000);
    expect(limitBytes(' 100 ', 'GB')).toBe(100_000_000_000);
  });

  it('refuses what is not a positive amount', () => {
    for (const bad of ['', '0', '-1', '1e3', 'abc', '1.234', '1,5', '.5', '5.']) {
      expect(limitBytes(bad, 'GB'), bad).toBeNull();
    }
  });

  it('refuses an amount too large to hold exactly', () => {
    expect(limitBytes('99999999999999', 'TB')).toBeNull();
  });
});

describe('splitLimit', () => {
  it('reads a stored limit back in the unit it was entered in', () => {
    expect(splitLimit(50_000_000_000)).toEqual({ amount: '50', unit: 'GB' });
    expect(splitLimit(1_500_000_000_000)).toEqual({ amount: '1.5', unit: 'TB' });
    expect(splitLimit(limitBytes('2.25', 'TB') as number)).toEqual({ amount: '2.25', unit: 'TB' });
  });
});
