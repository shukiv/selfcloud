// SPDX-License-Identifier: AGPL-3.0-or-later

// The code editor's unsaved text, kept across a sign-in for the same person (design-ce 4.3 and 4.6, decision 133). The text is kept in
// this browser tab only (sessionStorage), one entry per person and file, so it comes back when that person signs in again and opens
// the file. Nothing here reads the session: the page passes the person's id, and the session's own changes call the purges.
// Every storage call is in try/catch: a store that throws or is full means the text is not kept, and nothing else changes.
// The module imports nothing, so the entry chunk that signs people in and out does not grow with the editor.

const PREFIX = "selfcloud.code-unsaved:";

// Kept is one kept text: the editor's text (line breaks as LF, which is how the editor holds it), the ETag the edit was based on (the
// If-Match of a save of this text), the file's byte order mark and line break (lib/code/edit.ts), its encoding (UTF-8 only, as the
// editor reads it), and the time it was kept.
export interface Kept {
  text: string;
  etag: string;
  bom: boolean;
  sep: "\n" | "\r\n" | "\r";
  enc: "utf-8";
  at: number;
}

// keyOf is the entry of one person's file. The person's id is encoded and ends at a colon, so one id never reads another's entry.
const keyOf = (user: string, node: string) => `${PREFIX}${encodeURIComponent(user)}:${node}`;

// keepUnsaved stores a text for a person's file. It says whether the store took it: false is "not kept" (the store refused, or is full).
export function keepUnsaved(user: string, node: string, text: Omit<Kept, "enc" | "at">): boolean {
  try {
    const rec: Kept = { text: text.text, etag: text.etag, bom: text.bom, sep: text.sep, enc: "utf-8", at: Date.now() };
    window.sessionStorage.setItem(keyOf(user, node), JSON.stringify(rec));
    return true;
  } catch {
    return false;
  }
}

// readUnsaved gives the text kept for a person's file, or null when there is none, or the entry is not one this module wrote.
export function readUnsaved(user: string, node: string): Kept | null {
  try {
    const raw = window.sessionStorage.getItem(keyOf(user, node));
    if (raw === null) return null;
    const v = JSON.parse(raw) as Partial<Kept> | null;
    if (
      typeof v !== "object" ||
      v === null ||
      typeof v.text !== "string" ||
      typeof v.etag !== "string" ||
      typeof v.bom !== "boolean" ||
      (v.sep !== "\n" && v.sep !== "\r\n" && v.sep !== "\r") ||
      v.enc !== "utf-8" ||
      typeof v.at !== "number"
    ) {
      return null;
    }
    return v as Kept;
  } catch {
    return null;
  }
}

// dropUnsaved removes the text kept for a person's file: a save that took it, a discard, a file that is gone or forbidden.
export function dropUnsaved(user: string, node: string): void {
  try {
    window.sessionStorage.removeItem(keyOf(user, node));
  } catch {
    // A store that refuses a removal keeps its entry; the entry is still the person's own, and the next purge removes it.
  }
}

// keysWhere lists this module's keys in the tab whose person matches drop.
function keysWhere(drop: (user: string) => boolean): string[] {
  const out: string[] = [];
  try {
    const s = window.sessionStorage;
    for (let i = 0; i < s.length; i++) {
      const k = s.key(i);
      if (k === null || !k.startsWith(PREFIX)) continue;
      const user = decodeURIComponent(k.slice(PREFIX.length, k.indexOf(":", PREFIX.length)));
      if (drop(user)) out.push(k);
    }
  } catch {
    return out;
  }
  return out;
}

// purgeOtherUnsaved removes the text of every person but this one. It runs when a person signs in, before anything is shown.
export function purgeOtherUnsaved(user: string): void {
  for (const k of keysWhere((u) => u !== user)) {
    try {
      window.sessionStorage.removeItem(k);
    } catch {
      // See dropUnsaved.
    }
  }
}

// purgeAllUnsaved removes the text of every person, at an explicit sign-out.
export function purgeAllUnsaved(): void {
  for (const k of keysWhere(() => true)) {
    try {
      window.sessionStorage.removeItem(k);
    } catch {
      // See dropUnsaved.
    }
  }
}
