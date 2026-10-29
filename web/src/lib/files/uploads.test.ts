// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import { headFingerprint, sha256Base64 } from './digest';

// The upload manager reads window.localStorage and writes beforeunload, so the module is loaded after a
// window stub exists. Its imports reach the page's origin only through the stubbed window.
type Uploads = typeof import('./uploads');
let U: Uploads;

const LM = 1_700_000_000_000;
const USER_A = 'usr_aaaaaaaa';
const USER_B = 'usr_bbbbbbbb';
const FLD = 'fld_12345678';
const KEY = 'selfcloud.uploads';
// HEAD is a well-formed fingerprint for records that do not depend on the bytes.
const HEAD = 'a'.repeat(64);

let mem: Map<string, string>;

// FakeXHR answers the piece requests from a queue and keeps what was sent.
interface Reply {
  status: number;
  headers?: Record<string, string>;
  body?: string;
}
class FakeXHR {
  static sent: FakeXHR[] = [];
  static replies: Reply[] = [];
  headers: Record<string, string> = {};
  status = 0;
  responseText = '';
  withCredentials = false;
  upload: { onprogress: null | ((e: unknown) => void) } = { onprogress: null };
  onload: null | (() => void) = null;
  onerror: null | (() => void) = null;
  onabort: null | (() => void) = null;
  private resp: Record<string, string> = {};
  open(): void {}
  setRequestHeader(k: string, v: string): void {
    this.headers[k] = v;
  }
  getResponseHeader(k: string): string | null {
    return this.resp[k.toLowerCase()] ?? null;
  }
  abort(): void {
    this.onabort?.();
  }
  send(): void {
    FakeXHR.sent.push(this);
    const reply = FakeXHR.replies.shift() ?? { status: 500 };
    queueMicrotask(() => {
      this.status = reply.status;
      this.responseText = reply.body ?? '';
      for (const [k, v] of Object.entries(reply.headers ?? {})) this.resp[k.toLowerCase()] = v;
      this.onload?.();
    });
  }
}

// rec is a stored record in the shape the client writes.
function rec(user: string, name: string, size: number, head: string, url = '/api/v1/uploads/upl_1') {
  return { user, name, size, lastModified: LM, mime: 'text/plain', target: { folder: FLD }, url, onConflict: 'fail', head };
}
function putRecords(list: unknown[]): void {
  mem.set(KEY, JSON.stringify(list));
}
function storedRecords(): Array<{ user: string; url: string; name: string }> {
  return JSON.parse(mem.get(KEY) ?? '[]');
}

function stubFetch(handler: (method: string, url: string) => Response): string[] {
  const calls: string[] = [];
  vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET';
    calls.push(`${method} ${url}`);
    return handler(method, url);
  });
  return calls;
}

beforeAll(async () => {
  vi.stubGlobal('window', { localStorage: undefined, addEventListener() {}, removeEventListener() {}, location: { href: 'http://localhost/' } });
  U = await import('./uploads');
});

beforeEach(() => {
  mem = new Map();
  vi.stubGlobal('window', {
    localStorage: {
      getItem: (k: string) => mem.get(k) ?? null,
      setItem: (k: string, v: string) => void mem.set(k, v),
      removeItem: (k: string) => void mem.delete(k),
    },
    addEventListener() {},
    removeEventListener() {},
    location: { href: 'http://localhost/' },
  });
  FakeXHR.sent = [];
  FakeXHR.replies = [];
  vi.stubGlobal('XMLHttpRequest', FakeXHR);
});

afterEach(() => vi.unstubAllGlobals());

describe('stored records', () => {
  it('keeps only records of the shape the client writes, for the user who owns them', async () => {
    const good = rec(USER_A, 'a.txt', 4, HEAD);
    putRecords([
      good,
      rec(USER_A, 'bad-url.txt', 4, HEAD, '/api/v1/uploads/../../admin'),
      rec(USER_A, 'absolute.txt', 4, HEAD, 'https://evil.example/api/v1/uploads/upl_1'),
      rec(USER_A, 'encoded.txt', 4, HEAD, '/api/v1/uploads/..%2Fadmin'),
      { ...rec(USER_A, 'bad-folder.txt', 4, HEAD), target: { folder: '../fld' } },
      { ...rec(USER_A, 'bad-parent.txt', 4, HEAD), target: { folder: FLD, parent: 'nod_<script>' } },
      { ...rec(USER_A, 'bad-path.txt', 4, HEAD), target: { folder: FLD, path: 'relative' } },
      rec(USER_A, 'negative.txt', -1, HEAD),
      rec(USER_A, 'fraction.txt', 1.5, HEAD),
      rec(USER_A, 'empty-head.txt', 4, ''),
      rec(USER_A, 'bad-head.txt', 4, 'not-hex'),
      { ...rec(USER_A, 'bad-mode.txt', 4, HEAD), onConflict: 'overwrite' },
      { ...rec(USER_A, 'control\u0007.txt', 4, HEAD) },
      rec(USER_B, 'theirs.txt', 4, HEAD),
      'not a record',
      null,
    ]);
    expect(U.readRecords(USER_A).map((r) => r.name)).toEqual(['a.txt']);
    expect(U.readRecords(USER_B).map((r) => r.name)).toEqual(['theirs.txt']);
    expect(U.readRecords(null)).toEqual([]);
  });

  it('a record without a fingerprint is not kept: its file starts a fresh upload', () => {
    putRecords([rec(USER_A, 'no-head.txt', 4, '')]);
    const m = new U.UploadManager();
    m.bind(USER_A);
    expect(U.readRecords(USER_A)).toEqual([]);
    expect(get(U.uploads)).toEqual([]);
  });

  it('shows only the signed-in user\'s uploads, and a sign-out removes them from this browser', () => {
    putRecords([rec(USER_A, 'a.txt', 1, HEAD), rec(USER_B, 'b.txt', 1, HEAD, '/api/v1/uploads/upl_b')]);
    const m = new U.UploadManager();
    m.bind(USER_A);
    expect(get(U.uploads).map((u) => u.name)).toEqual(['a.txt']);
    expect(get(U.uploads)[0].status).toBe('interrupted');

    m.bind(null);
    expect(get(U.uploads)).toEqual([]);
    expect(storedRecords().map((r) => r.name)).toEqual(['b.txt']);
  });
});

describe('resuming', () => {
  it('an upload whose bytes are all in resumes as awaiting_name, never done', async () => {
    const file = new File(['hello world'], 'a.txt', { lastModified: LM });
    putRecords([rec(USER_A, 'a.txt', file.size, await headFingerprint(file))]);
    const m = new U.UploadManager();
    m.bind(USER_A);
    const id = get(U.uploads)[0].id;
    const calls = stubFetch(
      () =>
        new Response(null, {
          status: 200,
          headers: { 'Upload-Offset': String(file.size), 'Selfcloud-Upload-State': 'awaiting_commit' },
        }),
    );

    expect(await m.attach(id, file)).toBe(true);
    await vi.waitFor(() => expect(get(U.uploads)[0].status).toBe('awaiting_name'));
    expect(get(U.uploads)[0].status).not.toBe('done');
    expect(get(U.uploads)[0].nodeID).toBeNull();
    expect(calls).toEqual(['HEAD /api/v1/uploads/upl_1']);
    expect(FakeXHR.sent).toEqual([]);
  });

  it('a file whose first MiB differs from the stored upload starts a new upload, and the old one is dropped', async () => {
    const old = new File(['hello world'], 'a.txt', { lastModified: LM });
    putRecords([rec(USER_A, 'a.txt', old.size, await headFingerprint(old))]);
    const m = new U.UploadManager();
    m.bind(USER_A);
    const id = get(U.uploads)[0].id;
    const other = new File(['HELLO WORLD'], 'a.txt', { lastModified: LM });
    const calls = stubFetch((method) =>
      method === 'POST'
        ? new Response(null, { status: 201, headers: { Location: '/api/v1/uploads/upl_new' } })
        : new Response(null, { status: 204 }),
    );
    FakeXHR.replies = [{ status: 204, headers: { 'Upload-Offset': '11', 'Selfcloud-Node-ID': 'nod_new00001' } }];

    expect(await m.attach(id, other)).toBe(true);
    expect(calls).toContain('DELETE /api/v1/uploads/upl_1');
    await vi.waitFor(() => expect(get(U.uploads)[0].status).toBe('done'));
    expect(calls).toContain('POST /api/v1/uploads');
    expect(storedRecords().some((r) => r.url === '/api/v1/uploads/upl_1')).toBe(false);
  });

  it('a file with the stored fingerprint resumes the stored upload', async () => {
    const file = new File(['hello world'], 'a.txt', { lastModified: LM });
    putRecords([rec(USER_A, 'a.txt', file.size, await headFingerprint(file))]);
    const m = new U.UploadManager();
    m.bind(USER_A);
    const id = get(U.uploads)[0].id;
    stubFetch(() => new Response(null, { status: 200, headers: { 'Upload-Offset': '0' } }));
    FakeXHR.replies = [{ status: 204, headers: { 'Upload-Offset': '11', 'Selfcloud-Node-ID': 'nod_res00001' } }];

    expect(await m.attach(id, file)).toBe(true);
    await vi.waitFor(() => expect(get(U.uploads)[0].status).toBe('done'));
    expect(get(U.uploads)[0].url).toBe('/api/v1/uploads/upl_1');
  });
});

describe('sending pieces', () => {
  it('a piece carries its SHA-256, and the answer with a node ID makes the upload done', async () => {
    const file = new File(['hello world'], 'a.txt', { lastModified: LM });
    const m = new U.UploadManager();
    m.bind(USER_A);
    stubFetch(() => new Response(null, { status: 201, headers: { Location: '/api/v1/uploads/upl_new' } }));
    FakeXHR.replies = [{ status: 204, headers: { 'Upload-Offset': '11', 'Selfcloud-Node-ID': 'nod_done0001' } }];

    m.add([{ file, target: { folder: FLD } }]);
    await vi.waitFor(() => expect(get(U.uploads)[0].status).toBe('done'));
    expect(get(U.uploads)[0].nodeID).toBe('nod_done0001');
    expect(FakeXHR.sent[0].headers['Upload-Checksum']).toBe(`sha256 ${await sha256Base64(file)}`);
    expect(FakeXHR.sent[0].headers['Upload-Checksum']).toBe(
      'sha256 uU0nuZNNPgilLlLX2n2r+sSE7+N6U4DukIj3rOLvzek=',
    );
    expect(storedRecords()).toEqual([]);
  });

  it('all bytes in without a node ID ends in awaiting_name, not done', async () => {
    const file = new File(['hello world'], 'a.txt', { lastModified: LM });
    const m = new U.UploadManager();
    m.bind(USER_A);
    stubFetch(() => new Response(null, { status: 201, headers: { Location: '/api/v1/uploads/upl_new' } }));
    FakeXHR.replies = [{ status: 204, headers: { 'Upload-Offset': '11' } }];

    m.add([{ file, target: { folder: FLD } }]);
    await vi.waitFor(() => expect(get(U.uploads)[0].status).toBe('awaiting_name'));
    expect(get(U.uploads)[0].nodeID).toBeNull();
    expect(storedRecords().map((r) => r.url)).toEqual(['/api/v1/uploads/upl_new']);
  });

  it('a piece refused for its checksum is read again once, then sent again', async () => {
    const file = new File(['hello world'], 'a.txt', { lastModified: LM });
    const m = new U.UploadManager();
    m.bind(USER_A);
    const calls = stubFetch((method) =>
      method === 'POST'
        ? new Response(null, { status: 201, headers: { Location: '/api/v1/uploads/upl_new' } })
        : new Response(null, { status: 200, headers: { 'Upload-Offset': '0' } }),
    );
    FakeXHR.replies = [
      { status: 460, body: JSON.stringify({ error: { code: 'checksum_mismatch', message: 'x' } }) },
      { status: 204, headers: { 'Upload-Offset': '11', 'Selfcloud-Node-ID': 'nod_ret00001' } },
    ];

    m.add([{ file, target: { folder: FLD } }]);
    await vi.waitFor(() => expect(get(U.uploads)[0].status).toBe('done'));
    expect(FakeXHR.sent).toHaveLength(2);
    expect(calls.filter((c) => c.startsWith('HEAD'))).toHaveLength(1);
  });

  it('a piece refused twice fails the upload', async () => {
    const file = new File(['hello world'], 'a.txt', { lastModified: LM });
    const m = new U.UploadManager();
    m.bind(USER_A);
    stubFetch((method) =>
      method === 'POST'
        ? new Response(null, { status: 201, headers: { Location: '/api/v1/uploads/upl_new' } })
        : new Response(null, { status: 200, headers: { 'Upload-Offset': '0' } }),
    );
    const refused = { status: 460, body: JSON.stringify({ error: { code: 'checksum_mismatch', message: 'x' } }) };
    FakeXHR.replies = [refused, refused];

    m.add([{ file, target: { folder: FLD } }]);
    await vi.waitFor(() => expect(get(U.uploads)[0].status).toBe('failed'));
    expect(get(U.uploads)[0].error?.code).toBe('checksum_mismatch');
  });
});

describe('a drop link transport', () => {
  it('a drop upload is done when its last piece is taken, though the server names no node to a visitor', async () => {
    const file = new File(['hello world'], 'a.txt', { lastModified: LM });
    let list: Array<{ status: string; nodeID: string | null }> = [];
    const m = new U.UploadManager(() => Date.now(), {
      bearer: () => 'scl_visitor',
      metadata: () => ({ name: 'a.txt' }),
      fresh: async () => {},
      sink: (items) => {
        list = items;
      },
    });
    stubFetch((method) =>
      method === 'POST'
        ? new Response(null, { status: 201, headers: { Location: '/api/v1/uploads/upl_drop' } })
        : new Response(null, { status: 204 }),
    );
    FakeXHR.replies = [{ status: 204, headers: { 'Upload-Offset': '11' } }];

    m.add([{ file, target: { folder: '' } }]);
    await vi.waitFor(() => expect(list[0]?.status).toBe('done'));
    expect(list[0].nodeID).toBeNull();
  });
});
