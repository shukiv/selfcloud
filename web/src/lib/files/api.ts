// SPDX-License-Identifier: AGPL-3.0-or-later

// The file routes the browser calls. Each returns what the server answers, or throws an ApiError.
import { api, ApiError } from '../api';
import { get } from '../store';
import { session } from '../session';
import type { Page, FolderAccess, Job, Node, SignedLink, TrashItem, Version } from './types';

// Every list is read in pages of this size.
export const PAGE = 50;

// listAll reads every page of a cursor-paged route. A cap stops a runaway list.
async function listAll<T>(path: string, maxPages = 40): Promise<T[]> {
  const out: T[] = [];
  let cursor: string | undefined;
  for (let i = 0; i < maxPages; i++) {
    const sep = path.includes('?') ? '&' : '?';
    const q = `${path}${sep}limit=500${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`;
    const page = await api<Page<T>>('GET', q);
    out.push(...(page.items ?? []));
    if (!page.next_cursor) break;
    cursor = page.next_cursor;
  }
  return out;
}

// myFolders reads the team spaces the caller reaches, and its personal folder. Hidden spaces are left out,
// unless hidden asks for only those.
// needSession refuses a read that would go out with no session: one still running when the person signed out.
// The server would answer 401 to it, and nothing is wanted from it.
function needSession(): void {
  if (get(session) === null) throw new ApiError(0, 'signed_out', {}, 0);
}

export async function myFolders(hidden = false): Promise<FolderAccess[]> {
  needSession();
  return listAll<FolderAccess>(hidden ? '/api/v1/folders?hidden=true' : '/api/v1/folders');
}

export function getMe(): Promise<{ personal_folder_id: string | null }> {
  return api<{ personal_folder_id: string | null }>('GET', '/api/v1/me');
}

// childrenPage reads one page of a directory's children. A null dir reads the folder's root.
export async function childrenPage(folder: string, dir: string | null, cursor?: string): Promise<Page<Node>> {
  needSession();
  const c = cursor ? `&cursor=${encodeURIComponent(cursor)}` : '';
  if (dir) return api<Page<Node>>('GET', `/api/v1/nodes/${dir}/children?limit=${PAGE}${c}`);
  return api<Page<Node>>('GET', `/api/v1/folders/${folder}/children?path=%2F&limit=${PAGE}${c}`);
}

export async function statNode(nod: string): Promise<Node> {
  needSession();
  return api<Node>('GET', `/api/v1/nodes/${nod}`);
}

// statOrNull reads a node, or null when the caller cannot reach it (not_found). Other errors throw.
export async function statOrNull(nod: string): Promise<Node | null> {
  try {
    return await statNode(nod);
  } catch (err) {
    if (err instanceof ApiError && err.code === 'not_found') return null;
    throw err;
  }
}

export function renameNode(nod: string, name: string, ifMatch?: string): Promise<Node> {
  return api<Node>('PATCH', `/api/v1/nodes/${nod}`, {
    body: { name },
    headers: ifMatch ? { 'If-Match': ifMatch } : undefined,
  });
}

// Transfer is the target of a move or copy: a folder, and a directory in it by ID or by path.
export interface Transfer {
  folder?: string;
  parent?: string;
  path?: string;
  name?: string;
  on_conflict?: 'fail' | 'rename' | 'replace';
}

export function moveNode(nod: string, body: Transfer, ifMatch?: string): Promise<Node> {
  return api<Node>('POST', `/api/v1/nodes/${nod}/move`, {
    body,
    headers: ifMatch ? { 'If-Match': ifMatch } : undefined,
  });
}

// copyNode answers the new node (201) or, for a large folder, the job that makes it (202).
export function copyNode(nod: string, body: Transfer): Promise<Node | Job> {
  return api<Node | Job>('POST', `/api/v1/nodes/${nod}/copy`, { body });
}

export function getJob(job: string): Promise<Job> {
  return api<Job>('GET', `/api/v1/jobs/${job}`);
}

export function deleteNode(nod: string): Promise<void> {
  return api<void>('DELETE', `/api/v1/nodes/${nod}`);
}

export function restoreNode(nod: string): Promise<Node> {
  return api<Node>('POST', `/api/v1/nodes/${nod}/restore`);
}

// createShortcut makes a shortcut to target in the directory dir. The name is the one the directory's readers will see.
export function createShortcut(dir: string, target: string, name: string): Promise<Node> {
  return api<Node>('POST', `/api/v1/nodes/${dir}/shortcuts`, { body: { target, name } });
}

// updateAppearance sets a node's colour and description. A null colour clears it; so does an empty description.
export function updateAppearance(nod: string, body: { color?: string | null; description?: string | null }): Promise<Node> {
  return api<Node>('PATCH', `/api/v1/nodes/${nod}`, { body });
}

export function mkdir(folder: string, path: string): Promise<Node> {
  return api<Node>('POST', `/api/v1/folders/${folder}/directories`, { body: { path, parents: false } });
}

// contentPath is the same-origin download address. It carries no credential: the session cookie sends.
export function contentPath(nod: string, disposition: 'attachment' | 'inline' = 'attachment'): string {
  return `/api/v1/nodes/${nod}/content?disposition=${disposition}`;
}

// signedLink asks for a short-lived link to a file's contents. The URL is for the caller to use at once;
// it is never stored or shown.
export function signedLink(nod: string, disposition: 'attachment' | 'inline', ttl: number): Promise<SignedLink> {
  return api<SignedLink>('POST', `/api/v1/nodes/${nod}/links`, { body: { disposition, ttl } });
}

export function listTrash(folder: string | null, cursor?: string): Promise<Page<TrashItem>> {
  const params = new URLSearchParams({ limit: String(PAGE) });
  if (folder) params.set('folder', folder);
  if (cursor) params.set('cursor', cursor);
  return api<Page<TrashItem>>('GET', `/api/v1/trash?${params.toString()}`);
}

export function purgeTrashed(nod: string): Promise<void> {
  return api<void>('DELETE', `/api/v1/trash/${nod}`);
}

// emptyTrashStep purges up to 1000 items and says whether more remain.
export function emptyTrashStep(folder: string | null): Promise<{ purged: number; more: boolean }> {
  return api<{ purged: number; more: boolean }>('POST', '/api/v1/trash/empty', {
    body: folder ? { folder } : {},
  });
}

export function listVersions(nod: string): Promise<Page<Version>> {
  return api<Page<Version>>('GET', `/api/v1/nodes/${nod}/versions`);
}

export function updateVersion(nod: string, ver: string, body: { label?: string | null; keep_forever?: boolean }): Promise<Version> {
  return api<Version>('PATCH', `/api/v1/nodes/${nod}/versions/${ver}`, { body });
}

export function restoreVersion(nod: string, ver: string, ifMatch: string): Promise<Node> {
  return api<Node>('POST', `/api/v1/nodes/${nod}/versions/${ver}/restore`, { headers: { 'If-Match': ifMatch } });
}

export function deleteVersion(nod: string, ver: string): Promise<void> {
  return api<void>('DELETE', `/api/v1/nodes/${nod}/versions/${ver}`);
}

export function commitUpload(uploadPath: string, body: { name?: string; on_conflict?: string }): Promise<Node> {
  return api<Node>('POST', `${uploadPath}/commit`, { body });
}

export function serverLimits(): Promise<{ max_chunk_size: number; max_file_size: number }> {
  return api<{ limits?: { max_chunk_size: number; max_file_size: number } }>('GET', '/api/v1/info', {
    noRedirect: true,
  }).then((info) => info.limits ?? { max_chunk_size: 8 * 1024 * 1024, max_file_size: 0 });
}
