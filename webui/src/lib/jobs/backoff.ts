// SPDX-License-Identifier: AGPL-3.0-or-later

// Polling of a background job (GET /jobs/{job}). The wait grows from half a second and stops growing at eight
// seconds, so a long deletion is checked a few times a minute, not every half second.

export const FIRST_DELAY = 500;
export const MAX_DELAY = 8000;

// nextDelay is the wait before poll number attempt (0 for the first wait), in milliseconds.
export function nextDelay(attempt: number): number {
  const n = Math.max(0, Math.floor(attempt));
  return Math.min(MAX_DELAY, FIRST_DELAY * 2 ** Math.min(n, 30));
}

// A poll asks at most MAX_POLLS times, about fifteen minutes at the longest wait, and then gives up and says
// so: the job goes on on the server, and the page tells the user to look later. A failed check is tried again;
// only MAX_ERRORS failures in a row are reported, so one rate limit or lost connection does not show a job
// that is still running as failed.
export const MAX_POLLS = 120;
export const MAX_ERRORS = 3;

// poll calls check until it answers done, with the backoff between calls. It returns a function that stops it.
// A stopped poll never calls check again, and never reports, so a page that is left stops asking. Exactly one
// of onDone, onError and onGiveUp is called, once, unless the poll is stopped first.
export function poll<T>(
  check: () => Promise<T>,
  isDone: (value: T) => boolean,
  onDone: (value: T) => void,
  onError: (err: unknown) => void,
  onGiveUp: () => void = () => undefined,
  schedule: (fn: () => void, ms: number) => unknown = setTimeout,
  cancel: (handle: unknown) => void = (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
): () => void {
  let stopped = false;
  let handle: unknown = null;
  let polls = 0;
  let errors = 0;
  const again = () => {
    if (polls >= MAX_POLLS) {
      onGiveUp();
      return;
    }
    handle = schedule(step, nextDelay(polls - 1));
  };
  const step = () => {
    handle = null;
    if (stopped) return;
    polls++;
    check().then(
      (value) => {
        if (stopped) return;
        errors = 0;
        if (isDone(value)) {
          onDone(value);
          return;
        }
        again();
      },
      (err: unknown) => {
        if (stopped) return;
        if (++errors >= MAX_ERRORS) {
          onError(err);
          return;
        }
        again();
      },
    );
  };
  handle = schedule(step, 0);
  return () => {
    stopped = true;
    if (handle !== null) cancel(handle);
    handle = null;
  };
}
