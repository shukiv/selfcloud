// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Node } from './types';

export type Crumb<T> = { kind: 'item'; item: T } | { kind: 'gap'; hidden: T[] };

// trimCrumbs keeps the first item and the last keep - 1 items. The ones between collapse into one gap,
// which the interface shows as a menu. Fewer items than keep are all kept.
export function trimCrumbs<T>(items: T[], keep: number): Crumb<T>[] {
  const k = Math.max(3, Math.floor(keep));
  if (items.length <= k) return items.map((item) => ({ kind: 'item', item }));
  const tail = items.slice(items.length - (k - 1));
  const hidden = items.slice(1, items.length - (k - 1));
  return [{ kind: 'item', item: items[0] }, { kind: 'gap', hidden }, ...tail.map((item) => ({ kind: 'item' as const, item }))];
}

// reachableAncestors walks parent_id upward from parentID with fetchNode, and returns the ancestors it
// can reach, nearest last. It stops at the first node that cannot be reached (fetchNode returns null),
// so no name above that point is ever read or shown. The walk is capped at maxDepth steps.
export async function reachableAncestors(
  parentID: string | null,
  fetchNode: (id: string) => Promise<Node | null>,
  maxDepth = 64,
): Promise<Node[]> {
  const chain: Node[] = [];
  let id = parentID;
  for (let depth = 0; id && depth < maxDepth; depth++) {
    const node = await fetchNode(id);
    if (!node) break;
    chain.unshift(node);
    id = node.parent_id;
  }
  return chain;
}
