// SPDX-License-Identifier: AGPL-3.0-or-later

// A small client for the resumable upload protocol (tus 1.0.0) that the server speaks. It is written
// here, not taken from a library, because the one library the project considered ships a dependency
// with no licence field, which the licence check refuses (see docs/design.md, section 10).
import { ApiError } from '../apiError';
import { currentCsrf } from '../session';

export const TUS_VERSION = '1.0.0';
export const UPLOADS = '/api/v1/uploads';
// UPLOAD_PATH is the only shape of an upload's URL the client uses. A URL read from a stored record or a
// Location header that does not match it is refused, so a stored value cannot aim a request anywhere else.
export const UPLOAD_PATH = /^\/api\/v1\/uploads\/[A-Za-z0-9_-]+$/;

// UploadError is an answer the server refused with. state is Selfcloud-Upload-State when the server
// sent one (awaiting_commit), or null.
export class UploadError extends ApiError {
  readonly state: string | null;
  constructor(status: number, code: string, details: Record<string, unknown>, retryAfter: number, state: string | null) {
    super(status, code, details, retryAfter);
    this.name = 'UploadError';
    this.state = state;
  }
}

// encodeMetadata writes the Upload-Metadata header: comma-separated "key base64(value)" pairs. Values
// with no entry are left out.
export function encodeMetadata(meta: Record<string, string | undefined>): string {
  const parts: string[] = [];
  for (const [k, v] of Object.entries(meta)) {
    if (v === undefined || v === '') continue;
    const bytes = new TextEncoder().encode(v);
    let bin = '';
    for (const b of bytes) bin += String.fromCharCode(b);
    parts.push(`${k} ${btoa(bin)}`);
  }
  return parts.join(',');
}

// parseError reads the error envelope of a refused answer. A body that is not the envelope gives the
// code "internal".
export function parseError(
  status: number,
  body: string,
  retryAfterHeader: string | null,
  state: string | null,
): UploadError {
  let code = 'internal';
  let details: Record<string, unknown> = {};
  try {
    const env = JSON.parse(body) as { error?: { code?: unknown; details?: unknown } };
    if (typeof env.error?.code === 'string') code = env.error.code;
    if (typeof env.error?.details === 'object' && env.error.details !== null) {
      details = env.error.details as Record<string, unknown>;
    }
  } catch {
    // Not the envelope; keep the status-based default.
  }
  if (status === 460) code = code === 'internal' ? 'checksum_mismatch' : code;
  const retry = Number(retryAfterHeader ?? 0);
  return new UploadError(status, code, details, Number.isFinite(retry) ? retry : 0, state);
}

// locationPath turns a Location header into a same-origin upload path, whatever origin the server put in
// it. It answers null for anything that is not an upload's path.
export function locationPath(location: string, base: string = pageBase()): string | null {
  let pathname: string;
  try {
    pathname = new URL(location, base).pathname;
  } catch {
    return null;
  }
  return UPLOAD_PATH.test(pathname) ? pathname : null;
}

function pageBase(): string {
  return typeof window !== 'undefined' ? window.location.href : 'http://localhost/';
}

function csrfHeaders(): Record<string, string> {
  const csrf = currentCsrf();
  return csrf ? { 'X-CSRF-Token': csrf } : {};
}

// Bearer gives the credential of one request. A visitor who opened a link that anyone can open has no cookie:
// the page passes a getter, which is read again for every request, so a credential renewed during a long upload
// is the one sent. A getter that answers null sends no credential at all.
export type Bearer = () => string | null;

// requestAuth is what a request carries to authenticate. With a bearer it sends that and no cookie; without one
// it is the signed-in user's session, cookie and CSRF token, as before.
export function requestAuth(bearer?: Bearer): { credentials: RequestCredentials; headers: Record<string, string> } {
  if (!bearer) return { credentials: 'same-origin', headers: csrfHeaders() };
  const token = bearer();
  return { credentials: 'omit', headers: token ? { Authorization: `Bearer ${token}` } : {} };
}

// createUpload starts an upload of length bytes and returns its path. The server refuses a name that is
// taken under fail with conflict, which the caller turns into a question.
export async function createUpload(
  length: number,
  meta: Record<string, string | undefined>,
  bearer?: Bearer,
): Promise<string> {
  const auth = requestAuth(bearer);
  let res: Response;
  try {
    res = await fetch(UPLOADS, {
      method: 'POST',
      credentials: auth.credentials,
      cache: 'no-store',
      headers: {
        'Tus-Resumable': TUS_VERSION,
        'Upload-Length': String(length),
        'Upload-Metadata': encodeMetadata(meta),
        ...auth.headers,
      },
    });
  } catch {
    throw new ApiError(0, 'network', {}, 0);
  }
  if (!res.ok) throw await refused(res);
  const path = locationPath(res.headers.get('Location') ?? '');
  if (!path) throw new ApiError(res.status, 'internal', {}, 0);
  return path;
}

async function refused(res: Response): Promise<UploadError> {
  const body = await res.text().catch(() => '');
  return parseError(res.status, body, res.headers.get('Retry-After'), res.headers.get('Selfcloud-Upload-State'));
}

// UploadHead is what HEAD says: the durable offset, and awaiting_commit when every byte is in and the upload
// waits for a name.
export interface UploadHead {
  offset: number;
  state: string | null;
}

// headOffset asks how far an upload got, for resuming.
export async function headOffset(path: string, bearer?: Bearer): Promise<UploadHead> {
  const auth = requestAuth(bearer);
  let res: Response;
  try {
    res = await fetch(path, {
      method: 'HEAD',
      credentials: auth.credentials,
      cache: 'no-store',
      headers: { 'Tus-Resumable': TUS_VERSION, ...auth.headers },
    });
  } catch {
    throw new ApiError(0, 'network', {}, 0);
  }
  if (!res.ok) throw parseError(res.status, '', null, null);
  const off = Number(res.headers.get('Upload-Offset'));
  if (!Number.isFinite(off) || off < 0) throw new ApiError(res.status, 'internal', {}, 0);
  return { offset: off, state: res.headers.get('Selfcloud-Upload-State') };
}

// ChunkResult is what a piece's answer carries: the new durable offset, and the file's ID when the
// piece completed the upload.
export interface ChunkResult {
  offset: number;
  nodeID: string | null;
}

// sendChunk sends one piece at offset. XMLHttpRequest is used because fetch reports no upload progress.
// track receives the request, so a pause or a cancel can abort it. checksum is the piece's base64 SHA-256,
// sent as Upload-Checksum; the server answers 460 when it does not match the bytes it got.
export function sendChunk(
  path: string,
  offset: number,
  chunk: Blob,
  onProgress: (sent: number) => void,
  track: (xhr: XMLHttpRequest) => void,
  checksum: string | null = null,
  bearer?: Bearer,
): Promise<ChunkResult> {
  return new Promise<ChunkResult>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    track(xhr);
    const auth = requestAuth(bearer);
    xhr.open('PATCH', path, true);
    xhr.withCredentials = auth.credentials === 'same-origin';
    xhr.setRequestHeader('Tus-Resumable', TUS_VERSION);
    xhr.setRequestHeader('Upload-Offset', String(offset));
    xhr.setRequestHeader('Content-Type', 'application/offset+octet-stream');
    if (checksum) xhr.setRequestHeader('Upload-Checksum', `sha256 ${checksum}`);
    for (const [k, v] of Object.entries(auth.headers)) xhr.setRequestHeader(k, v);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(e.loaded);
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        const next = Number(xhr.getResponseHeader('Upload-Offset'));
        resolve({
          offset: Number.isFinite(next) ? next : offset + chunk.size,
          nodeID: xhr.getResponseHeader('Selfcloud-Node-ID'),
        });
        return;
      }
      reject(
        parseError(
          xhr.status,
          xhr.responseText,
          xhr.getResponseHeader('Retry-After'),
          xhr.getResponseHeader('Selfcloud-Upload-State'),
        ),
      );
    };
    xhr.onerror = () => reject(new ApiError(0, 'network', {}, 0));
    xhr.onabort = () => reject(new AbortedError());
    xhr.send(chunk);
  });
}

// AbortedError is a piece stopped on purpose (a pause or a cancel). It is not a failure.
export class AbortedError extends Error {
  constructor() {
    super('aborted');
    this.name = 'AbortedError';
  }
}

// deleteUpload drops an upload on the server, which frees its reservation.
export async function deleteUpload(path: string, bearer?: Bearer): Promise<void> {
  const auth = requestAuth(bearer);
  const res = await fetch(path, {
    method: 'DELETE',
    credentials: auth.credentials,
    cache: 'no-store',
    headers: { 'Tus-Resumable': TUS_VERSION, ...auth.headers },
  });
  if (!res.ok && res.status !== 404) throw await refused(res);
}
