// SPDX-License-Identifier: AGPL-3.0-or-later

// The code editor's conflict answers (design-ce 4.4, decision 130; the version source is decision 135). The check reads the file's
// current etag from its node's stat, which records no view, and never from a response header. Keep as a copy saves the draft as a
// new file beside the original: the folder path comes from parent_id up to the root, and the first free name is "name (n).ext" for
// n from 2, at most COPY_TRIES names. The original is never changed.
import { ApiError } from "../apiError";
import { statNode } from "../files/api";
import type { Node } from "../files/types";
import { currentCsrf } from "../session";
import { failureOfStatus, type SaveFailure } from "./save";

// COPY_TRIES bounds the names a copy tries: "name (2).ext" to "name (21).ext" (design 4.4).
export const COPY_TRIES = 20;

// MAX_DEPTH bounds the walk up a folder chain, so a damaged chain cannot loop.
const MAX_DEPTH = 256;

export type EtagResult = { ok: true; etag: string } | { ok: false; why: SaveFailure };

// currentEtag reads a file's current etag from its node's stat (GET /api/v1/nodes/{nod}), which records no view (design 4.4).
// A refusal of the stat is the save's table of failures (failureOfStatus), and a stat with no quoted etag is failed.
export async function currentEtag(nod: string): Promise<EtagResult> {
  let n: Node;
  try {
    n = await statNode(nod);
  } catch (err) {
    return { ok: false, why: err instanceof ApiError ? failureOfStatus(err.status) : "failed" };
  }
  const etag = n.etag ?? "";
  if (etag.length < 2 || !etag.startsWith('"') || !etag.endsWith('"')) return { ok: false, why: "failed" };
  return { ok: true, etag };
}

// copyName is the name of the n-th copy of a file: "name (n).ext", with the extension after the last dot. A name with no
// extension, or a dot file (".env"), gets the number at its end.
export function copyName(name: string, n: number): string {
  const dot = name.lastIndexOf(".");
  if (dot <= 0) return `${name} (${n})`;
  return `${name.slice(0, dot)} (${n})${name.slice(dot)}`;
}

// dirsOf gives the names of the folders between a file's folder and its root, outermost first. It walks parent_id up with a stat of
// each folder, which records no view. The root is the node whose parent_id is null; its name is not part of a path.
async function dirsOf(parentId: string | null): Promise<string[]> {
  const names: string[] = [];
  let id = parentId;
  for (let depth = 0; id !== null; depth++) {
    if (depth >= MAX_DEPTH) throw new Error("the folder chain is too deep");
    const n = await statNode(id);
    if (n.parent_id === null) break;
    names.unshift(n.name);
    id = n.parent_id;
  }
  return names;
}

// A failed copy says whether a request was sent: only a sent copy can have reached the server, so only it can be a lost reply (LOW-B).
export type CopyResult = { ok: true; node: Node } | { ok: false; why: SaveFailure | "noName"; sent: boolean };

// copyFailureOfStatus names a refused copy (LOW-2): a name the server will not take (400) and a folder that is gone (404) have words
// of their own; every other refusal is the save's table (failureOfStatus).
function copyFailureOfStatus(status: number): SaveFailure {
  if (status === 400) return "badName";
  if (status === 404) return "folderGone";
  return failureOfStatus(status);
}

// keepCopy saves bytes as a new file in the folder of file, under the first free copy name. Each try is a POST with If-None-Match: *,
// so a taken name answers 412 and the next name is tried. Any other answer stops the loop and is returned. The bounded loop gives up
// after COPY_TRIES names with "noName". The original file is not touched.
export async function keepCopy(file: Node, mime: string, bytes: Uint8Array): Promise<CopyResult> {
  if (!file.folder_id) return { ok: false, why: "failed", sent: false };
  let dirs: string[];
  try {
    dirs = await dirsOf(file.parent_id);
  } catch (err) {
    return { ok: false, why: err instanceof ApiError ? copyFailureOfStatus(err.status) : "failed", sent: false };
  }
  const csrf = currentCsrf();
  for (let n = 2; n < 2 + COPY_TRIES; n++) {
    const path = `/${[...dirs, copyName(file.name, n)].join("/")}`;
    const headers: Record<string, string> = {
      Accept: "application/json",
      "If-None-Match": "*",
      "Content-Type": mime || "application/octet-stream",
    };
    if (csrf) headers["X-CSRF-Token"] = csrf;
    let res: Response;
    try {
      res = await fetch(`/api/v1/folders/${encodeURIComponent(file.folder_id)}/files?path=${encodeURIComponent(path)}`, {
        method: "POST",
        headers,
        body: bytes as BodyInit,
        credentials: "same-origin",
        cache: "no-store",
      });
    } catch {
      return { ok: false, why: "failed", sent: true };
    }
    if (res.status === 412) {
      await res.body?.cancel().catch(() => undefined);
      continue;
    }
    if (res.status !== 201) {
      await res.body?.cancel().catch(() => undefined);
      return { ok: false, why: copyFailureOfStatus(res.status), sent: true };
    }
    let node: unknown;
    try {
      node = await res.json();
    } catch {
      return { ok: false, why: "failed", sent: true };
    }
    if (typeof node !== "object" || node === null || typeof (node as { id?: unknown }).id !== "string") {
      return { ok: false, why: "failed", sent: true };
    }
    return { ok: true, node: node as Node };
  }
  return { ok: false, why: "noName", sent: true };
}
