// SPDX-License-Identifier: AGPL-3.0-or-later

// The rules of the Home page, as plain functions: how many items each section asks for, when the storage notice
// shows, and how the waiting count reads. The server decides every right; these only shape what Home shows.
import type { MeStorage } from './storage/logic';
import { heldBytes } from './storage/logic';

// HOME_SECTION_SIZE is how many items each section of Home reads.
export const HOME_SECTION_SIZE = 8;

// STORAGE_NOTICE_SHARE is the share of the caller's limit at which Home says how full it is.
export const STORAGE_NOTICE_SHARE = 0.8;

// storageNoticePercent returns the percentage of the caller's limit that is held, rounded down, when it is at least
// 80% of a real limit. A null or zero limit is no limit, and gives null. The result is never above 100.
export function storageNoticePercent(s: MeStorage | null): number | null {
  if (!s || s.quota_bytes === null || s.quota_bytes <= 0) return null;
  const share = heldBytes(s) / s.quota_bytes;
  if (share < STORAGE_NOTICE_SHARE) return null;
  return Math.min(100, Math.floor(share * 100));
}

// waitingCount is how many access requests the caller may decide, shown in the Waiting section. Zero hides the section.
export function waitingCount(items: readonly unknown[]): number {
  return items.length;
}
