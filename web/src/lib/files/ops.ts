// SPDX-License-Identifier: AGPL-3.0-or-later

// Operations the file browser composes from the API: names, upload targets, dropped files and downloads.
// The server checks everything again; these checks only spare a round trip and give the person an early
// answer.
import { contentPath } from './api';
import type { Here } from './location';
import { joinPath } from './location';
import type { UploadTarget } from './uploads';

const NAME_MAX_BYTES = 255;

// CONTROL matches control, format and line-separator characters, which a name may not hold.
const CONTROL = new RegExp('[\\u0000-\\u001f\\u007f-\\u009f\\u200b-\\u200f\\u2028-\\u202e\\u2060-\\u206f\\ufeff]');

export type NameProblem = 'empty' | 'slash' | 'dot' | 'long' | 'edge' | 'control';

// nameProblem reports why a name cannot be used, or null when it can. The server's rules are the same.
export function nameProblem(raw: string): NameProblem | null {
  const name = raw.normalize('NFC');
  if (name.trim() === '') return 'empty';
  if (name.startsWith(' ') || name.endsWith(' ')) return 'edge';
  if (name.includes('/') || name.includes('\\')) return 'slash';
  if (name === '.' || name === '..') return 'dot';
  if (new TextEncoder().encode(name).length > NAME_MAX_BYTES) return 'long';
  if (CONTROL.test(name)) return 'control';
  return null;
}

// nameMessageKey is the catalogue key of a name problem.
export function nameMessageKey(p: NameProblem): string {
  return `files.name.${p}`;
}

// uploadTargetFor is where a file goes when it is dropped or picked in the browser's current place. A
// file in a folder's subdirectory needs the place's path; without one, the upload cannot be made.
export function uploadTargetFor(here: Here, relDir: string): UploadTarget | null {
  if (relDir === '') {
    if (here.dir) return { folder: here.folder, parent: here.dir.id };
    return { folder: here.folder, path: '/' };
  }
  const base = here.path;
  if (base === null) return null;
  const path = relDir.split('/').reduce<string | null>((p, seg) => (p === null ? null : joinPath(p, seg)), base);
  if (path === null) return null;
  return { folder: here.folder, path, parents: true };
}

// DroppedFile is a file with the folders it sits in, relative to the place it was dropped on.
export interface DroppedFile {
  file: File;
  relDir: string;
}

// fromFileList reads a picked file list. A folder pick carries each file's relative path.
export function fromFileList(list: FileList | File[]): DroppedFile[] {
  return Array.from(list).map((file) => {
    const rel = (file as File & { webkitRelativePath?: string }).webkitRelativePath ?? '';
    const parts = rel.split('/');
    parts.pop();
    return { file, relDir: parts.join('/') };
  });
}

// fromDataTransfer reads files dropped on the browser, following dropped folders. Browsers without the
// entries API get the flat file list.
export async function fromDataTransfer(dt: DataTransfer): Promise<DroppedFile[]> {
  const entries: FileSystemEntry[] = [];
  for (const item of Array.from(dt.items ?? [])) {
    const entry = (item as DataTransferItem & { webkitGetAsEntry?: () => FileSystemEntry | null }).webkitGetAsEntry?.();
    if (entry) entries.push(entry);
  }
  if (entries.length === 0) return fromFileList(dt.files);
  const out: DroppedFile[] = [];
  for (const e of entries) await walk(e, '', out);
  return out;
}

async function walk(entry: FileSystemEntry, dir: string, out: DroppedFile[]): Promise<void> {
  if (entry.isFile) {
    const file = await new Promise<File>((resolve, reject) => (entry as FileSystemFileEntry).file(resolve, reject));
    out.push({ file, relDir: dir });
    return;
  }
  if (!entry.isDirectory) return;
  const reader = (entry as FileSystemDirectoryEntry).createReader();
  const name = dir === '' ? entry.name : `${dir}/${entry.name}`;
  // readEntries returns at most a batch at a time; read until it is empty.
  for (;;) {
    const batch = await new Promise<FileSystemEntry[]>((resolve, reject) => reader.readEntries(resolve, reject));
    if (batch.length === 0) break;
    for (const child of batch) await walk(child, name, out);
  }
}

// downloadNode starts a download of one file. It asks the server first: a download the no-download rule
// refuses answers 403, which a plain link could not show, so the request is made as a HEAD. A refusal
// resolves to false.
export async function startDownload(nod: string): Promise<boolean> {
  return startDownloadURL(contentPath(nod, 'attachment'));
}

// startDownloadURL is startDownload for any same-origin content address, such as an earlier version's.
export async function startDownloadURL(url: string): Promise<boolean> {
  const res = await fetch(url, { method: 'HEAD', credentials: 'same-origin', cache: 'no-store' });
  if (!res.ok) return false;
  const a = document.createElement('a');
  a.href = url;
  a.setAttribute('download', '');
  a.rel = 'noopener';
  document.body.appendChild(a);
  a.click();
  a.remove();
  return true;
}
