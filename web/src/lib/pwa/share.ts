// SPDX-License-Identifier: AGPL-3.0-or-later

// The share page's side of the share target (decision 82). The service worker holds the files of a share in memory, under the
// id the share page's address carries, and gives them to the page once, by message over a port. This module asks for them and
// tells the worker when they are refused, cancelled or dropped. It never writes a file to storage: the page keeps the files it
// took in memory only, for the time-box below.

// ANSWER_LIMIT is how long the page waits for the worker's answer before it takes that there is none. A worker that was asleep
// wakes on the message, so the wait is short. An answer that comes after the limit is lost: the page has already taken that there
// is none, and the worker has already let go of the files when it handed them over (decision 82, round 3, L4).
const ANSWER_LIMIT = 10_000;

// PAGE_LIMIT is how long the page keeps its copy of a share's files after its last take of the share: sixty seconds (decision 82,
// round 3, M1). A signed-in person who leaves the share page, by the sidebar or otherwise, and comes back later gets nothing.
const PAGE_LIMIT = 60 * 1000;

// SIGNED_OUT_LIMIT is how long the page keeps the files of a share it took while signed out: five minutes. Signing in with an
// emailed code takes a few minutes at most, and the page takes the share again after the sign-in, which then gives it PAGE_LIMIT.
// A person who walks away at the sign-in page does not leave the files in the tab for good (decision 82, round 3, M1).
const SIGNED_OUT_LIMIT = 5 * 60 * 1000;

// taken keeps each id's answer, and the timer that forgets it, for the time-box. React runs an effect twice in development, and
// the second run must get the same files, not the empty answer the worker gives a second time. A timer, not an unmount clean-up,
// ends a copy: StrictMode runs effects twice, so a clean-up would drop the files it has just taken.
interface Taken {
  answer: Promise<File[]>;
  timer: ReturnType<typeof setTimeout> | undefined;
}
const taken = new Map<string, Taken>();

// takeShare gives the files the worker holds for a share id, or [] when it holds none for that id (gone, never held, or no worker
// in control of this page). Each take restarts the time-box: PAGE_LIMIT, or SIGNED_OUT_LIMIT for a take made while signed out.
export function takeShare(id: string, opts: { signedOut?: boolean } = {}): Promise<File[]> {
  if (id === "") return Promise.resolve([]);
  let entry = taken.get(id);
  if (entry === undefined) {
    entry = { answer: ask(id), timer: undefined };
    taken.set(id, entry);
  }
  clearTimeout(entry.timer);
  entry.timer = setTimeout(() => forget(id), opts.signedOut ? SIGNED_OUT_LIMIT : PAGE_LIMIT);
  return entry.answer;
}

// takenIDs lists the ids whose files the page still holds. It reads only, and restarts no time-box.
export function takenIDs(): string[] {
  return [...taken.keys()];
}

// forget ends the page's copy of one share.
function forget(id: string): void {
  const entry = taken.get(id);
  if (entry) clearTimeout(entry.timer);
  taken.delete(id);
}

// ask sends SHARE_TAKE to the worker that controls the page, with a port for the answer.
function ask(id: string): Promise<File[]> {
  const worker = typeof navigator !== "undefined" ? navigator.serviceWorker?.controller : null;
  if (!worker) return Promise.resolve([]);
  return new Promise((resolve) => {
    const channel = new MessageChannel();
    const timer = setTimeout(() => {
      channel.port1.close();
      resolve([]);
    }, ANSWER_LIMIT);
    channel.port1.onmessage = (event: MessageEvent<{ files?: unknown }>) => {
      clearTimeout(timer);
      channel.port1.close();
      const files = event.data?.files;
      resolve(Array.isArray(files) ? (files as File[]) : []);
    };
    worker.postMessage({ type: "SHARE_TAKE", id }, [channel.port2]);
  });
}

// dropShare tells the worker to forget the files of a share it still holds, when the person refuses or cancels, and forgets the
// page's own copy too. A share the worker handed over is already gone; the message then does nothing.
export function dropShare(id: string): void {
  if (id === "") return;
  forget(id);
  const worker = typeof navigator !== "undefined" ? navigator.serviceWorker?.controller : null;
  worker?.postMessage({ type: "SHARE_DROP", id });
}

// forgetShare drops the page's copy of one share's files, once they have gone to the upload queue (decision 82, round 2): a
// revisit of the address must not bring them back.
export function forgetShare(id: string): void {
  forget(id);
}

// clearShares drops the page's copy of every share, and tells the worker to drop every share it still holds: at a sign-out, when
// the person changes, and when the server ends the session (decision 82, round 2; round 3, L5).
export function clearShares(): void {
  for (const id of [...taken.keys()]) forget(id);
  const worker = typeof navigator !== "undefined" ? navigator.serviceWorker?.controller : null;
  worker?.postMessage({ type: "SHARE_DROP_ALL" });
}
