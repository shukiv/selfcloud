// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Here } from './location';
import type { Node } from './types';

// A crumb is a place, or a gap that stands for the places hidden in it, or the mark for the folders above a trail cut at the
// 64 nearest (decision 140), which leads nowhere.
export type Crumb<T> = { kind: 'item'; item: T } | { kind: 'gap'; hidden: T[] } | { kind: 'above' };

// trimCrumbs keeps the first item and the last keep - 1 items. The ones between collapse into one gap,
// which the interface shows as a menu. Fewer items than keep are all kept.
export function trimCrumbs<T>(items: T[], keep: number): Crumb<T>[] {
  const k = Math.max(3, Math.floor(keep));
  if (items.length <= k) return items.map((item) => ({ kind: 'item', item }));
  const tail = items.slice(items.length - (k - 1));
  const hidden = items.slice(1, items.length - (k - 1));
  return [{ kind: 'item', item: items[0] }, { kind: 'gap', hidden }, ...tail.map((item) => ({ kind: 'item' as const, item }))];
}

// trailCrumbs is the crumbs of a place: the root when it is reached, the trail, and the directory shown, with the middle of
// a long way collapsed. place makes the crumb of a folder. A truncated trail has no root crumb, since its root is not known:
// the mark for the folders above comes first instead (decision 140).
export function trailCrumbs<T>(here: Here, root: T, place: (n: Node) => T): Crumb<T>[] {
  const out: T[] = [];
  if (here.rootReachable) out.push(root);
  for (const n of here.trail) out.push(place(n));
  if (here.dir) out.push(place(here.dir));
  const crumbs = trimCrumbs(out, 4);
  return here.truncated ? [{ kind: 'above' }, ...crumbs] : crumbs;
}
