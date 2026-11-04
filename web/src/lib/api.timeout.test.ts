// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, REQUEST_TIMEOUT_MS } from './api';
import { errorMessage } from './errors';

// A request that never gets an answer keeps its busy dialog open. api() ends such a call with a timeout, so the person
// can go on; the call's own caller decides what the page says about a write that may have happened.
describe('a request with no answer', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('ends with the timeout code once the time limit passes (the fetch never answers)', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (_path: string, init: RequestInit) =>
          new Promise<Response>((_resolve, reject) => {
            (init.signal as AbortSignal).addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
          }),
      ),
    );
    await expect(api('POST', '/api/v1/accounts/acc_1/users', { body: {}, timeoutMs: 20 })).rejects.toMatchObject({
      code: 'timeout',
      status: 0,
    });
  });

  it('ends with the timeout code when the answer starts but its body never comes', async () => {
    const stalled = new ReadableStream<Uint8Array>({ start: () => undefined });
    vi.stubGlobal('fetch', vi.fn(async () => new Response(stalled, { status: 200 })));
    await expect(api('GET', '/api/v1/accounts/acc_1/usage', { timeoutMs: 20 })).rejects.toMatchObject({ code: 'timeout' });
  });

  // Only a write may have been saved, so the timeout tells a read from a write (guard: the timeout message names writes only).
  it('marks a timed-out read as not a write, and a timed-out write as one', async () => {
    vi.stubGlobal('fetch', vi.fn((_p: string, init: RequestInit) => new Promise<Response>((_r, rej) => (init.signal as AbortSignal).addEventListener('abort', () => rej(new DOMException('a', 'AbortError'))))));
    const read = await api('GET', '/api/v1/accounts/acc_1/usage', { timeoutMs: 20 }).catch((e: unknown) => e);
    expect(read).toMatchObject({ code: 'timeout', details: { write: false } });
    const write = await api('PATCH', '/api/v1/accounts/acc_1', { body: {}, timeoutMs: 20 }).catch((e: unknown) => e);
    expect(write).toMatchObject({ code: 'timeout', details: { write: true } });
    expect(errorMessage(read).key).toBe('error.timeoutRead');
    expect(errorMessage(write).key).toBe('error.timeout');
  });

  it('gives an answer that arrives in time, and leaves its request alone afterwards', async () => {
    const signals: AbortSignal[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_path: string, init: RequestInit) => {
        signals.push(init.signal as AbortSignal);
        return new Response(JSON.stringify({ ok: true }), { status: 200 });
      }),
    );
    await expect(api('GET', '/api/v1/accounts/acc_1/usage', { timeoutMs: 30 })).resolves.toEqual({ ok: true });
    await new Promise((r) => setTimeout(r, 60));
    expect(signals[0].aborted).toBe(false);
  });

  // A call that gives no time limit is cut at the default, a minute (guard: the default applies, and is not zero).
  it('cuts a call with no time given at 60 s, and not before', async () => {
    vi.useFakeTimers();
    try {
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
      const pending = api('GET', '/api/v1/accounts/acc_1/usage').catch((e: unknown) => e);
      await vi.advanceTimersByTimeAsync(59_999);
      expect(signals[0].aborted).toBe(false);
      await vi.advanceTimersByTimeAsync(1);
      expect(signals[0].aborted).toBe(true);
      expect(await pending).toMatchObject({ code: 'timeout' });
    } finally {
      vi.useRealTimers();
    }
  });

  // A cancel while the answer's body is still arriving is a cancelled call, not an answer with no body: a success with a null
  // body would make the caller read fields that are not there (guard: a cancel during the body read is a failure).
  it('a cancel during the body read ends as cancelled, not as a null answer', async () => {
    const stalled = new ReadableStream<Uint8Array>({ start: () => undefined });
    vi.stubGlobal('fetch', vi.fn(async () => new Response(stalled, { status: 201 })));
    const caller = new AbortController();
    const pending = api('POST', '/api/v1/accounts', { body: {}, signal: caller.signal, timeoutMs: 60_000 });
    await new Promise((r) => setTimeout(r, 10));
    caller.abort();
    await expect(pending).rejects.toMatchObject({ code: 'cancelled' });
  });

  it('is limited by the default when no time is given, and a cancelled call ends as cancelled', async () => {
    expect(REQUEST_TIMEOUT_MS).toBe(60_000);
    const caller = new AbortController();
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (_path: string, init: RequestInit) =>
          new Promise<Response>((_resolve, reject) => {
            (init.signal as AbortSignal).addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
          }),
      ),
    );
    const pending = api('POST', '/api/v1/accounts/acc_1/users', { body: {}, signal: caller.signal });
    await Promise.resolve();
    caller.abort();
    await expect(pending).rejects.toMatchObject({ code: 'cancelled' });
  });
});
