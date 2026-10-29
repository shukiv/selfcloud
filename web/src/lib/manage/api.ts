// SPDX-License-Identifier: AGPL-3.0-or-later

// The calls the owner and administration pages make. Each is a thin call through api(): the server checks
// every rule, and the pages add none. Errors are ApiErrors, shown by code.
import { api } from '../api';
import type { Page } from '../files/types';
import type { AuditEntry } from '../audit/sentence';
import type {
  Account,
  ApiKey,
  Group,
  GroupMember,
  Info,
  Job,
  NewKey,
  Person,
  PersonReply,
  Space,
  SpaceDetail,
  Usage,
} from './types';

export const PAGE_SIZE = 50;

function query(params: Record<string, string | number | undefined>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '') q.set(k, String(v));
  }
  const s = q.toString();
  return s === '' ? '' : `?${s}`;
}

const acc = (id: string) => `/api/v1/accounts/${id}`;

// ---- installation (admin) ----

export function listAccounts(p: { q?: string; status?: string; cursor?: string }): Promise<Page<Account>> {
  return api<Page<Account>>('GET', `/api/v1/accounts${query({ q: p.q, status: p.status, cursor: p.cursor, limit: PAGE_SIZE })}`);
}

export function getAccount(id: string): Promise<Account> {
  return api<Account>('GET', acc(id));
}

export interface NewAccount {
  name: string;
  quota_bytes: number;
  external_id?: string;
  owner: { email: string; display_name?: string; locale?: string; verification: 'email' | 'link' };
}

export function createAccount(body: NewAccount): Promise<Account & { owner?: PersonReply }> {
  return api<Account & { owner?: PersonReply }>('POST', '/api/v1/accounts', { body });
}

export function updateAccount(id: string, body: { name?: string; quota_bytes?: number; status?: 'active' | 'suspended' }): Promise<Account> {
  return api<Account>('PATCH', acc(id), { body });
}

// deleteAccount answers 202 with a job, which removes the account in the background.
export function deleteAccount(id: string): Promise<Job> {
  return api<Job>('DELETE', acc(id));
}

export function getJob(id: string): Promise<Job> {
  return api<Job>('GET', `/api/v1/jobs/${id}`);
}

export function listKeys(id: string): Promise<{ items: ApiKey[] }> {
  return api<{ items: ApiKey[] }>('GET', `${acc(id)}/keys`);
}

export function createKey(id: string, body: { name: string; expires_at?: string }): Promise<NewKey> {
  return api<NewKey>('POST', `${acc(id)}/keys`, { body });
}

export function revokeKey(id: string, key: string): Promise<void> {
  return api<void>('DELETE', `${acc(id)}/keys/${key}`);
}

export function listAdmins(cursor?: string): Promise<Page<Person>> {
  return api<Page<Person>>('GET', `/api/v1/admins${query({ cursor, limit: PAGE_SIZE })}`);
}

export function createAdmin(body: { email: string; display_name?: string; verification: 'email' | 'link' }): Promise<PersonReply> {
  return api<PersonReply>('POST', '/api/v1/admins', { body });
}

export function deleteAdmin(id: string): Promise<void> {
  return api<void>('DELETE', `/api/v1/admins/${id}`);
}

export function getInfo(): Promise<Info> {
  return api<Info>('GET', '/api/v1/info');
}

// ---- an account (owner session, or an admin) ----

export function usage(id: string): Promise<Usage> {
  return api<Usage>('GET', `${acc(id)}/usage`);
}

export function listPeople(id: string, cursor?: string): Promise<Page<Person>> {
  return api<Page<Person>>('GET', `${acc(id)}/users${query({ cursor, limit: PAGE_SIZE })}`);
}

export function getPerson(id: string, usr: string): Promise<Person> {
  return api<Person>('GET', `${acc(id)}/users/${usr}`);
}

export interface NewPerson {
  email: string;
  display_name?: string;
  locale?: string;
  role: 'owner' | 'member';
  verification: 'email' | 'link';
}

export function createPerson(id: string, body: NewPerson): Promise<PersonReply> {
  return api<PersonReply>('POST', `${acc(id)}/users`, { body });
}

export function updatePerson(
  id: string,
  usr: string,
  body: { role?: 'owner' | 'member'; status?: 'active' | 'disabled' },
): Promise<Person> {
  return api<Person>('PATCH', `${acc(id)}/users/${usr}`, { body });
}

// removePerson removes a person. When its My files hold files, the server refuses unless the call names
// transfer_to (an active member, which takes the files) or delete_files (which deletes them for good).
export function removePerson(id: string, usr: string, files?: { transfer_to?: string; delete_files?: boolean }): Promise<void> {
  return api<void>('DELETE', `${acc(id)}/users/${usr}${query({
    transfer_to: files?.transfer_to,
    delete_files: files?.delete_files ? 'true' : undefined,
  })}`);
}

export function resendVerification(id: string, usr: string, method: 'email' | 'link'): Promise<PersonReply> {
  return api<PersonReply>('POST', `${acc(id)}/users/${usr}/verification`, { body: { method } });
}

export function sendPasswordReset(id: string, usr: string): Promise<PersonReply> {
  return api<PersonReply>('POST', `${acc(id)}/users/${usr}/password-reset`, { body: { method: 'email' } });
}

export function listGroups(id: string, cursor?: string): Promise<Page<Group>> {
  return api<Page<Group>>('GET', `${acc(id)}/groups${query({ cursor, limit: PAGE_SIZE })}`);
}

export function createGroup(id: string, name: string): Promise<Group> {
  return api<Group>('POST', `${acc(id)}/groups`, { body: { name } });
}

export function renameGroup(id: string, grp: string, name: string): Promise<Group> {
  return api<Group>('PATCH', `${acc(id)}/groups/${grp}`, { body: { name } });
}

export function deleteGroup(id: string, grp: string): Promise<void> {
  return api<void>('DELETE', `${acc(id)}/groups/${grp}`);
}

export function listGroupMembers(id: string, grp: string, cursor?: string): Promise<Page<GroupMember>> {
  return api<Page<GroupMember>>('GET', `${acc(id)}/groups/${grp}/members${query({ cursor, limit: PAGE_SIZE })}`);
}

export function addGroupMember(id: string, grp: string, usr: string): Promise<void> {
  return api<void>('PUT', `${acc(id)}/groups/${grp}/members/${usr}`);
}

export function removeGroupMember(id: string, grp: string, usr: string): Promise<void> {
  return api<void>('DELETE', `${acc(id)}/groups/${grp}/members/${usr}`);
}

// listSpaces lists the team spaces of the account (the management list; no personal folders).
export function listSpaces(id: string, cursor?: string): Promise<Page<Space>> {
  return api<Page<Space>>('GET', `${acc(id)}/folders${query({ cursor, limit: PAGE_SIZE })}`);
}

// getSpace reads one team space with the totals of its tree: the files it holds and their bytes.
export function getSpace(id: string, fld: string): Promise<SpaceDetail> {
  return api<SpaceDetail>('GET', `${acc(id)}/folders/${fld}`);
}

export function createSpace(id: string, name: string): Promise<Space> {
  return api<Space>('POST', `${acc(id)}/folders`, { body: { name } });
}

export function renameSpace(id: string, fld: string, name: string): Promise<Space> {
  return api<Space>('PATCH', `${acc(id)}/folders/${fld}`, { body: { name } });
}

export function deleteSpace(id: string, fld: string): Promise<void> {
  return api<void>('DELETE', `${acc(id)}/folders/${fld}`);
}

export function auditLog(
  id: string,
  p: { action?: string; actor?: string; cursor?: string; limit?: number },
): Promise<Page<AuditEntry & { id: string; time: string }>> {
  return api<Page<AuditEntry & { id: string; time: string }>>(
    'GET',
    `${acc(id)}/audit${query({ action: p.action, actor: p.actor, cursor: p.cursor, limit: p.limit ?? PAGE_SIZE })}`,
  );
}
