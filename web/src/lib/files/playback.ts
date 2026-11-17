// SPDX-License-Identifier: AGPL-3.0-or-later

// playback holds the one media session of the signed-in person: the playlist a video or audio file opened, the file that
// plays now, and the element that plays it. The shell keeps it while the person browses. Closing the viewer on an audio
// file hides the viewer and leaves the audio playing in the mini player; closing it on a video, or a stop, or a sign-out or
// a change of person, ends the session: nothing plays on, and the queue is dropped. The Media page hosts the same session
// while it is open (decision 186): the player moves to the page's stage, and leaving the page is closing the viewer.
//
// The session is not React state, so a page change does not end it. The shell's host (MediaHost, then MediaDock) renders
// it and reads it through subscribePlayback.

import type { Node } from "./types";
import type { Pager } from "./pager";
import { get } from "../store";
import { session, sessionKey } from "../session";
import { registerBusy } from "../pwa/busy";
import { parkBox } from "./playerBox";

// PlaybackSeed is what the viewer plays: the file it starts with, the folder's files (read a page at a time) or the
// selection the person chose. folderName is the queue's folder as the person sees it (the place they opened it from); a
// children listing does not carry the parent's name on its files, so the player cannot read it from the node (mp7).
export interface PlaybackSeed {
  start: Node;
  folder: Pager;
  selection?: Node[];
  // shuffle starts the queue with shuffle on, dealt the way the queue's own Shuffle button deals it (queue.ts toggleShuffle).
  // Left out, the queue starts in order.
  shuffle?: boolean;
  folderName?: string;
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

// PageHosts are the Media page's places for the session (decision 186): the stage the player box moves into, and the
// queue panel below it. The page gives them while it is open; the session draws into them instead of the viewer.
export interface PageHosts {
  stage: HTMLElement;
  queue: HTMLElement;
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
  // page is the Media page's hosts while that page is open, or null. With them, the session draws on the page, and
  // neither the dialog nor the mini player shows it.
  page: PageHosts | null;
  current: NowPlaying | null;
  media: HTMLMediaElement | null;
  nav: PlaybackNav | null;
}

// A file that plays keeps the app's update waiting (decision 65).
registerBusy(() => {
  const m = snapshot.media;
  return !!m && !m.paused && !m.ended;
});

const IDLE: PlaybackSnapshot = { id: 0, key: "", seed: null, viewerOpen: false, page: null, current: null, media: null, nav: null };

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

// attachPage gives the Media page's hosts to the session and returns the page's detach. The player box moves into the
// stage at once (MediaDock reads the page on every render). The detach parks the box before the hosts leave the page,
// clears the page, and closes the viewer one microtask later: a mount that detaches and attaches again in the same tick
// (StrictMode's double effect) is not a leave, so the session goes on. A detach of hosts the session no longer holds (a
// newer page attached meanwhile) does nothing.
export function attachPage(hosts: PageHosts): () => void {
  set({ page: hosts });
  return () => {
    if (snapshot.page !== hosts) return;
    parkBox();
    set({ page: null });
    queueMicrotask(() => {
      if (snapshot.page === null) closeViewer();
    });
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
  // The page keeps its hosts: the Media page stays open with its empty state, ready for the next session.
  set({ ...IDLE, page: snapshot.page });
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
