// SPDX-License-Identifier: AGPL-3.0-or-later

// Resume is where a video or audio file starts again for the person who stopped it. The server keeps one place per user
// and file (GET and PUT /api/v1/nodes/{nod}/resume). This module holds the rules the player applies: when a saved place is
// used, and when a new one is written. It reads no clock of its own: the caller passes the time.

// Resume is what the player knows of the place stopped at in a file. "pending": the read is not back, and nothing is saved
// (a save then would replace the real place with the start). "none": no place is stored, so saves go on. "unknown": the
// read failed; nothing is saved until the person moves the playhead, since a failed read says nothing about the stored
// place. "place": the stored place, in seconds, with the file's length.
export type Resume =
  | { kind: "pending" }
  | { kind: "none" }
  | { kind: "unknown" }
  | { kind: "place"; position: number; duration: number };

// LATE_START_S: a place that arrives after the file has started still applies while the file is in its first seconds
// and the person has not moved the playhead. Past this, the file has played on from the start, so the place is not applied.
export const LATE_START_S = 10;

// SAVE_EVERY_MS is the least time between two writes made while the file plays.
export const SAVE_EVERY_MS = 15_000;
// RESUME_FROM_S: a place nearer the start than this is not worth resuming, so the file starts at the beginning.
export const RESUME_FROM_S = 5;
// RESUME_UNTIL_END_S: a place this close to the end means the person had finished the file, so it starts over.
export const RESUME_UNTIL_END_S = 10;

// startAt is the time a file starts at, given the place saved for it and the file's duration (0 when unknown). It is 0
// unless the saved place is far enough from both ends of the file.
export function startAt(saved: { position: number; duration: number } | null, duration: number): number {
  if (saved === null) return 0;
  const pos = saved.position;
  if (!Number.isFinite(pos) || pos < RESUME_FROM_S) return 0;
  if (duration > 0 && pos >= duration - RESUME_UNTIL_END_S) return 0;
  return pos;
}

// SaveClock decides when a place is written. A tick while the file plays writes at most once per SAVE_EVERY_MS. A pause
// writes once, when the place has moved since the last write, whatever the time since then. Nothing else writes.
export class SaveClock {
  private last = Number.NEGATIVE_INFINITY;
  private written: number | null = null;

  // tick says whether a tick at time now (milliseconds) at position pos should write.
  tick(now: number, pos: number): boolean {
    if (now - this.last < SAVE_EVERY_MS) return false;
    return this.write(now, pos);
  }

  // pause says whether a pause at position pos should write: only when the place has moved since the last write.
  pause(now: number, pos: number): boolean {
    if (this.written !== null && Math.abs(this.written - pos) < 0.5) return false;
    return this.write(now, pos);
  }

  // ended says the file has ended: its place is written as the beginning, and the next write waits for the clock as usual.
  ended(now: number): boolean {
    this.last = now;
    this.written = 0;
    return true;
  }

  private write(now: number, pos: number): boolean {
    this.last = now;
    this.written = pos;
    return true;
  }
}
