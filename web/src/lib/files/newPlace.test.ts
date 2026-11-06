// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from 'vitest';
import { myFilesPlace, newEntries, newPlaceFor, placeOfHere, queueInto, type NewPlace } from './newPlace';
import { uploadTargetWhere } from './ops';
import { manager } from './uploads';
import type { Here } from './location';

const keys = (p: NewPlace) => newEntries(p).filter((e) => e.visible).map((e) => `${e.key}:${e.enabled ? 'on' : 'off'}`);

describe('where the New actions act', () => {
  it('acts on the folder shown on a files page', () => {
    const shown = myFilesPlace('fld_mine');
    expect(newPlaceFor(true, shown, 'fld_mine')).toBe(shown);
  });

  it('acts on My files off the files pages, never on a folder the page did not show', () => {
    const shown = myFilesPlace('fld_team');
    expect(newPlaceFor(false, shown, 'fld_mine')?.where).toEqual({ folder: 'fld_mine', dirID: null, path: '/' });
  });

  it('waits on a files page whose folder is not read yet, and does not fall back to My files', () => {
    expect(newPlaceFor(true, null, 'fld_mine')).toBeNull();
  });

  it('has no place for a person with no My files (an installation admin)', () => {
    expect(newPlaceFor(false, null, null)).toBeNull();
    expect(newPlaceFor(true, null, null)).toBeNull();
  });

  it('takes the place shown from the page: its folder, its directory by ID, and its path', () => {
    const here = { folder: 'fld_t', dir: { id: 'nod_d' }, path: '/a/b' } as unknown as Here;
    expect(placeOfHere(here, true, true).where).toEqual({ folder: 'fld_t', dirID: 'nod_d', path: '/a/b' });
    const root = { folder: 'fld_t', dir: null, path: '/' } as unknown as Here;
    expect(placeOfHere(root, true, true).where).toEqual({ folder: 'fld_t', dirID: null, path: '/' });
  });
});

describe('which entries the place takes (the toolbar rules)', () => {
  it('My files takes all three', () => {
    expect(keys(myFilesPlace('fld_mine'))).toEqual(['newFolder:on', 'upload:on', 'uploadFolder:on']);
  });

  it('a directory with no path takes files only: no folder can be made or uploaded there', () => {
    const p: NewPlace = { where: { folder: 'fld_t', dirID: 'nod_d', path: null }, canFolder: false, canUpload: true, canUploadFolder: false, readOnly: false, canEdit: true };
    expect(keys(p)).toEqual(['upload:on']);
  });

  it('a read-only folder shows every entry disabled, so the reason can show with them, and sends nothing elsewhere', () => {
    const p: NewPlace = { where: { folder: 'fld_t', dirID: null, path: '/' }, canFolder: false, canUpload: false, canUploadFolder: false, readOnly: true, canEdit: false };
    expect(keys(p)).toEqual(['newFolder:off', 'upload:off', 'uploadFolder:off']);
  });
});

describe('queueInto', () => {
  afterEach(() => vi.restoreAllMocks());

  const file = (name: string) => new File(['x'], name);

  it('puts a picked file at the root of My files, and a folder pick under the path of its place', () => {
    const add = vi.spyOn(manager, 'add').mockImplementation(() => undefined);
    const skipped = queueInto({ folder: 'fld_mine', dirID: null, path: '/' }, [
      { file: file('a.txt'), relDir: '' },
      { file: file('b.txt'), relDir: 'photos/2026' },
    ]);
    expect(skipped).toBe(0);
    expect(add).toHaveBeenCalledTimes(1);
    const batch = add.mock.calls[0][0];
    expect(batch.map((b) => b.target)).toEqual([
      { folder: 'fld_mine', path: '/' },
      { folder: 'fld_mine', path: '/photos/2026', parents: true },
    ]);
  });

  it('puts a file into the directory shown by its ID, and counts a folder it cannot place as skipped', () => {
    const add = vi.spyOn(manager, 'add').mockImplementation(() => undefined);
    const skipped = queueInto({ folder: 'fld_t', dirID: 'nod_d', path: null }, [
      { file: file('a.txt'), relDir: '' },
      { file: file('b.txt'), relDir: 'sub' },
    ]);
    expect(skipped).toBe(1);
    expect(add.mock.calls[0][0].map((b) => b.target)).toEqual([{ folder: 'fld_t', parent: 'nod_d' }]);
  });

  it('queues nothing when nothing could be placed', () => {
    const add = vi.spyOn(manager, 'add').mockImplementation(() => undefined);
    expect(queueInto({ folder: 'fld_t', dirID: null, path: null }, [{ file: file('a.txt'), relDir: 'x' }])).toBe(1);
    expect(add).not.toHaveBeenCalled();
  });

  it('keeps the upload target of a place the same as the one the page gives (uploadTargetWhere)', () => {
    expect(uploadTargetWhere({ folder: 'f', dirID: null, path: '/' }, '')).toEqual({ folder: 'f', path: '/' });
    expect(uploadTargetWhere({ folder: 'f', dirID: 'd', path: '/x' }, '')).toEqual({ folder: 'f', parent: 'd' });
    expect(uploadTargetWhere({ folder: 'f', dirID: null, path: '/x' }, 'y')).toEqual({ folder: 'f', path: '/x/y', parents: true });
  });
});
