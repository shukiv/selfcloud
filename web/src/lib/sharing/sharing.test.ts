// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../apiError';
import { ancestorIDs, canRemoveHere, originOf } from './inherited';
import { grantableRoles, shareCap } from './roles';
import { COMBO_START, comboReducer, debounce, type ComboState } from './combobox';
import { REQUEST_SENT_KEY, requestOutcome } from './requests';
import { expiryFromInput, expiryLimit, dateInputValue } from './limits';
import { translate } from '../i18n';
import type { DirectoryEntry } from '../files/types';

describe('the role cap for the picker', () => {
  it('lets a manager grant every role, up to manager', () => {
    expect(grantableRoles(shareCap({ role: 'manager', editorsCanShare: false, managersOnly: true }))).toEqual([
      'viewer',
      'commenter',
      'contributor',
      'editor',
      'manager',
    ]);
  });

  it('lets an editor grant up to editor only where editors may share', () => {
    expect(grantableRoles(shareCap({ role: 'editor', editorsCanShare: true, managersOnly: false }))).toEqual([
      'viewer',
      'commenter',
      'contributor',
      'editor',
    ]);
    expect(grantableRoles(shareCap({ role: 'editor', editorsCanShare: false, managersOnly: false }))).toEqual([]);
  });

  it('gives an editor nothing where the space keeps sharing for managers', () => {
    expect(grantableRoles(shareCap({ role: 'editor', editorsCanShare: true, managersOnly: true }))).toEqual([]);
  });

  it('gives a contributor, a commenter or a viewer nothing', () => {
    for (const role of ['viewer', 'commenter', 'contributor'] as const) {
      expect(grantableRoles(shareCap({ role, editorsCanShare: true, managersOnly: false }))).toEqual([]);
    }
    expect(grantableRoles(shareCap({ role: undefined, editorsCanShare: true, managersOnly: false }))).toEqual([]);
  });
});

describe('the inherited label', () => {
  it('shows nothing for a permission on the item itself', () => {
    expect(originOf({ inherited_from: null }, new Map())).toEqual({ kind: 'own' });
  });

  it('names an ancestor the caller can read', () => {
    const names = new Map([['nod_parent01', 'Reports']]);
    expect(originOf({ inherited_from: 'nod_parent01' }, names)).toEqual({ kind: 'named', id: 'nod_parent01', name: 'Reports' });
  });

  it('says only "a parent folder" for an ancestor the caller cannot read', () => {
    expect(originOf({ inherited_from: 'nod_hidden01' }, new Map())).toEqual({ kind: 'parent', id: 'nod_hidden01' });
  });

  it('offers removal only on the item itself', () => {
    expect(canRemoveHere({ inherited_from: null })).toBe(true);
    expect(canRemoveHere({ inherited_from: 'nod_parent01' })).toBe(false);
  });

  it('lists each ancestor once', () => {
    expect(
      ancestorIDs([{ inherited_from: 'nod_a' }, { inherited_from: null }, { inherited_from: 'nod_a' }, { inherited_from: 'nod_b' }]),
    ).toEqual(['nod_a', 'nod_b']);
  });
});

describe('the people combobox', () => {
  const ann: DirectoryEntry = { id: 'usr_ann0000001', kind: 'user', name: 'Ann', email: 'ann@example.com' };
  const fin: DirectoryEntry = { id: 'grp_fin0000001', kind: 'group', name: 'Finance' };

  function typed(q: string, from: ComboState = COMBO_START): ComboState {
    return comboReducer(from, { type: 'type', query: q });
  }

  it('stays closed under two characters', () => {
    const s = typed('a');
    expect(s.open).toBe(false);
    expect(s.status).toBe('idle');
  });

  it('opens and loads on a query of two characters', () => {
    const s = typed('an');
    expect(s.open).toBe(true);
    expect(s.status).toBe('loading');
  });

  it('shows results, with the first option active', () => {
    let s = typed('an');
    s = comboReducer(s, { type: 'results', query: 'an', items: [ann, fin] });
    expect(s.status).toBe('ready');
    expect(s.active).toBe(0);
  });

  it('drops an answer to a query that is no longer there', () => {
    let s = typed('an');
    s = typed('ann', s);
    const stale = comboReducer(s, { type: 'results', query: 'an', items: [ann] });
    expect(stale).toBe(s);
    expect(stale.status).toBe('loading');
  });

  it('moves the active option with the arrows and wraps at both ends', () => {
    let s = comboReducer(typed('an'), { type: 'results', query: 'an', items: [ann, fin] });
    s = comboReducer(s, { type: 'move', delta: 1 });
    expect(s.active).toBe(1);
    s = comboReducer(s, { type: 'move', delta: 1 });
    expect(s.active).toBe(0);
    s = comboReducer(s, { type: 'move', delta: -1 });
    expect(s.active).toBe(1);
  });

  it('chooses the active option and closes', () => {
    let s = comboReducer(typed('an'), { type: 'results', query: 'an', items: [ann, fin] });
    s = comboReducer(s, { type: 'move', delta: 1 });
    s = comboReducer(s, { type: 'choose' });
    expect(s.chosen).toEqual(fin);
    expect(s.query).toBe('Finance');
    expect(s.open).toBe(false);
  });

  it('forgets a choice when the person types again', () => {
    let s = comboReducer(typed('an'), { type: 'results', query: 'an', items: [ann] });
    s = comboReducer(s, { type: 'choose' });
    s = typed('annx', s);
    expect(s.chosen).toBeNull();
  });

  it('reopens a closed list with items, and leaves an empty one closed', () => {
    let s = comboReducer(typed('an'), { type: 'results', query: 'an', items: [ann] });
    s = comboReducer(s, { type: 'close' });
    expect(comboReducer(s, { type: 'open' }).open).toBe(true);
    // With nothing to show, reopening changes nothing: the list is never shown empty.
    const loading = typed('zz');
    expect(comboReducer(loading, { type: 'open' })).toBe(loading);
  });

  it('reports a failed search as an error, not as no matches', () => {
    let s = typed('an');
    s = comboReducer(s, { type: 'fail', query: 'an' });
    expect(s.status).toBe('error');
    expect(s.items).toEqual([]);
  });

  it('clears to the start state', () => {
    expect(comboReducer(typed('an'), { type: 'clear' })).toEqual(COMBO_START);
  });

  // Decision 96: focusing the field under two characters opens the account's list, the answer to an empty q.
  const list = (items: DirectoryEntry[], more = false) => ({ type: 'listed' as const, items, more });

  it('focus on an empty field opens the list, loading until the answer comes', () => {
    const s = comboReducer(COMBO_START, { type: 'focus' });
    expect(s.open).toBe(true);
    expect(s.status).toBe('loading');
    const got = comboReducer(s, list([ann, fin]));
    expect(got.status).toBe('ready');
    expect(got.items).toEqual([ann, fin]);
    expect(got.open).toBe(true);
    // No option is active until an arrow key is pressed, so Enter right after the focus picks nothing (review L3).
    expect(got.active).toBe(-1);
  });

  it('an arrow from no option goes to the first option, and the up arrow to the last', () => {
    let s = comboReducer(COMBO_START, { type: 'focus' });
    s = comboReducer(s, list([ann, fin]));
    expect(s.active).toBe(-1);
    expect(comboReducer(s, { type: 'move', delta: 1 }).active).toBe(0);
    expect(comboReducer(s, { type: 'move', delta: -1 }).active).toBe(1);
  });

  it('reopens the cached list without a second answer', () => {
    let s = comboReducer(COMBO_START, { type: 'focus' });
    s = comboReducer(s, list([ann], true));
    s = comboReducer(s, { type: 'close' });
    s = comboReducer(s, { type: 'focus' });
    expect(s.open).toBe(true);
    expect(s.items).toEqual([ann]);
    expect(s.more).toBe(true);
    expect(s.status).toBe('ready');
  });

  it('shows the list for one character, and searches from two', () => {
    let s = comboReducer(COMBO_START, { type: 'focus' });
    s = comboReducer(s, list([ann, fin]));
    s = typed('a', s);
    expect(s.open).toBe(true);
    expect(s.items).toEqual([ann, fin]);
    s = typed('an', s);
    expect(s.status).toBe('loading');
    expect(s.items).toEqual([]);
  });

  it('shows the focus list again when the query is cleared below two characters', () => {
    let s = comboReducer(COMBO_START, { type: 'focus' });
    s = comboReducer(s, list([ann, fin]));
    s = typed('an', s);
    s = comboReducer(s, { type: 'results', query: 'an', items: [fin] });
    s = typed('', s);
    expect(s.open).toBe(true);
    expect(s.items).toEqual([ann, fin]);
  });

  it('drops a late focus answer once two characters are typed', () => {
    let s = comboReducer(COMBO_START, { type: 'focus' });
    s = typed('an', s);
    const late = comboReducer(s, list([ann, fin], true));
    expect(late.items).toEqual([]);
    expect(late.status).toBe('loading');
    expect(late.open).toBe(true);
  });

  it('a failed focus answer shows an error while the field is short, and not after', () => {
    let s = comboReducer(COMBO_START, { type: 'focus' });
    s = comboReducer(s, { type: 'listfail' });
    expect(s.status).toBe('error');
    expect(s.items).toEqual([]);
    const typing = typed('an', comboReducer(COMBO_START, { type: 'focus' }));
    expect(comboReducer(typing, { type: 'listfail' })).toBe(typing);
  });

  it('a session change drops the cached list', () => {
    let s = comboReducer(COMBO_START, { type: 'focus' });
    s = comboReducer(s, list([ann]));
    s = comboReducer(s, { type: 'clear' });
    expect(s.focus).toBeNull();
    expect(comboReducer(s, { type: 'focus' }).status).toBe('loading');
  });

  it('a field that was never focused stays closed under two characters, even with a cached list', () => {
    // A list answered without a focus (as the mention picker's field never asks for one) is not opened by a short query.
    const s = comboReducer(COMBO_START, list([ann]));
    expect(typed('a', s).open).toBe(false);
  });

  it('carries the more flag of a search answer', () => {
    let s = comboReducer(typed('an'), { type: 'results', query: 'an', items: [ann], more: true });
    expect(s.more).toBe(true);
    s = comboReducer(typed('an'), { type: 'results', query: 'an', items: [ann] });
    expect(s.more).toBe(false);
  });
});

describe('debounce', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('runs once, after the last call', () => {
    const fn = vi.fn();
    const d = debounce(fn, 250);
    d.call('a');
    vi.advanceTimersByTime(100);
    d.call('ab');
    vi.advanceTimersByTime(249);
    expect(fn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith('ab');
  });

  it('cancel drops a call that has not run', () => {
    const fn = vi.fn();
    const d = debounce(fn, 250);
    d.call('x');
    d.cancel();
    vi.advanceTimersByTime(1000);
    expect(fn).not.toHaveBeenCalled();
  });
});

describe('the access-request confirmation', () => {
  it('reads the same whatever the server answered about the item', () => {
    // The server answers 202 whether or not the item exists or is reachable, and the form must not say which.
    for (const status of [202, 403, 404, 409]) {
      expect(requestOutcome(status === 202 ? undefined : new ApiError(status, 'not_found', {}, 0))).toBe('sent');
    }
    expect(requestOutcome(new ApiError(403, 'forbidden', {}, 0))).toBe('sent');
    expect(requestOutcome(new ApiError(404, 'not_found', {}, 0))).toBe('sent');
  });

  it('shows the one confirmation text in every language', () => {
    const en = translate('en', REQUEST_SENT_KEY);
    expect(en).toBe('If this item exists, the people who manage it have been asked.');
    for (const l of ['es', 'fr', 'pt', 'ru', 'ar', 'he'] as const) {
      expect(translate(l, REQUEST_SENT_KEY)).not.toBe(REQUEST_SENT_KEY);
    }
  });

  it('reports a rate limit and a lost connection apart from the item', () => {
    expect(requestOutcome(new ApiError(429, 'rate_limited', {}, 60))).toBe('limited');
    expect(requestOutcome(new ApiError(0, 'network', {}, 0))).toBe('failed');
    expect(requestOutcome(new Error('x'))).toBe('failed');
  });

  // A request that timed out may have been received: the page must not say it failed (guard: a write's timeout is unsure).
  it('says a timed-out request is unsure, since the server may have received it', () => {
    expect(requestOutcome(new ApiError(0, 'timeout', { write: true }, 0))).toBe('unsure');
    expect(requestOutcome(new ApiError(0, 'network', {}, 0))).toBe('failed');
  });
});

describe('expiry', () => {
  it('has no limit when the cap is zero', () => {
    expect(expiryLimit(0, new Date())).toBeNull();
  });

  it('counts the cap in days from now', () => {
    const now = new Date(2026, 9, 29, 12, 0, 0);
    expect(dateInputValue(expiryLimit(30, now)!)).toBe('2026-11-28');
  });

  it('ends a chosen day at its last second, and an empty value never ends', () => {
    const iso = expiryFromInput('2026-11-28');
    expect(iso).not.toBeNull();
    expect(new Date(iso!).getDate()).toBe(28);
    expect(expiryFromInput('')).toBeNull();
    expect(expiryFromInput('28/11/2026')).toBeNull();
  });
});
