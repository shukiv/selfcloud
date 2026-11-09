// SPDX-License-Identifier: AGPL-3.0-or-later

// The read side of the code viewer's text rule (design-ce 2.3, decision 115). The stored type is never trusted on its own:
// the first bytes are sniffed, and a file the sniff calls binary never reaches CodeMirror. The write side (encoding a save,
// the line breaks it keeps) comes with the editor.

// SNIFF_BYTES is how much of a file the sniff reads: the first 8 KiB, in one Range request.
export const SNIFF_BYTES = 8 * 1024;

// Sniff is what the first bytes say: UTF-8 text (or ASCII), binary (a NUL byte), or UTF-16 (its byte order mark), which
// is not UTF-8 text either.
export type Sniff = "text" | "binary" | "utf16";

// sniff reads the first SNIFF_BYTES of a file. A UTF-16 byte order mark, either order, means UTF-16. Otherwise a NUL byte
// means binary. An empty file is text.
export function sniff(head: Uint8Array): Sniff {
  const b = head.subarray(0, SNIFF_BYTES);
  if (b.length >= 2 && ((b[0] === 0xff && b[1] === 0xfe) || (b[0] === 0xfe && b[1] === 0xff))) return "utf16";
  return b.includes(0) ? "binary" : "text";
}

// Decoded is the text the viewer shows. bom says the bytes start with a UTF-8 byte order mark, which stays in the text (the
// editor writes it back). utf8 is false when the bytes are not valid UTF-8: each bad sequence is then shown as U+FFFD,
// read-only, and nothing is written.
export interface Decoded {
  text: string;
  bom: boolean;
  utf8: boolean;
}

// decodeText decodes the bytes of a read. cut says the read stopped before the end of the file (the preview reads the first
// 1 MiB), so a character the cut split is left out rather than shown as a bad sequence.
export function decodeText(bytes: Uint8Array, cut: boolean): Decoded {
  const b = cut ? bytes.subarray(0, wholeEnd(bytes)) : bytes;
  const bom = b.length >= 3 && b[0] === 0xef && b[1] === 0xbb && b[2] === 0xbf;
  try {
    return { text: new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(b), bom, utf8: true };
  } catch {
    return { text: new TextDecoder("utf-8", { ignoreBOM: true }).decode(b), bom, utf8: false };
  }
}

// wholeEnd is the end of the last whole character of a cut read: a lead byte at the end whose sequence the cut split is left
// out, with its continuation bytes (at most three).
function wholeEnd(b: Uint8Array): number {
  let i = b.length - 1;
  let back = 0;
  while (i >= 0 && back < 3 && (b[i] & 0xc0) === 0x80) {
    i--;
    back++;
  }
  if (i < 0) return b.length;
  const lead = b[i];
  const need = lead >= 0xf0 ? 4 : lead >= 0xe0 ? 3 : lead >= 0xc0 ? 2 : 1;
  return b.length - i < need ? i : b.length;
}
