// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Node } from "./types";
import { isPlayable } from "./queue";

// Pager loads a folder's playable files one page at a time, as the player needs them. It never reads a page nobody asked
// for: the playlist asks for the next page when it reaches the end of what it has, and the file picker asks when the
// person presses Load more. A page with no playable file is read and passed over. The folder's listing order is the
// server's: each page continues it.

// PageLike is the part of a listing page the pager reads.
export interface PageLike {
  items: Node[];
  next_cursor?: string;
}

// LIMIT is how many playable files a pager holds in all. It keeps a folder of many thousands from being read whole.
export const PAGER_LIMIT = 500;

// MoreResult is what an ask for the next page brings. "end" is the folder having no more to give (no page left, or the
// limit reached). "failed" is a page that could not be read: the folder may have more, and the same page can be asked
// for again. The two are never the same answer.
export type MoreResult = { kind: "page"; items: Node[] } | { kind: "end" } | { kind: "failed" };

export class Pager {
  // loaded is the playable files read so far, in the order the pages gave them. It never holds more than PAGER_LIMIT.
  loaded: Node[];
  // cut is set once a file was left out because the limit was reached: the folder holds more than the pager keeps.
  cut = false;
  // cursor is the next page to read, or null when the folder has no more pages. A failed read leaves it as it was.
  cursor: string | null;
  private busy: Promise<MoreResult> | null = null;

  constructor(
    seed: Node[],
    cursor: string | null,
    private readonly fetchPage: (cursor: string) => Promise<PageLike>,
  ) {
    const playable = seed.filter((n) => isPlayable(n));
    this.loaded = playable.slice(0, PAGER_LIMIT);
    this.cut = playable.length > PAGER_LIMIT;
    this.cursor = cursor;
  }

  // hasMore says whether the folder may have a further page to give: a page is left and the limit is not reached. It is
  // true after a failed read too, since that page is still there to be read.
  get hasMore(): boolean {
    return this.cursor !== null && this.loaded.length < PAGER_LIMIT;
  }

  // more reads the next page. A call made while a page is being read waits for that page, so two asks read one page.
  more(): Promise<MoreResult> {
    if (this.busy) return this.busy;
    if (!this.hasMore || this.cursor === null) return Promise.resolve({ kind: "end" });
    const at = this.cursor;
    this.busy = this.fetchPage(at)
      .then((page): MoreResult => {
        this.cursor = page.next_cursor ?? null;
        const all = page.items.filter((n) => isPlayable(n) && !this.loaded.some((x) => x.id === n.id));
        // The limit is kept exactly: the files past it are left out, and cut says so.
        const room = Math.max(0, PAGER_LIMIT - this.loaded.length);
        const fresh = all.slice(0, room);
        if (all.length > fresh.length) this.cut = true;
        this.loaded = [...this.loaded, ...fresh];
        return { kind: "page", items: fresh };
      })
      .catch((): MoreResult => ({ kind: "failed" }))
      .finally(() => {
        this.busy = null;
      });
    return this.busy;
  }
}
