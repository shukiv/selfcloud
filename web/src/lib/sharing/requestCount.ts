// SPDX-License-Identifier: AGPL-3.0-or-later

import { writable } from 'svelte/store';
import { ApiError } from '../apiError';
import { listAccessRequests } from './api';

// pendingRequests is how many access requests the caller may decide, or null before the first read. It is read
// when the shell starts and after a decision; it is never polled.
export const pendingRequests = writable<number | null>(null);

// refreshPendingRequests reads the count. A caller who may decide none gets an empty list, and no count shows.
export async function refreshPendingRequests(): Promise<number | null> {
  try {
    const { items } = await listAccessRequests();
    pendingRequests.set(items.length);
    return items.length;
  } catch (err) {
    if (err instanceof ApiError) pendingRequests.set(null);
    return null;
  }
}
