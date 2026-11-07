// SPDX-License-Identifier: AGPL-3.0-or-later

// The share page's side of the share target (decision 82). The service worker holds the files of a share in memory, under the
// id the share page's address carries, and gives them to the page once, by message over a port. This module asks for them and
// tells the worker when they are refused or cancelled. Nothing here stores a file: the page keeps the files in memory only.

// ANSWER_LIMIT is how long the page waits for the worker's answer before it takes that there is none. A worker that was asleep
// wakes on the message, so the wait is short.
const ANSWER_LIMIT = 10_000;

// taken keeps each id's answer for the life of the page. React runs an effect twice in development, and the second run must get
// the same files, not the empty answer the worker gives a second time.
const taken = new Map<string, Promise<File[]>>();

// takeShare gives the files the worker holds for a share id, or [] when it holds none for that id (gone, never held, or no worker
// in control of this page).
export function takeShare(id: string): Promise<File[]> {
  if (id === "") return Promise.resolve([]);
  let answer = taken.get(id);
  if (answer === undefined) {
    answer = ask(id);
    taken.set(id, answer);
  }
  return answer;
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
  taken.delete(id);
  const worker = typeof navigator !== "undefined" ? navigator.serviceWorker?.controller : null;
  worker?.postMessage({ type: "SHARE_DROP", id });
}

// forgetShare drops the page's copy of one share's files, once they have gone to the upload queue (decision 82, round 2): a
// revisit of the address must not bring them back.
export function forgetShare(id: string): void {
  taken.delete(id);
}

// clearShares drops the page's copy of every share: at a sign-out, and when the person changes (decision 82, round 2).
export function clearShares(): void {
  taken.clear();
}
