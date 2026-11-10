// SPDX-License-Identifier: AGPL-3.0-or-later

// The code editor's unsaved text, kept across a sign-in for the same person (design-ce 4.3 and 4.6, not in this repository; decision 133). The text is kept in
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
  // A tab that heard another tab's explicit sign-out keeps nothing until the next sign-in (see endedElsewhere).
  if (endedElsewhere) return false;
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

// The sign-out channel (decision 133, rule 5): an explicit sign-out tells the other tabs of this browser, so that each drops the text
// it kept. The message carries no text, only the fact. A browser without channels has no other tab to tell, and keeps today's
// behaviour: each tab's own text is kept as before.
export const SIGNOUT_CHANNEL = "selfcloud-code-unsaved";
const SIGNED_OUT = "signed-out";
const SIGNED_IN = "signed-in";

// endedElsewhere is set in a tab that heard another tab's sign-out. The session end that follows is that sign-out, so this tab keeps
// nothing until a sign-in happens in this browser: this tab's own, or another tab's sign-in message (noteSignedIn).
let endedElsewhere = false;

// channel is this tab's one channel, made on first use. Its own messages are not delivered to it, so a sign-out in this tab does not
// set the flag here.
let channel: BroadcastChannel | null = null;

function openChannel(): BroadcastChannel | null {
  if (channel !== null) return channel;
  if (typeof BroadcastChannel !== "function") return null;
  try {
    channel = new BroadcastChannel(SIGNOUT_CHANNEL);
  } catch {
    return null;
  }
  return channel;
}

// announceSignOut tells the other tabs that the person signed out on purpose. The sign-out calls it after this tab's own purge.
export function announceSignOut(): void {
  try {
    openChannel()?.postMessage({ type: SIGNED_OUT });
  } catch {
    // A channel that refuses the message: the other tabs keep their text, as they do in a browser without channels.
  }
}

// watchSignOuts makes this tab hear the other tabs' explicit sign-outs: it drops every text it kept and keeps nothing until the next
// sign-in. It returns the function that stops the watch. session.ts starts it when the app loads, so every tab has it.
export function watchSignOuts(): () => void {
  const ch = openChannel();
  if (ch === null) return () => undefined;
  ch.onmessage = (e: MessageEvent) => {
    const msg = e.data as { type?: unknown } | null;
    if (msg?.type === SIGNED_IN) {
      // A sign-in anywhere in this browser ends the keep-nothing window. It names no person and drops no text: the purge of other
      // people's drafts is the signing-in tab's own, at its sign-in (session.ts).
      endedElsewhere = false;
      return;
    }
    if (msg?.type !== SIGNED_OUT) return;
    endedElsewhere = true;
    purgeAllUnsaved();
  };
  return () => {
    // Stopping the watch closes this tab's channel. A later announce or watch opens a new one.
    ch.onmessage = null;
    ch.close();
    if (channel === ch) channel = null;
  };
}

// noteSignedIn is called when this tab signs a person in (session.ts). It keeps text again in this tab, and tells the other tabs of
// the browser, which keep text again too (review round 2, N1). The message carries no text.
export function noteSignedIn(): void {
  endedElsewhere = false;
  try {
    openChannel()?.postMessage({ type: SIGNED_IN });
  } catch {
    // A channel that refuses the message: the other tabs keep their window, as they do in a browser without channels.
  }
}
