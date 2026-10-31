// SPDX-License-Identifier: AGPL-3.0-or-later

// Thumbnails for the grid. A tile asks the server for one size of an image, and shows the image when it answers 200,
// keeps the file-type icon while the server answers 202 (the thumbnail is being made), and stops asking after a
// bounded number of waits. The loader is written without the DOM, so its waits and its stop can be tested directly.

import { Limiter } from './limiter';

// The size a tile asks for: the server's default thumbnail size (derived.thumb_sizes).
export const THUMB_SIZE = 256;

// A 202 or a 429 carries Retry-After in seconds. The wait is that many seconds, kept between MIN_WAIT and MAX_WAIT.
// Without a usable header the wait is DEFAULT_WAIT. A tile asks at most MAX_WAITS more times, then shows its icon,
// so a thumbnail that never arrives stops the work for that tile.
export const DEFAULT_WAIT = 2000;
export const MIN_WAIT = 1000;
export const MAX_WAIT = 30000;
export const MAX_WAITS = 20;

// THUMB_CONCURRENCY caps the thumbnail requests in flight, across every tile on the page.
export const THUMB_CONCURRENCY = 4;

// thumbnailPath is the same-origin address of a thumbnail. The server answers with the image or a wait.
export function thumbnailPath(nod: string, size: number = THUMB_SIZE): string {
  return `/api/v1/nodes/${nod}/thumbnail?size=${size}`;
}

// waitMs turns a Retry-After header into the wait before the next ask, in milliseconds.
export function waitMs(retryAfter: string | null): number {
  const seconds = Number(retryAfter);
  if (!Number.isFinite(seconds) || seconds <= 0) return DEFAULT_WAIT;
  return Math.min(MAX_WAIT, Math.max(MIN_WAIT, Math.round(seconds * 1000)));
}

// Step is what one answer means for the tile.
export type Step =
  | { kind: 'image' } // 200: the body is the image
  | { kind: 'redirect' } // an opaque redirect: the content origin serves the image (content_url is set)
  | { kind: 'wait'; ms: number } // 202 or 429: ask again after ms
  | { kind: 'none' }; // anything else, including 404: the icon stays, and nothing is asked again

// stepFor reads an answer. type is the Response type: an opaque redirect has no status to read.
export function stepFor(res: { status: number; type?: string; retryAfter?: string | null }): Step {
  if (res.type === 'opaqueredirect') return { kind: 'redirect' };
  if (res.status === 200) return { kind: 'image' };
  if (res.status === 202 || res.status === 429) return { kind: 'wait', ms: waitMs(res.retryAfter ?? null) };
  return { kind: 'none' };
}

// ThumbSink receives what the tile shows. image gets an address for an <img>: a blob address the loader revokes when it
// stops, or the thumbnail's own address when the browser must follow a redirect. none means the icon stays.
export interface ThumbSink {
  image(src: string): void;
  // redirect gives the browser the thumbnail's own address. done is called once the browser has loaded it or failed.
  redirect(src: string, done: () => void): void;
  none(): void;
}

// ThumbDeps are the parts a test replaces: the request, the timers, the object addresses and the limit on requests.
export interface ThumbDeps {
  fetch: typeof fetch;
  schedule: (fn: () => void, ms: number) => unknown;
  cancel: (handle: unknown) => void;
  createURL: (blob: Blob) => string;
  revokeURL: (url: string) => void;
  limiter: Limiter;
}

// liveDeps is the browser's own parts. The limiter is shared, so the page's tiles together stay within THUMB_CONCURRENCY.
let shared: Limiter | null = null;
export function liveDeps(): ThumbDeps {
  shared ??= new Limiter(THUMB_CONCURRENCY);
  return {
    fetch: (input, init) => fetch(input, init),
    schedule: (fn, ms) => setTimeout(fn, ms),
    cancel: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
    createURL: (blob) => URL.createObjectURL(blob),
    revokeURL: (url) => URL.revokeObjectURL(url),
    limiter: shared,
  };
}

// watchThumbnail asks for the thumbnail at path until the answer is final, and reports it to sink. It returns a function
// that stops it: a stopped watch never asks again, cancels its wait and its request, and revokes its image address.
// Exactly one of image and none is reported, unless the watch is stopped first.
export function watchThumbnail(path: string, sink: ThumbSink, deps: ThumbDeps = liveDeps()): () => void {
  let stopped = false;
  let waits = 0;
  let handle: unknown = null;
  let blobURL: string | null = null;
  // release ends the slot a redirected image holds, if one is held.
  let release: (() => void) | null = null;
  const abort = typeof AbortController === 'undefined' ? null : new AbortController();

  const finish = (src: string | null) => {
    if (stopped) return;
    if (src === null) sink.none();
    else sink.image(src);
  };

  const again = (ms: number) => {
    if (++waits > MAX_WAITS) {
      finish(null);
      return;
    }
    handle = deps.schedule(ask, ms);
  };

  const ask = () => {
    handle = null;
    if (stopped) return;
    deps
      .limiter.run(() => deps.fetch(path, { credentials: 'same-origin', redirect: 'manual', signal: abort?.signal }))
      .then(
        async (res) => {
          if (stopped) return;
          const step = stepFor({ status: res.status, type: res.type, retryAfter: res.headers.get('Retry-After') });
          if (step.kind === 'wait') {
            again(step.ms);
            return;
          }
          if (step.kind === 'redirect') {
            // The browser makes the image request, so it is counted here: this slot is held until the browser has loaded
            // the image or failed, and so the cap of THUMB_CONCURRENCY covers these requests too.
            void deps.limiter.run(
              () =>
                new Promise<void>((resolve) => {
                  release = resolve;
                  if (stopped) resolve();
                  else sink.redirect(path, resolve);
                }),
            );
            return;
          }
          if (step.kind === 'none') {
            finish(null);
            return;
          }
          // A body that is aborted or fails midway leaves the icon, quietly: nothing is logged, and nothing is asked again.
          const blob = await res.blob().catch(() => null);
          if (stopped) return;
          if (blob === null) {
            finish(null);
            return;
          }
          blobURL = deps.createURL(blob);
          finish(blobURL);
        },
        () => {
          // A network failure or a stopped request: the icon stays, and nothing is asked again.
          finish(null);
        },
      );
  };

  handle = deps.schedule(ask, 0);
  return () => {
    if (stopped) return;
    stopped = true;
    abort?.abort();
    if (handle !== null) deps.cancel(handle);
    handle = null;
    if (blobURL !== null) deps.revokeURL(blobURL);
    blobURL = null;
    release?.();
    release = null;
  };
}
