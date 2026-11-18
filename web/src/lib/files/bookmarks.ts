// SPDX-License-Identifier: AGPL-3.0-or-later

// The bookmark rules the player applies (decision 218 owns the server half): the cap and the label's length, the order a list
// is shown in, and the shapes the hook and the dialog share. The api functions live in ./api; BookmarkApi is the four of them
// as one object, so the player can be given them or not (a share link and an impersonated session get none).
import type { Bookmark } from './api';

// BOOKMARK_LIMIT is the most bookmarks one person may keep in one file; the server refuses the next with a 409 (reason limit).
export const BOOKMARK_LIMIT = 200;

// LABEL_MAX is the most characters a bookmark's label holds; the server trims and caps it the same way.
export const LABEL_MAX = 100;

// BookmarkApi is the four bookmark routes as one object the player is given.
export interface BookmarkApi {
  list: (nod: string) => Promise<Bookmark[]>;
  add: (nod: string, position: number, label: string) => Promise<Bookmark>;
  relabel: (nod: string, bmk: string, label: string) => Promise<Bookmark>;
  remove: (nod: string, bmk: string) => Promise<void>;
}

// sortBookmarks orders a list by its place, then by when it was made, then by its id, into a new array; the one given is untouched.
export function sortBookmarks(items: Bookmark[]): Bookmark[] {
  return [...items].sort((a, b) => {
    if (a.position_seconds !== b.position_seconds) return a.position_seconds - b.position_seconds;
    if (a.created_at !== b.created_at) return a.created_at < b.created_at ? -1 : 1;
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
  });
}

// Marks is what the player's controls are given: the file's bookmarks in order, whether the list has been read, whether it is
// at the cap, and the two actions the More menu and the dialog run.
export interface Marks {
  items: Bookmark[];
  ready: boolean;
  full: boolean;
  add: () => void;
  openList: () => void;
}
