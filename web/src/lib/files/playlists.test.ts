// SPDX-License-Identifier: AGPL-3.0-or-later

// The playlist import and export routes (decision 237): what the client sends, and the text a refusal gets. The api
// function is mocked, so these judge the client's side only; the server half lives with the routes.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api', async (orig) => ({ ...(await orig<typeof import('../api')>()), api: vi.fn() }));

import { api } from '../api';
import { ApiError } from '../apiError';
import { loadLang } from '../i18n';
import { session, type Session } from '../session';
import { fileErrorText } from './fileErrors';
import { exportPlaylist, importPlaylist, playlistErrorText, type PlaylistDetail, type SkipReason } from './playlists';

const detail: PlaylistDetail = {
  id: 'pls_1',
  name: 'Mix',
  entry_count: 1,
  created_at: '2026-11-01T00:00:00Z',
  updated_at: '2026-11-10T09:00:00Z',
  entries: [],
};

beforeEach(() => {
  session.set({ user: { id: 'u1' }, account: null, csrf: 'csrf', publicLinks: false } as unknown as Session);
  vi.mocked(api).mockReset();
});

afterEach(() => {
  session.set(null);
});

describe('the playlist import and export routes', () => {
  it('importPlaylist sends the file node and returns the playlist with the skipped lines', async () => {
    vi.mocked(api).mockResolvedValue({ playlist: detail, skipped: [{ line: 2, reason: 'url' }], skipped_total: 3 } as never);
    const out = await importPlaylist('nod_1');
    expect(api).toHaveBeenCalledWith('POST', '/api/v1/playlists/import', { body: { node_id: 'nod_1' } });
    expect(out.playlist.id).toBe('pls_1');
    expect(out.skipped).toEqual([{ line: 2, reason: 'url' }]);
    expect(out.skipped_total).toBe(3);
  });

  it('exportPlaylist sends the folder and returns the new node with the left-out count', async () => {
    vi.mocked(api).mockResolvedValue({ node: { id: 'nod_2', name: 'Mix (2).m3u8' }, left_out: 2 } as never);
    const out = await exportPlaylist('pls_1', 'nod_folder');
    expect(api).toHaveBeenCalledWith('POST', '/api/v1/playlists/pls_1/export', { body: { folder_id: 'nod_folder' } });
    expect(out.node.id).toBe('nod_2');
    expect(out.left_out).toBe(2);
  });

  it('the skip reasons are the six the server names', () => {
    const reasons: SkipReason[] = ['url', 'absolute', 'outside', 'not_found', 'duplicate', 'limit'];
    expect(reasons.length).toBe(6);
  });
});

describe('playlistErrorText', () => {
  beforeEach(async () => {
    await loadLang('en');
  });

  it.each<[string, Record<string, unknown>, string]>([
    ['conflict', { reason: 'limit' }, 'You have 100 playlists, the most allowed. Delete one before importing another.'],
    ['conflict', { reason: 'moved' }, 'The folder moved while exporting. Try again.'],
    ['invalid_request', {}, "This file is not an .m3u or .m3u8 playlist under 1 MB."],
    ['download_restricted', {}, "You can't download this file, so it can't be imported."],
    ['not_found', {}, "The file or folder is gone or you can't reach it."],
    ['invalid_name', {}, "This playlist's name can't be used as a file name. Rename it and try again."],
  ])('%s %j gets its own sentence', (code, details, want) => {
    expect(playlistErrorText(new ApiError(400, code, details, 0), 'en')).toBe(want);
  });

  it('a rate limit keeps the shared minutes sentence', () => {
    expect(playlistErrorText(new ApiError(429, 'rate_limited', {}, 120), 'en')).toBe('Too many attempts. Try again in 2 minutes.');
  });

  it('any other error falls back to the file error text', () => {
    const err = new ApiError(500, 'internal', {}, 0);
    expect(playlistErrorText(err, 'en')).toBe(fileErrorText(err, 'en'));
    expect(playlistErrorText(new Error('x'), 'en')).toBe(fileErrorText(new Error('x'), 'en'));
  });
});
