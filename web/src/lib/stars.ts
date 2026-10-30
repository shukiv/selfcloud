// SPDX-License-Identifier: AGPL-3.0-or-later

// The star, recent and shared-with-me calls of slice p2. Each is a thin call through api(); the server checks every
// rule. Node IDs in a path are encoded.
import { api } from './api';
import type { Node, Page } from './files/types';
import type { RecentItem } from './recent';
import { pageQuery } from './storage/logic';

// StarredItem is a node the caller starred, with the time it was starred.
export interface StarredItem extends Node {
  starred_at: string;
}

// SharedItem is a node shared with the caller, with the later of its share and the caller's last visit to it.
export interface SharedItem extends Node {
  shared_at: string;
}

const node = (id: string) => `/api/v1/nodes/${encodeURIComponent(id)}`;

// starNode stars a node the caller reaches. Starring twice is harmless.
export function starNode(nod: string): Promise<void> {
  return api<void>('PUT', `${node(nod)}/star`);
}

// unstarNode removes the caller's star from a node.
export function unstarNode(nod: string): Promise<void> {
  return api<void>('DELETE', `${node(nod)}/star`);
}

// listStarred reads one page of the caller's starred items, newest star first.
export function listStarred(limit: number, cursor?: string | null): Promise<Page<StarredItem>> {
  return api<Page<StarredItem>>('GET', `/api/v1/starred${pageQuery({ limit, cursor })}`);
}

// listRecent reads one page of the caller's recent items, most recent activity first.
export function listRecent(limit: number, cursor?: string | null): Promise<Page<RecentItem>> {
  return api<Page<RecentItem>>('GET', `/api/v1/recent${pageQuery({ limit, cursor })}`);
}

// removeRecent takes one item out of the caller's recent list. The item itself is not changed.
export function removeRecent(nod: string): Promise<void> {
  return api<void>('DELETE', `/api/v1/recent/${encodeURIComponent(nod)}`);
}

// clearRecent empties the caller's recent list.
export function clearRecent(): Promise<void> {
  return api<void>('DELETE', '/api/v1/recent');
}

// listSharedRecent reads one page of the items shared with the caller, most recently shared first.
export function listSharedRecent(limit: number, cursor?: string | null): Promise<Page<SharedItem>> {
  return api<Page<SharedItem>>('GET', `/api/v1/shared${pageQuery({ limit, cursor, extra: { order: 'recent' } })}`);
}
