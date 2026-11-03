// SPDX-License-Identifier: AGPL-3.0-or-later

// The list row under the mouse, kept outside React's state (review fix 7). A row's four icons mount only while the row is hovered,
// selected or focused; a state in the listing would redraw all its rows on every move of the pointer, so each row subscribes to
// this store and only the rows whose hover changed draw again.

let hovered: string | null = null;
const subscribers = new Set<() => void>();

export function getRowHover(): string | null {
  return hovered;
}

export function setRowHover(id: string | null): void {
  if (hovered === id) return;
  hovered = id;
  for (const notify of subscribers) notify();
}

export function subscribeRowHover(notify: () => void): () => void {
  subscribers.add(notify);
  return () => {
    subscribers.delete(notify);
  };
}
