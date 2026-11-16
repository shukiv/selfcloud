// SPDX-License-Identifier: AGPL-3.0-or-later

// The running session's queue, reached from outside the player: the Media page's Playlists tab adds a file after the one on
// screen (Play next). The playlist registers its control while it is mounted and takes it back when it unmounts, so a control
// is there only while a session runs. The control reads the queue as it is at the call (the playlist's own ref), never a value
// a render held, so an add lands after whatever plays now. The tab watches the control as a store, so its Play next appears and
// goes with the session, as the row's menu is drawn.

import type { Node } from "./types";

// QueueControl is what the page may ask of the running queue.
export interface QueueControl {
  // addNext puts a file after the one on screen, as the queue's own add panel does (queue.ts addNext).
  addNext: (node: Node) => void;
}

let control: QueueControl | null = null;
const subscribers = new Set<() => void>();

// getQueueControl is the running queue's control, or null when no session runs.
export function getQueueControl(): QueueControl | null {
  return control;
}

// setQueueControl registers the running queue's control, or clears it (null). The playlist gives its own on mount and, on
// unmount, clears it only while it is still the one registered, so a newer session's control is never dropped by an older leave.
export function setQueueControl(next: QueueControl | null): void {
  if (control === next) return;
  control = next;
  for (const notify of subscribers) notify();
}

// subscribeQueueControl watches the control: a caller re-draws when a session registers its control or takes it back.
export function subscribeQueueControl(notify: () => void): () => void {
  subscribers.add(notify);
  return () => {
    subscribers.delete(notify);
  };
}