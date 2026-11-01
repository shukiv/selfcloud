// SPDX-License-Identifier: AGPL-3.0-or-later

// focusAfterClose returns the element the focus goes to when the viewer closes. It is the first candidate row that is still
// in the listing, in the order given (the file playing at close, then the file that opened the viewer). A row that has gone
// (deleted, moved, or no longer listed) is skipped. When none is left, it is the listing itself, so the focus never falls
// back to the body. It returns null only when there is no listing to focus.
export function focusAfterClose(listing: HTMLElement | null, candidates: ReadonlyArray<string | null | undefined>): HTMLElement | null {
  if (!listing) return null;
  const rows = Array.from(listing.querySelectorAll<HTMLElement>("[data-id]"));
  for (const id of candidates) {
    if (!id) continue;
    const row = rows.find((x) => x.dataset.id === id);
    if (row) return row;
  }
  if (!listing.hasAttribute("tabindex")) listing.tabIndex = -1;
  return listing;
}
