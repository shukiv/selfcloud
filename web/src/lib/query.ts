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
// and the links a node opens from, and the folder tree beside a listing. A change anywhere refreshes all of them, so no page shows what another changed.
const treeKeys = ['children', 'place', 'trash', 'node-link', 'tree'];

// Branch names one folder of the tree beside a listing: a space, and a directory in it (null for the space's root).
export interface Branch {
  folder: string;
  dir: string | null;
}

// refreshTree marks every read of the tree stale for the session that is signed in. An active read refetches at once.
// touched names the branches of the folder tree that the change reached (the folders items left and entered); only those
// are read again. A change that names none (a restore from the trash, a purge) re-reads every branch the sidebar shows
// open, once each; the closed ones are stale and are read when they are next shown.
export function refreshTree(client: QueryClient, touched?: Branch[]): Promise<unknown> {
  const key = sessionKey(get(session));
  const rest = treeKeys.filter((k) => k !== 'tree').map((k) => client.invalidateQueries({ queryKey: [key, k] }));
  if (touched === undefined) {
    return Promise.all([...rest, client.invalidateQueries({ queryKey: [key, 'tree'] })]);
  }
  const seen = new Set<string>();
  const branches = touched
    .filter((b) => {
      const id = `${b.folder}/${b.dir ?? ''}`;
      if (seen.has(id)) return false;
      seen.add(id);
      return true;
    })
    .map((b) => client.invalidateQueries({ queryKey: [key, 'tree', b.folder, b.dir], exact: true }));
  return Promise.all([...rest, ...branches]);
}
