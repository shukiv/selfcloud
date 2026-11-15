// SPDX-License-Identifier: AGPL-3.0-or-later

// The sleep timer stops the player after a set time, or at the end of the file on screen. It is a small store of its
// own, not the component's state, because the queue moves to the next file with a new element while a timed choice
// keeps counting: the choice outlives any one element, and pauses whatever plays when its time comes (getPlayback).
// A session that ends — a stop, or a change of person, which playback already watches — turns the timer Off, so it
// never reaches into another person's player. The component reads it through subscribeSleep.

import { getPlayback, subscribePlayback } from "./playback";

// SleepChoice is what the person picked: Off, a timed choice in minutes, or the end of the file on screen.
export type SleepChoice = "off" | "end" | 15 | 30 | 45 | 60;

export interface SleepState {
  choice: SleepChoice;
  // endsAt is the clock time (Date.now) a timed choice stops at, or null for Off and End of this file.
  endsAt: number | null;
}

let state: SleepState = { choice: "off", endsAt: null };
let timer: ReturnType<typeof setTimeout> | null = null;
const listeners = new Set<() => void>();

function emit(): void {
  for (const fn of listeners) fn();
}

function clearTimer(): void {
  if (timer !== null) {
    clearTimeout(timer);
    timer = null;
  }
}

export function getSleep(): SleepState {
  return state;
}

export function subscribeSleep(fn: () => void): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

// stopsAtEnd says the file on screen should stay ended instead of advancing the queue (End of this file).
export function stopsAtEnd(): boolean {
  return state.choice === "end";
}

export function setSleep(choice: SleepChoice): void {
  clearTimer();
  if (choice === "off" || choice === "end") {
    state = { choice, endsAt: null };
    emit();
    return;
  }
  const ms = choice * 60_000;
  state = { choice, endsAt: Date.now() + ms };
  timer = setTimeout(() => {
    timer = null;
    // Pause whatever plays now, which may be a later file than the one the choice was set on. Only paused: the
    // person can press Play again.
    getPlayback().media?.pause();
    state = { choice: "off", endsAt: null };
    emit();
  }, ms);
  emit();
}

// A session that ends turns the timer Off. Playback notifies on a stop and on a change of person alike.
subscribePlayback(() => {
  if (getPlayback().seed === null && state.choice !== "off") setSleep("off");
});
