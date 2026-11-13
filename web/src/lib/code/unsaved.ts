// SPDX-License-Identifier: AGPL-3.0-or-later

// The code editor's unsaved text, kept across a sign-in for the same person (decision 133).
// This module is the part the sign-in and sign-out paths need, and it is in the first load: the purges at every sign-in and sign-out,
// and the sign-out channel. The text itself is kept and read in kept.ts, which only the editor loads. Nothing here
// reads the session: the session's own changes call the purges and the channel. The module imports nothing, so the entry chunk that
// signs people in and out does not grow with the editor.

// PREFIX is the key prefix of every kept entry in the tab's sessionStorage (kept.ts writes them; the purges here find them).
export const PREFIX = "selfcloud.code-unsaved:";

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
      // A store that refuses a removal keeps its entry; the next purge removes it.
    }
  }
}

// purgeAllUnsaved removes the text of every person, at an explicit sign-out.
export function purgeAllUnsaved(): void {
  for (const k of keysWhere(() => true)) {
    try {
      window.sessionStorage.removeItem(k);
    } catch {
      // See purgeOtherUnsaved.
    }
  }
}

// The sign-out channel (decision 133, rule 5): an explicit sign-out tells the other tabs of this browser, so that each drops the text
// it kept. A sign-in tells them too, with the signing-in person's id, so that a tab which kept nothing after another tab's sign-out keeps
// text again, and drops the text of anyone else. Neither message carries text. A browser without channels has no other tab to tell.
export const SIGNOUT_CHANNEL = "selfcloud-code-unsaved";
const SIGNED_OUT = "signed-out";
const SIGNED_IN = "signed-in";

// endedElsewhere is set in a tab that heard another tab's sign-out. The session end that follows is that sign-out, so this tab keeps
// nothing until a sign-in happens in this browser: this tab's own, or another tab's sign-in message (noteSignedIn).
let endedElsewhere = false;

// channel is this tab's one channel, made on first use. Its own messages are not delivered to it, so a sign-out or sign-in in this tab
// does not set or clear the flag here.
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

// signedOutElsewhere says this tab heard another tab's explicit sign-out and has not seen a sign-in since. kept.ts keeps nothing then.
export function signedOutElsewhere(): boolean {
  return endedElsewhere;
}

// announceSignOut tells the other tabs that the person signed out on purpose. The sign-out calls it after this tab's own purge.
export function announceSignOut(): void {
  try {
    openChannel()?.postMessage({ type: SIGNED_OUT });
  } catch {
    // A channel that refuses the message: the other tabs keep their text, as they do in a browser without channels.
  }
}

// watchSignOuts makes this tab hear the other tabs' explicit sign-outs and sign-ins: a sign-out drops every text it kept and keeps
// nothing until a sign-in; a sign-in ends that window. It returns the function that stops the watch, which closes the channel.
// session.ts starts the watch when the app loads, so every tab has it.
export function watchSignOuts(): () => void {
  const ch = openChannel();
  if (ch === null) return () => undefined;
  ch.onmessage = (e: MessageEvent) => {
    const msg = e.data as { type?: unknown } | null;
    if (msg?.type === SIGNED_IN) {
      // A sign-in anywhere in this browser ends the keep-nothing window, and drops the text of every other person kept here.
      endedElsewhere = false;
      const who = (msg as { user?: unknown }).user;
      if (typeof who === "string") purgeOtherUnsaved(who);
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
// the browser, which keep text again too and drop the text of anyone else. The message names the person and carries no text.
export function noteSignedIn(user: string): void {
  endedElsewhere = false;
  purgeOtherUnsaved(user);
  try {
    openChannel()?.postMessage({ type: SIGNED_IN, user });
  } catch {
    // A channel that refuses the message: the other tabs keep their window, as they do in a browser without channels.
  }
}
