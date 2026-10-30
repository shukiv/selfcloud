// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { HOME_SECTION_SIZE, storageNoticePercent, waitingCount } from './home';
import type { MeStorage } from './storage/logic';

const storage = (used: number, reserved: number, quota: number | null): MeStorage => ({
  used_bytes: used,
  reserved_bytes: reserved,
  quota_bytes: quota,
  available_bytes: null,
});

describe('Home sections', () => {
  it('asks for eight items per section', () => {
    expect(HOME_SECTION_SIZE).toBe(8);
  });

  it('counts the requests waiting for the caller', () => {
    expect(waitingCount([])).toBe(0);
    expect(waitingCount([{}, {}, {}])).toBe(3);
  });
});

describe('storageNoticePercent', () => {
  it('shows nothing without a limit', () => {
    expect(storageNoticePercent(null)).toBeNull();
    expect(storageNoticePercent(storage(900, 0, null))).toBeNull();
    expect(storageNoticePercent(storage(900, 0, 0))).toBeNull();
  });

  it('shows nothing below 80% of the limit', () => {
    expect(storageNoticePercent(storage(799, 0, 1000))).toBeNull();
    expect(storageNoticePercent(storage(500, 299, 1000))).toBeNull();
  });

  it('shows the share from 80%, counting unfinished uploads', () => {
    expect(storageNoticePercent(storage(800, 0, 1000))).toBe(80);
    expect(storageNoticePercent(storage(700, 150, 1000))).toBe(85);
    expect(storageNoticePercent(storage(999, 0, 1000))).toBe(99);
  });

  it('never shows more than 100%', () => {
    expect(storageNoticePercent(storage(1500, 0, 1000))).toBe(100);
  });
});
