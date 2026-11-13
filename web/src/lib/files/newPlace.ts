// SPDX-License-Identifier: AGPL-3.0-or-later

// The sidebar's New button (decision 66): where a new folder or an upload goes, and which entries the place takes. The files
// page writes the place it shows (newPlace); the sidebar reads it there. Off the files pages the New actions go to the
// person's own My files root, and never to a folder the page did not show.
import { userWritable } from '../store';
import type { Here } from './location';
import { parseFilesPath } from './route';
import { atLeast } from './roles';
import { uploadTargetWhere, type DroppedFile, type UploadWhere } from './ops';
import { manager } from './uploads';

// NewPlace is the place the New actions act on, and what the place takes. readOnly is a place the person cannot add to at
// all: its entries are shown disabled, with the reason, and nothing is sent elsewhere.
export interface NewPlace {
  where: UploadWhere;
  canFolder: boolean;
  canUpload: boolean;
  canUploadFolder: boolean;
  readOnly: boolean;
  // canEdit says the person edits where the file goes (an editor or a manager there): only then the New menu offers
  // documents (decision 91). It is the role on the directory shown, or on the folder at its root.
  canEdit: boolean;
  // nodeID is the directory a new document goes in: the directory shown, or the folder's root node (decision 91). Null when
  // the place has none, such as My files off the files pages.
  nodeID?: string | null;
}

// DocKind is a kind of blank Office document the New menu makes (decision 91).
export type DocKind = 'docx' | 'xlsx' | 'pptx';
export const DOC_KINDS: DocKind[] = ['docx', 'xlsx', 'pptx'];

// BlankKind is a blank file the New menu can make: an Office kind, Drawing (odg), or Text file (txt).
export type BlankKind = DocKind | 'odg' | 'txt';

// documentKinds is the document kinds the New menu offers in a place: only while office editing is on (officeOn), and only in a
// place that takes new files (decision 91). A read-only place and a place with no node to hold the file offer none, and so does
// an installation admin signed in as a user (impersonating): the route refuses it there.
export function documentKinds(p: NewPlace | null, officeOn: boolean, impersonating = false): BlankKind[] {
  if (impersonating || p === null || p.readOnly || !p.canUpload || !p.canEdit || !p.nodeID) return [];
  // The empty text file is made with office editing off too (the server makes txt either way); the Office kinds and Drawing need it.
  return officeOn ? [...DOC_KINDS, 'odg', 'txt'] : ['txt'];
}

// placeForRoute is the place the New actions may use on the page at pathname, or null. FilesPage writes newPlace in an effect
// after it renders, so the stored place can still be the one from the folder or directory before (the race behind decision
// 91's). A place is used only when it is the one the page at pathname shows:
//   /files/{fld}         the folder's root: the folder and no directory;
//   /files/{fld}/{nod}   that directory of the folder;
//   /n/{nod}             the place whose directory (or folder root node) is that node, so the entries show only where the node is
//                        the place's own directory;
//   /files               null: the page redirects to the personal folder's address and shows no place of its own.
// Anything else, and a null place, gives null: the entries are hidden and nothing is sent.
export function placeForRoute(p: NewPlace | null, pathname: string): NewPlace | null {
  if (p === null) return null;
  const r = parseFilesPath(pathname);
  if (r === null) return null;
  switch (r.kind) {
    case 'folder':
      return p.where.folder === r.folder && p.where.dirID === null ? p : null;
    case 'dir':
      return p.where.folder === r.folder && p.where.dirID === r.dir ? p : null;
    case 'node':
      return p.nodeID === r.node ? p : null;
    default:
      return null;
  }
}

// newPlace is the place the files page shows, written by FilesPage and cleared when it is left. null until the place is read.
export const newPlace = userWritable<NewPlace | null>(null);

// newPlaceFailure is the text of the error when the page could not read its place (wf1, review item 2), or null. The sidebar's
// disabled New gives it as the reason in place of "Loading the folder", so a failed read does not look like a read still going.
export const newPlaceFailure = userWritable<string | null>(null);

// myFilesPlace is the person's own My files root: everything can be added there.
export function myFilesPlace(personalID: string): NewPlace {
  return { where: { folder: personalID, dirID: null, path: '/' }, canFolder: true, canUpload: true, canUploadFolder: true, readOnly: false, canEdit: true, nodeID: null };
}

// placeOfHere is the place the files page shows, with what the caller may do in it (the page's own rules).
export function placeOfHere(here: Here, canUpload: boolean, canUploadFolder: boolean): NewPlace {
  return {
    where: { folder: here.folder, dirID: here.dir?.id ?? null, path: here.path },
    canFolder: canUploadFolder,
    canUpload,
    canUploadFolder,
    readOnly: !canUpload,
    canEdit: atLeast(here.dir ? here.dirRole : here.folderRole, 'editor'),
    nodeID: here.dir?.id ?? here.rootNodeID ?? null,
  };
}

// newPlaceFor says where the New actions act now: on a files page, the folder shown (null while it is not read); on any other
// page, the person's own My files root (null for a person with no My files, such as an installation admin).
export function newPlaceFor(onFiles: boolean, shown: NewPlace | null, personalID: string | null): NewPlace | null {
  if (onFiles) return shown;
  return personalID ? myFilesPlace(personalID) : null;
}

export type NewKey = 'newFolder' | 'upload' | 'uploadFolder';

export interface NewEntry {
  key: NewKey;
  visible: boolean;
  enabled: boolean;
}

// newEntries is the New menu for a place, by the toolbar's rules: an entry the place cannot take is hidden. A read-only place
// shows all three disabled, so the reason shows where they are.
export function newEntries(p: NewPlace): NewEntry[] {
  return [
    { key: 'newFolder', visible: p.canFolder || p.readOnly, enabled: p.canFolder && !p.readOnly },
    { key: 'upload', visible: p.canUpload || p.readOnly, enabled: p.canUpload && !p.readOnly },
    { key: 'uploadFolder', visible: p.canUploadFolder || p.readOnly, enabled: p.canUploadFolder && !p.readOnly },
  ];
}

// queueInto starts the uploads of picked or dropped files into a place. It returns how many could not be placed (a folder's
// subdirectory with no path to it).
export function queueInto(where: UploadWhere, files: DroppedFile[]): number {
  const batch: Array<{ file: File; target: NonNullable<ReturnType<typeof uploadTargetWhere>> }> = [];
  let skipped = 0;
  for (const d of files) {
    const target = uploadTargetWhere(where, d.relDir);
    if (target) batch.push({ file: d.file, target });
    else skipped++;
  }
  if (batch.length > 0) manager.add(batch);
  return skipped;
}
