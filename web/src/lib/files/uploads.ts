// SPDX-License-Identifier: AGPL-3.0-or-later

import { writable } from 'svelte/store';
import { ApiError } from '../apiError';
import { session } from '../session';
import { commitUpload } from './api';
import { headFingerprint, sha256Base64 } from './digest';
import { Limiter } from './limiter';
import { validID } from './route';
import { conflictReducer, INITIAL_CONFLICT, type ConflictChoice, type ConflictState } from './conflict';
import {
  AbortedError,
  createUpload,
  deleteUpload,
  headOffset,
  sendChunk,
  UPLOAD_PATH,
  UploadError,
  type ChunkResult,
} from './tus';

// The upload manager: the queue, the pieces, the pauses and the conflicts. It holds at most three uploads
// at once, queues the rest, and keeps the list the tray shows in a store. The list belongs to the signed-in
// user: it follows the session, and a sign-out empties it.

export const MAX_PARALLEL = 3;
const STORE_KEY = 'selfcloud.uploads';
const MAX_TRANSIENT_RETRIES = 6;
// AWAITING is the Selfcloud-Upload-State of an upload whose bytes are all in and that waits for a name.
const AWAITING = 'awaiting_commit';
const NAME_MAX = 255;
const MIME_MAX = 255;
const PATH_MAX = 4096;
const CONTROL = /[\u0000-\u001f\u007f]/;
const USER = /^[A-Za-z0-9_-]{1,64}$/;
// A record always has its fingerprint: an empty one would match nothing and is refused.
const FINGERPRINT = /^[0-9a-f]{64}$/;
const MODES = new Set(['fail', 'rename', 'replace']);

export type UploadStatus =
  | 'queued'
  | 'uploading'
  | 'paused'
  | 'waiting' // too many uploads, or rate limited: retried later
  | 'conflict' // the name is taken; the batch question is open
  | 'awaiting_name' // all bytes arrived and a name is taken: ask for another
  | 'interrupted' // from an earlier page: pick the file again to resume
  | 'done'
  | 'skipped'
  | 'failed'
  | 'cancelled';

// Target is where the file goes: a folder, and a directory in it by ID or by path.
export interface UploadTarget {
  folder: string;
  parent?: string;
  path?: string;
  parents?: boolean;
}

export interface UploadItem {
  id: number;
  name: string;
  size: number;
  mime: string;
  lastModified: number;
  target: UploadTarget;
  status: UploadStatus;
  offset: number;
  speed: number; // bytes a second, a moving average
  onConflict: 'fail' | 'rename' | 'replace';
  url: string | null;
  nodeID: string | null;
  error: ApiError | null;
  // The name a file would take on the answer to awaiting_name, typed by the person.
  suggested: string;
  // head is the SHA-256 of the file's first MiB when the upload started. It is always set on a stored upload.
  head: string;
}

interface Runtime {
  file: File | null;
  xhr: XMLHttpRequest | null;
  stop: boolean;
  transient: number;
  // resent is set once a piece was sent again after a checksum mismatch.
  resent: boolean;
  lastAt: number;
  lastOffset: number;
  lastSpeed: number;
}

// PendingRecord is what is kept for an upload, so a later page can resume it. user is the signed-in user
// who started it; records of other users are not shown.
export interface PendingRecord {
  user: string;
  name: string;
  size: number;
  lastModified: number;
  mime: string;
  target: UploadTarget;
  url: string;
  onConflict: 'fail' | 'rename' | 'replace';
  head: string;
}

// Items are what the tray shows. Each change publishes a new array, so the tray re-renders.
export const uploads = writable<UploadItem[]>([]);

// conflictAsk is the name the tray must ask about (the name is taken), or null.
export const conflictAsk = writable<string | null>(null);

function freshRuntime(file: File): Runtime {
  return { file, xhr: null, stop: false, transient: 0, resent: false, lastAt: 0, lastOffset: 0, lastSpeed: 0 };
}

// validRecord returns the record when every field has the shape the client writes, and null otherwise. A
// stored value is read back from the browser, so it is checked like any other input: the URL must be an
// upload's path, and IDs, names, sizes and the target must be what a record holds.
function validRecord(v: unknown): PendingRecord | null {
  if (typeof v !== 'object' || v === null) return null;
  const o = v as Record<string, unknown>;
  if (typeof o.target !== 'object' || o.target === null) return null;
  const tg = o.target as Record<string, unknown>;

  const folder = typeof tg.folder === 'string' && validID('fld', tg.folder) ? tg.folder : null;
  const parent =
    tg.parent === undefined ? undefined : typeof tg.parent === 'string' && validID('nod', tg.parent) ? tg.parent : null;
  const path =
    tg.path === undefined
      ? undefined
      : typeof tg.path === 'string' && tg.path.startsWith('/') && tg.path.length <= PATH_MAX && !CONTROL.test(tg.path)
        ? tg.path
        : null;
  const parents = tg.parents === undefined ? undefined : typeof tg.parents === 'boolean' ? tg.parents : null;
  if (folder === null || parent === null || path === null || parents === null) return null;

  const user = typeof o.user === 'string' && USER.test(o.user) ? o.user : null;
  const name = typeof o.name === 'string' && o.name.length > 0 && o.name.length <= NAME_MAX && !CONTROL.test(o.name) ? o.name : null;
  const size = typeof o.size === 'number' && Number.isSafeInteger(o.size) && o.size >= 0 ? o.size : null;
  const lastModified = typeof o.lastModified === 'number' && Number.isSafeInteger(o.lastModified) ? o.lastModified : null;
  const mime = typeof o.mime === 'string' && o.mime.length <= MIME_MAX ? o.mime : null;
  const url = typeof o.url === 'string' && UPLOAD_PATH.test(o.url) ? o.url : null;
  const head = typeof o.head === 'string' && FINGERPRINT.test(o.head) ? o.head : null;
  const onConflict =
    typeof o.onConflict === 'string' && MODES.has(o.onConflict) ? (o.onConflict as PendingRecord['onConflict']) : null;
  if (user === null || name === null || size === null || lastModified === null || mime === null) return null;
  if (url === null || head === null || onConflict === null) return null;

  const target: UploadTarget = { folder: folder as string };
  if (parent !== undefined) target.parent = parent;
  if (path !== undefined) target.path = path;
  if (parents !== undefined) target.parents = parents;
  return { user, name, size, lastModified, mime, target, url, onConflict, head };
}

// readAll returns every stored record that passes validRecord. Anything else is dropped.
function readAll(): PendingRecord[] {
  try {
    const raw = window.localStorage.getItem(STORE_KEY);
    if (!raw) return [];
    const list = JSON.parse(raw) as unknown;
    if (!Array.isArray(list)) return [];
    const out: PendingRecord[] = [];
    for (const v of list) {
      const r = validRecord(v);
      if (r) out.push(r);
    }
    return out;
  } catch {
    return [];
  }
}

// readRecords returns the records of one user. No user, no records.
export function readRecords(userID: string | null): PendingRecord[] {
  if (userID === null) return [];
  return readAll().filter((r) => r.user === userID);
}

function writeAll(list: PendingRecord[]): void {
  try {
    if (list.length === 0) window.localStorage.removeItem(STORE_KEY);
    else window.localStorage.setItem(STORE_KEY, JSON.stringify(list));
  } catch {
    // Not kept: this page can still finish the uploads it has.
  }
}

function sameUpload(r: PendingRecord, it: UploadItem, user: string): boolean {
  return r.user === user && r.name === it.name && r.size === it.size && r.lastModified === it.lastModified;
}

// UploadManager runs the uploads. One instance serves the page.
export class UploadManager {
  private items: UploadItem[] = [];
  private runtime = new Map<number, Runtime>();
  private limiter = new Limiter(MAX_PARALLEL);
  private nextID = 1;
  private conflict: ConflictState = INITIAL_CONFLICT;
  private chunkSize = 8 * 1024 * 1024;
  private user: string | null = null;
  private guard = (e: BeforeUnloadEvent) => {
    e.preventDefault();
    e.returnValue = '';
  };
  private guarded = false;

  constructor(private now: () => number = () => Date.now()) {}

  setChunkSize(bytes: number): void {
    if (Number.isFinite(bytes) && bytes >= 1024) this.chunkSize = Math.floor(bytes);
  }

  // bind follows the signed-in user. A change of user, or a sign-out (null), stops what runs and empties the
  // list. The user's earlier uploads show as interrupted. A sign-out also removes that user's records from
  // this browser, so the next person here finds none.
  bind(userID: string | null): void {
    const previous = this.user;
    if (userID === previous) return;
    for (const rt of this.runtime.values()) {
      rt.stop = true;
      rt.xhr?.abort();
    }
    this.runtime.clear();
    this.items = [];
    this.conflict = INITIAL_CONFLICT;
    this.user = userID;
    if (previous !== null && userID === null) {
      writeAll(readAll().filter((r) => r.user !== previous));
    }
    for (const r of readRecords(userID)) {
      this.items.push(
        this.item({
          name: r.name,
          size: r.size,
          mime: r.mime,
          lastModified: r.lastModified,
          target: r.target,
          status: 'interrupted',
          offset: 0,
          url: r.url,
          nodeID: null,
          error: null,
          onConflict: r.onConflict,
          head: r.head,
        }),
      );
    }
    this.publish();
  }

  // add queues files for upload. A file is queued at once; the limiter decides when it runs.
  add(files: Array<{ file: File; target: UploadTarget }>): void {
    for (const { file, target } of files) {
      const it = this.item({
        name: file.name,
        size: file.size,
        mime: file.type,
        lastModified: file.lastModified,
        target,
        status: 'queued',
        offset: 0,
        url: null,
        nodeID: null,
        error: null,
        onConflict: 'fail',
      });
      this.runtime.set(it.id, freshRuntime(file));
      this.items.push(it);
      this.schedule(it.id);
    }
    this.publish();
  }

  // attach gives an interrupted upload its file again. The name, size and time must match what was stored,
  // and so must the first MiB: when the bytes differ, the stored upload is dropped and the file starts a new
  // upload. It answers false when the file cannot be the one stored.
  async attach(id: number, file: File): Promise<boolean> {
    const it = this.find(id);
    if (!it || it.status !== 'interrupted') return false;
    if (file.name !== it.name || file.size !== it.size || file.lastModified !== it.lastModified) return false;
    const head = await headFingerprint(file);
    const again = this.find(id);
    if (!again || again.status !== 'interrupted') return false;
    // An empty fingerprint is never a match: the file is treated as a different one.
    if (again.head === '' || head !== again.head) {
      this.drop(id);
      this.add([{ file, target: again.target }]);
      return true;
    }
    this.runtime.set(id, freshRuntime(file));
    this.update(id, { status: 'queued' });
    this.schedule(id);
    return true;
  }

  pause(id: number): void {
    const rt = this.runtime.get(id);
    if (!rt) return;
    rt.stop = true;
    rt.xhr?.abort();
    this.update(id, { status: 'paused', speed: 0 });
  }

  resume(id: number): void {
    const it = this.find(id);
    if (!it || it.status !== 'paused') return;
    const rt = this.runtime.get(id);
    if (rt) rt.stop = false;
    this.update(id, { status: 'queued' });
    this.schedule(id);
  }

  // cancel stops the upload and drops it on the server, which frees its reservation.
  async cancel(id: number): Promise<void> {
    const it = this.find(id);
    if (!it) return;
    const rt = this.runtime.get(id);
    if (rt) {
      rt.stop = true;
      rt.xhr?.abort();
    }
    this.update(id, { status: 'cancelled', speed: 0 });
    this.forget(id);
    if (it.url) {
      try {
        await deleteUpload(it.url);
      } catch {
        // The server drops it at its expiry anyway.
      }
    }
  }

  // retry runs a failed upload again. A failed creation starts over; a failed upload resumes from its
  // durable offset.
  retry(id: number): void {
    const it = this.find(id);
    if (!it || it.status !== 'failed') return;
    if (!this.runtime.get(id)?.file) return;
    this.runtime.get(id)!.transient = 0;
    this.update(id, { status: 'queued', error: null });
    this.schedule(id);
  }

  // commitName gives an upload that waits for a name its new name and sends the commit. Only the commit's
  // answer with a node ID makes the upload done.
  async commitName(id: number, name: string): Promise<void> {
    const it = this.find(id);
    if (!it || it.status !== 'awaiting_name' || !it.url) return;
    try {
      const node = await commitUpload(it.url, { name, on_conflict: 'fail' });
      this.finish(id, node.id);
    } catch (err) {
      if (err instanceof ApiError && err.code === 'conflict') {
        this.update(id, { suggested: name, error: err });
        return;
      }
      this.update(id, { status: 'failed', error: err instanceof ApiError ? err : new ApiError(0, 'internal', {}, 0) });
    }
  }

  // answerConflict settles the open question. remember makes the choice hold for the rest of the batch.
  answerConflict(choice: ConflictChoice, remember: boolean): void {
    const asked = this.items.find((x) => x.status === 'conflict');
    if (!asked) return;
    const { state, effect } = conflictReducer(this.conflict, { type: 'answer', choice, forBatch: remember });
    this.conflict = state;
    this.applyConflict(asked.id, effect);
    this.publish();
    this.askNext();
  }

  // dismiss takes a finished, failed or cancelled item off the tray.
  dismiss(id: number): void {
    this.items = this.items.filter((x) => x.id !== id);
    this.runtime.delete(id);
    this.publish();
  }

  clearFinished(): void {
    for (const it of this.items) {
      if (['done', 'skipped', 'cancelled', 'failed'].includes(it.status)) this.runtime.delete(it.id);
    }
    this.items = this.items.filter((x) => !['done', 'skipped', 'cancelled', 'failed'].includes(x.status));
    this.publish();
  }

  // The question the tray should ask, if any: the first upload whose name is taken.
  pendingConflictName(): string | null {
    return this.conflict.asking;
  }

  get active(): number {
    return this.items.filter((x) => ['queued', 'uploading', 'waiting', 'conflict', 'awaiting_name'].includes(x.status))
      .length;
  }

  // ---- internals ----

  private item(partial: Partial<UploadItem> & Pick<UploadItem, 'name' | 'size' | 'mime' | 'lastModified' | 'target' | 'status'>): UploadItem {
    return {
      id: this.nextID++,
      offset: 0,
      speed: 0,
      onConflict: 'fail',
      url: null,
      nodeID: null,
      error: null,
      suggested: '',
      head: '',
      ...partial,
    } as UploadItem;
  }

  private find(id: number): UploadItem | undefined {
    return this.items.find((x) => x.id === id);
  }

  private update(id: number, patch: Partial<UploadItem>): void {
    this.items = this.items.map((x) => (x.id === id ? { ...x, ...patch } : x));
    this.publish();
  }

  private publish(): void {
    uploads.set(this.items);
    conflictAsk.set(this.conflict.asking);
    this.syncGuard();
  }

  // The page warns before it closes while uploads run.
  private syncGuard(): void {
    const want = this.active > 0;
    if (want && !this.guarded) {
      window.addEventListener('beforeunload', this.guard);
      this.guarded = true;
    } else if (!want && this.guarded) {
      window.removeEventListener('beforeunload', this.guard);
      this.guarded = false;
    }
  }

  // drop takes an upload off the list and the records, and asks the server to drop it.
  private drop(id: number): void {
    const it = this.find(id);
    this.forget(id);
    this.dismiss(id);
    if (it?.url) void deleteUpload(it.url).catch(() => undefined);
  }

  private forget(id: number): void {
    const it = this.find(id);
    if (!it || this.user === null) return;
    const user = this.user;
    writeAll(readAll().filter((r) => !sameUpload(r, it, user)));
  }

  private remember(it: UploadItem): void {
    if (this.user === null) return;
    const user = this.user;
    const list = readAll().filter((r) => !sameUpload(r, it, user));
    if (it.url) {
      list.push({
        user,
        name: it.name,
        size: it.size,
        lastModified: it.lastModified,
        mime: it.mime,
        target: it.target,
        url: it.url,
        onConflict: it.onConflict,
        head: it.head,
      });
    }
    writeAll(list);
  }

  private schedule(id: number): void {
    void this.limiter.run(() => this.drive(id));
  }

  private finish(id: number, nodeID: string): void {
    this.update(id, { status: 'done', offset: this.find(id)?.size ?? 0, nodeID, speed: 0, error: null });
    this.forget(id);
    this.runtime.delete(id);
  }

  // awaitName puts an upload whose bytes are all in into the state where the person names it. Its URL stays
  // in the records, so a later page can commit it.
  private awaitName(id: number, error: ApiError | null = null): void {
    const it = this.find(id);
    if (!it) return;
    this.update(id, { status: 'awaiting_name', suggested: it.suggested || it.name, offset: it.size, speed: 0, error });
  }

  private applyConflict(id: number, effect: { kind: string; choice?: ConflictChoice }): void {
    if (effect.kind === 'apply' && effect.choice) {
      if (effect.choice === 'skip') {
        this.update(id, { status: 'skipped' });
        this.runtime.delete(id);
        return;
      }
      this.update(id, { onConflict: effect.choice === 'rename' ? 'rename' : 'replace', status: 'queued' });
      this.schedule(id);
    }
  }

  // askNext opens the question for the next upload whose name is taken, if no batch choice settles it.
  private askNext(): void {
    const next = this.items.find((x) => x.status === 'conflict');
    if (!next) return;
    const { state, effect } = conflictReducer(this.conflict, { type: 'conflict', name: next.name });
    this.conflict = state;
    this.applyConflict(next.id, effect);
    // The question is published whether or not an item changed, so the tray can ask it.
    this.publish();
    if (effect.kind === 'apply') this.askNext();
  }

  private async drive(id: number): Promise<void> {
    const rt = this.runtime.get(id);
    const it = this.find(id);
    if (!rt || !it || rt.stop || !rt.file) return;
    const file = rt.file;
    try {
      if (!it.url) {
        this.update(id, { status: 'uploading' });
        const head = await headFingerprint(file);
        const url = await createUpload(file.size, {
          folder: it.target.folder,
          parent: it.target.parent,
          path: it.target.parent ? undefined : (it.target.path ?? '/'),
          parents: it.target.parents ? 'true' : undefined,
          name: it.name,
          on_conflict: it.onConflict,
          mime_type: it.mime || undefined,
        });
        this.update(id, { url, head });
        const cur = this.find(id);
        if (cur) this.remember(cur);
      } else {
        // A resumed upload asks how far it got before it sends more.
        this.update(id, { status: 'uploading' });
        if (!(await this.syncOffset(id, it.url, file.size))) return;
      }
      await this.pieces(id, file);
    } catch (err) {
      this.failed(id, err);
    }
  }

  // syncOffset asks the server how far an upload got. It answers false when the upload is waiting for a name
  // (or all its bytes are in), and the caller sends nothing more.
  private async syncOffset(id: number, url: string, size: number): Promise<boolean> {
    const head = await headOffset(url);
    if (head.state === AWAITING || head.offset >= size) {
      this.awaitName(id);
      return false;
    }
    this.update(id, { offset: head.offset });
    return true;
  }

  private async pieces(id: number, file: File): Promise<void> {
    const rt = this.runtime.get(id);
    if (!rt) return;
    for (;;) {
      if (rt.stop) return;
      const it = this.find(id);
      if (!it || !it.url) return;
      if (it.offset >= file.size) {
        // All bytes are in and no node came back: the upload waits for a name. It is never done without a node.
        this.awaitName(id);
        return;
      }
      const from = it.offset;
      const to = Math.min(file.size, from + this.chunkSize);
      const piece = file.slice(from, to);
      const checksum = await sha256Base64(piece);
      if (rt.stop) return;
      rt.lastAt = this.now();
      rt.lastOffset = from;
      let res: ChunkResult;
      try {
        res = await sendChunk(
          it.url,
          from,
          piece,
          (sent) => this.progress(id, from + sent),
          (xhr) => (rt.xhr = xhr),
          checksum,
        );
      } catch (err) {
        rt.xhr = null;
        if (err instanceof AbortedError || rt.stop) return;
        if (err instanceof UploadError && err.status === 409 && err.state === AWAITING) {
          this.awaitName(id, err);
          return;
        }
        if (err instanceof UploadError && err.status === 460 && !rt.resent) {
          // The piece did not match its checksum. Once, the offset is read again and the piece is sent again.
          rt.resent = true;
          if (!(await this.syncOffset(id, it.url, file.size))) return;
          continue;
        }
        if (this.transient(err) && rt.transient < MAX_TRANSIENT_RETRIES) {
          rt.transient++;
          await this.backoff(rt.transient);
          if (rt.stop) return;
          // Ask where the server got to, then continue from there.
          if (!(await this.syncOffset(id, it.url, file.size))) return;
          continue;
        }
        this.failed(id, err);
        return;
      }
      rt.xhr = null;
      rt.transient = 0;
      rt.resent = false;
      if (res.nodeID) {
        this.finish(id, res.nodeID);
        return;
      }
      this.update(id, { offset: res.offset, speed: this.speedFor(rt, res.offset) });
    }
  }

  private progress(id: number, sent: number): void {
    const it = this.find(id);
    if (!it) return;
    const rt = this.runtime.get(id);
    if (rt) this.update(id, { offset: sent, speed: this.speedFor(rt, sent) });
  }

  // speedFor is an exponential moving average of the bytes a second since the last piece started.
  private speedFor(rt: Runtime, at: number): number {
    const dt = (this.now() - rt.lastAt) / 1000;
    if (dt <= 0) return rt.lastSpeed;
    const rate = Math.max(0, (at - rt.lastOffset) / dt);
    rt.lastSpeed = rt.lastSpeed === 0 ? rate : 0.7 * rt.lastSpeed + 0.3 * rate;
    rt.lastAt = this.now();
    rt.lastOffset = at;
    return rt.lastSpeed;
  }

  private transient(err: unknown): boolean {
    if (!(err instanceof ApiError)) return false;
    return err.code === 'network' || err.status === 502 || err.status === 503 || err.status === 504;
  }

  private backoff(attempt: number): Promise<void> {
    const ms = Math.min(30_000, 500 * 2 ** attempt);
    return new Promise((resolve) => setTimeout(resolve, ms));
  }

  private failed(id: number, err: unknown): void {
    const apiErr = err instanceof ApiError ? err : new ApiError(0, 'internal', {}, 0);
    if (apiErr.code === 'conflict' && this.find(id)?.status !== 'awaiting_name' && !this.find(id)?.url) {
      // A name taken at creation: the batch question decides.
      this.update(id, { status: 'conflict', error: null });
      this.askNext();
      return;
    }
    if (apiErr.status === 429 && (apiErr.code === 'too_many_uploads' || apiErr.code === 'rate_limited')) {
      const wait = Math.max(2, apiErr.retryAfter || 3);
      this.update(id, { status: 'waiting', error: apiErr });
      const rt = this.runtime.get(id);
      if (rt) rt.stop = false;
      setTimeout(() => this.schedule(id), wait * 1000);
      return;
    }
    this.update(id, { status: 'failed', error: apiErr, speed: 0 });
    if (apiErr.code === 'upload_expired' || apiErr.code === 'not_found') this.forget(id);
  }
}

export const manager = new UploadManager();

// The list follows the session: a sign-in shows that user's uploads, a sign-out clears them.
session.subscribe((s) => manager.bind(s?.user.id ?? null));
