// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it, vi } from 'vitest';
import { MAX_DELAY, MAX_ERRORS, MAX_POLLS, nextDelay, poll } from './backoff';

describe('nextDelay', () => {
  it('doubles from half a second and stops at eight seconds', () => {
    expect([0, 1, 2, 3, 4, 5, 6].map(nextDelay)).toEqual([500, 1000, 2000, 4000, 8000, 8000, 8000]);
    expect(nextDelay(1000)).toBe(MAX_DELAY);
    expect(nextDelay(-3)).toBe(500);
  });
});

describe('poll', () => {
  it('waits longer each time until the job is done, then reports once', async () => {
    vi.useFakeTimers();
    const answers = ['queued', 'running', 'succeeded'];
    const check = vi.fn(async () => answers.shift() as string);
    const done = vi.fn();
    const onError = vi.fn();
    poll(check, (s) => s === 'succeeded', done, onError);
    await vi.advanceTimersByTimeAsync(0);
    expect(check).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(500);
    expect(check).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(1000);
    expect(check).toHaveBeenCalledTimes(3);
    expect(done).toHaveBeenCalledWith('succeeded');
    await vi.advanceTimersByTimeAsync(60_000);
    expect(check).toHaveBeenCalledTimes(3);
    expect(onError).not.toHaveBeenCalled();
    vi.useRealTimers();
  });

  it('stops asking when the page is left, and never reports afterwards', async () => {
    vi.useFakeTimers();
    const check = vi.fn(async () => 'running');
    const done = vi.fn();
    const stop = poll(check, () => false, done, () => undefined);
    await vi.advanceTimersByTimeAsync(0);
    stop();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(check).toHaveBeenCalledTimes(1);
    expect(done).not.toHaveBeenCalled();
    vi.useRealTimers();
  });

  it('reports an error once, after failures in a row, and stops', async () => {
    vi.useFakeTimers();
    const check = vi.fn(async () => {
      throw new Error('gone');
    });
    const onError = vi.fn();
    poll(check, () => false, () => undefined, onError);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(check).toHaveBeenCalledTimes(MAX_ERRORS);
    expect(onError).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(check).toHaveBeenCalledTimes(MAX_ERRORS);
    vi.useRealTimers();
  });

  it('rides out a passing failure, so a deletion that goes on is not shown as failed', async () => {
    vi.useFakeTimers();
    const answers: Array<string | Error> = ['running', new Error('rate limited'), 'running', 'succeeded'];
    const check = vi.fn(async () => {
      const a = answers.shift() as string | Error;
      if (a instanceof Error) throw a;
      return a;
    });
    const done = vi.fn();
    const onError = vi.fn();
    poll(check, (s) => s === 'succeeded', done, onError);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(onError).not.toHaveBeenCalled();
    expect(done).toHaveBeenCalledWith('succeeded');
    vi.useRealTimers();
  });

  it('gives up after a bounded number of polls, says so once, and stops asking', async () => {
    vi.useFakeTimers();
    const check = vi.fn(async () => 'running');
    const done = vi.fn();
    const onError = vi.fn();
    const onGiveUp = vi.fn();
    poll(check, () => false, done, onError, onGiveUp);
    await vi.advanceTimersByTimeAsync(MAX_POLLS * MAX_DELAY);
    expect(check).toHaveBeenCalledTimes(MAX_POLLS);
    expect(onGiveUp).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(10 * MAX_DELAY);
    expect(check).toHaveBeenCalledTimes(MAX_POLLS);
    expect(done).not.toHaveBeenCalled();
    expect(onError).not.toHaveBeenCalled();
    vi.useRealTimers();
  });
});
