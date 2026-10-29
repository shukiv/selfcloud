// SPDX-License-Identifier: AGPL-3.0-or-later

// collectAll reads every page of a cursor-paged list, up to maxPages. complete is false when the limit stopped
// it, so a caller that counts or looks names up knows the answer is partial.
export async function collectAll<T>(
  fetchPage: (cursor?: string) => Promise<{ items: T[]; next_cursor?: string }>,
  maxPages = 20,
): Promise<{ items: T[]; complete: boolean }> {
  const items: T[] = [];
  let cursor: string | undefined;
  for (let i = 0; i < maxPages; i++) {
    const page = await fetchPage(cursor);
    items.push(...page.items);
    if (!page.next_cursor) return { items, complete: true };
    cursor = page.next_cursor;
  }
  return { items, complete: false };
}
