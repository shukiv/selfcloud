// SPDX-License-Identifier: AGPL-3.0-or-later

// The storage calls. Each is a thin call through api(); the server checks every rule. Ids in a path are encoded.
import { userWritable } from '../store';
import { sessionChanged } from '../session';
import { api } from '../api';
import type { Page } from '../files/types';
import { pageQuery, type MeStorage } from './logic';
import type { Breakdown } from './breakdown';

// myStorage is the caller's own storage, as the navigation meter shows it. null until read, and for an admin.
export const myStorage = userWritable<MeStorage | null>(null);

// latestMeterRequest numbers every GET /me call, of either kind. Only the last call started may set myStorage, so the meter
// always shows the figures of the last call started.
let latestMeterRequest = 0;

// latestBreakdownRequest numbers the breakdown reads alone. The breakdown has its own counter, so a plain refresh that starts
// later does not drop it: a breakdown answer sets myBreakdown unless a newer breakdown read has started (wf1; the shared
// counter dropped the breakdown whenever a sidebar refresh raced it).
let latestBreakdownRequest = 0;

// myBreakdown is the old versions and the trash of My files, read by the Storage page (decision 79). It is kept apart
// from myStorage, so a plain refresh of the meter does not clear it.
export const myBreakdown = userWritable<Breakdown | null>(null);

// refreshMyStorage reads GET /me and keeps only its storage block.
export async function refreshMyStorage(): Promise<void> {
  const changed = sessionChanged();
  const request = ++latestMeterRequest;
  const me = await api<{ storage?: MeStorage }>('GET', '/api/v1/me');
  if (request === latestMeterRequest && !changed()) myStorage.set(me.storage ?? null);
}

// refreshMyBreakdown reads GET /me with the breakdown asked for: the meter's figures, and the old versions and the trash.
// Its meter figures are set only when it is also the last call started, the same as a plain refresh, so an older breakdown
// never overwrites a newer plain refresh.
export async function refreshMyBreakdown(): Promise<void> {
  const changed = sessionChanged();
  const request = ++latestMeterRequest;
  const breakdownRequest = ++latestBreakdownRequest;
  const me = await api<{ storage?: MeStorage & { breakdown?: Breakdown } }>('GET', '/api/v1/me?storage=breakdown');
  if (changed()) return;
  const s = me.storage;
  if (request === latestMeterRequest) {
    myStorage.set(s ? { used_bytes: s.used_bytes, reserved_bytes: s.reserved_bytes, quota_bytes: s.quota_bytes, available_bytes: s.available_bytes } : null);
  }
  if (breakdownRequest === latestBreakdownRequest) myBreakdown.set(s?.breakdown ?? null);
}

// A file in a listing. in_trash is true when the file is in the trash, or below a trashed directory.
export interface LargestFile {
  id: string;
  name: string;
  size: number;
  versions_bytes: number;
  in_trash: boolean;
  modified_at: string;
  // The media type the file was stored with, and the name of the folder it sits in ("" or absent at the top of My files).
  mime_type?: string;
  parent_name?: string;
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

// myLargest reads one page of the caller's My files, largest first. kind narrows it to one kind of file, or to the trash
// ("" is every file).
export function myLargest(limit: number, cursor?: string | null, kind = ''): Promise<Page<LargestFile>> {
  return api<Page<LargestFile>>('GET', `/api/v1/me/storage/largest${pageQuery({ limit, cursor, extra: { kind } })}`);
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
