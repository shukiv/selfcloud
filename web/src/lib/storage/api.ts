// SPDX-License-Identifier: AGPL-3.0-or-later

// The storage calls. Each is a thin call through api(); the server checks every rule. Ids in a path are encoded.
import { userWritable } from '../store';
import { sessionChanged } from '../session';
import { api } from '../api';
import type { Page } from '../files/types';
import { pageQuery, type MeStorage } from './logic';

// myStorage is the caller's own storage, as the navigation meter shows it. null until read, and for an admin.
export const myStorage = userWritable<MeStorage | null>(null);

// refreshMyStorage reads GET /me and keeps only its storage block.
export async function refreshMyStorage(): Promise<void> {
  const changed = sessionChanged();
  const me = await api<{ storage?: MeStorage }>('GET', '/api/v1/me');
  if (!changed()) myStorage.set(me.storage ?? null);
}

// A file in a listing. in_trash is true when the file is in the trash, or below a trashed directory.
export interface LargestFile {
  id: string;
  name: string;
  size: number;
  versions_bytes: number;
  in_trash: boolean;
  modified_at: string;
}

// A file of a team space, as the account's largest list names it.
export interface SpaceFile extends LargestFile {
  folder_id: string;
  folder_name: string;
}

// The account's storage block, for the account, and per person and team space for the owner and the key.
export interface AccountStorage {
  account: {
    used_bytes: number;
    reserved_bytes: number;
    quota_bytes: number | null;
    available_bytes: number | null;
    user_quota_bytes: number;
  };
  folders?: Array<{
    id: string;
    name?: string;
    kind: 'team' | 'personal';
    used_bytes: number;
    used_files: number;
    reserved_bytes: number;
  }>;
  users?: StoragePerson[];
  users_next_cursor?: string | null;
}

export interface StoragePerson {
  id: string;
  display_name: string;
  email: string;
  status: string;
  used_bytes: number;
  reserved_bytes: number;
  quota_bytes: number | null;
  effective_quota_bytes: number | null;
}

const acc = (id: string) => `/api/v1/accounts/${encodeURIComponent(id)}`;

// myLargest reads one page of the caller's My files, largest first.
export function myLargest(limit: number, cursor?: string | null): Promise<Page<LargestFile>> {
  return api<Page<LargestFile>>('GET', `/api/v1/me/storage/largest${pageQuery({ limit, cursor })}`);
}

// accountStorage reads the account's totals. An owner or the key also gets the team spaces and a page of people.
export function accountStorage(id: string, usersCursor?: string | null, usersLimit?: number): Promise<AccountStorage> {
  const q = new URLSearchParams();
  if (usersLimit !== undefined) q.set('users_limit', String(usersLimit));
  if (usersCursor) q.set('users_cursor', usersCursor);
  const s = q.toString();
  return api<AccountStorage>('GET', `${acc(id)}/storage${s === '' ? '' : `?${s}`}`);
}

// accountLargest reads one page of the largest files of the account's team spaces. folder narrows it to one space.
export function accountLargest(id: string, opts: { folder?: string; limit?: number; cursor?: string | null }): Promise<Page<SpaceFile>> {
  return api<Page<SpaceFile>>(
    'GET',
    `${acc(id)}/storage/largest${pageQuery({ limit: opts.limit, cursor: opts.cursor, extra: { folder: opts.folder } })}`,
  );
}

// setPolicyDefault sets the limit each person gets by default. 0 means no default limit.
export function setPolicyDefault(id: string, userQuotaBytes: number): Promise<{ user_quota_bytes: number }> {
  return api<{ user_quota_bytes: number }>('PATCH', `${acc(id)}/storage-policy`, { body: { user_quota_bytes: userQuotaBytes } });
}

// setPersonLimit sets one person's own limit: null follows the default, 0 is no limit.
export function setPersonLimit(id: string, usr: string, quotaBytes: number | null): Promise<unknown> {
  return api<unknown>('PATCH', `${acc(id)}/users/${encodeURIComponent(usr)}`, { body: { quota_bytes: quotaBytes } });
}
