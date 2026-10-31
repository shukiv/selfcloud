// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Limiter } from './limiter';
import {
  DEFAULT_WAIT,
  MAX_WAIT,
  MAX_WAITS,
  MIN_WAIT,
  stepFor,
  thumbnailPath,
  watchThumbnail,
  waitMs,
  type ThumbDeps,
} from './thumbnail';

// answer makes the Response the server would send; an opaque redirect has no status, as a browser gives it.
function answer(status: number, headers: Record<string, string> = {}): Response {
  return new Response(status === 200 ? new Uint8Array([0xff, 0xd8, 0xff]) : null, { status, headers });
}
function opaque(): Response {
  return { status: 0, type: 'opaqueredirect', headers: new Headers() } as unknown as Response;
}

function deps(fetchImpl: ThumbDeps['fetch']) {
  const createURL = vi.fn(() => 'blob:thumb-1');
  const revokeURL = vi.fn();
  const d: ThumbDeps = {
    fetch: fetchImpl,
    schedule: (fn, ms) => setTimeout(fn, ms),
    cancel: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
    createURL,
    revokeURL,
    limiter: new Limiter(4),
  };
  return { d, createURL, revokeURL };
}

// settle lets the promise chains of a request run to their next wait. The limiter takes its slot a few steps after a
// request starts, so a count of slots is read only after these.
async function settle() {
  await vi.advanceTimersByTimeAsync(0);
  for (let i = 0; i < 50; i++) await Promise.resolve();
}

function sink() {
  return {
    image: vi.fn<(src: string) => void>(),
    none: vi.fn<() => void>(),
    redirect: vi.fn<(src: string, done: () => void) => void>(),
  };
}

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
});

describe('waitMs', () => {
  it('reads Retry-After as seconds and keeps the wait between the minimum and the maximum', () => {
    expect(waitMs('5')).toBe(5000);
    expect(waitMs('0')).toBe(DEFAULT_WAIT);
    expect(waitMs(null)).toBe(DEFAULT_WAIT);
    expect(waitMs('Wed, 21 Oct 2026 07:28:00 GMT')).toBe(DEFAULT_WAIT);
    expect(waitMs('0.1')).toBe(MIN_WAIT);
    expect(waitMs('3600')).toBe(MAX_WAIT);
  });
});

describe('stepFor', () => {
  it('reads each answer as one step', () => {
    expect(stepFor({ status: 200 })).toEqual({ kind: 'image' });
    expect(stepFor({ status: 0, type: 'opaqueredirect' })).toEqual({ kind: 'redirect' });
    expect(stepFor({ status: 202, retryAfter: '2' })).toEqual({ kind: 'wait', ms: 2000 });
    expect(stepFor({ status: 429, retryAfter: '5' })).toEqual({ kind: 'wait', ms: 5000 });
    expect(stepFor({ status: 404 })).toEqual({ kind: 'none' });
    expect(stepFor({ status: 500 })).toEqual({ kind: 'none' });
    expect(stepFor({ status: 413 })).toEqual({ kind: 'none' });
  });

  it('builds the thumbnail address at the default size', () => {
    expect(thumbnailPath('nod_1')).toBe('/api/v1/nodes/nod_1/thumbnail?size=256');
    expect(thumbnailPath('nod_1', 1024)).toBe('/api/v1/nodes/nod_1/thumbnail?size=1024');
  });
});

describe('watchThumbnail', () => {
  it('keeps the icon while the server answers 202, then shows the image', async () => {
    const fetchImpl = vi.fn<ThumbDeps['fetch']>().mockResolvedValueOnce(answer(202, { 'Retry-After': '2' })).mockResolvedValueOnce(answer(200));
    const { d, createURL } = deps(fetchImpl);
    const s = sink();
    watchThumbnail('/api/v1/nodes/nod_1/thumbnail?size=256', s, d);
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(s.image).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1999);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(fetchImpl).toHaveBeenCalledTimes(2);
    expect(createURL).toHaveBeenCalledTimes(1);
    expect(s.image).toHaveBeenCalledWith('blob:thumb-1');
    expect(s.none).not.toHaveBeenCalled();
  });

  it('asks with the same credential rules as the page and never follows a redirect itself', async () => {
    const fetchImpl = vi.fn<ThumbDeps['fetch']>().mockResolvedValue(answer(404));
    const { d } = deps(fetchImpl);
    watchThumbnail('/api/v1/nodes/nod_1/thumbnail?size=256', sink(), d);
    await vi.advanceTimersByTimeAsync(0);
    const init = fetchImpl.mock.calls[0][1] as RequestInit;
    expect(init.credentials).toBe('same-origin');
    expect(init.redirect).toBe('manual');
  });

  it('stops asking after the cap, and says so once', async () => {
    const fetchImpl = vi.fn<ThumbDeps['fetch']>(async () => answer(202, { 'Retry-After': '1' }));
    const { d } = deps(fetchImpl);
    const s = sink();
    watchThumbnail('/api/v1/nodes/nod_1/thumbnail?size=256', s, d);
    await vi.advanceTimersByTimeAsync(10 * 60_000);
    // One ask, then one for each of the MAX_WAITS waits.
    expect(fetchImpl).toHaveBeenCalledTimes(MAX_WAITS + 1);
    expect(s.none).toHaveBeenCalledTimes(1);
    expect(s.image).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(10 * 60_000);
    expect(fetchImpl).toHaveBeenCalledTimes(MAX_WAITS + 1);
  });

  it('caps each wait, so a large Retry-After does not hold a tile for an hour', async () => {
    const fetchImpl = vi.fn<ThumbDeps['fetch']>(async () => answer(429, { 'Retry-After': '3600' }));
    const { d } = deps(fetchImpl);
    watchThumbnail('/api/v1/nodes/nod_1/thumbnail?size=256', sink(), d);
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(MAX_WAIT - 1);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(fetchImpl).toHaveBeenCalledTimes(2);
  });

  it('shows the icon at once for a 404, and asks no more', async () => {
    const fetchImpl = vi.fn<ThumbDeps['fetch']>(async () => answer(404));
    const { d } = deps(fetchImpl);
    const s = sink();
    watchThumbnail('/api/v1/nodes/nod_1/thumbnail?size=256', s, d);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(s.none).toHaveBeenCalledTimes(1);
  });

  it('shows the icon when the request fails on the network', async () => {
    const fetchImpl = vi.fn<ThumbDeps['fetch']>(async () => {
      throw new TypeError('network');
    });
    const { d } = deps(fetchImpl);
    const s = sink();
    watchThumbnail('/api/v1/nodes/nod_1/thumbnail?size=256', s, d);
    await vi.advanceTimersByTimeAsync(0);
    expect(s.none).toHaveBeenCalledTimes(1);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it('hands the browser the thumbnail address when the content origin serves it', async () => {
    const fetchImpl = vi.fn<ThumbDeps['fetch']>(async () => opaque());
    const { d, createURL } = deps(fetchImpl);
    const s = sink();
    watchThumbnail('/api/v1/nodes/nod_1/thumbnail?size=256', s, d);
    await vi.advanceTimersByTimeAsync(0);
    expect(s.redirect).toHaveBeenCalledWith('/api/v1/nodes/nod_1/thumbnail?size=256', expect.any(Function));
    expect(s.image).not.toHaveBeenCalled();
    expect(createURL).not.toHaveBeenCalled();
  });

  it('counts the browser load of a redirected image against the cap, until the browser says it is done', async () => {
    const fetchImpl = vi.fn<ThumbDeps['fetch']>(async () => opaque());
    const { d } = deps(fetchImpl);
    const s = sink();
    watchThumbnail('/api/v1/nodes/nod_1/thumbnail?size=256', s, d);
    await settle();
    expect(d.limiter.running).toBe(1);
    const done = s.redirect.mock.calls[0][1];
    done();
    await settle();
    expect(d.limiter.running).toBe(0);
  });

  it('frees the slot of a redirected image when the watch stops', async () => {
    const fetchImpl = vi.fn<ThumbDeps['fetch']>(async () => opaque());
    const { d } = deps(fetchImpl);
    const stop = watchThumbnail('/api/v1/nodes/nod_1/thumbnail?size=256', sink(), d);
    await settle();
    expect(d.limiter.running).toBe(1);
    stop();
    await settle();
    expect(d.limiter.running).toBe(0);
  });

  it('shows the icon, quietly, when the body cannot be read: aborted or failing midway', async () => {
    const fetchImpl = vi.fn<ThumbDeps['fetch']>(async () => {
      const res = answer(200);
      res.blob = () => Promise.reject(new TypeError('body stream failed'));
      return res;
    });
    const { d, createURL } = deps(fetchImpl);
    const s = sink();
    const errors = vi.spyOn(console, 'error');
    watchThumbnail('/api/v1/nodes/nod_1/thumbnail?size=256', s, d);
    await vi.advanceTimersByTimeAsync(0);
    expect(s.none).toHaveBeenCalledTimes(1);
    expect(s.image).not.toHaveBeenCalled();
    expect(createURL).not.toHaveBeenCalled();
    expect(errors).not.toHaveBeenCalled();
    errors.mockRestore();
  });

  it('stops at once: no ask after the stop, the wait is cancelled, and no timer is left', async () => {
    const fetchImpl = vi.fn<ThumbDeps['fetch']>(async () => answer(202, { 'Retry-After': '5' }));
    const { d } = deps(fetchImpl);
    const s = sink();
    const stop = watchThumbnail('/api/v1/nodes/nod_1/thumbnail?size=256', s, d);
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    stop();
    expect(vi.getTimerCount()).toBe(0);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(s.image).not.toHaveBeenCalled();
    expect(s.none).not.toHaveBeenCalled();
  });

  it('drops an answer that arrives after the stop, and makes no image address for it', async () => {
    let finish: (r: Response) => void = () => undefined;
    const fetchImpl = vi.fn<ThumbDeps['fetch']>(() => new Promise<Response>((resolve) => (finish = resolve)));
    const { d, createURL, revokeURL } = deps(fetchImpl);
    const s = sink();
    const stop = watchThumbnail('/api/v1/nodes/nod_1/thumbnail?size=256', s, d);
    await vi.advanceTimersByTimeAsync(0);
    stop();
    finish(answer(200));
    await vi.advanceTimersByTimeAsync(0);
    expect(createURL).not.toHaveBeenCalled();
    expect(revokeURL).not.toHaveBeenCalled();
    expect(s.image).not.toHaveBeenCalled();
  });

  it('revokes its image address when it stops', async () => {
    const fetchImpl = vi.fn<ThumbDeps['fetch']>(async () => answer(200));
    const { d, revokeURL } = deps(fetchImpl);
    const s = sink();
    const stop = watchThumbnail('/api/v1/nodes/nod_1/thumbnail?size=256', s, d);
    await vi.advanceTimersByTimeAsync(0);
    expect(s.image).toHaveBeenCalledWith('blob:thumb-1');
    stop();
    expect(revokeURL).toHaveBeenCalledWith('blob:thumb-1');
    stop();
    expect(revokeURL).toHaveBeenCalledTimes(1);
  });
});
