// SPDX-License-Identifier: AGPL-3.0-or-later

// The saved playlists of the signed-in person (decision 58): the calls of the playlist routes, and the one mapping the
// web needs, from a playlist's entries to the queue's. The server decides what an entry may show; this module only reads
// its answer. An entry the server marks unavailable becomes a queue row with no name, and is never played.

import { api, ApiError } from '../api';
import { translate, type Lang } from '../i18n';
import { fileErrorText } from './fileErrors';
import { unavailableEntry, type Entry } from './queue';
import type { Node } from './types';

export interface PlaylistSummary {
  id: string;
  name: string;
  entry_count: number;
  created_at: string;
  updated_at: string;
}

export interface PlaylistEntryRow {
  entry_id: string;
  position: number;
  node_id: string;
  unavailable: boolean;
  node?: Node;
}

export interface PlaylistDetail extends PlaylistSummary {
  entries: PlaylistEntryRow[];
}

// listPlaylists reads the person's playlists, without their entries.
export async function listPlaylists(): Promise<PlaylistSummary[]> {
  return (await api<{ items: PlaylistSummary[] }>('GET', '/api/v1/playlists')).items;
}

// createPlaylist makes a playlist with the files given, in order, in one request: the server keeps nothing when one of the
// files is refused, so a retry makes the only playlist.
export function createPlaylist(name: string, nodes: string[] = []): Promise<PlaylistSummary> {
  return api<PlaylistSummary>('POST', '/api/v1/playlists', { body: { name, nodes } });
}

export function getPlaylist(pls: string): Promise<PlaylistDetail> {
  return api<PlaylistDetail>('GET', `/api/v1/playlists/${pls}`);
}

export function renamePlaylist(pls: string, name: string): Promise<PlaylistSummary> {
  return api<PlaylistSummary>('PATCH', `/api/v1/playlists/${pls}`, { body: { name } });
}

export function deletePlaylist(pls: string): Promise<void> {
  return api<void>('DELETE', `/api/v1/playlists/${pls}`);
}

// SkipReason is why the server skipped a line of an imported playlist file (decision 237).
export type SkipReason = 'url' | 'absolute' | 'outside' | 'not_found' | 'duplicate' | 'limit';

export interface ImportResult {
  playlist: PlaylistDetail;
  skipped: { line: number; reason: SkipReason }[];
  skipped_total: number;
}

export interface ExportResult {
  node: Node;
  left_out: number;
}

// importPlaylist reads an .m3u or .m3u8 file into a new playlist. The server answers the lines it skipped, with their
// total, which may be more than the ones it names.
export function importPlaylist(nodeId: string): Promise<ImportResult> {
  return api<ImportResult>('POST', '/api/v1/playlists/import', { body: { node_id: nodeId } });
}

// exportPlaylist writes the playlist as an .m3u8 file into the folder given, and answers the new node with the count of
// entries left out.
export function exportPlaylist(pls: string, folderId: string): Promise<ExportResult> {
  return api<ExportResult>('POST', `/api/v1/playlists/${pls}/export`, { body: { folder_id: folderId } });
}

// playlistErrorText is the text of an import or export refusal, in lang: the codes these routes answer get their own
// sentence, and anything else falls back to the file error text.
export function playlistErrorText(err: unknown, lang: Lang): string {
  if (err instanceof ApiError) {
    if (err.code === 'conflict' && err.details.reason === 'limit') return translate(lang, 'files.playlists.errLimit');
    if (err.code === 'conflict' && err.details.reason === 'moved') return translate(lang, 'files.playlists.errMoved');
    const key = PLAYLIST_KEYS[err.code];
    if (key) return translate(lang, key);
  }
  return fileErrorText(err, lang);
}

const PLAYLIST_KEYS: Record<string, string> = {
  invalid_request: 'files.playlists.errNotPlaylist',
  download_restricted: 'files.playlists.errRestricted',
  not_found: 'files.playlists.errGone',
  invalid_name: 'files.playlists.errName',
};

// replaceEntries sets the playlist to these files, in this order. The server keeps the entries it already has.
export function replaceEntries(pls: string, nodes: string[]): Promise<void> {
  return api<void>('PUT', `/api/v1/playlists/${pls}/entries`, { body: { nodes } });
}

export function removeEntry(pls: string, entry: string): Promise<void> {
  return api<void>('DELETE', `/api/v1/playlists/${pls}/entries/${entry}`);
}

export function reorderEntries(pls: string, entries: string[]): Promise<void> {
  return api<void>('PUT', `/api/v1/playlists/${pls}/order`, { body: { entries } });
}

// entriesOf maps a playlist's entries to queue entries: a reachable file plays, and an unavailable one is a row that is
// skipped and has no name.
export function entriesOf(detail: PlaylistDetail): Entry[] {
  return detail.entries.map((e) => {
    if (e.unavailable || !e.node) return unavailableEntry(e.node_id);
    return { node: e.node, mark: 'ok' as const };
  });
}

// savedNodes is the list a queue saves: the files of its entries, in order, without the unavailable ones.
export function savedNodes(entries: Entry[]): string[] {
  return entries.filter((e) => e.mark !== 'unavailable').map((e) => e.node.id);
}
