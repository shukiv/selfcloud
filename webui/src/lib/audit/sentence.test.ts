// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { actorRef, isolate, renderSentence, sentence, type AuditEntry, type Names } from './sentence';

const names: Names = {
  person: (id) => (id === 'usr_dana' ? 'Dana' : id === 'usr_omer' ? 'Omer' : undefined),
  space: (id) => (id === 'fld_fin' ? 'Finance' : undefined),
};

const entry = (over: Partial<AuditEntry>): AuditEntry => ({
  action: 'folder.create',
  actor: { kind: 'user', id: 'usr_dana' },
  target: 'fld_fin',
  ...over,
});

describe('actorRef', () => {
  it('uses the name of a known person, and a generic word otherwise', () => {
    expect(actorRef({ kind: 'user', id: 'usr_dana' }, names)).toEqual({ text: 'Dana' });
    expect(actorRef({ kind: 'user', id: 'usr_gone' }, names)).toEqual({ key: 'audit.someone' });
    expect(actorRef({ kind: 'account_key', id: 'key_1' }, names)).toEqual({ key: 'audit.apiKey' });
    expect(actorRef({ kind: 'system', id: 'job_1' }, names)).toEqual({ key: 'audit.system' });
  });
});

describe('sentence', () => {
  it('names the team space from the details, then from the lookup', () => {
    expect(sentence(entry({ action: 'folder.create', details: { name: 'Finance' } }), names)).toEqual({
      key: 'audit.folder.create',
      params: { actor: { text: 'Dana' }, name: { text: 'Finance' } },
    });
    expect(sentence(entry({ action: 'folder.update' }), names).params.name).toEqual({ text: 'Finance' });
    expect(sentence(entry({ action: 'folder.delete', target: 'fld_x' }), names).params.name).toEqual({
      key: 'audit.aSpace',
    });
  });

  it('names a person target from the lookup', () => {
    const s = sentence(entry({ action: 'user.create', actor: { kind: 'user', id: 'usr_dana' }, target: 'usr_omer' }), names);
    expect(s).toEqual({ key: 'audit.user.create', params: { actor: { text: 'Dana' }, target: { text: 'Omer' } } });
  });

  it('chooses the owner invitation and the password reset by their details', () => {
    expect(sentence(entry({ action: 'user.create', details: { role: 'owner' } }), names).key).toBe('audit.user.createOwner');
    expect(sentence(entry({ action: 'user.create', details: { role: 'member' } }), names).key).toBe('audit.user.create');
    expect(sentence(entry({ action: 'verification.send', details: { purpose: 'reset' } }), names).key).toBe(
      'audit.verification.reset',
    );
    expect(sentence(entry({ action: 'verification.send', details: { purpose: 'verify' } }), names).key).toBe(
      'audit.verification.send',
    );
  });

  it('names a group, and never a team space, for a group action', () => {
    const s = sentence(entry({ action: 'group.create', target: 'grp_1', details: { name: 'Staff' } }), names);
    expect(s).toEqual({ key: 'audit.group.create', params: { actor: { text: 'Dana' }, name: { text: 'Staff' } } });
    expect(sentence(entry({ action: 'group.delete', target: 'fld_fin' }), names).params.name).toEqual({ key: 'audit.aGroup' });
  });

  it('gives a grant no person or space it cannot know', () => {
    expect(sentence(entry({ action: 'grant.set', target: 'fld_fin/usr_omer' }), names)).toEqual({
      key: 'audit.grant.set',
      params: { actor: { text: 'Dana' } },
    });
  });

  it('falls back to a generic sentence for an action it does not know', () => {
    expect(sentence(entry({ action: 'something.new' }), names)).toEqual({
      key: 'audit.unlisted',
      params: { actor: { text: 'Dana' } },
    });
  });
});

describe('renderSentence', () => {
  it('isolates names and translates generic words', () => {
    const seen: Array<[string, Record<string, string> | undefined]> = [];
    const translate = (key: string, params?: Record<string, string>) => {
      seen.push([key, params]);
      return `${key}`;
    };
    const out = renderSentence(
      { key: 'audit.folder.create', params: { actor: { key: 'audit.someone' }, name: { text: 'Finance' } } },
      translate,
    );
    expect(out).toBe('audit.folder.create');
    expect(seen[0]).toEqual(['audit.someone', undefined]);
    const final = seen[seen.length - 1];
    expect(final[0]).toBe('audit.folder.create');
    expect(final[1]?.name).toBe(isolate('Finance'));
    expect(final[1]?.actor).toBe('audit.someone');
  });
});
