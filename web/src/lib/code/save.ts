// SPDX-License-Identifier: AGPL-3.0-or-later

// The code editor's reads and writes of a file's bytes (decision 124 to 4.3). The version of a file is the etag in its node's
// JSON answer (decision 135), never a response header. The open is a GET of the whole file with If-Match set to that etag; the
// save is a PUT with If-Match set to the etag of the last answer and the stored type sent unchanged. Neither goes through
// api(), which parses JSON and does not give the response's headers, and a save's body is bytes, not JSON.
//
// The answers are mapped by status to a short name the page shows with its own words. The server's text is never shown.
import { ApiError } from "../apiError";
import { contentPath } from "../files/api";
import { currentCsrf } from "../session";
import type { Node } from "../files/types";
import { EDIT_LIMIT } from "./edit";

// SaveFailure is why a save did not happen: a stale ETag (conflict), a live office lock (locked), a role that changed
// (forbidden), a file that was deleted (gone), a draft that is not UTF-8 text (notText), a draft over the limit (tooLarge), and
// anything else, including no answer at all (failed).
// signedOut is a sign-in that has ended (401): the draft stays and the page offers to sign in again. storageFull is a store
// with no room left (507). badName and folderGone are a copy's own refusals (400 404); see keepCopy.
export type SaveFailure =
  | "conflict"
  | "locked"
  | "forbidden"
  | "gone"
  | "notText"
  | "tooLarge"
  | "signedOut"
  | "storageFull"
  | "badName"
  | "folderGone"
  | "failed";

export type SaveResult = { ok: true; node: Node } | { ok: false; why: SaveFailure };

// SAVE_TIMEOUT_MS is how long a save may wait for its answer before it is given up as failed.
export const SAVE_TIMEOUT_MS = 60_000;

const FAILURE_BY_STATUS: Readonly<Record<number, SaveFailure>> = {
  400: "notText",
  403: "forbidden",
  404: "gone",
  409: "locked",
  412: "conflict",
  413: "tooLarge",
  401: "signedOut",
  507: "storageFull",
};

// failureOfStatus names a refused answer's failure (design 4.3): the table above, and "failed" for any other status.
export function failureOfStatus(status: number): SaveFailure {
  return FAILURE_BY_STATUS[status] ?? "failed";
}

// quoted says a value is a strong ETag as the server writes it: a quoted string. The server refuses any other If-Match, so the
// page does not send one.
function quoted(etag: string): boolean {
  return etag.length >= 2 && etag.startsWith('"') && etag.endsWith('"');
}

// readCapped reads a body to its end, or to one byte past limit, which is refused (413). The rest is cancelled, so a file
// larger than the editor takes is never held in memory whole.
async function readCapped(body: ReadableStream<Uint8Array>, limit: number): Promise<Uint8Array> {
  const reader = body.getReader();
  const parts: Uint8Array[] = [];
  let total = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      total += value.length;
      if (total > limit) throw new ApiError(413, "too_large", {}, 0);
      parts.push(value);
    }
  } finally {
    await reader.cancel().catch(() => undefined);
  }
  const out = new Uint8Array(total);
  let at = 0;
  for (const p of parts) {
    out.set(p, at);
    at += p.length;
  }
  return out;
}

// readWhole reads a file to the editor: the whole file, with no Range, and If-Match set to etag, the version the node's answer
// gave (decision 135). The server answers with the bytes only when the file still has that version, and with 412 when it has
// changed; the caller then reads the node again. The answer's ETag header is never read: a proxy in front may drop or weaken it.
// A GET of the content counts as a view, as every read of a file does. A refusal is thrown with its status. An etag that is not a
// quoted tag is refused before anything is sent (500), as saveText does.
export async function readWhole(id: string, etag: string): Promise<Uint8Array> {
  if (!quoted(etag)) throw new ApiError(500, "internal", {}, 0);
  const res = await fetch(contentPath(id, "inline"), {
    credentials: "same-origin",
    cache: "no-store",
    headers: { "If-Match": etag },
  });
  if (res.status !== 200) {
    await res.body?.cancel().catch(() => undefined);
    throw new ApiError(res.status, "internal", {}, 0);
  }
  return res.body === null ? new Uint8Array(0) : readCapped(res.body, EDIT_LIMIT);
}

// saveText writes a draft's bytes over the file read with etag. The size is checked before anything is sent: a draft over the
// limit is refused here, and the server would refuse it too. The stored type is sent exactly as the file has it, so a save never
// changes it (decision 124). The answer is the node, whose ETag is the next If-Match.
export async function saveText(nod: string, etag: string, storedType: string, bytes: Uint8Array): Promise<SaveResult> {
  if (bytes.length > EDIT_LIMIT) return { ok: false, why: "tooLarge" };
  if (!quoted(etag)) return { ok: false, why: "failed" };
  const headers: Record<string, string> = {
    Accept: "application/json",
    "If-Match": etag,
    "Content-Type": storedType || "application/octet-stream",
  };
  const csrf = currentCsrf();
  if (csrf) headers["X-CSRF-Token"] = csrf;
  // A save with no answer within SAVE_TIMEOUT_MS is aborted and reads as failed, so the page offers a retry. The timer
  // runs until the answer's body is read, since a body can stall too.
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), SAVE_TIMEOUT_MS);
  try {
    let res: Response;
    try {
      res = await fetch(`/api/v1/nodes/${encodeURIComponent(nod)}/content`, {
        method: "PUT",
        headers,
        body: bytes as BodyInit,
        credentials: "same-origin",
        cache: "no-store",
        signal: ctrl.signal,
      });
    } catch {
      return { ok: false, why: "failed" };
    }
    if (res.status !== 200) {
      await res.body?.cancel().catch(() => undefined);
      return { ok: false, why: FAILURE_BY_STATUS[res.status] ?? "failed" };
    }
    let got: unknown;
    try {
      got = await res.json();
    } catch {
      return { ok: false, why: "failed" };
    }
    if (typeof got !== "object" || got === null || typeof (got as { etag?: unknown }).etag !== "string") {
      return { ok: false, why: "failed" };
    }
    return { ok: true, node: got as Node };
  } finally {
    clearTimeout(timer);
  }
}
