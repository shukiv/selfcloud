// SPDX-License-Identifier: AGPL-3.0-or-later

// The tags of audio files (decision 56): the title, artist, album and duration the server reads from a file. The browser keeps
// one answer per node in a small cache that the player, the mini player and the playlist share. A value is text only: it is
// shown as a React text node, never as markup, and it is never used as an address.
//
// A failed read keeps what was shown. A pending answer is asked again after a short wait, a bounded number of times; after that
// the node stays as it was, and the player plays on regardless.

import { api, ApiError } from '../api';
import { session, sessionKey } from '../session';
import { get } from '../store';

// TagValues are the text values and the duration of one file, as the server sends them.
export interface TagValues {
  title?: string;
  artist?: string;
  album?: string;
  duration_ms?: number;
}

// TagState is what the cache knows of a node: a read in progress, a read that found nothing (or was refused), or the values.
export type TagState = ({ state: 'ready' } & TagValues) | { state: 'none' } | { state: 'pending' };

// MAX_TEXT is the most characters a value may have (the server cuts at 256); a longer one is refused, not shortened.
export const MAX_TEXT = 256;
// MAX_DURATION_MS is a week: the server's own limit.
export const MAX_DURATION_MS = 7 * 24 * 60 * 60 * 1000;
// WAIT_MS is the wait before a pending read is asked again, and MAX_WAITS how many times it is asked before it is left alone.
export const WAIT_MS = 2000;
export const MAX_WAITS = 6;
// MAX_BACKOFF_MS caps the back-off between tries of a request that failed: it doubles from WAIT_MS, and MAX_WAITS bounds the tries.
export const MAX_BACKOFF_MS = 30_000;
// BATCH is the most node IDs one batch request names (the server's limit).
export const BATCH = 100;

// text reads a value that is a string of at most MAX_TEXT characters with no control or format character; any other value
// is left out. Control characters are refused, not stripped, so what is shown is what the server read.
function text(v: unknown): string | undefined {
  if (typeof v !== 'string' || v.length === 0) return undefined;
  if ([...v].length > MAX_TEXT) return undefined;
  // Control characters and bidirectional or other format controls (except the zero-width joiners) are not plain text.
  if (/[\u0000-\u001f\u007f-\u009f‎‏‪-‮⁦-⁩﻿]/u.test(v)) return undefined;
  return v;
}

// duration reads a duration in milliseconds: a whole number from 0 to a week.
function duration(v: unknown): number | undefined {
  if (typeof v !== 'number' || !Number.isInteger(v) || v < 0 || v > MAX_DURATION_MS) return undefined;
  return v;
}

// parseTagState reads one answer of the tags routes. Anything that is not one of the three states is not an answer: null.
export function parseTagState(raw: unknown): TagState | null {
  if (typeof raw !== 'object' || raw === null) return null;
  const r = raw as Record<string, unknown>;
  if (r.state === 'pending') return { state: 'pending' };
  if (r.state === 'none') return { state: 'none' };
  if (r.state !== 'ready') return null;
  const out: { state: 'ready' } & TagValues = { state: 'ready' };
  const title = text(r.title);
  const artist = text(r.artist);
  const album = text(r.album);
  const dur = duration(r.duration_ms);
  if (title !== undefined) out.title = title;
  if (artist !== undefined) out.artist = artist;
  if (album !== undefined) out.album = album;
  if (dur !== undefined) out.duration_ms = dur;
  return out;
}

// The cache: one entry per node ID, and the listeners that show it.
const cache = new Map<string, TagState>();
const listeners = new Set<() => void>();
const waits = new Map<string, number>();
// failures counts, per node, the requests that failed and were tried again.
const failures = new Map<string, number>();
// inflight holds the nodes with a read on its way, covers the nodes whose cover was seen, and epoch counts the resets.
const inflight = new Set<string>();
const covers = new Set<string>();
let epoch = 0;

// version counts the changes, so a component that reads many nodes can follow them with one snapshot.
let version = 0;

function publish(): void {
  version++;
  for (const l of listeners) l();
}

// subscribeTags is the store the components read through useSyncExternalStore.
export function subscribeTags(fn: () => void): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

// tagsVersion is the number of changes so far: the snapshot a list of many nodes reads.
export function tagsVersion(): number {
  return version;
}

// getTags returns what is known of a node, or undefined when nothing is.
export function getTags(nodeId: string): TagState | undefined {
  return cache.get(nodeId);
}

// seedTags stores what a listing already sent: a node with tags gets them without a request.
export function seedTags(nodeId: string, values: TagValues | undefined): void {
  if (values === undefined || cache.get(nodeId)?.state === 'ready') return;
  const parsed = parseTagState({ state: 'ready', ...values });
  if (parsed === null) return;
  cache.set(nodeId, parsed);
  publish();
}

// resetTags empties the cache, and an answer still on its way is dropped when it comes. One person's reads are not kept for
// another.
export function resetTags(): void {
  epoch++;
  cache.clear();
  waits.clear();
  failures.clear();
  inflight.clear();
  covers.clear();
  publish();
}

// A change of person (a sign-out, a sign-in as someone else, or a new session token) forgets what was read. The first value the
// store gives is only the starting point.
let watchedKey: string | null = null;
session.subscribe((s) => {
  const key = sessionKey(s);
  if (watchedKey !== null && key !== watchedKey) resetTags();
  watchedKey = key;
});

// markCover records that a file's cover was seen, so a playlist row and the lock screen can show it too. The listing's
// has_thumbnail says false until the first cover has been made, which the player itself asks for.
export function markCover(nodeId: string): void {
  if (covers.has(nodeId)) return;
  covers.add(nodeId);
  publish();
}

// hasCover says a file's cover was seen in this session.
export function hasCover(nodeId: string): boolean {
  return covers.has(nodeId);
}

// needSession refuses a read that would go out with no session (the person signed out while it was waiting).
function needSession(): void {
  if (get(session) === null) throw new ApiError(0, 'signed_out', {}, 0);
}

// getNodeTags reads the tags of one node (GET /api/v1/nodes/{nod}/tags). A pending read answers with its state.
export function getNodeTags(nod: string): Promise<unknown> {
  needSession();
  return api<unknown>('GET', `/api/v1/nodes/${nod}/tags`);
}

// getMediaTags reads the tags of up to BATCH nodes (GET /api/v1/media/tags). The answer maps each node that the caller can read
// and that is an audio file to its state; the others are absent.
export function getMediaTags(ids: string[]): Promise<{ items?: Record<string, unknown> }> {
  needSession();
  return api<{ items?: Record<string, unknown> }>('GET', `/api/v1/media/tags?ids=${ids.map(encodeURIComponent).join(',')}`);
}

// loadTags asks for the tags of the given nodes and stores the answers. A node with an answer, or with a read already on its
// way, is not asked for. The pending nodes of a batch are asked again together after WAIT_MS, up to MAX_WAITS times each. A failed
// request stores nothing, so what is shown stays. A node that is absent from the answer is "none".
export async function loadTags(ids: string[]): Promise<void> {
  const at = epoch;
  const wanted = [...new Set(ids)].filter((id) => {
    const st = cache.get(id)?.state;
    return st !== 'ready' && st !== 'none' && !inflight.has(id);
  });
  for (const id of wanted) inflight.add(id);
  for (let i = 0; i < wanted.length; i += BATCH) {
    const chunk = wanted.slice(i, i + BATCH);
    let res: { items?: Record<string, unknown> } | null = null;
    try {
      res = await getMediaTags(chunk);
    } catch {
      // A failed read keeps what is shown; the player does not wait for it.
    }
    if (at !== epoch) return;
    for (const id of chunk) inflight.delete(id);
    if (res === null) {
      scheduleRetry(chunk, at);
      continue;
    }
    const items = res.items ?? {};
    const again: string[] = [];
    for (const id of chunk) {
      const st = parseTagState(items[id]) ?? { state: 'none' as const };
      failures.delete(id);
      cache.set(id, st);
      if (st.state === 'pending') again.push(id);
    }
    publish();
    scheduleAgain(again);
  }
}

// scheduleAgain asks for the pending nodes that have waits left, in one request after the wait. A node out of waits keeps its
// pending state, and its file name stays on screen.
function scheduleAgain(ids: string[]): void {
  const left = ids.filter((id) => (waits.get(id) ?? 0) < MAX_WAITS);
  if (left.length === 0) return;
  for (const id of left) waits.set(id, (waits.get(id) ?? 0) + 1);
  setTimeout(() => {
    const still = left.filter((id) => cache.get(id)?.state === 'pending');
    if (still.length > 0) void loadTags(still);
  }, WAIT_MS);
}

// scheduleRetry asks again for the nodes of a request that failed (a network error, or a whole request refused), after a
// back-off that doubles from WAIT_MS and is capped at MAX_BACKOFF_MS. Each node is tried at most MAX_WAITS times in all; after
// that it keeps what it showed. A new epoch (a sign-out) drops the retry.
function scheduleRetry(ids: string[], at: number): void {
  const left = ids.filter((id) => (failures.get(id) ?? 0) < MAX_WAITS);
  if (left.length === 0) return;
  let attempt = 0;
  for (const id of left) {
    const n = failures.get(id) ?? 0;
    attempt = Math.max(attempt, n);
    failures.set(id, n + 1);
  }
  const delay = Math.min(WAIT_MS * 2 ** attempt, MAX_BACKOFF_MS);
  setTimeout(() => {
    if (at !== epoch) return;
    void loadTags(left);
  }, delay);
}

// displayTitle is the title shown for a file: its tag, or its name when there is no tag.
export function displayTitle(name: string, tags: TagState | undefined): string {
  return tags?.state === 'ready' && tags.title !== undefined ? tags.title : name;
}

// displayArtist is the artist shown for a file, or nothing.
export function displayArtist(tags: TagState | undefined): string | undefined {
  return tags?.state === 'ready' ? tags.artist : undefined;
}
