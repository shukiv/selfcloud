// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { homeFor, mayOpen, targetOf } from './routes';
import type { User } from './types';

const owner: User = { id: 'usr_o', email: 'o@example.com', display_name: 'O', locale: 'en', role: 'owner', status: 'active' };
const member: User = { ...owner, id: 'usr_m', role: 'member' };
const admin: User = { ...owner, id: 'usr_a', role: 'admin' };

describe('targetOf', () => {
  it('reads the owner pages', () => {
    expect(targetOf('/manage')).toEqual({ area: 'manage', section: 'overview' });
    expect(targetOf('/manage/people')).toEqual({ area: 'manage', section: 'people' });
    expect(targetOf('/manage/groups/')).toEqual({ area: 'manage', section: 'groups' });
    expect(targetOf('/manage/spaces')).toEqual({ area: 'manage', section: 'spaces' });
    expect(targetOf('/manage/storage')).toEqual({ area: 'manage', section: 'storage' });
    expect(targetOf('/manage/audit')).toEqual({ area: 'manage', section: 'audit' });
  });

  it('reads the administration pages, and a tenant by its ID', () => {
    expect(targetOf('/admin')).toEqual({ area: 'admin', section: 'tenants', account: null });
    expect(targetOf('/admin/tenants')).toEqual({ area: 'admin', section: 'tenants', account: null });
    expect(targetOf('/admin/admins')).toEqual({ area: 'admin', section: 'admins', account: null });
    expect(targetOf('/admin/installation')).toEqual({ area: 'admin', section: 'installation', account: null });
    expect(targetOf('/admin/tenants/acc_123')).toEqual({ area: 'admin', section: 'tenant', account: 'acc_123' });
  });

  it('is null for a path that names no page', () => {
    expect(targetOf('/manage/nope')).toBeNull();
    expect(targetOf('/admin/tenants/a b')).toBeNull();
    expect(targetOf('/admin/tenants/acc_1/extra')).toBeNull();
    expect(targetOf('/admin/nope')).toBeNull();
    expect(targetOf('/files')).toBeNull();
  });
});

describe('mayOpen', () => {
  const manage = targetOf('/manage')!;
  const admins = targetOf('/admin/admins')!;

  it('opens the owner pages for an owner only', () => {
    expect(mayOpen(manage, owner)).toBe(true);
    expect(mayOpen(manage, member)).toBe(false);
    expect(mayOpen(manage, admin)).toBe(false);
    expect(mayOpen(manage, undefined)).toBe(false);
  });

  it('opens the administration pages for an installation admin only', () => {
    expect(mayOpen(admins, admin)).toBe(true);
    expect(mayOpen(admins, owner)).toBe(false);
    expect(mayOpen(admins, member)).toBe(false);
  });
});

describe('homeFor', () => {
  it('sends an admin to the tenants list and everyone else to the drive', () => {
    expect(homeFor(admin)).toBe('/admin/tenants');
    expect(homeFor(owner)).toBe('/files');
    expect(homeFor(undefined)).toBe('/files');
  });
});
