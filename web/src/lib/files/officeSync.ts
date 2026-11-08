// SPDX-License-Identifier: AGPL-3.0-or-later

// The editor's names, copies and changes, shared between the editor tab and the other tabs (decision 107). The editor tab
// reads a name the editor sends and finds a copy by its name; it announces a changed file to the other tabs on a channel that
// carries ids only. A tab listens for a change made by the same signed-in person, and reads again the folder branch it names.
import type { QueryClient } from "@tanstack/react-query";
import { refreshTree, type Branch } from "../query";
import { get } from "../store";
import { session } from "../session";
import type { Node } from "./types";

export const OFFICE_CHANNEL = "selfcloud.office";
// ANNOUNCE_MS is the least time between two announcements for one node, on the sending side.
export const ANNOUNCE_MS = 5_000;
// FLUSH_MS is how long the receiving side waits for more changes, so one burst reads each branch once.
const FLUSH_MS = 100;

const NODE_ID = /^nod_[A-Za-z0-9_-]+$/;
const ID = /^[A-Za-z0-9_-]{1,80}$/;

// decodeEditorName reads a name the editor sends percent-encoded (File_Rename's NewName). A malformed encoding, or nothing
// usable, is null: the caller then shows nothing for it and reads the file instead.
export function decodeEditorName(raw: unknown): string | null {
  if (typeof raw !== "string") return null;
  let v: string;
  try {
    v = decodeURIComponent(raw);
  } catch {
    return null;
  }
  v = v.trim();
  return v === "" ? null : v;
}

// extOf is a name's extension, with its dot; "" when the name has none (a dot first is not an extension).
export function extOf(name: string): string {
  const i = name.lastIndexOf(".");
  return i > 0 ? name.slice(i) : "";
}

// displayName is a name the editor sent, with the extension of the file it was before when the editor sent it without one.
// The editor drops the extension from a rename, so the bar shows the name it will have, until the file is read again.
export function displayName(signalled: string, before: string): string {
  const ext = extOf(before);
  return extOf(signalled) === "" && ext !== "" ? signalled + ext : signalled;
}

// isSameName says a file's name is the name the editor gave: exactly it, or the name without its extension.
export function isSameName(nodeName: string, signalled: string): boolean {
  if (signalled === "") return false;
  if (nodeName === signalled) return true;
  const ext = extOf(nodeName);
  return ext !== "" && nodeName === signalled + ext;
}

// findCopy finds the file a Save As made, by its name, among a folder's children. Names are unique in a folder, so a
// name that matches nothing, or more than one file, is no match: the caller does not guess.
export function findCopy(items: Node[], name: string): Node | null {
  const exact = items.find((n) => n.kind === "file" && n.name === name);
  if (exact !== undefined) return exact;
  const loose = items.filter((n) => n.kind === "file" && isSameName(n.name, name));
  return loose.length === 1 ? (loose[0] ?? null) : null;
}

// NodeChanged is the announcement of a changed file: the node, its folder and its parent directory, and the user it is for.
// It never carries a name, a token or a session key.
export interface NodeChanged {
  node: string;
  folder: string;
  parent: string;
  user: string;
}

// parseNodeChanged reads an announcement from a channel. Anything without the ids and the user id is no announcement.
export function parseNodeChanged(data: unknown): ({ kind: "node-changed" } & NodeChanged) | null {
  if (typeof data !== "object" || data === null) return null;
  const d = data as Record<string, unknown>;
  if (d.kind !== "node-changed") return null;
  if (typeof d.node !== "string" || !NODE_ID.test(d.node)) return null;
  if (typeof d.folder !== "string" || !ID.test(d.folder)) return null;
  if (typeof d.parent !== "string" || !ID.test(d.parent)) return null;
  if (typeof d.user !== "string" || d.user === "") return null;
  return { kind: "node-changed", node: d.node, folder: d.folder, parent: d.parent, user: d.user };
}

// makeAnnouncer returns the sender: it calls post for a change, at most once per node in ANNOUNCE_MS. The result says whether
// the change was sent. now is the clock; a test passes its own.
export function makeAnnouncer(post: (c: NodeChanged) => void, now: () => number = Date.now): (c: NodeChanged) => boolean {
  const last = new Map<string, number>();
  return (c: NodeChanged): boolean => {
    const t = now();
    const prev = last.get(c.node);
    if (prev !== undefined && t - prev < ANNOUNCE_MS) return false;
    last.set(c.node, t);
    post({ node: c.node, folder: c.folder, parent: c.parent, user: c.user });
    return true;
  };
}

// currentUserId is the id of the person signed in now, or null with no session.
function currentUserId(): string | null {
  const s = get(session);
  return s ? s.user.id : null;
}

function postOfficeChange(c: NodeChanged): void {
  try {
    if (typeof BroadcastChannel !== "function") return;
    const ch = new BroadcastChannel(OFFICE_CHANNEL);
    ch.postMessage({ kind: "node-changed", node: c.node, folder: c.folder, parent: c.parent, user: c.user });
    ch.close();
  } catch {
    // No channel: the other tabs read the list when they are next shown.
  }
}

const announcer = makeAnnouncer(postOfficeChange);

// announceNodeChanged tells the other tabs that a file changed in a folder and directory. It is sent for the person signed in,
// and not at all with no session or no parent directory.
export function announceNodeChanged(node: string, folder: string, parent: string | null): boolean {
  const user = currentUserId();
  if (user === null || parent === null) return false;
  return announcer({ node, folder, parent, user });
}

// makeReceiver returns the receiver: a change for the signed-in person names a branch to read again; the branches of one
// burst are read together, once each. me gives the signed-in user id at the time a message arrives.
export function makeReceiver(client: QueryClient, me: () => string | null, flushMs = FLUSH_MS) {
  const pending = new Map<string, Branch>();
  let timer: ReturnType<typeof setTimeout> | null = null;
  const flush = () => {
    timer = null;
    const branches = [...pending.values()];
    pending.clear();
    if (branches.length > 0) void refreshTree(client, branches);
  };
  return {
    receive(data: unknown): void {
      const c = parseNodeChanged(data);
      if (c === null) return;
      const user = me();
      if (user === null || c.user !== user) return;
      pending.set(`${c.folder}/${c.parent}`, { folder: c.folder, dir: c.parent });
      if (timer === null) timer = setTimeout(flush, flushMs);
    },
    stop(): void {
      if (timer !== null) clearTimeout(timer);
      timer = null;
      pending.clear();
    },
  };
}

// listenOfficeChanges listens on the channel for the whole app. It returns the function that stops it.
export function listenOfficeChanges(client: QueryClient): () => void {
  const r = makeReceiver(client, currentUserId);
  let ch: BroadcastChannel | null = null;
  try {
    if (typeof BroadcastChannel === "function") {
      ch = new BroadcastChannel(OFFICE_CHANNEL);
      ch.onmessage = (ev: MessageEvent) => r.receive(ev.data);
    }
  } catch {
    ch = null;
  }
  return () => {
    ch?.close();
    r.stop();
  };
}
