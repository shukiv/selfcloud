// SPDX-License-Identifier: AGPL-3.0-or-later

// playback holds the one media session of the signed-in person: the playlist a video or audio file opened, the file that
// plays now, and the element that plays it. The shell keeps it while the person browses. Closing the viewer on an audio
// file hides the viewer and leaves the audio playing in the mini player; closing it on a video, or a stop, or a sign-out or
// a change of person, ends the session: nothing plays on, and the queue is dropped.
//
// The session is not React state, so a page change does not end it. The shell's host (MediaHost, then MediaDock) renders
// it and reads it through subscribePlayback.

import type { Node } from "./types";
import type { Pager } from "./pager";
import { get } from "../store";
import { session, sessionKey } from "../session";
import { registerBusy } from "../pwa/busy";

// PlaybackSeed is what the viewer plays: the file it starts with, the folder's files (read a page at a time) or the
// selection the person chose.
export interface PlaybackSeed {
  start: Node;
  folder: Pager;
  selection?: Node[];
}

// NowPlaying is the file on screen of the playlist.
export interface NowPlaying {
  node: Node;
  kind: "audio" | "video";
}

// PlaybackNav steps the playlist. The functions are stable for the session: the playlist re-registers its current ones.
export interface PlaybackNav {
  next: () => void;
  previous: () => void;
}

export interface PlaybackSnapshot {
  // id counts the sessions started. The host keys its player by it, so a new session is a new player.
  id: number;
  // key is the person and token the session was started under (sessionKey). A write made for the session after the
  // person changed would be another person's, so the session checks it before it writes.
  key: string;
  // seed is what the viewer was started with, or null when no session is running.
  seed: PlaybackSeed | null;
  // viewerOpen says the viewer is on screen; with it closed, only the mini player shows the session.
  viewerOpen: boolean;
  current: NowPlaying | null;
  media: HTMLMediaElement | null;
  nav: PlaybackNav | null;
}

// A file that plays keeps the app's update waiting (decision 65).
registerBusy(() => {
  const m = snapshot.media;
  return !!m && !m.paused && !m.ended;
});

const IDLE: PlaybackSnapshot = { id: 0, key: "", seed: null, viewerOpen: false, current: null, media: null, nav: null };

let snapshot: PlaybackSnapshot = IDLE;
// onClosedFn is the page's callback. It is told the file on screen once, when the viewer closes, so the page can move the
// focus back and clear what it showed.
let onClosedFn: ((current: Node | null) => void) | null = null;
const listeners = new Set<() => void>();

function set(next: Partial<PlaybackSnapshot>): void {
  snapshot = { ...snapshot, ...next };
  for (const fn of listeners) fn();
}

export function getPlayback(): PlaybackSnapshot {
  return snapshot;
}

export function subscribePlayback(fn: () => void): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

// startPlayback opens a session with the seed and shows the viewer. A session that is running is replaced: its player
// unmounts, and its audio stops.
export function startPlayback(seed: PlaybackSeed, onClosed?: (current: Node | null) => void): void {
  onClosedFn = onClosed ?? null;
  set({ id: snapshot.id + 1, key: currentSessionKey(), seed, viewerOpen: true, current: null, media: null, nav: null });
}

export function setCurrent(current: NowPlaying | null): void {
  if (snapshot.seed === null) return;
  set({ current });
}

export function setMedia(media: HTMLMediaElement | null): void {
  if (snapshot.seed === null) return;
  set({ media });
}

export function setNav(nav: PlaybackNav | null): void {
  if (snapshot.seed === null) return;
  set({ nav });
}

export function showViewer(): void {
  if (snapshot.seed !== null) set({ viewerOpen: true });
}

// tellClosed passes the file on screen to the page's callback, once.
function tellClosed(): void {
  const fn = onClosedFn;
  onClosedFn = null;
  fn?.(snapshot.current?.node ?? null);
}

// closeViewer is the person closing the viewer. Audio that has a player goes on in the mini player; anything else ends the
// session. The page is told the file on screen either way, so it can put the focus back.
export function closeViewer(): void {
  if (snapshot.seed === null) return;
  const audio = snapshot.current?.kind === "audio";
  if (audio && snapshot.media !== null) {
    set({ viewerOpen: false });
    tellClosed();
    return;
  }
  endPlayback();
}

// stopPlayback ends the session at once: the audio stops, and the queue is dropped.
export function stopPlayback(): void {
  if (snapshot.seed === null) return;
  endPlayback();
}

function endPlayback(): void {
  tellClosed();
  set(IDLE);
}

// A change of person (a sign-out, a sign-in as someone else, or a new session token) ends the session. The first value
// the store gives is only the starting point: nothing is ended by it.
let watchedKey: string | null = null;
session.subscribe((s) => {
  const key = sessionKey(s);
  if (watchedKey !== null && key !== watchedKey) stopPlayback();
  watchedKey = key;
});

// currentSessionKey is the key of the session now, for tests that need it without a subscription.
export const currentSessionKey = (): string => sessionKey(get(session));
