// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from 'vitest';
import { auditExportURL, auditLog } from './api';

afterEach(() => vi.unstubAllGlobals());

function answer(status: number, body: unknown) {
  const fn = vi.fn(async (_url: string, _init: RequestInit = {}) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));
  vi.stubGlobal('fetch', fn);
  return fn;
}

function requested(fn: ReturnType<typeof answer>): URL {
  return new URL(String(fn.mock.calls[0][0]), 'https://drive.example.com');
}

describe('auditLog', () => {
  it('sends since and until as the instants it is given', async () => {
    const fn = answer(200, { items: [] });
    await auditLog('acc_1', { since: '2026-10-01T00:00:00.000Z', until: '2026-10-02T00:00:00.000Z', action: 'user.create', actor: 'usr_1' });
    const url = requested(fn);
    expect(url.pathname).toBe('/api/v1/accounts/acc_1/audit');
    expect(url.searchParams.get('since')).toBe('2026-10-01T00:00:00.000Z');
    expect(url.searchParams.get('until')).toBe('2026-10-02T00:00:00.000Z');
    expect(url.searchParams.get('action')).toBe('user.create');
    expect(url.searchParams.get('actor')).toBe('usr_1');
  });

  it('leaves an empty or missing range out of the query', async () => {
    const fn = answer(200, { items: [] });
    await auditLog('acc_1', { since: undefined, until: '' });
    const url = requested(fn);
    expect(url.searchParams.has('since')).toBe(false);
    expect(url.searchParams.has('until')).toBe(false);
  });
});

describe('auditExportURL', () => {
  it('names the export route with the CSV format and only the filters set', () => {
    const url = new URL(auditExportURL('acc_1', { since: '2026-10-01T00:00:00.000Z', until: '2026-10-02T00:00:00.000Z', action: 'user.create', actor: 'usr_1' }), 'https://drive.example.com');
    expect(url.pathname).toBe('/api/v1/accounts/acc_1/audit/export');
    expect(url.searchParams.get('format')).toBe('csv');
    expect(url.searchParams.get('since')).toBe('2026-10-01T00:00:00.000Z');
    expect(url.searchParams.get('until')).toBe('2026-10-02T00:00:00.000Z');
    expect(url.searchParams.get('action')).toBe('user.create');
    expect(url.searchParams.get('actor')).toBe('usr_1');
  });

  it('leaves an unset filter out', () => {
    expect(auditExportURL('acc_1', {})).toBe('/api/v1/accounts/acc_1/audit/export?format=csv');
    expect(auditExportURL('acc_1', { action: '', actor: '', since: '', until: '' })).toBe('/api/v1/accounts/acc_1/audit/export?format=csv');
  });
});