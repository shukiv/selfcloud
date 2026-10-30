// SPDX-License-Identifier: AGPL-3.0-or-later

import type { User } from './types';

// The owner and administration pages, and the rule that decides who may open each one. The server checks
// every call; this only decides which page a person is shown. A page the person may not open is the
// not-found view, not a redirect (docs/design.md, section 10).

export type ManageSection = 'overview' | 'people' | 'groups' | 'spaces' | 'storage' | 'links' | 'audit';
export type AdminSection = 'tenants' | 'tenant' | 'admins' | 'installation';

export type Target =
  | { area: 'manage'; section: ManageSection }
  | { area: 'admin'; section: AdminSection; account: string | null };

const MANAGE: Record<string, ManageSection> = {
  '/manage': 'overview',
  '/manage/people': 'people',
  '/manage/groups': 'groups',
  '/manage/spaces': 'spaces',
  '/manage/storage': 'storage',
  '/manage/links': 'links',
  '/manage/audit': 'audit',
};

// targetOf reads a path (without its query) as one of the owner or administration pages. It returns null
// for any other path, and for a path under /manage or /admin that names no page. An administration tenant
// is /admin/tenants/{acc}, and its account ID must be a plain identifier.
export function targetOf(path: string): Target | null {
  const clean = path.length > 1 && path.endsWith('/') ? path.slice(0, -1) : path;
  if (clean === '/manage' || clean.startsWith('/manage/')) {
    const section = MANAGE[clean];
    return section ? { area: 'manage', section } : null;
  }
  if (clean === '/admin' || clean === '/admin/tenants') {
    return { area: 'admin', section: 'tenants', account: null };
  }
  if (clean === '/admin/admins') return { area: 'admin', section: 'admins', account: null };
  if (clean === '/admin/installation') return { area: 'admin', section: 'installation', account: null };
  const tenant = /^\/admin\/tenants\/([A-Za-z0-9_]+)$/.exec(clean);
  if (tenant) return { area: 'admin', section: 'tenant', account: tenant[1] };
  return null;
}

// mayOpen says whether the person may see a page. The owner pages are for an account owner; the
// administration pages are for an installation admin. An admin has no account, so it never sees the owner
// pages, and an owner is not an admin.
export function mayOpen(target: Target, user: User | undefined): boolean {
  if (!user) return false;
  return target.area === 'manage' ? user.role === 'owner' : user.role === 'admin';
}

// homeFor is the page the root address opens for a person: an admin's home is the tenants list, since an
// admin has no drive of its own.
export function homeFor(user: User | undefined): string {
  return user?.role === 'admin' ? '/admin/tenants' : '/files';
}
