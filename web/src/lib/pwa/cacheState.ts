// SPDX-License-Identifier: AGPL-3.0-or-later

// The app's caches on this computer, and the signed-out marker that keeps the worker from storing app files while nobody is
// signed in (decision 65). Kept apart from the worker's page code so the session module can use it without a cycle.

// CACHE_PREFIX names every cache the worker and the page make. Sign-out removes all of them but the state.
export const CACHE_PREFIX = "selfcloud-";
// STATE_CACHE holds the signed-out marker and the record of the last activated build (public/sw.js).
export const STATE_CACHE = "selfcloud-state";
const SIGNED_OUT = "/signed-out";
const OFFLINE = "/offline.html";

let lastSeq = 0;

// nextSeq numbers the page's messages to the worker. The number is a time, so a later page's messages are newer than an
// earlier page's, and the worker orders them by it (public/sw.js, accept).
export function nextSeq(): number {
  lastSeq = Math.max(Date.now(), lastSeq + 1);
  return lastSeq;
}

// markSignedOut removes the app's caches on this computer and leaves the marker, so a later visit to the sign-in page stores
// nothing. The worker is asked too, in case it holds a cache the page cannot reach.
export async function markSignedOut(): Promise<void> {
  if (!("caches" in window)) return;
  try {
    for (const key of await caches.keys()) {
      if (key.startsWith(CACHE_PREFIX) && key !== STATE_CACHE) await caches.delete(key);
    }
    const state = await caches.open(STATE_CACHE);
    await state.put(SIGNED_OUT, new Response("signed out"));
    navigator.serviceWorker?.controller?.postMessage({ type: "CLEAR_CACHES", seq: nextSeq() });
  } catch {
    // The browser kept no caches, or refused to clear them; there is nothing more the page can do.
  }
}

// offlinePageCached is true when one of the app's caches holds the offline page.
async function offlinePageCached(): Promise<boolean> {
  for (const key of await caches.keys()) {
    if (key.startsWith(CACHE_PREFIX) && key !== STATE_CACHE && (await (await caches.open(key)).match(OFFLINE))) return true;
  }
  return false;
}

// markSignedIn lifts the marker: the worker may store app files again. Called when a person signs in, and when a page loads
// for a signed-in person. The worker fetches the offline page again only when the marker was there, or when the page is not
// in the cache: a visit that finds both in place costs no request.
export async function markSignedIn(): Promise<void> {
  if (!("caches" in window)) return;
  try {
    const state = await caches.open(STATE_CACHE);
    const wasSignedOut = (await state.match(SIGNED_OUT)) !== undefined;
    if (wasSignedOut) await state.delete(SIGNED_OUT);
    if (wasSignedOut || !(await offlinePageCached())) {
      navigator.serviceWorker?.controller?.postMessage({ type: "PRECACHE", seq: nextSeq() });
    }
  } catch {
    // Storage is blocked; the app still works, without the cache.
  }
}
