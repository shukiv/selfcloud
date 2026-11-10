// SPDX-License-Identifier: AGPL-3.0-or-later

// The code editor's reads and writes of a file's bytes (design-ce 4.1 to 4.3). The open is a GET of the whole file, which
// keeps its ETag; the save is a PUT with If-Match set to that ETag and the stored type sent unchanged. Neither goes through
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
export type SaveFailure = "conflict" | "locked" | "forbidden" | "gone" | "notText" | "tooLarge" | "failed";

export type SaveResult = { ok: true; node: Node } | { ok: false; why: SaveFailure };

// SAVE_TIMEOUT_MS is how long a save may wait for its answer before it is given up as failed (LOW-3).
export const SAVE_TIMEOUT_MS = 60_000;

const FAILURE_BY_STATUS: Readonly<Record<number, SaveFailure>> = {
  400: "notText",
  403: "forbidden",
  404: "gone",
  409: "locked",
  412: "conflict",
  413: "tooLarge",
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

// readWhole reads a file to the editor: the whole file, with no Range, and the ETag it was read with. A GET of the content
// counts as a view, as every read of a file does. A refusal is thrown with its status, and so is an answer with no ETag, which
// a save could not be checked against (500).
export async function readWhole(id: string): Promise<{ bytes: Uint8Array; etag: string }> {
  const res = await fetch(contentPath(id, "inline"), { credentials: "same-origin", cache: "no-store" });
  if (res.status !== 200) {
    await res.body?.cancel().catch(() => undefined);
    throw new ApiError(res.status, "internal", {}, 0);
  }
  const etag = res.headers.get("ETag") ?? "";
  if (!quoted(etag)) {
    await res.body?.cancel().catch(() => undefined);
    throw new ApiError(500, "internal", {}, 0);
  }
  const bytes = res.body === null ? new Uint8Array(0) : await readCapped(res.body, EDIT_LIMIT);
  return { bytes, etag };
}

// saveText writes a draft's bytes over the file read with etag. The size is checked before anything is sent: a draft over the
// limit is refused here, and the server would refuse it too. The stored type is sent exactly as the file has it, so a save never
// changes it (design-ce 4.2). The answer is the node, whose ETag is the next If-Match.
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
  // A save with no answer within SAVE_TIMEOUT_MS is aborted and reads as failed, so the page offers a retry (LOW-3). The timer
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
