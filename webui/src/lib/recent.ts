// SPDX-License-Identifier: AGPL-3.0-or-later

// The rules of the recent list, as plain functions. The server decides what is recorded and kept; these only say
// what a row shows.
import type { Node } from './files/types';

// RecentItem is a node in the caller's recent list. viewed_at is the caller's last open, and edited_at the last
// change the caller made; either may be null. last_at is the later of the two.
export interface RecentItem extends Node {
  viewed_at: string | null;
  edited_at: string | null;
  last_at: string;
}

// Activity is the caller's latest act on an item: an open, or a change.
export interface Activity {
  kind: 'opened' | 'changed';
  at: string;
}

// lastActivity says what the caller last did with an item, and when. When both times are set, the later one wins;
// a tie goes to the open, which the server records more often.
export function lastActivity(item: RecentItem): Activity {
  const viewed = item.viewed_at;
  const edited = item.edited_at;
  if (viewed && edited) {
    return Date.parse(edited) > Date.parse(viewed) ? { kind: 'changed', at: edited } : { kind: 'opened', at: viewed };
  }
  if (edited) return { kind: 'changed', at: edited };
  return { kind: 'opened', at: viewed ?? item.last_at };
}

// activityKey is the catalogue key of the line that says what the caller did last.
export function activityKey(a: Activity): 'recent.opened' | 'recent.changed' {
  return a.kind === 'changed' ? 'recent.changed' : 'recent.opened';
}

// mergeRecent appends a page to the items already shown, leaving out any item that is already there, so a page read
// twice does not repeat a row.
export function mergeRecent(shown: RecentItem[], page: RecentItem[]): RecentItem[] {
  const seen = new Set(shown.map((i) => i.id));
  return [...shown, ...page.filter((i) => !seen.has(i.id))];
}
