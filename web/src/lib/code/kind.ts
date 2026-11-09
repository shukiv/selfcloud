// SPDX-License-Identifier: AGPL-3.0-or-later

// The rule order that decides whether a file opens in the code viewer (design-ce 2.1, decision 115). The first rule that
// matches decides:
//
//   1. an office type: the office viewer and editor, as before; never the code viewer;
//   2. an image type: the image viewer, as before (SVG included: it opens as a picture);
//   3. a name in the code list, and the sniff says text: the code viewer, whatever the stored type says (a .ts file stored as
//      video/mp2t, a .rs file stored as application/rls-services+xml);
//   4. a text type (text/* or the TEXT set), and the sniff says text: the code viewer;
//   5. any other name stored as application/octet-stream, and the sniff says text: the viewer as plain text;
//   6. the sniff says binary (or UTF-16): the viewers as before (PDF, media) or the file's card.
import { baseType, IMAGE, OFFICE, previewable, TEXT } from "../files/kind";
import type { Node } from "../files/types";
import type { Sniff } from "./encoding";
import { packOf, type Pack } from "./names";

type Named = Pick<Node, "kind" | "name" | "mime_type">;

// The type the server stores when nothing says what a file holds.
const OCTET = "application/octet-stream";

// The packs of the text types, for a rule-4 file whose name is not in the list.
const TYPE_PACK: Readonly<Record<string, Pack>> = {
  "application/json": "json",
  "application/xml": "xml",
  "text/xml": "xml",
  "application/javascript": "javascript",
  "text/javascript": "javascript",
  "application/x-yaml": "yaml",
  "application/yaml": "yaml",
  "application/toml": "toml",
  "application/x-sh": "shell",
  "application/sql": "sql",
  "application/x-httpd-php": "php",
  "text/html": "html",
  "text/css": "css",
};

function textType(mt: string): boolean {
  return mt.startsWith("text/") || TEXT.has(mt);
}

// needsSniff says the file is one rules 3 to 5 may take, so its first bytes are read before it is shown. Rules 1 and 2 come
// first: an office file or a picture is never sniffed.
export function needsSniff(node: Named): boolean {
  if (node.kind !== "file") return false;
  const mt = baseType(node.mime_type);
  if (OFFICE.has(mt) || IMAGE.has(mt)) return false;
  return packOf(node.name) !== null || textType(mt) || mt === OCTET;
}

// Viewer is how a file shows in the code viewer: "code" (rules 3 and 4) or "plain" (rule 5), and the language pack.
export interface Viewer {
  kind: "code" | "plain";
  pack: Pack;
}

// viewerOf applies the rule order to a file and its sniff, and gives the viewer, or null when the file does not open there.
export function viewerOf(node: Named, sniffed: Sniff): Viewer | null {
  if (!needsSniff(node) || sniffed !== "text") return null;
  const pack = packOf(node.name);
  if (pack !== null) return { kind: "code", pack };
  const mt = baseType(node.mime_type);
  if (textType(mt)) return { kind: "code", pack: Object.hasOwn(TYPE_PACK, mt) ? TYPE_PACK[mt] : "text" };
  return { kind: "plain", pack: "text" };
}

// opensInViewer says a file opens in the viewer: one it shows already (a picture, a PDF, media, text), or one the sniff may
// find to be text. A file the sniff finds binary shows its card there.
export function opensInViewer(node: Named): boolean {
  return previewable(node) || needsSniff(node);
}
