// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { planNodeLink } from './nodeLink';
import type { Node } from './types';

const FLD = 'fld_12345678';
function node(over: Partial<Node> & Pick<Node, 'id' | 'kind' | 'parent_id'>): Node {
  return { folder_id: FLD, name: 'x', size: 0, ...over } as Node;
}

describe('the /n/ address decision', () => {
  const root = node({ id: 'nod_root0001', kind: 'dir', parent_id: null });
  const dir = node({ id: 'nod_dir00001', kind: 'dir', parent_id: root.id });
  const file = node({ id: 'nod_file0001', kind: 'file', parent_id: dir.id });
  const rootFile = node({ id: 'nod_rfile001', kind: 'file', parent_id: root.id });

  it('opens a folder root', () => {
    expect(planNodeLink(root, null)).toEqual({ kind: 'folder', folder: FLD });
  });

  it('opens a file in a directory in its listing, with the preview', () => {
    expect(planNodeLink(file, dir)).toEqual({ kind: 'in-folder', folder: FLD, dir: dir.id, preview: file.id });
  });

  it('opens a file in the folder root in the root listing', () => {
    expect(planNodeLink(rootFile, root)).toEqual({ kind: 'in-folder', folder: FLD, dir: null, preview: rootFile.id });
  });

  it('shows a file on its own when its parent cannot be reached, and never the folder root', () => {
    const plan = planNodeLink(file, null);
    expect(plan).toEqual({ kind: 'alone', node: file });
    expect(plan.kind).not.toBe('in-folder');
  });

  it('opens a directory whose parent cannot be reached on the directory itself', () => {
    // A shared directory: its folder root is not reachable, so the root listing would be not found.
    expect(planNodeLink(dir, null)).toEqual({ kind: 'in-folder', folder: FLD, dir: dir.id });
  });

  it('selects a directory in its parent listing', () => {
    expect(planNodeLink(dir, root)).toEqual({ kind: 'in-folder', folder: FLD, dir: null, select: dir.id });
  });
});
