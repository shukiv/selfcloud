// SPDX-License-Identifier: AGPL-3.0-or-later

// The code viewer's reads (decision 115). A file the rules may take is sniffed first: its first 8 KiB, in one Range request.
// Only a file the sniff finds to be text is read for showing, and only its first 1 MiB, as the preview always read. Both
// reads are on this origin, inline, so a viewer who may not download the file can still read text the server shows inline.
// A GET of the content counts as a view; the server keeps one view of a file per person in ten minutes, so the sniff and the
// read count once.
import { ApiError } from "../apiError";
import { contentPath } from "../files/api";
import { TEXT_PREVIEW_BYTES } from "../files/kind";
import type { Node } from "../files/types";
import { decodeText, sniff, SNIFF_BYTES } from "./encoding";
import { viewerOf, type Viewer } from "./kind";

// readHead reads the first n bytes of a file, or gives "restricted" when the server refuses the read (403). An empty file
// answers 416, and gives no bytes. Only a 206 answer is read: a 200 is the whole file (a proxy that dropped the Range header),
// which could be gigabytes, so it is refused unread. A 206 is read as a stream and no further than n bytes, whatever it sends.
export async function readHead(id: string, n: number): Promise<Uint8Array | "restricted"> {
  const res = await fetch(contentPath(id, "inline"), {
    credentials: "same-origin",
    cache: "no-store",
    headers: { Range: `bytes=0-${n - 1}` },
  });
  if (res.status !== 206 || res.body === null) {
    // The body of a refusal is not needed: let the connection go.
    await res.body?.cancel().catch(() => undefined);
    if (res.status === 403) return "restricted";
    if (res.status === 416) return new Uint8Array(0);
    throw new ApiError(res.status, "internal", {}, 0);
  }
  return readUpTo(res.body, n);
}

// readUpTo reads a body until it has n bytes or it ends, and cancels whatever is left.
async function readUpTo(body: ReadableStream<Uint8Array>, n: number): Promise<Uint8Array> {
  const reader = body.getReader();
  const out = new Uint8Array(n);
  let got = 0;
  try {
    while (got < n) {
      const { done, value } = await reader.read();
      if (done) break;
      const take = Math.min(value.length, n - got);
      out.set(value.subarray(0, take), got);
      got += take;
    }
  } finally {
    await reader.cancel().catch(() => undefined);
  }
  return out.slice(0, got);
}

// Code is a file's text as the viewer shows it, and how it shows. utf8 is false when the bytes are not valid UTF-8: the text
// then has U+FFFD for each bad sequence, and the viewer says so (decision 115). bom says the file starts with a UTF-8 byte
// order mark, which the text leaves out (decision 130).
export interface Code {
  text: string;
  viewer: Viewer;
  utf8: boolean;
  bom: boolean;
}

// sniffs reads a file's first bytes and applies the rule order: the viewer it opens in, null when it does not open in the
// code viewer, or "restricted".
export async function sniffs(node: Node): Promise<Viewer | null | "restricted"> {
  if (node.size === 0) return viewerOf(node, "text");
  const head = await readHead(node.id, SNIFF_BYTES);
  if (head === "restricted") return head;
  return viewerOf(node, sniff(head));
}

// readCode sniffs a file and, when it is text, reads it for showing: the first TEXT_PREVIEW_BYTES. It gives the text, null
// when the file does not open in the code viewer, or "restricted". An empty file is never asked for.
export async function readCode(node: Node): Promise<Code | null | "restricted"> {
  if (node.size === 0) {
    const viewer = viewerOf(node, "text");
    return viewer && { text: "", viewer, utf8: true, bom: false };
  }
  const head = await readHead(node.id, SNIFF_BYTES);
  if (head === "restricted") return head;
  const viewer = viewerOf(node, sniff(head));
  if (viewer === null) return null;
  // A file no larger than the sniff was read whole by it.
  const bytes = node.size <= SNIFF_BYTES ? head : await readHead(node.id, TEXT_PREVIEW_BYTES);
  if (bytes === "restricted") return bytes;
  const { text, utf8, bom } = decodeText(bytes, bytes.length < node.size);
  return { text, viewer, utf8, bom };
}
