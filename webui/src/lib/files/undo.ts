// SPDX-License-Identifier: AGPL-3.0-or-later

export const UNDO_WINDOW_MS = 8000;

export interface UndoEntry {
  id: number;
  label: string;
  // run undoes the action. It reports how many of its items could not be undone (0 when all were).
  run: () => Promise<number>;
  expiresAt: number;
}

// UndoStack holds the actions that can still be undone, newest last. An entry lasts UNDO_WINDOW_MS
// from when it was pushed; an expired entry is dropped, never undone. The clock is injectable for tests.
export class UndoStack {
  private entries: UndoEntry[] = [];
  private nextID = 1;

  constructor(private now: () => number = () => Date.now()) {}

  push(label: string, run: () => Promise<number>): UndoEntry {
    const entry: UndoEntry = { id: this.nextID++, label, run, expiresAt: this.now() + UNDO_WINDOW_MS };
    this.entries.push(entry);
    return entry;
  }

  // current is the newest entry still in its window, or null.
  current(): UndoEntry | null {
    this.expire();
    return this.entries[this.entries.length - 1] ?? null;
  }

  // take removes the newest live entry and returns it, so it can be run once.
  take(): UndoEntry | null {
    const entry = this.current();
    if (entry) this.entries = this.entries.filter((e) => e.id !== entry.id);
    return entry;
  }

  // expire drops the entries whose window has passed.
  expire(): void {
    const t = this.now();
    this.entries = this.entries.filter((e) => e.expiresAt > t);
  }

  clear(): void {
    this.entries = [];
  }

  get size(): number {
    this.expire();
    return this.entries.length;
  }
}
