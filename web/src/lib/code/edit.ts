// SPDX-License-Identifier: AGPL-3.0-or-later

// Who may edit a file in the code editor, and how a draft is written back (decision 124, 3.1, 4.2, 5). The entry is the
// code viewer's own: the pencil in the preview, and the More menu's Edit. The office editor's entry (lib/files/editor.ts)
// is not used here, so a code file never reaches it, and the office feature flag does not gate the code editor.
import { can } from "../files/roles";
import type { Node } from "../files/types";
import { baseType, IMAGE, OFFICE, TEXT } from "../files/kind";
import type { Viewer } from "./kind";
import { packOf } from "./names";

// EDIT_LIMIT is the largest file the editor takes, and the largest draft it sends: 2 MiB, counted in bytes after encoding.
export const EDIT_LIMIT = 2 * 1024 * 1024;

type Editable = Pick<Node, "kind" | "name" | "mime_type" | "role" | "size">;

// fitsLimit says the file is no larger than the editor takes.
function fitsLimit(n: Editable): boolean {
  return n.size <= EDIT_LIMIT;
}

// codeEditEntry is "edit" when the pencil is offered for a file the code viewer shows (rules 3 and 4 of lib/code/kind.ts, which
// the viewer has already applied: viewer is its verdict), and null otherwise. Only an editor gets it (design 5, and the owner's
// answer: editors only); a viewer, a commenter and a contributor read the file. A rule-5 file (plain text with no name in the
// list) gets no pencil.
export function codeEditEntry(n: Editable, viewer: Viewer | null): "edit" | null {
  if (n.kind !== "file" || viewer === null || viewer.kind !== "code") return null;
  // An office file or a picture is never a code file, whatever the caller's viewer says (rules 1 and 2 come first).
  const mt = baseType(n.mime_type);
  if (OFFICE.has(mt) || IMAGE.has(mt)) return null;
  if (!can.edit(n.role) || !fitsLimit(n)) return null;
  return "edit";
}

// codeMenuEntry says the More menu offers Edit for a file, before the file is sniffed: a file whose name is in the code list, or
// whose stored type is text, and which is an editor's file of up to 2 MiB. A file that the sniff then finds binary opens and
// shows its own notice (the editor refuses it), which is the one place the menu cannot see the bytes.
export function codeMenuEntry(n: Editable): boolean {
  if (n.kind !== "file") return false;
  const mt = baseType(n.mime_type ?? "");
  if (OFFICE.has(mt) || IMAGE.has(mt)) return false;
  const named = packOf(n.name) !== null;
  const texty = mt.startsWith("text/") || TEXT.has(mt);
  if (!named && !texty) return false;
  return can.edit(n.role) && fitsLimit(n);
}

// lineBreakOf is the style of a file's first line break: CRLF, CR or LF. A file with no break is LF. The text is the one read
// (before the editor normalises it), so the style is the file's own.
export function lineBreakOf(text: string): "\r\n" | "\n" | "\r" {
  const i = text.search(/[\r\n]/);
  if (i < 0) return "\n";
  if (text[i] === "\n") return "\n";
  return text[i + 1] === "\n" ? "\r\n" : "\r";
}

// mixedBreaks says a file uses more than one style of line break (design 4.8). A save writes the first style on every line, so
// the file's other breaks change, and the page says so.
export function mixedBreaks(text: string): boolean {
  const styles = [/\r\n/.test(text), /(^|[^\r])\n/.test(text), /\r(?!\n)/.test(text)];
  return styles.filter(Boolean).length > 1;
}

// normalizeText is the text as the editor holds it: every line break is LF, which is how CodeMirror keeps a document.
export function normalizeText(text: string): string {
  return text.replace(/\r\n?/g, "\n");
}

const BOM = [0xef, 0xbb, 0xbf];

// encodeDraft is the bytes of a draft: its lines joined with the file's own break, encoded as UTF-8, with the byte order mark the
// file had at its start (the reader took it off). A draft of a file that had neither is written as it is.
export function encodeDraft(text: string, bom: boolean, sep: string): Uint8Array {
  const body = new TextEncoder().encode(sep === "\n" ? text : text.replace(/\n/g, sep));
  if (!bom) return body;
  const out = new Uint8Array(BOM.length + body.length);
  out.set(BOM, 0);
  out.set(body, BOM.length);
  return out;
}
