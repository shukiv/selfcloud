// SPDX-License-Identifier: AGPL-3.0-or-later

// Whether this page is doing something a reload would cut off: a file playing, or an upload under way (decision 65). The
// modules that own those states register a check here when they load, so the worker's page code imports neither of them.

const sources = new Set<() => boolean>();

// registerBusy adds a check. It returns a function that removes it.
export function registerBusy(source: () => boolean): () => void {
  sources.add(source);
  return () => {
    sources.delete(source);
  };
}

// isBusy is true when any registered check says the page is busy.
export function isBusy(): boolean {
  for (const source of sources) {
    if (source()) return true;
  }
  return false;
}
