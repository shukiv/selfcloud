// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { advance, choose, newQueue, replaceEntries, unavailableEntry, type Entry, type Queue } from './queue';
import { entriesOf, savedNodes, type PlaylistDetail } from './playlists';
import type { Node } from './types';

// node makes a playable audio file for the queue.
function node(id: string): Node {
  return { id, folder_id: 'fld_1', parent_id: 'nod_root', name: `${id}.mp3`, kind: 'file', size: 10, mime_type: 'audio/mpeg',
    created_at: '', modified_at: '', no_download: false, editors_can_share: false } as unknown as Node;
}

const ok = (id: string): Entry => ({ node: node(id), mark: 'ok' });

describe('saved playlists in the queue (decision 58)', () => {
  it('an unavailable entry is never picked, and a run of them stops nothing', () => {
    // Twelve unavailable entries in a row, more than MAX_FAILURE_RUN (10): playback must still reach the last file.
    const many: Entry[] = [ok('nod_a'), ...Array.from({ length: 12 }, (_, i) => unavailableEntry(`nod_gone${i}`)), ok('nod_z')];
    let q: Queue = { ...newQueue([], '', { shuffle: false, repeat: 'off' }), entries: many, current: null, pos: 0 };
    const played: string[] = [];
    for (let i = 0; i < 5; i++) {
      const a = advance(q, false);
      if (a.kind !== 'play') break;
      played.push(a.id);
      q = { ...a.queue, current: a.id };
    }
    expect(played).toEqual(['nod_a', 'nod_z']);
    expect(q.run).toBe(0);
  });

  it('choose refuses an unavailable entry: it cannot be made the file on screen, nor turned into a playable one', () => {
    const q0 = replaceEntries(newQueue([node('nod_a')], 'nod_a', { shuffle: false, repeat: 'off' }), [ok('nod_a'), unavailableEntry('nod_gone')]);
    const q1 = choose(q0, 'nod_gone');
    expect(q1).toBe(q0);
    expect(q1.current).toBe('nod_a');
    expect(q1.entries.find((e) => e.node.id === 'nod_gone')?.mark).toBe('unavailable');
  });

  it('an unavailable row has no name and is not a file that can play', () => {
    const e = unavailableEntry('nod_x');
    expect(e.mark).toBe('unavailable');
    expect(e.node.name).toBe('');
  });

  it('replaceEntries keeps the file on screen and cuts a long list like the queue does', () => {
    const q = newQueue([node('nod_a'), node('nod_b')], 'nod_a', { shuffle: false, repeat: 'off' });
    const next = replaceEntries(q, [ok('nod_c'), unavailableEntry('nod_d')]);
    expect(next.current).toBe('nod_a');
    expect(next.entries.map((e) => e.node.id)).toEqual(['nod_c', 'nod_d']);
    const long = Array.from({ length: 510 }, (_, i) => ok(`nod_${i}`));
    const capped = replaceEntries(q, long);
    expect(capped.entries).toHaveLength(500);
    expect(capped.capped).toBe(true);
  });

  it('a saved list maps unavailable entries to rows with no name, and saves only the files', () => {
    const detail: PlaylistDetail = {
      id: 'pls_1', name: 'Evening', entry_count: 2, created_at: '', updated_at: '',
      entries: [
        { entry_id: 'ple_1', position: 0, node_id: 'nod_a', unavailable: false, node: node('nod_a') },
        { entry_id: 'ple_2', position: 1, node_id: 'nod_gone', unavailable: true },
      ],
    };
    const entries = entriesOf(detail);
    expect(entries.map((e) => e.mark)).toEqual(['ok', 'unavailable']);
    expect(entries[1].node.name).toBe('');
    expect(savedNodes(entries)).toEqual(['nod_a']);
  });
});
