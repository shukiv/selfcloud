// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';
import { beginSignOut, endSignOut } from './session';

// A request already on its way when a sign-out begins would be answered after the server ended the session, with a 401
// the browser reports as an error. A sign-out aborts such requests before its own call goes out.
describe('a sign-out and the requests in flight', () => {
  afterEach(() => {
    endSignOut();
    vi.unstubAllGlobals();
  });

  it('aborts a request in flight when the sign-out begins, and the request fails as refused by the sign-out', async () => {
    const signals: AbortSignal[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (_path: string, init: RequestInit) =>
          new Promise<Response>((_resolve, reject) => {
            const signal = init.signal as AbortSignal;
            signals.push(signal);
            signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
          }),
      ),
    );
    const pending = api('GET', '/api/v1/notifications/unread');
    await Promise.resolve();
    expect(signals).toHaveLength(1);
    beginSignOut();
    expect(signals[0].aborted).toBe(true);
    await expect(pending).rejects.toMatchObject({ code: 'signing_out' });
  });

  it('leaves a request that finished alone, and aborts nothing once the sign-out is over', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ count: 1 }), { status: 200 })));
    await expect(api('GET', '/api/v1/notifications/unread')).resolves.toEqual({ count: 1 });
    beginSignOut();
    endSignOut();
    await expect(api('GET', '/api/v1/notifications/unread')).resolves.toEqual({ count: 1 });
  });
});
