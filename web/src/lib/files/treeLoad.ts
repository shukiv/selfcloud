// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Node, Page } from "./types";

// MAX_PAGES caps how far one folder is read for the tree: 10 pages of 50 is 500 folders. A folder with more shows a note.
export const MAX_PAGES = 10;

export interface Dirs {
  dirs: Node[];
  // capped says the folder has more folders than were read.
  capped: boolean;
}

// loadDirs reads the folders inside one place, for the tree. The server lists folders first, then shortcuts, then files, so
// the reading stops at the first page that has anything else, or has no next page; no page of files is read for the tree.
export async function loadDirs(read: (cursor?: string) => Promise<Page<Node>>, maxPages = MAX_PAGES): Promise<Dirs> {
  const dirs: Node[] = [];
  let cursor: string | undefined;
  for (let i = 0; i < maxPages; i++) {
    const page = await read(cursor);
    for (const n of page.items) if (n.kind === "dir") dirs.push(n);
    if (!page.next_cursor || page.items.some((n) => n.kind !== "dir")) return { dirs, capped: false };
    cursor = page.next_cursor;
  }
  return { dirs, capped: true };
}
