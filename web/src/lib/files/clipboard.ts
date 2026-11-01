// SPDX-License-Identifier: AGPL-3.0-or-later

import { writable } from "../store";
import { session } from "../session";
import type { Node } from "./types";

// The file browser's clipboard: the items copied or cut with Ctrl+C or Ctrl+X. It lasts while the tab does, across folders,
// until the items are pasted by a move, the person cancels a cut with Escape, or the person signs out. It is kept in this
// page only: the system clipboard is not used, since the files are not on this computer.
export type ClipMode = "copy" | "cut";

export interface Clip {
  mode: ClipMode;
  nodes: Node[];
}

export const clipboard = writable<Clip | null>(null);

export function setClip(mode: ClipMode, nodes: Node[]): void {
  clipboard.set({ mode, nodes });
}

export function clearClip(): void {
  clipboard.set(null);
}

// The clipboard holds one person's items, so a change of person (a sign-out, or a sign-in as someone else) empties it, wherever
// the page is. The subscription is made when the module loads; the store calls it at once, which only records the person.
let person: string | null = null;
session.subscribe((s) => {
  const who = s?.user.id ?? "";
  if (person !== null && who !== person) clearClip();
  person = who;
});

// cutIDs is the IDs of the items a cut has taken, which the listing shows dimmed until they are pasted or the cut is cancelled.
export function cutIDs(clip: Clip | null): Set<string> {
  return new Set(clip?.mode === "cut" ? clip.nodes.map((n) => n.id) : []);
}
