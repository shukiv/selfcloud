// SPDX-License-Identifier: AGPL-3.0-or-later

import { userWritable } from '../store';
import { ApiError } from '../apiError';
import { sessionChanged } from '../session';
import { listAccessRequests } from './api';

// pendingRequests is how many access requests the caller may decide, or null before the first read. It is read
// when the shell starts and after a decision; it is never polled.
export const pendingRequests = userWritable<number | null>(null);

// refreshPendingRequests reads the count. A caller who may decide none gets an empty list, and no count shows.
export async function refreshPendingRequests(): Promise<number | null> {
  const changed = sessionChanged();
  try {
    const { items } = await listAccessRequests();
    if (changed()) return null;
    pendingRequests.set(items.length);
    return items.length;
  } catch (err) {
    if (err instanceof ApiError && !changed()) pendingRequests.set(null);
    return null;
  }
}
