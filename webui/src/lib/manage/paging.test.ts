// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { collectAll } from './paging';

// pages returns a fetcher over fixed pages, keyed by cursor.
function pages(list: { items: number[]; next_cursor?: string }[]) {
  const seen: (string | undefined)[] = [];
  const fetchPage = async (cursor?: string) => {
    seen.push(cursor);
    const index = cursor === undefined ? 0 : Number(cursor);
    return list[index];
  };
  return { fetchPage, seen };
}

describe('collectAll', () => {
  it('reads every page in order', async () => {
    const { fetchPage, seen } = pages([{ items: [1, 2], next_cursor: '1' }, { items: [3], next_cursor: '2' }, { items: [4] }]);
    const out = await collectAll(fetchPage);
    expect(out).toEqual({ items: [1, 2, 3, 4], complete: true });
    expect(seen).toEqual([undefined, '1', '2']);
  });

  it('stops at the page limit and says the answer is partial', async () => {
    const { fetchPage } = pages([{ items: [1], next_cursor: '1' }, { items: [2], next_cursor: '2' }, { items: [3] }]);
    expect(await collectAll(fetchPage, 2)).toEqual({ items: [1, 2], complete: false });
  });
});
