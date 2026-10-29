// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  audienceLabel,
  canDeleteAll,
  creatorLabel,
  deleteAllCount,
  folderLabel,
  itemLabel,
  kindLabel,
  linkListQuery,
  mergeLinks,
  NO_FILTER,
  removeLink,
  statusKeys,
  type AccountLink,
} from './links';

// row returns a link with the given fields over a plain organisation link in a team space.
function row(id: string, over: Partial<AccountLink> = {}): AccountLink {
  return {
    id,
    node_id: `nod_${id}`,
    audience: 'organisation',
    role: 'viewer',
    upload_only: false,
    expires_at: null,
    created_at: '2026-10-01T00:00:00Z',
    paused: false,
    password_set: false,
    created_by: 'usr_ann',
    created_by_kind: 'user',
    created_by_name: 'Ann',
    visitors: 0,
    folder_id: 'fld_team',
    folder_kind: 'team',
    folder_name: 'Finance',
    folder_owner_id: '',
    folder_owner_name: '',
    item_name: 'report.pdf',
    item_path: '/Q3/report.pdf',
    hidden: false,
    ...over,
  };
}

describe('linkListQuery', () => {
  it('leaves out the filters that say all', () => {
    expect(linkListQuery(NO_FILTER, undefined, 50)).toBe('?limit=50');
  });

  it('names the audience and kind, and the cursor', () => {
    expect(linkListQuery({ audience: 'anyone', kind: 'drop' }, 'abc', 50)).toBe(
      '?audience=anyone&kind=drop&limit=50&cursor=abc',
    );
    expect(linkListQuery({ audience: 'organisation', kind: 'access' }, undefined, 20)).toBe(
      '?audience=organisation&kind=access&limit=20',
    );
  });

  it('encodes a cursor', () => {
    expect(linkListQuery(NO_FILTER, 'a b&c', 50)).toBe('?limit=50&cursor=a+b%26c');
  });
});

describe('mergeLinks', () => {
  it('replaces the rows on a reset', () => {
    const page = { items: [row('b')] };
    expect(mergeLinks([row('a')], page, true)).toEqual([row('b')]);
  });

  it('adds the next page after the rows shown, once per link', () => {
    const shown = [row('a'), row('b')];
    const merged = mergeLinks(shown, { items: [row('b'), row('c')] }, false);
    expect(merged.map((l) => l.id)).toEqual(['a', 'b', 'c']);
  });
});

describe('itemLabel', () => {
  it('shows the item name and path to a viewer who may see them', () => {
    expect(itemLabel(row('a'), false)).toEqual({ text: 'report.pdf' });
  });

  it('says whose My files a hidden item is in, for the owner', () => {
    const l = row('a', {
      hidden: true,
      item_name: null,
      item_path: null,
      folder_kind: 'personal',
      folder_name: '',
      folder_owner_id: 'usr_bob',
      folder_owner_name: 'Bob',
    });
    expect(itemLabel(l, false)).toEqual({ key: 'links.item.hiddenMember', params: { member: 'Bob' } });
  });

  it('shows no name to an admin', () => {
    const l = row('a', { hidden: true, item_name: null, item_path: null });
    expect(itemLabel(l, true)).toEqual({ key: 'links.item.hiddenAdmin' });
  });
});

describe('folderLabel', () => {
  it('names a team space', () => {
    expect(folderLabel(row('a'), 'usr_ann')).toEqual({ text: 'Finance' });
  });

  it('calls the viewer own My files "My files"', () => {
    const l = row('a', { folder_kind: 'personal', folder_name: '', folder_owner_id: 'usr_ann', folder_owner_name: 'Ann' });
    expect(folderLabel(l, 'usr_ann')).toEqual({ key: 'links.folder.mine' });
  });

  it("names another member's My files", () => {
    const l = row('a', { folder_kind: 'personal', folder_name: '', folder_owner_id: 'usr_bob', folder_owner_name: 'Bob' });
    expect(folderLabel(l, 'usr_ann')).toEqual({ key: 'links.folder.memberFiles', params: { name: 'Bob' } });
  });
});

describe('labels and status', () => {
  it('names the audience and the kind', () => {
    expect(audienceLabel(row('a', { audience: 'anyone' }))).toEqual({ key: 'links.audience.anyone' });
    expect(audienceLabel(row('a'))).toEqual({ key: 'links.audience.organisation' });
    expect(kindLabel(row('a', { upload_only: true, role: '' }))).toEqual({ key: 'links.kind.drop' });
    expect(kindLabel(row('a'))).toEqual({ key: 'links.kind.access' });
  });

  it('says who made the link, or that an account key did', () => {
    expect(creatorLabel(row('a'))).toEqual({ key: 'links.by.user', params: { name: 'Ann' } });
    expect(creatorLabel(row('a', { created_by_kind: 'account_key', created_by_name: '' }))).toEqual({
      key: 'links.by.key',
    });
  });

  it('lists paused, password and visitors, and nothing else', () => {
    expect(statusKeys(row('a'))).toEqual([{ key: 'links.status.visitors', params: { count: '0' } }]);
    const anyone = row('b', { audience: 'anyone', paused: true, password_set: true, visitors: undefined });
    expect(statusKeys(anyone)).toEqual([{ key: 'links.status.paused' }, { key: 'links.status.password' }]);
  });
});

describe('delete all public links', () => {
  it('names a whole, non-negative count', () => {
    expect(deleteAllCount(3)).toBe(3);
    expect(deleteAllCount(2.7)).toBe(2);
    expect(deleteAllCount(-4)).toBe(0);
    expect(deleteAllCount(Number.NaN)).toBe(0);
  });

  it('is offered only when there is a link and nothing is in progress', () => {
    expect(canDeleteAll(0, false)).toBe(false);
    expect(canDeleteAll(2, true)).toBe(false);
    expect(canDeleteAll(2, false)).toBe(true);
  });

  it('removes a deleted link from the rows shown', () => {
    expect(removeLink([row('a'), row('b')], 'a').map((l) => l.id)).toEqual(['b']);
  });
});
