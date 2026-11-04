// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { LOCK } from './locks';

// A lock key names the action's kind and its parent. Two actions on one row must not share a lock (a share and a delete of
// one space both run), and one user in two groups must not share one (removing them from one group does not block the other).
describe('the lock keys of the management pages', () => {
  it('gives two different actions on one row different keys', () => {
    expect(LOCK.spaceShare('fld_1')).not.toBe(LOCK.spaceDelete('fld_1'));
    expect(LOCK.spaceShare('fld_1')).not.toBe(LOCK.spaceOpenDelete('fld_1'));
    expect(LOCK.spaceOpenDelete('fld_1')).not.toBe(LOCK.spaceDelete('fld_1'));
    expect(LOCK.groupAdd('grp_1')).not.toBe(LOCK.groupDelete('grp_1'));
  });

  it('gives one user in two groups different keys', () => {
    expect(LOCK.groupMember('grp_a', 'usr_1')).not.toBe(LOCK.groupMember('grp_b', 'usr_1'));
  });

  it('gives the same action on different rows different keys', () => {
    expect(LOCK.spaceDelete('fld_1')).not.toBe(LOCK.spaceDelete('fld_2'));
    expect(LOCK.person('usr_1')).not.toBe(LOCK.person('usr_2'));
  });
});
