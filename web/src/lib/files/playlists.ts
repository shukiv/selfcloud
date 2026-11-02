// SPDX-License-Identifier: AGPL-3.0-or-later

// The saved playlists of the signed-in person (decision 58): the calls of the playlist routes, and the one mapping the
// web needs, from a playlist's entries to the queue's. The server decides what an entry may show; this module only reads
// its answer. An entry the server marks unavailable becomes a queue row with no name, and is never played.

import { api } from '../api';
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
