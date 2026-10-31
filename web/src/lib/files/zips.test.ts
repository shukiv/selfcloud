// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../api';
import { loadLang, translate } from '../i18n';
import { MAX_POLLS, poll } from '../jobs/backoff';
import type { Job } from './types';
import { makeZip, zipErrorText, zipFailureText, type ZipDeps, type ZipStatus } from './zips';

// Every text the zip path can show, in English, and each one's key, so a refusal is checked by the words it shows.
const KEYS = {
  tooLarge: 'files.zip.tooLarge',
  storage: 'files.zip.storage',
  storageAccount: 'files.zip.storageAccount',
  restricted: 'files.err.downloadRestricted',
  tooMany: 'files.zip.tooMany',
  tooManyAccount: 'files.zip.tooManyAccount',
  invalid: 'files.zip.invalid',
  failed: 'files.zip.failed',
};
const text = (key: string) => translate('en', key);

describe('zipFailureText', () => {
  beforeAll(async () => {
    await Promise.all([loadLang('he'), loadLang('ar')]);
  });

  it('names each refusal the zip routes give, and no raw code or reason', () => {
    const cases: Array<[string | undefined, string | undefined, string]> = [
      ['too_large', undefined, KEYS.tooLarge],
      ['storage_full', 'zip_disk', KEYS.storage],
      ['storage_full', 'zip_account_disk', KEYS.storageAccount],
      ['storage_full', undefined, KEYS.storage],
      ['download_restricted', undefined, KEYS.restricted],
      ['conflict', 'too_many_zips', KEYS.tooMany],
      ['conflict', 'too_many_zips_account', KEYS.tooManyAccount],
      ['conflict', 'something_else', KEYS.failed],
      ['invalid_request', undefined, KEYS.invalid],
      [undefined, undefined, KEYS.failed],
      ['internal', undefined, KEYS.failed],
    ];
    for (const [code, reason, key] of cases) {
      const shown = zipFailureText(code, reason, 'en');
      expect(shown, `${code}/${reason}`).toBe(text(key));
      expect(shown).not.toMatch(/zip_disk|zip_account_disk|too_many_zips|storage_full|too_large|_/);
    }
  });

  it('translates into the language asked for', () => {
    expect(zipFailureText('too_large', undefined, 'he')).toBe(translate('he', KEYS.tooLarge));
    expect(zipFailureText('storage_full', 'zip_account_disk', 'ar')).toBe(translate('ar', KEYS.storageAccount));
    expect(translate('he', KEYS.tooLarge)).not.toBe(translate('en', KEYS.tooLarge));
  });
});

describe('zipErrorText', () => {
  it('maps the refusals of the zip request before the file API does', () => {
    expect(zipErrorText(new ApiError(507, 'storage_full', { reason: 'zip_account_disk' }, 0), 'en')).toBe(text(KEYS.storageAccount));
    expect(zipErrorText(new ApiError(507, 'storage_full', { scope: 'server', available_bytes: 0 }, 0), 'en')).toBe(text(KEYS.storage));
    expect(zipErrorText(new ApiError(413, 'too_large', {}, 0), 'en')).toBe(text(KEYS.tooLarge));
    expect(zipErrorText(new ApiError(403, 'download_restricted', {}, 0), 'en')).toBe(text(KEYS.restricted));
    expect(zipErrorText(new ApiError(409, 'conflict', { reason: 'too_many_zips' }, 0), 'en')).toBe(text(KEYS.tooMany));
  });

  it('shows the rest as the file API shows it, and never the server text', () => {
    const limited = zipErrorText(new ApiError(429, 'rate_limited', {}, 30), 'en');
    expect(limited).not.toBe('');
    expect(limited).not.toMatch(/rate_limited/);
    expect(zipErrorText(new Error('the disk is full: /var/lib/x'), 'en')).toBe(translate('en', 'error.generic'));
    expect(zipErrorText(new ApiError(404, 'not_found', {}, 0), 'en')).toBe(translate('en', 'files.err.notFound'));
  });
});

// A job that answers each call with the next status in turn, and then the last one.
function jobs(statuses: Job['status'][], extra: Partial<Job> = {}) {
  const list = [...statuses];
  return vi.fn(async (id: string): Promise<Job> => {
    const status = list.length > 1 ? list.shift()! : list[0];
    return { id, kind: 'node.zip', target: 'zip_1', status, finished_at: null, ...extra };
  });
}

function deps(over: Partial<ZipDeps>): ZipDeps {
  return {
    create: vi.fn(async () => ({ id: 'job_1', kind: 'node.zip', target: 'zip_1', status: 'queued', finished_at: null }) as Job),
    job: jobs(['succeeded']),
    status: vi.fn(async (): Promise<ZipStatus> => ({ ready: true, skipped: 0 })),
    download: vi.fn(),
    poll,
    ...over,
  };
}

describe('makeZip', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('waits for the job, then starts the download and reports the items left out', async () => {
    const d = deps({ job: jobs(['queued', 'running', 'succeeded']), status: vi.fn(async () => ({ ready: true, skipped: 3 })) });
    const onReady = vi.fn();
    const onFailed = vi.fn();
    makeZip(['nod_a', 'nod_b'], { lang: 'en', onReady, onFailed, onGiveUp: vi.fn() }, d);
    await vi.advanceTimersByTimeAsync(0);
    expect(d.create).toHaveBeenCalledWith(['nod_a', 'nod_b']);
    expect(d.download).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(d.download).toHaveBeenCalledTimes(1);
    expect(d.download).toHaveBeenCalledWith('zip_1');
    expect(onReady).toHaveBeenCalledWith(3, 'zip_1');
    expect(onFailed).not.toHaveBeenCalled();
  });

  it('shows a failed job by its code, not by its text, and starts no download', async () => {
    const d = deps({
      job: jobs(['failed'], { error: 'the zip failed: a zip may hold at most 3 entries', error_code: 'too_large' }),
    });
    const onFailed = vi.fn();
    makeZip(['nod_a'], { lang: 'en', onReady: vi.fn(), onFailed, onGiveUp: vi.fn() }, d);
    await vi.advanceTimersByTimeAsync(0);
    expect(onFailed).toHaveBeenCalledWith(text(KEYS.tooLarge));
    expect(d.download).not.toHaveBeenCalled();
  });

  it('shows a failed job with no code as the generic failure', async () => {
    const d = deps({ job: jobs(['failed'], { error: 'the disk is full' }) });
    const onFailed = vi.fn();
    makeZip(['nod_a'], { lang: 'en', onReady: vi.fn(), onFailed, onGiveUp: vi.fn() }, d);
    await vi.advanceTimersByTimeAsync(0);
    expect(onFailed).toHaveBeenCalledWith(text(KEYS.failed));
  });

  it('shows a refusal of the request itself, and makes no job', async () => {
    const d = deps({ create: vi.fn(async () => Promise.reject(new ApiError(403, 'download_restricted', {}, 0))) });
    const onFailed = vi.fn();
    makeZip(['nod_a'], { lang: 'en', onReady: vi.fn(), onFailed, onGiveUp: vi.fn() }, d);
    await vi.advanceTimersByTimeAsync(0);
    expect(onFailed).toHaveBeenCalledWith(text(KEYS.restricted));
    expect(d.job).not.toHaveBeenCalled();
  });

  it('stops watching when it is stopped: no more polls, and no download', async () => {
    const d = deps({ job: jobs(['running']) });
    const onReady = vi.fn();
    const stop = makeZip(['nod_a'], { lang: 'en', onReady, onFailed: vi.fn(), onGiveUp: vi.fn() }, d);
    await vi.advanceTimersByTimeAsync(0);
    expect(d.job).toHaveBeenCalledTimes(1);
    stop();
    await vi.advanceTimersByTimeAsync(60 * 60_000);
    expect(d.job).toHaveBeenCalledTimes(1);
    expect(d.download).not.toHaveBeenCalled();
    expect(onReady).not.toHaveBeenCalled();
  });

  it('reports a job that takes longer than the page waits for, once', async () => {
    const d = deps({ job: jobs(['running']) });
    const onGiveUp = vi.fn();
    makeZip(['nod_a'], { lang: 'en', onReady: vi.fn(), onFailed: vi.fn(), onGiveUp }, d);
    await vi.advanceTimersByTimeAsync(60 * 60_000);
    expect(d.job).toHaveBeenCalledTimes(MAX_POLLS);
    expect(onGiveUp).toHaveBeenCalledTimes(1);
    expect(d.download).not.toHaveBeenCalled();
  });
});
