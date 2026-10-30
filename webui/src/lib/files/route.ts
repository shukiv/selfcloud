// SPDX-License-Identifier: AGPL-3.0-or-later

// The file browser's addresses:
//   /files                     My files (the caller's personal folder)
//   /files/{fld}               a folder's root
//   /files/{fld}/{nod}         a directory in that folder
//   /n/{nod}                   a node: its directory with it selected, or the preview of a file
// Each may carry ?select={nod} (highlight it) or ?preview={nod} (open the preview).

export type FilesRoute =
  | { kind: 'my' }
  | { kind: 'folder'; folder: string }
  | { kind: 'dir'; folder: string; dir: string }
  | { kind: 'node'; node: string }
  | null;

// validID accepts an ID with its type prefix and a lower-case suffix, as the server makes them.
export function validID(prefix: 'fld' | 'nod', s: string | null | undefined): s is string {
  return typeof s === 'string' && new RegExp(`^${prefix}_[a-z0-9]{8,64}$`).test(s);
}

// parseFilesPath reads a path (without its query). A path that is not one of the forms above is null.
export function parseFilesPath(path: string): FilesRoute {
  const parts = path.split('/').filter((p, i) => !(i === 0 && p === ''));
  if (parts.length === 0) return null;
  if (parts[0] === 'files') {
    if (parts.length === 1) return { kind: 'my' };
    if (parts.length === 2 && validID('fld', parts[1])) return { kind: 'folder', folder: parts[1] };
    if (parts.length === 3 && validID('fld', parts[1]) && validID('nod', parts[2])) {
      return { kind: 'dir', folder: parts[1], dir: parts[2] };
    }
    return null;
  }
  if (parts[0] === 'n' && parts.length === 2 && validID('nod', parts[1])) {
    return { kind: 'node', node: parts[1] };
  }
  return null;
}

// filesURL builds the address of a folder root or a directory.
export function filesURL(folder: string, dir: string | null, query: Record<string, string> = {}): string {
  const base = dir ? `/files/${folder}/${dir}` : `/files/${folder}`;
  const qs = new URLSearchParams(query).toString();
  return qs ? `${base}?${qs}` : base;
}
