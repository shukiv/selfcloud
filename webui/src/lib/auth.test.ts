// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The sign-out call is held open by the test, so the tests decide when it answers, times out or is overlapped.
const h = vi.hoisted(() => ({
  calls: [] as Array<{ signal?: AbortSignal }>,
  answers: [] as Array<() => void>,
}));

vi.mock('./api', () => ({
  api: vi.fn(
    (_method: string, _path: string, opts?: { signal?: AbortSignal }) =>
      new Promise<void>((resolve, reject) => {
        h.calls.push({ signal: opts?.signal });
        h.answers.push(() => resolve());
        opts?.signal?.addEventListener('abort', () => reject(new Error('aborted')));
      }),
  ),
  ApiError: class extends Error {},
}));

import { SIGN_OUT_TIMEOUT_MS, signOut } from './auth';
import { signingOut } from './session';

describe('signOut', () => {
  beforeEach(() => {
    h.calls.length = 0;
    h.answers.length = 0;
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('gives up a logout that hangs after its time limit, and lets the requests after it go again', async () => {
    vi.useFakeTimers();
    const done = signOut();
    expect(signingOut()).toBe(true);
    expect(h.calls[0]?.signal?.aborted).toBe(false);

    vi.advanceTimersByTime(SIGN_OUT_TIMEOUT_MS);
    await done;

    expect(h.calls[0]?.signal?.aborted).toBe(true);
    expect(signingOut()).toBe(false);
  });

  it('keeps the guard up until both of two overlapping sign-outs have ended', async () => {
    const first = signOut();
    const second = signOut();
    expect(signingOut()).toBe(true);

    h.answers[0]?.();
    await first;
    expect(signingOut()).toBe(true);

    h.answers[1]?.();
    await second;
    expect(signingOut()).toBe(false);
  });
});
