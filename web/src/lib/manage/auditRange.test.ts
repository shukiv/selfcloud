// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it } from 'vitest';
import { auditRange, dateEnd, dateStart, rangeInvalid } from './auditRange';

// The day a person picks is theirs: it starts and ends in the browser's zone, and the query carries the UTC instant.
// The zone is set for each case, as the browser's own would be, and put back afterwards.
const savedTZ = process.env.TZ;
afterEach(() => {
  if (savedTZ === undefined) delete process.env.TZ;
  else process.env.TZ = savedTZ;
});

describe("a date is a day in the viewer's time zone", () => {
  it("starts a From date at its day's 00:00", () => {
    process.env.TZ = 'Asia/Jerusalem';
    expect(dateStart('2026-10-20')).toBe('2026-10-19T21:00:00.000Z');
  });

  it('ends a To date at the next day 00:00, so the whole day is included', () => {
    process.env.TZ = 'Asia/Jerusalem';
    expect(dateEnd('2026-10-20')).toBe('2026-10-20T21:00:00.000Z');
  });

  // Israel leaves daylight saving on 2026-10-25, so that day is 25 hours long.
  it('covers the whole 25-hour day around the end of daylight saving in Jerusalem', () => {
    process.env.TZ = 'Asia/Jerusalem';
    expect(dateStart('2026-10-25')).toBe('2026-10-24T21:00:00.000Z');
    expect(dateEnd('2026-10-25')).toBe('2026-10-25T22:00:00.000Z');
    expect(Date.parse(dateEnd('2026-10-25')!) - Date.parse(dateStart('2026-10-25')!)).toBe(25 * 60 * 60 * 1000);
  });

  // New York enters daylight saving on 2026-03-08, so that day is 23 hours long.
  it('covers the whole 23-hour day around the start of daylight saving in New York', () => {
    process.env.TZ = 'America/New_York';
    expect(dateStart('2026-03-08')).toBe('2026-03-08T05:00:00.000Z');
    expect(dateEnd('2026-03-08')).toBe('2026-03-09T04:00:00.000Z');
    expect(Date.parse(dateEnd('2026-03-08')!) - Date.parse(dateStart('2026-03-08')!)).toBe(23 * 60 * 60 * 1000);
  });

  it('leaves an empty date out of the range', () => {
    process.env.TZ = 'UTC';
    expect(dateStart('')).toBeUndefined();
    expect(dateEnd('')).toBeUndefined();
    expect(auditRange('', '')).toEqual({});
    expect(auditRange('2026-10-01', '')).toEqual({ since: '2026-10-01T00:00:00.000Z' });
    expect(auditRange('', '2026-10-01')).toEqual({ until: '2026-10-02T00:00:00.000Z' });
  });
});

describe('rangeInvalid', () => {
  it('is false unless both dates are set', () => {
    expect(rangeInvalid('', '')).toBe(false);
    expect(rangeInvalid('2026-10-02', '')).toBe(false);
    expect(rangeInvalid('', '2026-10-01')).toBe(false);
  });

  it('is true only when the To day is before the From day', () => {
    expect(rangeInvalid('2026-10-02', '2026-10-01')).toBe(true);
    expect(rangeInvalid('2026-10-02', '2026-10-02')).toBe(false);
    expect(rangeInvalid('2026-10-01', '2026-10-02')).toBe(false);
  });
});