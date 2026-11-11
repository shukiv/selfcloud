// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { trailCrumbs, type Crumb } from './breadcrumbs';
import type { Here } from './location';
import type { Node } from './types';

const dir = (id: string, parent: string | null): Node => ({ id, folder_id: 'fld_a', parent_id: parent, name: id.slice(4), kind: 'dir' }) as Node;

// placeOf is the crumbs of a place, each named by its folder's ID, and the root by "root".
function placeOf(here: Here): Crumb<string>[] {
  return trailCrumbs(here, 'root', (n) => n.id);
}

const base = {
  folder: 'fld_a',
  folderName: 'Team A',
  folderRole: 'editor',
  dirRole: 'editor',
  spaceNoDownload: false,
} as const;

// chainOf is the folders d1 to dN, each in the one before; d1's parent is given.
function chainOf(n: number, top: string | null): Node[] {
  return Array.from({ length: n }, (_, i) => dir(`nod_d${i + 1}`, i === 0 ? top : `nod_d${i}`));
}

describe('the crumbs of a place', () => {
  it('a place whose root is reached: the root, then the trail, then the folder shown, the middle collapsed', () => {
    const trail = chainOf(3, 'nod_root');
    const here: Here = { ...base, dir: dir('nod_d4', 'nod_d3'), trail, rootReachable: true, truncated: false, reach: [], path: '/d1/d2/d3/d4' };
    expect(placeOf(here)).toEqual([
      { kind: 'item', item: 'root' },
      { kind: 'gap', hidden: ['nod_d1'] },
      { kind: 'item', item: 'nod_d2' },
      { kind: 'item', item: 'nod_d3' },
      { kind: 'item', item: 'nod_d4' },
    ]);
  });

  it('a place below a grant: the folder shown alone, as before', () => {
    const shown = dir('nod_d4', 'nod_d3');
    const here: Here = { ...base, folderName: null, dir: shown, trail: [], rootReachable: false, truncated: false, reach: [dir('nod_d3', 'nod_d2'), shown], path: null };
    expect(placeOf(here)).toEqual([{ kind: 'item', item: 'nod_d4' }]);
  });

  it('a truncated place (decision 140): the mark for the folders above first, then the 64 nearest and the folder shown, and no root', () => {
    const trail = chainOf(64, 'nod_above');
    const here: Here = { ...base, folderName: null, dir: dir('nod_d65', 'nod_d64'), trail, rootReachable: false, truncated: true, reach: [], path: null };
    const crumbs = placeOf(here);
    expect(crumbs[0]).toEqual({ kind: 'above' });
    expect(crumbs.slice(1)).toEqual([
      { kind: 'item', item: 'nod_d1' },
      { kind: 'gap', hidden: trail.slice(1, 62).map((n) => n.id) },
      { kind: 'item', item: 'nod_d63' },
      { kind: 'item', item: 'nod_d64' },
      { kind: 'item', item: 'nod_d65' },
    ]);
    expect(crumbs).not.toContainEqual({ kind: 'item', item: 'root' });
    expect(crumbs.filter((c) => c.kind === 'above')).toHaveLength(1);
  });

  it("a space's root: the root alone", () => {
    const here: Here = { ...base, dir: null, trail: [], rootReachable: true, truncated: false, reach: [], path: '/' };
    expect(placeOf(here)).toEqual([{ kind: 'item', item: 'root' }]);
  });
});
