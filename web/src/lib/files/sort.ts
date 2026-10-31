// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Node } from './types';

export type SortKey = 'name' | 'modified' | 'size';
export type SortDir = 'asc' | 'desc';

// sortNodes orders nodes for the list: folders first, then files, each group by the key. The name
// compares with the language's collation and numbers in order ("file 2" before "file 10").
export function sortNodes(nodes: Node[], key: SortKey, dir: SortDir, lang: string): Node[] {
  const collator = new Intl.Collator(lang, { numeric: true, sensitivity: 'base' });
  const sign = dir === 'asc' ? 1 : -1;
  const compare = (a: Node, b: Node): number => {
    switch (key) {
      case 'size':
        return a.size - b.size;
      case 'modified':
        return new Date(a.modified_at).getTime() - new Date(b.modified_at).getTime();
      default:
        return collator.compare(a.name, b.name);
    }
  };
  // Folders come first, then shortcuts, then files: each group is sorted by the key.
  const group = (n: Node) => (n.kind === 'dir' ? 0 : n.kind === 'shortcut' ? 1 : 2);
  return [...nodes].sort((a, b) => {
    if (group(a) !== group(b)) return group(a) - group(b);
    const c = compare(a, b) * sign;
    return c !== 0 ? c : collator.compare(a.name, b.name);
  });
}

// nextSort is what a click on a column header chooses: a new column starts ascending, the same
// column flips its direction.
export function nextSort(
  current: { key: SortKey; dir: SortDir },
  clicked: SortKey,
): { key: SortKey; dir: SortDir } {
  if (current.key !== clicked) return { key: clicked, dir: 'asc' };
  return { key: clicked, dir: current.dir === 'asc' ? 'desc' : 'asc' };
}
