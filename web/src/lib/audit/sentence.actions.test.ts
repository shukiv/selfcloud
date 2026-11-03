// SPDX-License-Identifier: AGPL-3.0-or-later

import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { isolateText, renderSentence, sentence, type AuditEntry, type Names } from './sentence';

// Every audit action the server writes reads as its own sentence (slice audit-s1), with full details and with none. A
// sentence never shows a raw ID, a placeholder or "undefined", and a name the row holds is shown as text.
const en = JSON.parse(readFileSync(join(process.cwd(), 'src/i18n/en.json'), 'utf8')) as Record<string, string>;

// translate fills the English text the way the page does. A parameter the sentence did not pass is kept as a marker,
// so a missing one shows up in the output instead of reading as an empty word.
const translate = (key: string, params?: Record<string, string>): string => {
  const text = en[key] ?? key;
  return text.replace(/\{(\w+)\}/g, (_, k: string) => (params?.[k] !== undefined ? params[k] : `<<missing ${k}>>`));
};

const names: Names = {
  person: (id) => (id === 'usr_mateo' ? 'Mateo Member' : id === 'usr_ada' ? 'Ada Admin' : undefined),
  space: () => undefined,
};

const read = (e: AuditEntry) => renderSentence(sentence(e, names), translate);

const user = { kind: 'user', id: 'usr_ada' };
const url = 'https://hooks.example.com';

interface Row {
  action: string;
  actor: AuditEntry['actor'];
  target: string;
  details: Record<string, unknown>;
}

// One row per action with the details the server writes for it (see the writers in internal/api and internal/sweep).
const ROWS: Row[] = [
  { action: 'access.approve', actor: user, target: 'arq_1', details: { node: 'nod_1', user: 'usr_mateo', role: 'viewer' } },
  { action: 'access.deny', actor: user, target: 'arq_1', details: { node: 'nod_1', user: 'usr_mateo', role: 'viewer' } },
  { action: 'account.comments_policy', actor: user, target: 'acc_1', details: { comments: true } },
  { action: 'account.quota_warning', actor: user, target: 'acc_1', details: { percent: 80 } },
  { action: 'account.sharing_policy', actor: user, target: 'acc_1', details: { public_links: false } },
  { action: 'account.storage_policy', actor: user, target: 'acc_1', details: { user_quota_bytes: 0 } },
  { action: 'auth.sign_in_link', actor: { kind: 'user', id: 'usr_mateo' }, target: 'usr_mateo', details: {} },
  { action: 'comment.create', actor: user, target: 'nod_1', details: { comment: 'cmt_1', reply: true, mentions: 1 } },
  { action: 'comment.delete', actor: user, target: 'nod_1', details: { comment: 'cmt_1', tombstone: false } },
  { action: 'comment.edit', actor: user, target: 'nod_1', details: { comment: 'cmt_1' } },
  { action: 'comment.resolve', actor: user, target: 'nod_1', details: { comment: 'cmt_1', resolved: true } },
  { action: 'label_field.create', actor: user, target: 'lbf_1', details: { type: 'choice', restricted: false, choices: 2 } },
  { action: 'label_field.delete', actor: user, target: 'lbf_1', details: { values: 3 } },
  { action: 'label_field.update', actor: user, target: 'lbf_1', details: { changed: ['restricted'], values: 3 } },
  {
    action: 'link.create',
    actor: user,
    target: 'shl_1',
    details: { audience: 'anyone', upload_only: false, role: 'viewer', password_set: true },
  },
  { action: 'link.delete', actor: user, target: 'shl_1', details: { audience: 'organisation', role: 'viewer', upload_only: false } },
  { action: 'link.delete_public', actor: user, target: 'acc_1', details: { count: 2 } },
  {
    action: 'link.expired',
    actor: { kind: 'system', id: 'sweep' },
    target: 'shl_1',
    details: { audience: 'anyone', upload_only: false, expires_at: '2026-10-01T00:00:00Z', uploads: 0 },
  },
  { action: 'link.open', actor: { kind: 'share_link', id: 'shl_1' }, target: 'shl_1', details: {} },
  { action: 'link.reset', actor: user, target: 'shl_1', details: { upload_only: false } },
  { action: 'link.throttled', actor: { kind: 'share_link', id: 'shl_1' }, target: 'shl_1', details: { reason: 'password_lockout' } },
  { action: 'link.update', actor: user, target: 'shl_1', details: { audience: 'anyone', role: 'viewer' } },
  { action: 'node.appearance', actor: user, target: 'nod_1', details: { color: '#ff0000', description: 'changed' } },
  { action: 'node.copy', actor: user, target: 'nod_3', details: { source: 'nod_2', nodes: 3, bytes: 100 } },
  { action: 'node.create', actor: user, target: 'nod_1', details: { kind: 'file', name: 'Report.pdf', parent: 'nod_p', size: 10 } },
  { action: 'node.create', actor: user, target: 'nod_1', details: { kind: 'dir', name: 'Photos', parent: 'nod_p' } },
  { action: 'node.label_clear', actor: user, target: 'nod_1', details: { field: 'lbf_1' } },
  { action: 'node.label_set', actor: user, target: 'nod_1', details: { field: 'lbf_1', type: 'choice' } },
  { action: 'node.link', actor: user, target: 'nod_1', details: { disposition: 'inline', ttl_seconds: 3600, expires_at: '2026-11-01T00:00:00Z' } },
  { action: 'node.lock', actor: user, target: 'nod_1', details: { session: 'wop_1' } },
  { action: 'node.lock', actor: user, target: 'nod_1', details: { session: 'wop_1', relock: true } },
  {
    action: 'node.move',
    actor: user,
    target: 'nod_1',
    details: {
      from: { parent: 'nod_p', folder: 'fld_1', name: 'a.txt' },
      to: { parent: 'nod_q', folder: 'fld_1', name: 'b.txt' },
    },
  },
  { action: 'node.office', actor: user, target: 'nod_1', details: { mode: 'edit', session: 'wop_1' } },
  { action: 'node.rename', actor: user, target: 'nod_1', details: { from: 'a.txt', to: 'b.txt' } },
  { action: 'node.shortcut', actor: user, target: 'nod_s', details: { name: 'Shortcut to a', parent: 'nod_1' } },
  { action: 'node.unlock', actor: { kind: 'system', id: '' }, target: 'nod_1', details: { reason: 'expired' } },
  { action: 'node.unlock', actor: user, target: 'nod_1', details: { reason: 'editor' } },
  { action: 'node.unlock', actor: user, target: 'nod_1', details: { reason: 'relock' } },
  { action: 'node.version.delete', actor: user, target: 'nod_1', details: { version: 'ver_1', size: 5 } },
  { action: 'node.version.restore', actor: user, target: 'nod_1', details: { version: 'ver_1', size: 5, previous_size: 6 } },
  { action: 'node.version.update', actor: user, target: 'nod_1', details: { version: 'ver_1', changes: { label: 'x' } } },
  { action: 'node.versions_pruned', actor: { kind: 'system', id: 'sweep' }, target: 'nod_1', details: { versions: 2, bytes: 10, reason: 'retention' } },
  { action: 'node.write', actor: user, target: 'nod_1', details: { size: 10, previous_size: 9 } },
  { action: 'node.zip', actor: user, target: 'zip_1', details: { nodes: ['nod_1'], entries: 3, skipped: 0, bytes: 10 } },
  { action: 'upload.create', actor: user, target: 'upl_1', details: { folder: 'fld_1', name: 'Big.iso', length: 10 } },
  { action: 'upload.delete', actor: user, target: 'upl_1', details: { reason: 'terminated', length: 10, durable_offset: 5 } },
  { action: 'upload.expire', actor: { kind: 'system', id: 'sweep' }, target: 'upl_1', details: { name: 'Big.iso', length: 10, durable_offset: 5 } },
  { action: 'user.files_moved', actor: user, target: 'usr_gone', details: { to: 'usr_mateo' } },
  { action: 'user.login', actor: { kind: 'user', id: 'usr_mateo' }, target: 'usr_mateo', details: {} },
  { action: 'user.login_failed', actor: { kind: 'user', id: 'usr_mateo' }, target: 'usr_mateo', details: {} },
  { action: 'user.logout', actor: { kind: 'user', id: 'usr_mateo' }, target: 'usr_mateo', details: {} },
  { action: 'user.sign_in_link', actor: user, target: 'usr_mateo', details: {} },
  { action: 'user.tokens_revoke', actor: user, target: 'usr_mateo', details: { user: 'usr_mateo', tokens_not_before: '2026-10-31T10:00:00Z' } },
  { action: 'webhook.create', actor: user, target: 'wh_1', details: { url, events: ['file.created'] } },
  { action: 'webhook.delete', actor: user, target: 'wh_1', details: { url } },
  { action: 'webhook.disabled', actor: { kind: 'system', id: 'webhooks' }, target: 'wh_1', details: { reason: 'failing', url } },
  { action: 'webhook.retry', actor: user, target: 'wh_1', details: { url } },
  { action: 'webhook.rewrap', actor: { kind: 'system', id: 'cli' }, target: 'wh_1', details: { previous: true } },
  { action: 'webhook.secret', actor: user, target: 'wh_1', details: { url } },
  { action: 'webhook.test', actor: user, target: 'wh_1', details: { url } },
  { action: 'webhook.update', actor: user, target: 'wh_1', details: { url, events: ['file.created'], state: 'active' } },
  // Rows that name a thing the row may lack: the empty-details run reads them with no name and no person.
  { action: 'folder.create', actor: user, target: 'fld_fin', details: { name: 'Finance' } },
  { action: 'folder.update', actor: user, target: 'fld_fin', details: { name: 'Finance' } },
  { action: 'folder.delete', actor: user, target: 'fld_fin', details: { name: 'Finance' } },
  { action: 'folder.settings', actor: user, target: 'fld_fin', details: { name: 'Finance' } },
  { action: 'group.create', actor: user, target: 'grp_1', details: { name: 'Staff' } },
  { action: 'group.update', actor: user, target: 'grp_1', details: { name: 'Staff' } },
  { action: 'group.delete', actor: user, target: 'grp_1', details: { name: 'Staff' } },
  { action: 'account.create', actor: user, target: 'acc_1', details: { name: 'Acme' } },
  { action: 'admin.create', actor: user, target: 'usr_gone', details: {} },
  { action: 'admin.delete', actor: user, target: 'usr_gone', details: {} },
  { action: 'user.verify', actor: user, target: 'usr_gone', details: {} },
];

// The actions that had no sentence before slice audit-s1 (the list the test of the server kept). Each must have a row.
const OLDER = [
  'access.approve', 'access.deny', 'account.comments_policy', 'account.quota_warning', 'account.sharing_policy',
  'account.storage_policy', 'auth.sign_in_link', 'comment.create', 'comment.delete', 'comment.edit', 'comment.resolve',
  'label_field.create', 'label_field.delete', 'label_field.update', 'link.create', 'link.delete', 'link.delete_public',
  'link.expired', 'link.open', 'link.reset', 'link.throttled', 'link.update', 'node.appearance', 'node.copy', 'node.create',
  'node.label_clear', 'node.label_set', 'node.link', 'node.lock', 'node.move', 'node.office', 'node.rename', 'node.shortcut',
  'node.unlock', 'node.version.delete', 'node.version.restore', 'node.version.update', 'node.versions_pruned', 'node.write',
  'node.zip', 'upload.create', 'upload.delete', 'upload.expire', 'user.files_moved', 'user.login', 'user.login_failed',
  'user.logout', 'user.sign_in_link', 'user.tokens_revoke', 'webhook.create', 'webhook.delete', 'webhook.disabled',
  'webhook.retry', 'webhook.rewrap', 'webhook.secret', 'webhook.test', 'webhook.update',
];

// A raw ID is a prefix and an underscore, as the server makes them (usr_, nod_, shl_ and so on).
const RAW_ID = /\b(usr|nod|fld|shl|upl|wh|grp|acc|arq|key|sil|job|zip|cmt|ver|lbf|wop|shc)_[A-Za-z0-9]/;
const LEFTOVER = /<<missing|\{[a-z_]+\}|undefined|null|NaN|\[object/;

// A sentence starts with a capital, or with a name, which the renderer isolates and which keeps its own case.
const STARTS = /^(⁨|\p{Lu})/u;
// The English generic words. A word sits next to the noun it repeats in "the team space a team space" only when a row
// lacks its name, so each generic word is checked against its own noun and against itself.
const GENERIC = [
  'a team space', 'a group', 'a tenant', 'a person', 'a file', 'a folder', 'an item', 'another user', 'a web address',
  'an installation admin', 'an admin',
];

function expectSentence(text: string, action: string) {
  expect(text, action).not.toBe('');
  expect(text, action).not.toMatch(LEFTOVER);
  expect(text, action).not.toMatch(RAW_ID);
  expect(text, action).not.toContain('made a change');
  expect(text, action).toMatch(STARTS);
  const plain = text.replace(/[⁨⁩]/g, '').toLowerCase();
  for (const g of GENERIC) {
    const noun = g.replace(/^(a|an|another) /, '');
    expect(plain, action).not.toContain(`${noun} ${g}`);
    expect(plain, action).not.toContain(`${g} ${g}`);
  }
}

describe('every audit action has a sentence of its own', () => {
  it('has a row for each of the actions that had none', () => {
    const rowed = new Set(ROWS.map((r) => r.action));
    expect(OLDER.filter((a) => !rowed.has(a))).toEqual([]);
  });

  for (const row of ROWS) {
    it(`${row.action} reads with its full details`, () => {
      const e = { action: row.action, actor: row.actor, target: row.target, details: row.details };
      const s = sentence(e, names);
      expect(s.key, row.action).not.toBe('audit.unlisted');
      expect(en[s.key], s.key).toBeDefined();
      expectSentence(read(e), row.action);
    });

    it(`${row.action} reads with empty details`, () => {
      const e = { action: row.action, actor: row.actor, target: row.target, details: {} };
      expectSentence(read(e), row.action);
    });
  }
});

describe('sentences fall back when a field is missing', () => {
  const at = (action: string, details: Record<string, unknown>, actor = user) =>
    read({ action, actor, target: 'nod_1', details });

  it('names a file or folder, and says so when the row lacks the name', () => {
    expect(at('node.create', { kind: 'file', name: 'Report.pdf' })).toBe(`${isolateText('Ada Admin')} added ${isolateText('Report.pdf')}.`);
    expect(at('node.create', { kind: 'file' })).toContain('added a file.');
    expect(at('node.create', { kind: 'dir' })).toContain('created a folder.');
  });

  it('reads an upload sent through a share link as sent by someone', () => {
    const text = read({
      action: 'node.create',
      actor: { kind: 'share_link', id: 'shl_1' },
      target: 'nod_1',
      details: { kind: 'file', name: 'Scan.pdf', via: 'share_link', uploader_name: 'Visitor', uploader_email: 'v@example.com' },
    });
    expect(text).toBe(`Someone sent ${isolateText('Scan.pdf')} through a link.`);
    expect(text).not.toContain('v@example.com');
  });

  it('chooses the rename, shortcut and comment sentence by what the row holds', () => {
    expect(at('node.rename', { from: 'a.txt' })).toContain('renamed an item.');
    expect(at('node.shortcut', {})).toContain('made a shortcut.');
    expect(at('comment.resolve', { comment: 'cmt_1' })).toContain('changed a comment');
    expect(at('comment.resolve', { resolved: false })).toContain('reopened');
  });

  it('gives the recipient of moved files a generic word when the page does not know them', () => {
    expect(at('user.files_moved', {})).toContain('another user');
    expect(at('user.files_moved', { to: 'usr_gone' })).toContain('another user');
  });

  it('shows a quota warning level and a storage default as the row has them', () => {
    expect(at('account.quota_warning', { percent: 80 }, user)).toContain(`${isolateText('80')} percent.`);
    expect(at('account.quota_warning', {})).toContain('changed the quota warning level');
    expect(at('account.storage_policy', { user_quota_bytes: 0 })).toContain('removed the default limit');
    expect(at('account.storage_policy', { user_quota_bytes: 5000 })).toContain('changed the default limit');
  });

  it('says a refused sign-in link was refused', () => {
    const e: AuditEntry = { action: 'auth.sign_in_link', actor: { kind: 'user', id: 'usr_mateo' }, target: 'usr_mateo', details: {}, outcome: 'failed' };
    expect(read(e)).toContain('was refused');
    expect(read({ ...e, outcome: undefined })).toContain('signed in with a sign-in link');
  });

  it('names what a webhook was disabled for', () => {
    expect(at('webhook.disabled', { reason: 'failing', url })).toContain('kept failing');
    expect(at('webhook.disabled', { reason: 'overflow', url })).toContain('queue of deliveries was full');
    expect(at('webhook.disabled', { reason: 'secret_unreadable', url })).toContain('could not be read');
    expect(at('webhook.disabled', {}, user)).toContain('disabled the webhook for a web address.');
  });

  it('names the new name of a moved item', () => {
    const text = read({
      action: 'node.move',
      actor: user,
      target: 'nod_1',
      details: { from: { name: 'a.txt' }, to: { name: 'b.txt' } },
    });
    expect(text).toBe(`${isolateText('Ada Admin')} moved ${isolateText('b.txt')}.`);
  });
});

// A name the user chose is shown as text: markup in it is not run and not read as tags.
describe('user-supplied names render as text', () => {
  const hostile = '<img src=x onerror=alert(1)>';
  const hostileNames: Names = { person: (id) => (id === 'usr_mateo' ? hostile : undefined), space: () => undefined };

  it('shows a file, folder or upload name literally', () => {
    const file = sentence({ action: 'node.create', actor: user, target: 'nod_1', details: { kind: 'file', name: hostile } }, names);
    expect(renderSentence(file, translate)).toBe(`${isolateText('Ada Admin')} added ${isolateText(hostile)}.`);
    const up = sentence({ action: 'upload.create', actor: user, target: 'upl_1', details: { name: hostile } }, names);
    expect(renderSentence(up, translate)).toContain(isolateText(hostile));
  });

  it('shows the old and new names of a rename literally', () => {
    const s = sentence({ action: 'node.rename', actor: user, target: 'nod_1', details: { from: hostile, to: 'b.txt' } }, names);
    expect(renderSentence(s, translate)).toBe(`${isolateText('Ada Admin')} renamed ${isolateText(hostile)} to ${isolateText('b.txt')}.`);
  });

  it('shows a webhook address literally', () => {
    const s = sentence({ action: 'webhook.create', actor: user, target: 'wh_1', details: { url: hostile } }, names);
    expect(renderSentence(s, translate)).toContain(isolateText(hostile));
  });

  it('shows a person named in a sentence literally', () => {
    const s = sentence({ action: 'user.sign_in_link', actor: user, target: 'usr_mateo', details: {} }, hostileNames);
    // The actor is not a person the lookup knows here, so it reads as a generic word.
    expect(renderSentence(s, translate)).toBe(`Someone made a sign-in link for ${isolateText(hostile)}.`);
  });
});
