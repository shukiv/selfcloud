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

// MAX_NAME_BYTES and MAX_EXT_BYTES are the server's limits (internal/api/transfer.go, office_saveas.go): a name is at most 255
// UTF-8 bytes, and an extension, with its dot, at most 32. A dot first is not an extension.
export const MAX_NAME_BYTES = 255;
export const MAX_EXT_BYTES = 32;

function utf8Len(s: string): number {
  return new TextEncoder().encode(s).length;
}

// extOf is a name's extension, with its dot; "" when the name has none, or when the ending is longer than MAX_EXT_BYTES (the
// server then keeps the whole name as the base).
export function extOf(name: string): string {
  const i = name.lastIndexOf(".");
  if (i <= 0) return "";
  const ext = name.slice(i);
  return utf8Len(ext) <= MAX_EXT_BYTES ? ext : "";
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

// SAVE_AS_WINDOW_MS is how long a Save As the page started may bind its copy (decision 107): the editor answers in seconds.
export const SAVE_AS_WINDOW_MS = 60_000;

// COPY_SKEW_MS is the allowance for a difference between the server's clock and this browser's, on a copy's creation time.
export const COPY_SKEW_MS = 30_000;

function escapeRe(v: string): string {
  return v.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// parseCreated reads a node's created_at. The server's clock may write more fraction digits than the three a JavaScript date
// is sure to read, so the fraction is cut or padded to three first. Anything else, or an unreadable time, is NaN.
export function parseCreated(v: string): number {
  const m = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d+))?(Z|[+-]\d{2}:\d{2})$/.exec(v);
  if (m === null) return NaN;
  const frac = (m[2] ?? "").padEnd(3, "0").slice(0, 3);
  return Date.parse(`${m[1]}.${frac}${m[3]}`);
}

// copyCandidates lists the files a Save As with this requested name may have made in its folder. A candidate is a file that
// was not in the folder when the Save As was asked (before: the ids then listed, see listDirectory), whose name is the requested
// name or the free form the server gives a taken name, and whose creation time is at or after the request less COPY_SKEW_MS.
// Names are compared in NFC, the form the server stores. The free form is "base (n).ext" with n from 1 to 1000; when
// "base (1000).ext" would not fit MAX_NAME_BYTES the server cuts the base, so no free form is a candidate then. The editor's
// answer chooses among these; it cannot make a file a candidate.
export function copyCandidates(items: Node[], requested: string, requestedAtMs: number, before: ReadonlySet<string>): Node[] {
  const req = requested.normalize("NFC");
  const ext = extOf(req);
  const base = ext === "" ? req : req.slice(0, req.length - ext.length);
  const freeFits = utf8Len(`${base} (1000)${ext}`) <= MAX_NAME_BYTES;
  const free = new RegExp(`^${escapeRe(base)} \\((?:[1-9]\\d{0,2}|1000)\\)${escapeRe(ext)}$`);
  return items.filter((n) => {
    if (n.kind !== "file" || before.has(n.id)) return false;
    const name = n.name.normalize("NFC");
    if (name !== req && !(freeFits && free.test(name))) return false;
    const made = parseCreated(n.created_at);
    return Number.isFinite(made) && made >= requestedAtMs - COPY_SKEW_MS;
  });
}

// listDirectory reads every child of a directory, page by page, through read (one page for a cursor). It throws when the
// directory holds more than cap children, or when a read fails: a caller then knows nothing of the directory and fails closed.
export async function listDirectory(
  read: (cursor?: string) => Promise<{ items: Node[]; next_cursor?: string | null }>,
  cap = 2000,
): Promise<Node[]> {
  const out: Node[] = [];
  let cursor: string | undefined;
  for (;;) {
    const page = await read(cursor);
    out.push(...page.items);
    if (out.length > cap) throw new Error("directory over the listing cap");
    if (!page.next_cursor) return out;
    cursor = page.next_cursor;
  }
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
