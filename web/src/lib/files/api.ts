// SPDX-License-Identifier: AGPL-3.0-or-later

// The file routes the browser calls. Each returns what the server answers, or throws an ApiError.
import { api, ApiError } from '../api';
import { get, writable } from '../store';
import { session } from '../session';
import type { Page, FolderAccess, Job, Node, SignedLink, TrashItem, Version } from './types';
import type { SortDir, SortKey } from './sort';

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

// childrenPage reads one page of a directory's children. A null dir reads the folder's root. With sort, the server
// orders the whole directory (folders, then shortcuts, then files, each by the sort), and the cursor is for that sort;
// without it the server keeps its default order, as API clients always had.
// signal (fu8) ends the read when the caller no longer wants it, such as a page that has left the folder.
export async function childrenPage(
  folder: string,
  dir: string | null,
  cursor?: string,
  sort?: { key: SortKey; dir: SortDir },
  signal?: AbortSignal,
): Promise<Page<Node>> {
  needSession();
  const c = cursor ? `&cursor=${encodeURIComponent(cursor)}` : '';
  const s = sort ? `&sort=${sort.key}&order=${sort.dir}` : '';
  if (dir) return api<Page<Node>>('GET', `/api/v1/nodes/${dir}/children?limit=${PAGE}${s}${c}`, { signal });
  return api<Page<Node>>('GET', `/api/v1/folders/${folder}/children?path=%2F&limit=${PAGE}${s}${c}`, { signal });
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

// createZipJob asks for a zip of the nodes named. The answer is the job; its target is the zip.
export function createZipJob(nodes: string[]): Promise<Job> {
  return api<Job>('POST', '/api/v1/zips', { body: { nodes } });
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

// mkdirIn makes a folder named name in the directory parent (a node ID). The parent is named by its ID, so a rename or
// move made in another session a moment before cannot send the folder to an old path (decision 74).
export function mkdirIn(folder: string, parent: string, name: string): Promise<Node> {
  return api<Node>('POST', `/api/v1/folders/${folder}/directories`, { body: { parent, name } });
}

// createDocument makes a blank Office document of kind (docx, xlsx or pptx) in the folder's root or the directory nod, a
// node ID (decision 91). The server names it, and answers the new node.
export function createDocument(nod: string, kind: 'docx' | 'xlsx' | 'pptx' | 'odg' | 'txt'): Promise<Node> {
  return api<Node>('POST', `/api/v1/nodes/${nod}/documents`, { body: { type: kind } });
}

// contentPath is the same-origin download address. It carries no credential: the session cookie sends.
export function contentPath(nod: string, disposition: 'attachment' | 'inline' = 'attachment'): string {
  return `/api/v1/nodes/${nod}/content?disposition=${disposition}`;
}

// versionContentPath is the download address of a past version of a file, as an attachment.
export function versionContentPath(nod: string, ver: string): string {
  return `/api/v1/nodes/${nod}/versions/${ver}/content?disposition=attachment`;
}

// inlineRange reads part of a file's contents for the browser's viewers: the first bytes of a text preview, or a subtitle
// file. It is the content route with the range the caller asks for.
export function inlineRange(nod: string, range: string): Promise<Response> {
  return fetch(contentPath(nod, 'inline'), { credentials: 'same-origin', cache: 'no-store', headers: { Range: range } });
}

// refreshNode reads a file's record again after a session end that may have gone through. It does not check the session
// first: the drawer that asks may be closing, and a read that fails is ignored.
export function refreshNode(nod: string): Promise<Node> {
  return api<Node>('GET', `/api/v1/nodes/${nod}`);
}

// unlockNode ends the editing session the person holds on a file.
export async function unlockNode(nod: string, signal?: AbortSignal): Promise<void> {
  await api('POST', `/api/v1/nodes/${nod}/unlock`, { signal });
}

// OfficeAnswer is the server's answer to a request for a file's editor: the editor's address and the token to post to it.
export interface OfficeAnswer {
  url: string;
  token: string;
  expires_at: string;
}

// requestOfficeEditor asks for a Collabora editor of a file, in the mode given. The page carries the token to the editor
// in a browser tab, and the token is not kept.
export function requestOfficeEditor(nod: string, mode: string): Promise<OfficeAnswer> {
  return api<OfficeAnswer>('POST', `/api/v1/nodes/${nod}/office`, { body: { mode } });
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

// COMMIT_TIMEOUT_MS is the wait for a commit. The server assembles a large file's pieces when it commits, which can take
// minutes; the default minute would mark the upload failed while the server may still finish it.
export const COMMIT_TIMEOUT_MS = 10 * 60_000;

export function commitUpload(uploadPath: string, body: { name?: string; on_conflict?: string }): Promise<Node> {
  return api<Node>('POST', `${uploadPath}/commit`, { body, timeoutMs: COMMIT_TIMEOUT_MS });
}

export interface ServerLimits {
  max_chunk_size: number;
  max_file_size: number;
}

// installLimits is the installation's upload limits, as the boot read of /api/v1/info gave them. It is set once per page load
// (Boot), so the files page does not read /info again to size its uploads (perf-1 3.2).
export const installLimits = writable<ServerLimits | null>(null);

export async function serverLimits(): Promise<ServerLimits> {
  const known = get(installLimits);
  if (known !== null) return known;
  const info = await api<{ limits?: ServerLimits }>('GET', '/api/v1/info', { noRedirect: true });
  const limits = info.limits ?? { max_chunk_size: 8 * 1024 * 1024, max_file_size: 0 };
  installLimits.set(limits);
  return limits;
}

// A place in a video or audio file: the seconds the person stopped at and the file's length (0 when unknown).
export interface ResumePlace {
  position_seconds: number;
  duration_seconds: number;
  updated_at?: string;
}

// getResume reads the signed-in person's own place in a file. A file with none reads as 0 seconds.
export function getResume(nod: string): Promise<ResumePlace> {
  needSession();
  return api<ResumePlace>('GET', `/api/v1/nodes/${nod}/resume`);
}

// putResume stores the place. The server keeps the last one only.
export function putResume(nod: string, position: number, duration: number): Promise<void> {
  needSession();
  return api<void>('PUT', `/api/v1/nodes/${nod}/resume`, { body: { position_seconds: position, duration_seconds: duration } });
}

// SubtitleFile is a .vtt or .srt file beside a video, with the same name.
export interface SubtitleFile {
  id: string;
  name: string;
  format: 'vtt' | 'srt';
}

// listSubtitles names the subtitle files beside a file. Each is read by its own content route.
export function listSubtitles(nod: string): Promise<SubtitleFile[]> {
  needSession();
  return api<{ items: SubtitleFile[] }>('GET', `/api/v1/nodes/${nod}/subtitles`).then((r) => r.items ?? []);
}
