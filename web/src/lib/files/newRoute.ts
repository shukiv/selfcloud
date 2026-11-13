// SPDX-License-Identifier: AGPL-3.0-or-later

import { validID } from "./route";

// The gallery's address (decision 108, Gallery rules): /new/{fld} for a folder's root and /new/{fld}/{nod} for a directory,
// with the kind of document the gallery offers. The parser is added beside this function by the gallery's builder.

// NewGalleryKind is the kind of document a gallery offers: a document, a spreadsheet or a presentation.
export type NewGalleryKind = "document" | "spreadsheet" | "presentation";

// newURL is the gallery address for the place: a folder's root when nod is null, else that directory.
export function newURL(fld: string, nod: string | null, kind: NewGalleryKind): string {
  const dir = nod === null ? "" : `/${encodeURIComponent(nod)}`;
  return `/new/${encodeURIComponent(fld)}${dir}?kind=${kind}`;
}

// NewRoute is the gallery's place read from its address.
export interface NewRoute {
  folder: string;
  dir: string | null;
  kind: NewGalleryKind;
}

const KINDS: readonly string[] = ["document", "spreadsheet", "presentation"];

// parseNewPath reads the gallery's path and its query (search, with its "?"). A missing or unknown kind, an invalid ID, or any
// other path is null: the gallery's page then shows the not-found page.
export function parseNewPath(path: string, search: string): NewRoute | null {
  const parts = path.split("/").filter((p, i) => !(i === 0 && p === ""));
  if (parts[0] !== "new") return null;
  const kind = new URLSearchParams(search).get("kind");
  if (kind === null || !KINDS.includes(kind)) return null;
  if (parts.length === 2 && validID("fld", parts[1])) return { folder: parts[1], dir: null, kind: kind as NewGalleryKind };
  if (parts.length === 3 && validID("fld", parts[1]) && validID("nod", parts[2])) {
    return { folder: parts[1], dir: parts[2], kind: kind as NewGalleryKind };
  }
  return null;
}
