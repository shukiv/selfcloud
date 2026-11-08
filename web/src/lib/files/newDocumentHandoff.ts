// SPDX-License-Identifier: AGPL-3.0-or-later

// The hand-over of a new document (decision 102, round 2 R5). The person's click makes a random key and opens the wait page
// with that key alone. The opener makes the file, once, and hands the result over under the key: on a BroadcastChannel, and in
// localStorage for a tab that loads after the message. The wait page only reads the hand-over; a GET never creates a file.
import { safeFrom } from "./editor";

export const KEY_PATTERN = /^[A-Za-z0-9_-]{22}$/;
const NOD = /^nod_[A-Za-z0-9_-]+$/;
const CHANNEL = "selfcloud.newdoc";
const PREFIX = "selfcloud.newdoc.";
// PENDING_MS is how long a started creation may take before its tab gives up; RESULT_MS how long a result waits for its tab.
const PENDING_MS = 120_000;
const RESULT_MS = 60_000;

export type Handoff = { node: string; from: string | null; tab: boolean } | { error: string };

export type Taken =
  | { state: "unknown" }
  | { state: "expired" }
  | { state: "pending"; expires: number }
  | { state: "done"; handoff: Handoff };

interface Entry {
  state: "pending" | "done";
  expires: number;
  handoff?: Handoff;
}

// newKey is 128 random bits, base64url, so a key is never one the page could guess.
export function newKey(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// waitPath is the address of the wait page: the key, and nothing else.
export function waitPath(k: string): string {
  return `/edit/wait?k=${k}`;
}

function store(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function readEntry(k: string): Entry | null {
  try {
    const raw = store()?.getItem(PREFIX + k);
    if (!raw) return null;
    const e = JSON.parse(raw) as Entry;
    if ((e.state !== "pending" && e.state !== "done") || typeof e.expires !== "number") return null;
    return e;
  } catch {
    return null;
  }
}

function writeEntry(k: string, e: Entry): void {
  try {
    store()?.setItem(PREFIX + k, JSON.stringify(e));
  } catch {
    // Storage is off: the channel still carries the result to a tab that is already open.
  }
}

function removeEntry(k: string): void {
  try {
    store()?.removeItem(PREFIX + k);
  } catch {
    // Nothing to remove.
  }
}

// purgeExpired removes the hand-overs whose time has passed, and any entry that cannot be read.
function purgeExpired(now: number): void {
  try {
    const s = store();
    if (s === null) return;
    const dead: string[] = [];
    for (let i = 0; i < s.length; i++) {
      const name = s.key(i);
      if (name === null || !name.startsWith(PREFIX)) continue;
      const e = readEntry(name.slice(PREFIX.length));
      if (e === null || e.expires <= now) dead.push(name);
    }
    for (const name of dead) s.removeItem(name);
  } catch {
    // The next creation tries again.
  }
}

function channel(): BroadcastChannel | null {
  try {
    return typeof BroadcastChannel === "function" ? new BroadcastChannel(CHANNEL) : null;
  } catch {
    return null;
  }
}

// startHandoff is the click's part: the key is pending until its result is handed over.
export function startHandoff(k: string, now: number = Date.now()): void {
  purgeExpired(now);
  writeEntry(k, { state: "pending", expires: now + PENDING_MS });
}

// finishHandoff stores the result for the key's tab, then tells any tab that is listening now.
export function finishHandoff(k: string, handoff: Handoff, now: number = Date.now()): void {
  writeEntry(k, { state: "done", expires: now + RESULT_MS, handoff });
  const ch = channel();
  ch?.postMessage({ k, handoff });
  ch?.close();
}

function validHandoff(h: Handoff | undefined): h is Handoff {
  if (h === undefined || h === null) return false;
  if ("error" in h) return typeof h.error === "string";
  return typeof h.node === "string" && NOD.test(h.node) && typeof h.tab === "boolean";
}

// takeHandoff reads the key's state, and takes a result: a taken result is gone from storage. A key nobody started, or one
// whose time has passed, is refused.
export function takeHandoff(k: string, now: number = Date.now()): Taken {
  if (!KEY_PATTERN.test(k)) return { state: "unknown" };
  const e = readEntry(k);
  if (e === null) return { state: "unknown" };
  if (e.expires <= now) {
    removeEntry(k);
    return { state: "expired" };
  }
  if (e.state === "pending") return { state: "pending", expires: e.expires };
  removeEntry(k);
  if (!validHandoff(e.handoff)) return { state: "unknown" };
  const h = e.handoff;
  if ("error" in h) return { state: "done", handoff: h };
  return { state: "done", handoff: { node: h.node, from: safeFrom(h.from), tab: h.tab } };
}

// listenHandoff calls cb when a result for the key is handed over while this tab is open. It returns the stop function.
export function listenHandoff(k: string, cb: (msg: { k: string; handoff: Handoff }) => void): () => void {
  const ch = channel();
  if (ch === null) return () => {};
  ch.onmessage = (ev: MessageEvent) => {
    const d = ev.data as { k?: unknown; handoff?: Handoff } | null;
    if (d !== null && d.k === k && d.handoff !== undefined) cb({ k, handoff: d.handoff });
  };
  return () => ch.close();
}
