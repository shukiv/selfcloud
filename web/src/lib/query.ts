// SPDX-License-Identifier: AGPL-3.0-or-later

import { QueryClient } from '@tanstack/react-query';
import { ApiError } from './apiError';
import { sessionKey, session } from './session';
import { get, resetUserStores } from './store';

// The query client for every read. A 4xx answer is final, so it is never retried; a network
// failure or a 5xx answer is retried once. Data is fresh for 30 seconds, and a window focus does
// not refetch, which keeps the listings still under the person's hand.
export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        refetchOnWindowFocus: false,
        retry: (failureCount, err) => {
          if (err instanceof ApiError && err.status >= 400 && err.status < 500) return false;
          return failureCount < 1;
        },
      },
      mutations: {
        retry: false,
      },
    },
  });
}

// queryKey prefixes a key with the session it was made under, so a sign-out or a sign-in as someone
// else never shows the previous person's data.
export function queryKey(...parts: unknown[]): unknown[] {
  return [sessionKey(get(session)), ...parts];
}

// clearOnSessionChange clears the cache whenever the session changes (a sign-in or a sign-out), so
// no answer given to one person reaches the next. It returns the function that stops the watch.
export function clearOnSessionChange(client: QueryClient): () => void {
  let key = sessionKey(get(session));
  return session.subscribe(() => {
    const next = sessionKey(get(session));
    if (next !== key) {
      key = next;
      client.clear();
      resetUserStores();
    }
  });
}

// treeKeys are the reads that show the place a person is in: a listing, the place's own name and path, the trash
// and the links a node opens from. A change anywhere refreshes all of them, so no page shows what another changed.
const treeKeys = ['children', 'place', 'trash', 'node-link'];

// refreshTree marks every read of the tree stale for the session that is signed in. An active read refetches at once.
export function refreshTree(client: QueryClient): Promise<unknown> {
  const key = sessionKey(get(session));
  return Promise.all(treeKeys.map((k) => client.invalidateQueries({ queryKey: [key, k] })));
}
