// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { activityKey, lastActivity, mergeRecent, type RecentItem } from './recent';

const item = (id: string, viewed: string | null, edited: string | null): RecentItem =>
  ({
    id,
    folder_id: 'fld_x',
    parent_id: 'nod_p',
    name: id,
    kind: 'file',
    size: 1,
    created_at: '2026-10-01T00:00:00Z',
    modified_at: '2026-10-01T00:00:00Z',
    no_download: false,
    editors_can_share: false,
    viewed_at: viewed,
    edited_at: edited,
    last_at: viewed ?? edited ?? '2026-10-01T00:00:00Z',
  }) as RecentItem;

describe('lastActivity', () => {
  it('is the open when only the caller opened the item', () => {
    expect(lastActivity(item('a', '2026-10-02T10:00:00Z', null))).toEqual({ kind: 'opened', at: '2026-10-02T10:00:00Z' });
  });

  it('is the change when only the caller changed the item', () => {
    expect(lastActivity(item('a', null, '2026-10-02T10:00:00Z'))).toEqual({ kind: 'changed', at: '2026-10-02T10:00:00Z' });
  });

  it('picks the later of an open and a change', () => {
    expect(lastActivity(item('a', '2026-10-03T00:00:00Z', '2026-10-02T00:00:00Z')).kind).toBe('opened');
    expect(lastActivity(item('a', '2026-10-01T00:00:00Z', '2026-10-02T00:00:00Z')).kind).toBe('changed');
  });

  it('gives the open on a tie', () => {
    expect(lastActivity(item('a', '2026-10-02T00:00:00Z', '2026-10-02T00:00:00Z')).kind).toBe('opened');
  });

  it('falls back to last_at when neither time is set', () => {
    const it = { ...item('a', null, null), last_at: '2026-10-05T00:00:00Z' };
    expect(lastActivity(it)).toEqual({ kind: 'opened', at: '2026-10-05T00:00:00Z' });
  });
});

describe('activityKey', () => {
  it('names the catalogue line for each kind', () => {
    expect(activityKey({ kind: 'opened', at: '' })).toBe('recent.opened');
    expect(activityKey({ kind: 'changed', at: '' })).toBe('recent.changed');
  });
});

describe('mergeRecent', () => {
  it('appends a page and leaves out items already shown', () => {
    const shown = [item('a', '2026-10-02T00:00:00Z', null), item('b', '2026-10-01T00:00:00Z', null)];
    const page = [item('b', '2026-10-01T00:00:00Z', null), item('c', '2026-09-30T00:00:00Z', null)];
    expect(mergeRecent(shown, page).map((i) => i.id)).toEqual(['a', 'b', 'c']);
  });
});
