// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { formatBytes, formatDate, daysUntil, formatRate } from './format';
import { reachableAncestors, trimCrumbs } from './breadcrumbs';
import { sortNodes, nextSort } from './sort';
import { clickSelect, moveFocus, selectAll, pruneSelection, EMPTY, type Selection } from './selection';
import { conflictReducer, INITIAL_CONFLICT } from './conflict';
import { UndoStack, UNDO_WINDOW_MS } from './undo';
import { Limiter } from './limiter';
import { parsePrefs, DEFAULT_PREFS } from './viewPrefs';
import { parseFilesPath, filesURL, validID } from './route';
import { atLeast, can, downloadAllowed } from './roles';
import { kindOf, previewable } from './kind';
import type { Node } from './types';

const FLD = 'fld_0123456789ab';
const NOD = 'nod_0123456789ab';

function node(over: Partial<Node> & { id: string; name: string }): Node {
  return {
    folder_id: FLD,
    parent_id: null,
    kind: 'file',
    size: 0,
    created_at: '2026-01-01T00:00:00Z',
    modified_at: '2026-01-01T00:00:00Z',
    no_download: false,
    editors_can_share: false,
    ...over,
  };
}

describe('format', () => {
  it('names sizes with decimal units and the language of the interface', () => {
    expect(formatBytes(0, 'en')).toBe('0 byte');
    expect(formatBytes(1500, 'en')).toMatch(/1\.5\s*kB/i);
    expect(formatBytes(2_500_000_000, 'en')).toMatch(/2\.5\s*GB/);
    // The decimal separator follows the language: French writes 2,5.
    expect(formatBytes(2_500_000_000, 'fr')).toMatch(/2,5/);
    expect(formatBytes(-5, 'en')).toBe('0 byte');
  });

  it('formats dates in the language, and an unparsable date as empty', () => {
    expect(formatDate('2026-10-29T10:30:00Z', 'en')).toMatch(/2026/);
    expect(formatDate('2026-10-29T10:30:00Z', 'he')).not.toBe(formatDate('2026-10-29T10:30:00Z', 'en'));
    expect(formatDate('not a date', 'en')).toBe('');
    expect(formatDate(null, 'en')).toBe('');
  });

  it('counts whole days until a purge, never below zero', () => {
    const now = Date.parse('2026-10-29T00:00:00Z');
    expect(daysUntil('2026-11-05T00:00:00Z', now)).toBe(7);
    expect(daysUntil('2026-11-05T00:00:01Z', now)).toBe(8);
    expect(daysUntil('2026-10-01T00:00:00Z', now)).toBe(0);
  });

  it('shows no rate for a speed of zero', () => {
    expect(formatRate(0, 'en')).toBe('');
    expect(formatRate(3_000_000, 'en')).toMatch(/MB/);
  });
});

describe('breadcrumbs', () => {
  const items = ['a', 'b', 'c', 'd', 'e', 'f', 'g'];

  it('keeps short paths whole', () => {
    expect(trimCrumbs(['a', 'b'], 4)).toEqual([
      { kind: 'item', item: 'a' },
      { kind: 'item', item: 'b' },
    ]);
  });

  it('collapses the middle into one gap, keeping the first and the last', () => {
    const out = trimCrumbs(items, 4);
    expect(out[0]).toEqual({ kind: 'item', item: 'a' });
    expect(out[1]).toEqual({ kind: 'gap', hidden: ['b', 'c', 'd'] });
    expect(out.slice(2)).toEqual([
      { kind: 'item', item: 'e' },
      { kind: 'item', item: 'f' },
      { kind: 'item', item: 'g' },
    ]);
  });

  it('never keeps fewer than three positions', () => {
    expect(trimCrumbs(items, 1).length).toBe(4); // first, gap, two at the end
  });

  it('walks parents upward and stops at the first node that cannot be reached', async () => {
    const tree: Record<string, Node> = {
      'nod_c000000001': node({ id: 'nod_c000000001', name: 'C', kind: 'dir', parent_id: 'nod_b000000001' }),
      'nod_b000000001': node({ id: 'nod_b000000001', name: 'B', kind: 'dir', parent_id: 'nod_a000000001' }),
      'nod_a000000001': node({ id: 'nod_a000000001', name: 'A', kind: 'dir', parent_id: null }),
    };
    // The root is not reachable: the walk from C stops at B, and A is never shown.
    const seen: string[] = [];
    const fetchNode = async (id: string) => {
      seen.push(id);
      if (id === 'nod_a000000001') return null;
      return tree[id] ?? null;
    };
    const chain = await reachableAncestors('nod_b000000001', fetchNode);
    expect(chain.map((n) => n.name)).toEqual(['B']);
    expect(seen).toEqual(['nod_b000000001', 'nod_a000000001']);
  });

  it('returns the whole chain, root first, when every ancestor is reachable', async () => {
    const tree: Record<string, Node> = {
      'nod_b000000001': node({ id: 'nod_b000000001', name: 'B', kind: 'dir', parent_id: 'nod_a000000001' }),
      'nod_a000000001': node({ id: 'nod_a000000001', name: 'A', kind: 'dir', parent_id: null }),
    };
    const chain = await reachableAncestors('nod_b000000001', async (id) => tree[id] ?? null);
    expect(chain.map((n) => n.name)).toEqual(['A', 'B']);
  });
});

describe('sorting', () => {
  const nodes = [
    node({ id: 'nod_1', name: 'file 10', size: 5, modified_at: '2026-03-01T00:00:00Z' }),
    node({ id: 'nod_2', name: 'Zdir', kind: 'dir' }),
    node({ id: 'nod_3', name: 'file 2', size: 50, modified_at: '2026-01-01T00:00:00Z' }),
    node({ id: 'nod_4', name: 'adir', kind: 'dir' }),
  ];

  it('puts folders first and sorts names with numbers in order', () => {
    expect(sortNodes(nodes, 'name', 'asc', 'en').map((n) => n.name)).toEqual([
      'adir',
      'Zdir',
      'file 2',
      'file 10',
    ]);
  });

  it('keeps folders first when sorting by size, descending within each group', () => {
    expect(sortNodes(nodes, 'size', 'desc', 'en').map((n) => n.id)).toEqual(['nod_4', 'nod_2', 'nod_3', 'nod_1']);
  });

  it('flips the direction on the same column and starts a new column ascending', () => {
    expect(nextSort({ key: 'name', dir: 'asc' }, 'name')).toEqual({ key: 'name', dir: 'desc' });
    expect(nextSort({ key: 'name', dir: 'asc' }, 'size')).toEqual({ key: 'size', dir: 'asc' });
  });
});

describe('selection', () => {
  const all = ['a', 'b', 'c', 'd', 'e'];

  it('selects a range with shift and adds with ctrl', () => {
    let s: Selection = clickSelect(EMPTY, all, 1);
    expect(s.ids).toEqual(['b']);
    s = clickSelect(s, all, 3, { shift: true });
    expect(s.ids).toEqual(['b', 'c', 'd']);
    s = clickSelect(clickSelect(EMPTY, all, 0), all, 4, { toggle: true });
    expect(s.ids).toEqual(['a', 'e']);
    s = clickSelect(s, all, 0, { toggle: true });
    expect(s.ids).toEqual(['e']);
  });

  it('moves the keyboard focus, and extends the range with shift', () => {
    let s = moveFocus(EMPTY, all, 1);
    expect(s.focus).toBe(0);
    s = moveFocus(s, all, 1, true);
    s = moveFocus(s, all, 1, true);
    expect(s.ids).toEqual(['a', 'b', 'c']);
    expect(moveFocus(s, all, 99).focus).toBe(4);
  });

  it('selects all and prunes what is gone', () => {
    expect(selectAll(all).ids).toHaveLength(5);
    const pruned = pruneSelection({ ids: ['a', 'zz'], anchor: 0, focus: 0 }, all);
    expect(pruned.ids).toEqual(['a']);
  });
});

describe('conflict choice', () => {
  it('asks once, and applies a batch choice to the rest', () => {
    let r = conflictReducer(INITIAL_CONFLICT, { type: 'conflict', name: 'a.txt' });
    expect(r.effect).toEqual({ kind: 'ask' });
    expect(r.state.asking).toBe('a.txt');
    r = conflictReducer(r.state, { type: 'answer', choice: 'replace', forBatch: true });
    expect(r.effect).toEqual({ kind: 'apply', choice: 'replace' });
    r = conflictReducer(r.state, { type: 'conflict', name: 'b.txt' });
    expect(r.effect).toEqual({ kind: 'apply', choice: 'replace' });
    expect(r.state.asking).toBeNull();
  });

  it('asks again for the next file when the choice was for one file only', () => {
    let r = conflictReducer(INITIAL_CONFLICT, { type: 'conflict', name: 'a.txt' });
    r = conflictReducer(r.state, { type: 'answer', choice: 'skip', forBatch: false });
    expect(r.effect).toEqual({ kind: 'apply', choice: 'skip' });
    r = conflictReducer(r.state, { type: 'conflict', name: 'b.txt' });
    expect(r.effect).toEqual({ kind: 'ask' });
  });
});

describe('undo stack', () => {
  it('keeps an entry for eight seconds and then drops it', () => {
    let t = 1000;
    const stack = new UndoStack(() => t);
    stack.push('delete a', async () => 0);
    expect(stack.current()?.label).toBe('delete a');
    t += UNDO_WINDOW_MS - 1;
    expect(stack.current()).not.toBeNull();
    t += 1;
    expect(stack.current()).toBeNull();
    expect(stack.size).toBe(0);
  });

  it('takes the newest live entry, once', async () => {
    let t = 0;
    const stack = new UndoStack(() => t);
    const ran: string[] = [];
    stack.push('first', async () => (ran.push('first'), 0));
    stack.push('second', async () => (ran.push('second'), 1));
    const top = stack.take();
    expect(top?.label).toBe('second');
    expect(await top!.run()).toBe(1);
    expect(stack.current()?.label).toBe('first');
    expect(ran).toEqual(['second']);
  });
});

describe('upload queue limiter', () => {
  it('never runs more than the slots at once, and runs the rest in order', async () => {
    const limiter = new Limiter(3);
    let active = 0;
    let peak = 0;
    const order: number[] = [];
    const tasks = Array.from({ length: 10 }, (_, i) =>
      limiter.run(async () => {
        active++;
        peak = Math.max(peak, active);
        order.push(i);
        await new Promise((r) => setTimeout(r, 2));
        active--;
        return i;
      }),
    );
    const results = await Promise.all(tasks);
    expect(peak).toBe(3);
    expect(results).toEqual([0, 1, 2, 3, 4, 5, 6, 7, 8, 9]);
    expect(order).toEqual([0, 1, 2, 3, 4, 5, 6, 7, 8, 9]);
    expect(limiter.running).toBe(0);
  });

  it('frees the slot when a task fails', async () => {
    const limiter = new Limiter(1);
    await expect(limiter.run(async () => Promise.reject(new Error('boom')))).rejects.toThrow('boom');
    expect(await limiter.run(async () => 'next')).toBe('next');
  });

  it('refuses a limit below one', () => {
    expect(() => new Limiter(0)).toThrow();
  });
});

describe('view preferences', () => {
  it('reads what it knows and ignores the rest', () => {
    expect(parsePrefs(null)).toEqual(DEFAULT_PREFS);
    expect(parsePrefs('not json')).toEqual(DEFAULT_PREFS);
    expect(parsePrefs('{"view":"grid","sort":{"key":"size","dir":"desc"}}')).toEqual({
      view: 'grid',
      sort: { key: 'size', dir: 'desc' },
    });
    expect(parsePrefs('{"view":"tiles","sort":{"key":"owner","dir":"up"}}')).toEqual(DEFAULT_PREFS);
  });
});

describe('addresses', () => {
  it('reads each form of the file address', () => {
    expect(parseFilesPath('/files')).toEqual({ kind: 'my' });
    expect(parseFilesPath(`/files/${FLD}`)).toEqual({ kind: 'folder', folder: FLD });
    expect(parseFilesPath(`/files/${FLD}/${NOD}`)).toEqual({ kind: 'dir', folder: FLD, dir: NOD });
    expect(parseFilesPath(`/n/${NOD}`)).toEqual({ kind: 'node', node: NOD });
  });

  it('refuses anything else, and IDs of the wrong type or shape', () => {
    expect(parseFilesPath(`/files/${NOD}`)).toBeNull();
    expect(parseFilesPath('/files/fld_x')).toBeNull();
    expect(parseFilesPath(`/files/${FLD}/${FLD}`)).toBeNull();
    expect(parseFilesPath('/files/a/b/c')).toBeNull();
    expect(parseFilesPath('/n/')).toBeNull();
    expect(validID('nod', 'nod_../../x')).toBe(false);
  });

  it('builds addresses with a query', () => {
    expect(filesURL(FLD, null)).toBe(`/files/${FLD}`);
    expect(filesURL(FLD, NOD, { preview: NOD })).toBe(`/files/${FLD}/${NOD}?preview=${NOD}`);
  });
});

describe('roles and downloads', () => {
  it('orders the roles and answers each operation by the table in section 8', () => {
    expect(atLeast('editor', 'contributor')).toBe(true);
    expect(atLeast('contributor', 'editor')).toBe(false);
    expect(atLeast(undefined, 'viewer')).toBe(false);
    expect(can.create('viewer')).toBe(false);
    expect(can.create('contributor')).toBe(true);
    expect(can.edit('contributor')).toBe(false);
    expect(can.edit('editor')).toBe(true);
    expect(can.read('viewer')).toBe(true);
  });

  it('hides downloads from viewers and commenters only where no_download is set', () => {
    expect(downloadAllowed({ no_download: false, role: 'viewer' })).toBe(true);
    expect(downloadAllowed({ no_download: true, role: 'viewer' })).toBe(false);
    expect(downloadAllowed({ no_download: true, role: 'commenter' })).toBe(false);
    expect(downloadAllowed({ no_download: true, role: 'contributor' })).toBe(true);
  });
});

describe('kinds', () => {
  it('classifies by media type and previews only the inline kinds', () => {
    expect(kindOf({ kind: 'dir' })).toBe('folder');
    expect(kindOf({ kind: 'file', mime_type: 'image/png' })).toBe('image');
    expect(kindOf({ kind: 'file', mime_type: 'text/plain; charset=utf-8' })).toBe('text');
    expect(kindOf({ kind: 'file', mime_type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' })).toBe('office');
    expect(previewable({ kind: 'file', mime_type: 'video/mp4' })).toBe(true);
    expect(previewable({ kind: 'file', mime_type: 'application/zip' })).toBe(false);
    expect(previewable({ kind: 'dir' })).toBe(false);
  });
});
