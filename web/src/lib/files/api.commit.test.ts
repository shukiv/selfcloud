// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from 'vitest';
import { commitUpload, COMMIT_TIMEOUT_MS } from './api';

// A commit assembles a large file on the server, so it can take minutes. It has its own, longer limit: the default minute
// would mark an upload failed while the server may still finish it (guard: the commit is not cut at the default).
describe('the upload commit', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('is cut at its own ten-minute limit, and not at the default minute', async () => {
    expect(COMMIT_TIMEOUT_MS).toBe(10 * 60_000);
    vi.useFakeTimers();
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
    const pending = commitUpload('/api/v1/uploads/upl_1', { name: 'big.bin' }).catch((e: unknown) => e);
    await vi.advanceTimersByTimeAsync(60_001);
    expect(signals[0].aborted).toBe(false);
    await vi.advanceTimersByTimeAsync(COMMIT_TIMEOUT_MS - 60_001);
    expect(signals[0].aborted).toBe(true);
    expect(await pending).toMatchObject({ code: 'timeout', details: { write: true } });
  });
});
