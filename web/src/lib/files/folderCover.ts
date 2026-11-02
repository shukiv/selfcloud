// SPDX-License-Identifier: AGPL-3.0-or-later

// The cover of an album whose files hold no picture (slice x6, decision 63). The image is looked for among the direct children
// of the audio file's own folder, and only images count (jpg, jpeg, png, webp). The rule, in order:
//   1. a name of cover, folder, front or album (case-insensitive, without its extension), in that order;
//   2. a name equal to the track's album tag or to the folder's name, or a name contained in the folder's name;
//   3. the folder's only image, when the whole folder was read.
// Step 3 needs the whole folder: up to PAGES_MAX pages of the folder are read (PAGE entries each), in the server's default
// order, and the walk stops early when a cover-named image is found, since step 1 cannot be beaten. A folder larger than
// that gets no step 3 answer. A good listing is kept for LISTING_TTL_MS, so a picture added later appears; a failed read is
// not kept at all.

import { childrenPage, statNode } from "./api";
import { session, sessionKey } from "../session";
import type { Node } from "./types";

// Listing is what is known of one folder: the first page of its children, whether that page is the whole folder, and the
// folder's name (absent for the folder of the root, and when the name could not be read).
export interface Listing {
  items: Node[];
  complete: boolean;
  folderName?: string;
}

// PREFERRED are the names of step 1, in order of preference.
export const PREFERRED = ["cover", "folder", "front", "album"] as const;
// PAGES_MAX is how many pages of a folder are read (250 entries at PAGE 50). LISTING_TTL_MS is how long a good listing is kept.
export const PAGES_MAX = 5;
export const LISTING_TTL_MS = 5 * 60 * 1000;
const IMAGE_EXT = new Set(["jpg", "jpeg", "png", "webp"]);

// stemOf returns the name of an image without its extension, normalised for comparison; null for a name that is not an image.
function stemOf(name: string): string | null {
  const m = /^(.*)\.([^.]+)$/.exec(name);
  if (m === null || !IMAGE_EXT.has(m[2].toLowerCase())) return null;
  return m[1].normalize("NFC").toLowerCase().trim();
}

// norm normalises a name, the album tag or the folder's name for comparison.
function norm(s: string | undefined): string {
  return (s ?? "").normalize("NFC").toLowerCase().trim();
}

// pickFolderCover applies the three steps to the children of one folder. It returns the image, or null for no cover.
export function pickFolderCover(items: Node[], o: { album?: string; folderName?: string; complete: boolean }): Node | null {
  const images = items
    .filter((n) => n.kind === "file" && stemOf(n.name) !== null)
    .map((n) => ({ node: n, stem: stemOf(n.name) as string }));
  if (images.length === 0) return null;
  const byName = (a: { node: Node }, b: { node: Node }) => (a.node.name < b.node.name ? -1 : a.node.name > b.node.name ? 1 : 0);

  // 1. The preferred names, in their order.
  for (const want of PREFERRED) {
    const hit = images.filter((i) => i.stem === want).sort(byName)[0];
    if (hit) return hit.node;
  }

  // 2. Named after the album tag or the folder: an equal name first, then a name contained in the folder's name. Two
  // images that are contained in the folder's name, and no equal one, are a tie: no cover.
  const album = norm(o.album);
  const folder = norm(o.folderName);
  const equal = images.filter((i) => (album !== "" && i.stem === album) || (folder !== "" && i.stem === folder)).sort(byName);
  if (equal.length > 0) return equal[0].node;
  if (folder !== "") {
    const inside = images.filter((i) => i.stem !== "" && folder.includes(i.stem));
    if (inside.length === 1) return inside[0].node;
  }

  // 3. The folder's only image, when the whole folder was read.
  if (o.complete && images.length === 1) {
    return images[0].node;
  }
  return null;
}

// Reader reads what is known of the folder of an audio file.
export type Reader = (n: Node) => Promise<Listing | null>;

// FolderCovers caches the listings of the folders: one listing per folder for LISTING_TTL_MS.
export interface FolderCovers {
  get(n: Node): Promise<Listing | null>;
  reset(): void;
}

// folderKeyOf names a file's folder: the same for every track of it.
export function folderKeyOf(n: Node): string {
  return `${n.folder_id}:${n.parent_id ?? ""}`;
}

// createFolderCovers builds the cache over a reader. A folder's tracks share one listing while it is fresh. A failed read is
// not kept, so the next track asks again.
export function createFolderCovers(read: Reader, now: () => number = Date.now, ttlMs: number = LISTING_TTL_MS): FolderCovers {
  let cache = new Map<string, { at: number; got: Promise<Listing | null> }>();
  return {
    get(n: Node) {
      const key = folderKeyOf(n);
      const have = cache.get(key);
      if (have !== undefined && now() - have.at < ttlMs) return have.got;
      const got = read(n).catch(() => null);
      const entry = { at: now(), got };
      cache.set(key, entry);
      void got.then((value) => {
        if (value === null && cache.get(key) === entry) cache.delete(key);
      });
      return got;
    },
    reset() {
      cache = new Map();
    },
  };
}

// isCoverName reports whether an entry is an image named cover: the one name the walk stops at.
function isCoverName(n: Node): boolean {
  return n.kind === "file" && stemOf(n.name) === PREFERRED[0];
}

// readFolder is the browser's reader: the folder's children, a page at a time (the listing's own route, with no sort, so the
// server's own order), and the folder's name from one stat of the parent. A failed listing is no listing; a failed stat leaves
// the name out.
export const readFolder: Reader = async (n) => {
  const items: Node[] = [];
  let cursor: string | undefined;
  let complete = false;
  for (let i = 0; i < PAGES_MAX; i++) {
    let page;
    try {
      page = await childrenPage(n.folder_id, n.parent_id, cursor);
    } catch {
      return null;
    }
    items.push(...(page.items ?? []));
    cursor = page.next_cursor || undefined;
    if (cursor === undefined) {
      complete = true;
      break;
    }
    if (items.some(isCoverName)) break;
  }
  let folderName: string | undefined;
  if (n.parent_id !== null) {
    try {
      folderName = (await statNode(n.parent_id)).name;
    } catch {
      folderName = undefined;
    }
  }
  return { items, complete, folderName };
};

// folderCovers is the page's cache. A change of person forgets what was read (the listings are the person's own).
export const folderCovers: FolderCovers = createFolderCovers(readFolder);
let watchedKey: string | null = null;
session.subscribe((s) => {
  const key = sessionKey(s);
  if (watchedKey !== null && key !== watchedKey) folderCovers.reset();
  watchedKey = key;
});
