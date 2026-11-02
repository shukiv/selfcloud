// SPDX-License-Identifier: AGPL-3.0-or-later

// The playlist of the media player: the playable files of a folder, or the selection, in an order the person can change.
// It holds no network state and reads no clock, so every rule here is a plain function that the tests can run. The
// player (components/files/MediaPlaylist.tsx) asks it what comes next, reports failures and playbacks back, and asks
// for more pages of the folder when the queue has nothing further loaded.
//
// Rules, in short. Without shuffle, the next file is the next one in the queue's order, and repeat all starts again
// from the top. With shuffle, the files are dealt into a round in a random order; a file is not played again until
// every file in the round has been played, and repeat all deals a new round. Failed files, and files the server refused
// a link for (restricted), are skipped, and a run of failures ends playback, so a folder of broken files does not loop.
// A no-download file plays like any other: the no-download rule binds downloads, and the player's link is not one.

import { kindOf } from "./kind";
import type { Node } from "./types";

// MAX_QUEUE is the most files a queue holds. A folder can hold more; the queue takes the first MAX_QUEUE playable files in
// the listing's order and says it has been cut (capped).
export const MAX_QUEUE = 500;

// MAX_FAILURE_RUN is how many files may fail in a row before playback stops. It is smaller than the queue when the queue
// is long, so a folder of broken files stops after this many link requests, not after all of them.
export const MAX_FAILURE_RUN = 10;

export type Repeat = "off" | "all" | "one";
// Mark says what the queue knows about an entry: ok (it may play), restricted (the server refused its link, so it is
// skipped without another request) or failed (the browser could not play it).
// Unavailable is a saved playlist's entry whose file the person cannot reach now, or that was deleted or lost (decision
// 58). It has no name and is never played, and it is never counted as a failure, so a run of them stops nothing.
export type Mark = "ok" | "restricted" | "failed" | "unavailable";

export interface Entry {
  node: Node;
  mark: Mark;
}

export interface Queue {
  entries: Entry[];
  // current is the ID of the file on screen, or null when none is (after the current file was removed).
  current: string | null;
  // pos is where the next file is looked for when there is no current file.
  pos: number;
  repeat: Repeat;
  shuffle: boolean;
  // plan is the rest of the round, in play order, when shuffle is on.
  plan: string[];
  // past is the files played in the round, latest last, so Previous can go back to them when shuffle is on.
  past: string[];
  // run counts the files that failed in a row. A file that plays resets it.
  run: number;
  // capped is set once the queue has refused a file because it is full.
  capped: boolean;
}

// Advance is the answer to "what plays next": a file to play, a request for more of the folder before the answer can be
// given, or the reason playback stops.
export type Advance =
  // again is true when the file to play is the one already on screen (the only playable file, under repeat all): the
  // caller must start it from the beginning, since no change of file will happen.
  | { kind: "play"; queue: Queue; id: string; again: boolean }
  | { kind: "more" }
  | { kind: "stop"; queue: Queue; reason: "end" | "failed" | "none" | "empty" };

export type Rng = () => number;

// isPlayable is true for the files the player plays: a video or an audio file.
export function isPlayable(node: Pick<Node, "kind" | "mime_type">): boolean {
  const k = kindOf(node);
  return k === "video" || k === "audio";
}

// markOf is the mark a file starts with. Every playable file starts ok: playing is not downloading, and the server answers
// the player's link for a no-download file as it does for any other (inline). A refusal, if the server makes one, marks
// the file restricted when it is asked for.
function markOf(_node: Node): Mark {
  return "ok";
}

// shuffled returns the list in a random order (Fisher-Yates), taking the numbers from rng.
function shuffled<T>(items: T[], rng: Rng): T[] {
  const out = items.slice();
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(rng() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

// dedupe keeps the first of each file, and only playable files.
function playableNodes(nodes: Node[]): Node[] {
  const seen = new Set<string>();
  const out: Node[] = [];
  for (const n of nodes) {
    if (!isPlayable(n) || seen.has(n.id)) continue;
    seen.add(n.id);
    out.push(n);
  }
  return out;
}

const okIDs = (q: Queue): string[] => q.entries.filter((e) => e.mark === "ok").map((e) => e.node.id);

// newQueue makes the queue for a list of files, starting at the file with the ID given. Files that are not playable are
// left out; the queue keeps the list's order.
export function newQueue(nodes: Node[], start: string, opts: { shuffle: boolean; repeat: Repeat }, rng: Rng = Math.random): Queue {
  const all = playableNodes(nodes);
  // The first MAX_QUEUE files in the list's order. The file to start with is kept even when it is past the cut.
  let kept = all.slice(0, MAX_QUEUE);
  const startNode = all.find((n) => n.id === start);
  if (startNode && !kept.some((n) => n.id === start)) kept = [startNode, ...kept.slice(0, MAX_QUEUE - 1)];
  const entries: Entry[] = kept.map((node) => ({ node, mark: markOf(node) }));
  const startIndex = entries.findIndex((e) => e.node.id === start);
  const q: Queue = {
    entries,
    current: startIndex >= 0 ? start : null,
    pos: Math.max(0, startIndex),
    repeat: opts.repeat,
    shuffle: false,
    plan: [],
    past: [],
    run: 0,
    // Cut only when files were left out: a folder of exactly MAX_QUEUE files is not cut.
    capped: all.length > entries.length,
  };
  return opts.shuffle ? toggleShuffle(q, rng) : q;
}

// pickNext finds the next file to play, from the queue as it stands. It returns the file and the queue with the file taken
// out of the round, "more" when the folder has more pages to load before the answer can be given, or null when nothing
// plays after the current file.
function pickNext(q: Queue, more: boolean, rng: Rng): { id: string; queue: Queue } | "more" | null {
  if (q.shuffle) {
    const ok = new Set(okIDs(q));
    const plan = q.plan.filter((id) => ok.has(id));
    if (plan.length > 0) return { id: plan[0], queue: { ...q, plan: plan.slice(1) } };
    if (more) return "more";
    if (q.repeat !== "all") return null;
    // A new round: everything playable, with the file just played last so it does not come straight back.
    const all = [...ok];
    const others = all.filter((id) => id !== q.current);
    const round = shuffled(others, rng);
    if (q.current !== null && ok.has(q.current)) round.push(q.current);
    if (round.length === 0) return null;
    return { id: round[0], queue: { ...q, plan: round.slice(1) } };
  }
  const first = q.current === null ? q.pos : q.entries.findIndex((e) => e.node.id === q.current) + 1;
  for (let i = Math.max(0, first); i < q.entries.length; i++) {
    if (q.entries[i].mark === "ok") return { id: q.entries[i].node.id, queue: q };
  }
  if (more) return "more";
  if (q.repeat !== "all") return null;
  for (let i = 0; i < q.entries.length; i++) {
    if (q.entries[i].mark === "ok") return { id: q.entries[i].node.id, queue: q };
  }
  return null;
}

// advance says what plays next after the current file, and returns the queue with that file as current. It is called when
// a file ends, when a file is skipped, and when the person presses Next. A repeat-one run is not advanced by the caller:
// the element loops on its own.
export function advance(q: Queue, more: boolean, rng: Rng = Math.random): Advance {
  if (q.run >= MAX_FAILURE_RUN) return { kind: "stop", queue: q, reason: "failed" };
  const r = pickNext(q, more, rng);
  if (r === "more") return { kind: "more" };
  if (r === null) {
    const anyOK = q.entries.some((e) => e.mark === "ok");
    // empty: the list has no playable file at all. none: it has some, and none is left that can play (every one failed or
    // may not be downloaded). end: the list has ended. failed (above) is the run of failures that stops playback.
    const reason = q.entries.length === 0 ? "empty" : anyOK ? "end" : "none";
    return { kind: "stop", queue: q, reason };
  }
  const past = q.current !== null && q.current !== r.id ? [...r.queue.past, q.current] : r.queue.past;
  return {
    kind: "play",
    id: r.id,
    again: r.id === q.current,
    queue: { ...r.queue, current: r.id, past: q.shuffle ? past : [] },
  };
}

// choose makes a file the one on screen because the person picked it (a row of the list). A file that failed before may
// be picked again (it is retried); a file the person may not download cannot be. The failure run starts again, so a
// stopped playlist can carry on. Under shuffle the file leaves the round, and the file that was on screen goes into the
// past, so Previous goes back to it and the round keeps its rule: a file is not played again before the round is over.
export function choose(q: Queue, id: string): Queue {
  const entry = q.entries.find((e) => e.node.id === id);
  // A restricted file and an unavailable saved entry cannot be picked: an unavailable one has no file to play.
  if (!entry || entry.mark === "restricted" || entry.mark === "unavailable") return q;
  const at = q.entries.findIndex((e) => e.node.id === id);
  const entries = q.entries.map((e) => (e.node.id === id ? { ...e, mark: "ok" as Mark } : e));
  if (!q.shuffle) return { ...q, entries, current: id, pos: at, run: 0 };
  const past = q.current !== null && q.current !== id ? [...q.past.filter((x) => x !== id), q.current] : q.past.filter((x) => x !== id);
  return { ...q, entries, current: id, plan: q.plan.filter((x) => x !== id), past, run: 0 };
}

// played marks that the current file has started playing: the run of failures starts again.
export function played(q: Queue, id: string): Queue {
  return q.current === id ? { ...q, run: 0 } : q;
}

// failed marks a file the browser could not play. The run of failures grows by one.
export function failed(q: Queue, id: string): Queue {
  return { ...mark(q, id, "failed"), run: q.run + 1 };
}

// restricted marks a file the person may not download. It is skipped without a request.
export function restricted(q: Queue, id: string): Queue {
  return mark(q, id, "restricted");
}

function mark(q: Queue, id: string, m: Mark): Queue {
  return { ...q, entries: q.entries.map((e) => (e.node.id === id ? { ...e, mark: m } : e)) };
}

// back goes to the file played before the current one: the one before it in the queue's order, or the one before it in
// the round when shuffle is on. It is null where there is no such file.
export function back(q: Queue): { queue: Queue; id: string } | null {
  const ok = new Set(okIDs(q));
  if (q.shuffle) {
    const prev = [...q.past].reverse().find((id) => ok.has(id));
    if (prev === undefined) return null;
    const past = q.past.slice(0, q.past.lastIndexOf(prev));
    const plan = q.current !== null ? [q.current, ...q.plan] : q.plan;
    return { id: prev, queue: { ...q, current: prev, past, plan } };
  }
  const here = q.entries.findIndex((e) => e.node.id === q.current);
  for (let i = here - 1; i >= 0; i--) {
    if (q.entries[i].mark === "ok") return { id: q.entries[i].node.id, queue: { ...q, current: q.entries[i].node.id } };
  }
  return null;
}

// toggleShuffle turns shuffle on, which deals a new round from the files not yet played, or off.
export function toggleShuffle(q: Queue, rng: Rng = Math.random): Queue {
  if (q.shuffle) return { ...q, shuffle: false, plan: [], past: [] };
  const others = okIDs(q).filter((id) => id !== q.current);
  return { ...q, shuffle: true, plan: shuffled(others, rng), past: [] };
}

// unavailableEntry is the row of a saved entry the person cannot reach. It carries the node's ID and nothing else: no
// name, size or type, since the server sent none.
export function unavailableEntry(nodeID: string): Entry {
  const node = { id: nodeID, folder_id: "", parent_id: null, name: "", kind: "file", size: 0, created_at: "", modified_at: "", no_download: false, editors_can_share: false } as unknown as Node;
  return { node, mark: "unavailable" };
}

// replaceEntries puts a list of entries in place of the queue's, keeping the file on screen and the settings. The list is
// cut to MAX_QUEUE like newQueue's. With shuffle on, the round is dealt again over the new list.
export function replaceEntries(q: Queue, entries: Entry[], rng: Rng = Math.random): Queue {
  const kept = entries.slice(0, MAX_QUEUE);
  const next: Queue = { ...q, entries: kept, pos: 0, shuffle: false, plan: [], past: [], run: 0, capped: entries.length > MAX_QUEUE };
  return q.shuffle ? toggleShuffle(next, rng) : next;
}

// cycleRepeat goes off, all, one, off.
export function cycleRepeat(q: Queue): Queue {
  const next: Repeat = q.repeat === "off" ? "all" : q.repeat === "all" ? "one" : "off";
  return { ...q, repeat: next };
}

// addNext puts a file after the current one. A file already in the queue is not added again.
export function addNext(q: Queue, node: Node, rng: Rng = Math.random): Queue {
  if (!isPlayable(node) || q.entries.some((e) => e.node.id === node.id)) return q;
  // A full queue refuses the file and says it has been cut, so the person knows it was not added.
  if (q.entries.length >= MAX_QUEUE) return { ...q, capped: true };
  const at = q.current === null ? q.pos : q.entries.findIndex((e) => e.node.id === q.current) + 1;
  const entries = [...q.entries];
  entries.splice(at, 0, { node, mark: markOf(node) });
  const plan = q.shuffle ? insertRandom([...q.plan], node.id, rng, 0) : q.plan;
  return { ...q, entries, plan, capped: q.capped };
}

// addEnd puts a file at the end of the queue. A file already in the queue is not added again.
export function addEnd(q: Queue, node: Node): Queue {
  if (!isPlayable(node) || q.entries.some((e) => e.node.id === node.id)) return q;
  if (q.entries.length >= MAX_QUEUE) return { ...q, capped: true };
  const entries = [...q.entries, { node, mark: markOf(node) }];
  const plan = q.shuffle ? [...q.plan, node.id] : q.plan;
  return { ...q, entries, plan, capped: q.capped };
}

// appendPage adds the playable files of a page of the folder, in order, until the queue is full. capped is set when the
// queue refused any of them.
export function appendPage(q: Queue, nodes: Node[], rng: Rng = Math.random): { queue: Queue; capped: boolean } {
  const have = new Set(q.entries.map((e) => e.node.id));
  const fresh = playableNodes(nodes).filter((n) => !have.has(n.id));
  const room = Math.max(0, MAX_QUEUE - q.entries.length);
  const taken = fresh.slice(0, room);
  // A copy: the round the queue holds is not changed by a page that is only being added to it.
  let plan = [...q.plan];
  if (q.shuffle) for (const n of taken) plan = insertRandom(plan, n.id, rng);
  const entries = [...q.entries, ...taken.map((node) => ({ node, mark: markOf(node) }))];
  // Cut only when a file of the page was left out.
  const capped = fresh.length > taken.length;
  return { queue: { ...q, entries, plan, capped: q.capped || capped }, capped };
}

// insertRandom puts an ID into a round at a random place after the first `from` files (so 0 means anywhere).
function insertRandom(plan: string[], id: string, rng: Rng, from = 0): string[] {
  const at = from + Math.floor(rng() * (plan.length - from + 1));
  plan.splice(at, 0, id);
  return plan;
}

// remove takes a file out of the queue. Removing the current file leaves no current file: the caller plays the next one.
export function remove(q: Queue, id: string): Queue {
  const at = q.entries.findIndex((e) => e.node.id === id);
  if (at < 0) return q;
  const entries = q.entries.filter((e) => e.node.id !== id);
  const current = q.current === id ? null : q.current;
  return {
    ...q,
    entries,
    current,
    pos: current === null ? at : q.pos,
    plan: q.plan.filter((x) => x !== id),
    past: q.past.filter((x) => x !== id),
  };
}

// move shifts a file by delta places in the queue's order: -1 is up, +1 is down. The move stops at the ends.
export function move(q: Queue, id: string, delta: number): Queue {
  const at = q.entries.findIndex((e) => e.node.id === id);
  const to = at + delta;
  if (at < 0 || to < 0 || to >= q.entries.length) return q;
  const entries = [...q.entries];
  [entries[at], entries[to]] = [entries[to], entries[at]];
  return { ...q, entries };
}
