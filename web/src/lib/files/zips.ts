// SPDX-License-Identifier: AGPL-3.0-or-later

// Download as zip. The server makes the zip as a job (POST /zips), and the job's target is the zip. The page polls the job,
// then reads the zip's headers (HEAD, which says how many items were left out) and starts the download as a browser link,
// so an interrupted download resumes through the browser. Every message is chosen by code: the server's text, and any ID,
// is never shown.

import { ApiError } from '../api';
import { fileErrorText } from './fileErrors';
import { createZipJob, getJob } from './api';
import { poll } from '../jobs/backoff';
import { translate, type Lang } from '../i18n';
import type { Job } from './types';

// zipPath is the address of a zip made by the caller.
export function zipPath(zip: string): string {
  return `/api/v1/zips/${zip}`;
}

// zipFailureText is the text for a refused zip, by its error code and reason. An unknown refusal, and a failed job with no
// code, get the generic text: the server's text is never shown.
export function zipFailureText(code: string | undefined, reason: string | undefined, lang: Lang): string {
  switch (code) {
    case 'too_large':
      return translate(lang, 'files.zip.tooLarge');
    case 'storage_full':
      return translate(lang, reason === 'zip_account_disk' ? 'files.zip.storageAccount' : 'files.zip.storage');
    case 'download_restricted':
      return translate(lang, 'files.err.downloadRestricted');
    case 'conflict':
      if (reason === 'too_many_zips') return translate(lang, 'files.zip.tooMany');
      if (reason === 'too_many_zips_account') return translate(lang, 'files.zip.tooManyAccount');
      return translate(lang, 'files.zip.failed');
    case 'invalid_request':
      return translate(lang, 'files.zip.invalid');
    default:
      return translate(lang, 'files.zip.failed');
  }
}

// ZIP_CODES are the refusals the zip routes give that have text of their own.
const ZIP_CODES = ['too_large', 'storage_full', 'download_restricted', 'conflict', 'invalid_request'];

// zipErrorText is the text for an error from the zip routes. The zip's own refusals come first; anything else is shown as
// the file API shows it.
export function zipErrorText(err: unknown, lang: Lang): string {
  if (err instanceof ApiError && ZIP_CODES.includes(err.code)) {
    const reason = typeof err.details.reason === 'string' ? err.details.reason : undefined;
    return zipFailureText(err.code, reason, lang);
  }
  return fileErrorText(err, lang);
}

// ZipStatus is what the zip's headers say: whether it is ready, and how many items the server left out.
export interface ZipStatus {
  ready: boolean;
  skipped: number;
}

// zipStatus reads the zip's headers without its body. 202 means the zip is still being made; 200 means it is ready.
export async function zipStatus(zip: string): Promise<ZipStatus> {
  const res = await fetch(zipPath(zip), { method: 'HEAD', credentials: 'same-origin', cache: 'no-store' });
  if (res.status === 202) return { ready: false, skipped: 0 };
  if (res.status !== 200) throw new ApiError(res.status, res.status === 404 ? 'not_found' : 'internal', {}, 0);
  return { ready: true, skipped: parseSkipped(res.headers.get('X-Selfcloud-Skipped')) };
}

// parseSkipped reads the left-out count from a zip's header. Only a whole, non-negative number counts; any other value
// (missing, empty, signed, fractional, exponent, or not a number) means nothing was left out.
export function parseSkipped(value: string | null): number {
  if (value === null) return 0;
  const v = value.trim();
  if (!/^\d+$/.test(v)) return 0;
  const n = Number(v);
  return Number.isSafeInteger(n) ? n : 0;
}

// ZIP_MAX_ITEMS is the most items one zip may name: the server's limit on its roots.
export const ZIP_MAX_ITEMS = 1000;

// zipTooManyText explains the item limit, in the language asked for.
export function zipTooManyText(lang: Lang): string {
  return translate(lang, 'files.zip.tooManyItems', { count: ZIP_MAX_ITEMS });
}

// downloadZip starts the download as a browser link. The browser resumes it if it is interrupted.
export function downloadZip(zip: string): void {
  const a = document.createElement('a');
  a.href = zipPath(zip);
  a.setAttribute('download', '');
  a.rel = 'noopener';
  document.body.appendChild(a);
  a.click();
  a.remove();
}

// ZipDeps are the parts a test replaces. The live ones are the routes above.
export interface ZipDeps {
  create: (nodes: string[]) => Promise<Job>;
  job: (id: string) => Promise<Job>;
  status: (zip: string) => Promise<ZipStatus>;
  download: (zip: string) => void;
  poll: typeof poll;
}

export const liveZipDeps: ZipDeps = {
  create: createZipJob,
  job: getJob,
  status: zipStatus,
  download: downloadZip,
  poll,
};

// ZipPhase is what the zip dialog shows. zip is the zip's ID: it is kept for the download again button and never shown.
export type ZipPhase =
  | { kind: 'making' }
  | { kind: 'ready'; skipped: number; zip: string }
  | { kind: 'failed'; text: string }
  | { kind: 'slow' };

// ZipHandlers are what the page shows. Exactly one of onReady, onFailed and onGiveUp is called, unless the watch is stopped.
export interface ZipHandlers {
  lang: Lang;
  onReady: (skipped: number, zip: string) => void;
  onFailed: (text: string) => void;
  onGiveUp: () => void;
}

// makeZip asks for a zip of nodes, watches its job until it ends, and starts the download when it succeeds. It returns a
// function that stops the watch. Stopping does not cancel the job: the server finishes it, and nothing is downloaded.
export function makeZip(nodes: string[], h: ZipHandlers, d: ZipDeps = liveZipDeps): () => void {
  let stopped = false;
  let stopPoll: (() => void) | null = null;
  const fail = (err: unknown) => {
    if (!stopped) h.onFailed(zipErrorText(err, h.lang));
  };
  // A selection over the server's limit is explained here, and no request is made.
  if (nodes.length > ZIP_MAX_ITEMS) {
    h.onFailed(zipTooManyText(h.lang));
    return () => undefined;
  }
  d.create(nodes).then(
    (job) => {
      if (stopped) return;
      stopPoll = d.poll(
        () => d.job(job.id),
        (j) => j.status === 'succeeded' || j.status === 'failed',
        (j) => {
          stopPoll = null;
          if (stopped) return;
          if (j.status !== 'succeeded') {
            h.onFailed(zipFailureText(j.error_code, j.error_reason, h.lang));
            return;
          }
          d.status(j.target).then(
            ({ skipped }) => {
              if (stopped) return;
              d.download(j.target);
              h.onReady(skipped, j.target);
            },
            fail,
          );
        },
        (err) => {
          stopPoll = null;
          fail(err);
        },
        () => {
          stopPoll = null;
          if (!stopped) h.onGiveUp();
        },
      );
    },
    fail,
  );
  return () => {
    if (stopped) return;
    stopped = true;
    stopPoll?.();
    stopPoll = null;
  };
}
