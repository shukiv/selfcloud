// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from 'vitest';
import { encodeMetadata, locationPath, parseError, sendChunk } from './tus';

describe('tus metadata', () => {
  it('writes key and base64 value pairs, leaving out empty keys', () => {
    expect(encodeMetadata({ folder: 'fld_1', name: 'a.txt', parent: undefined, on_conflict: 'fail' })).toBe(
      `folder ${btoa('fld_1')},name ${btoa('a.txt')},on_conflict ${btoa('fail')}`,
    );
  });

  it('encodes non-ASCII names as UTF-8 before base64', () => {
    const v = 'שלום.txt';
    const out = encodeMetadata({ name: v });
    const b64 = out.split(' ')[1];
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    expect(new TextDecoder().decode(bytes)).toBe(v);
  });
});

describe('refused answers', () => {
  it('reads the code and details of the error envelope', () => {
    const e = parseError(
      507,
      JSON.stringify({ error: { code: 'quota_exceeded', message: 'x', details: { scope: 'quota', available_bytes: 5 } } }),
      null,
      null,
    );
    expect(e.code).toBe('quota_exceeded');
    expect(e.status).toBe(507);
    expect(e.details.available_bytes).toBe(5);
  });

  it('keeps the upload state header, which asks for a name', () => {
    const e = parseError(409, '{"error":{"code":"conflict"}}', null, 'awaiting_commit');
    expect(e.state).toBe('awaiting_commit');
    expect(e.code).toBe('conflict');
  });

  it('falls back to internal for a body that is not the envelope, and reads Retry-After', () => {
    expect(parseError(500, '<html>', '7', null).code).toBe('internal');
    expect(parseError(429, '{}', '7', null).retryAfter).toBe(7);
  });

  it('reads a checksum refusal (460) as a checksum mismatch', () => {
    expect(parseError(460, '', null, null).code).toBe('checksum_mismatch');
  });
});

describe('locations', () => {
  it('keeps only the path of an absolute location, so the request stays on this origin', () => {
    expect(locationPath('https://elsewhere.example/api/v1/uploads/upl_1')).toBe('/api/v1/uploads/upl_1');
    expect(locationPath('/api/v1/uploads/upl_2')).toBe('/api/v1/uploads/upl_2');
  });
});

describe('upload locations', () => {
  it('keeps only an upload path, whatever origin the Location header names', () => {
    expect(locationPath('https://elsewhere.example/api/v1/uploads/upl_1')).toBe('/api/v1/uploads/upl_1');
    expect(locationPath('/api/v1/uploads/upl_2')).toBe('/api/v1/uploads/upl_2');
  });

  it('refuses any other path', () => {
    for (const bad of [
      '/api/v1/uploads/',
      '/api/v1/uploads/upl_1/../../admin',
      '/api/v1/uploads/..%2Fadmin',
      '/api/v1/uploads/upl%201',
      '/api/v1/nodes/nod_1',
      '/api/v1/uploads/upl_1/content',
      'javascript:alert(1)',
      'not a url at all',
    ]) {
      expect(locationPath(bad, 'http://localhost/'), bad).toBeNull();
    }
  });
});

// FakeXHR records the piece requests and answers them from a queue, so the checksum header can be read.
class FakeXHR {
  static sent: FakeXHR[] = [];
  static replies: Array<{ status: number; headers?: Record<string, string>; body?: string }> = [];
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

describe('a piece on the wire', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('carries its SHA-256 as Upload-Checksum, in sha256 <base64> form', async () => {
    FakeXHR.sent = [];
    FakeXHR.replies = [{ status: 204, headers: { 'Upload-Offset': '3', 'Selfcloud-Node-ID': 'nod_x' } }];
    vi.stubGlobal('XMLHttpRequest', FakeXHR);
    const out = await sendChunk('/api/v1/uploads/upl_1', 0, new Blob(['abc']), () => {}, () => {},
      'ungWv48Bz+pBQUDeXa4iI7ADYaOWF3qctBD/YfIAFa0=');
    expect(FakeXHR.sent[0].headers['Upload-Checksum']).toBe('sha256 ungWv48Bz+pBQUDeXa4iI7ADYaOWF3qctBD/YfIAFa0=');
    expect(out).toEqual({ offset: 3, nodeID: 'nod_x' });
  });

  it('sends no checksum header when there is no digest', async () => {
    FakeXHR.sent = [];
    FakeXHR.replies = [{ status: 204, headers: { 'Upload-Offset': '3' } }];
    vi.stubGlobal('XMLHttpRequest', FakeXHR);
    await sendChunk('/api/v1/uploads/upl_1', 0, new Blob(['abc']), () => {}, () => {}, null);
    expect(FakeXHR.sent[0].headers['Upload-Checksum']).toBeUndefined();
  });
});
