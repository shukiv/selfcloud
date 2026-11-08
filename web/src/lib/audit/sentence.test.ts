// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { actorRef, isolateText, renderSentence, sentence, type AuditEntry, type Names } from './sentence';

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
    // A row with no name reads with its own sentence, not "the team space" followed by a team space.
    expect(sentence(entry({ action: 'folder.delete', target: 'fld_x' }), names)).toEqual({
      key: 'audit.folder.delete.plain',
      params: { actor: { text: 'Dana' } },
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
    expect(sentence(entry({ action: 'group.delete', target: 'fld_fin' }), names)).toEqual({
      key: 'audit.group.delete.plain',
      params: { actor: { text: 'Dana' } },
    });
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
    // The first call looks for the word the catalogue puts first; the generic word is then translated on its own.
    expect(seen).toContainEqual(['audit.someone', undefined]);
    const final = seen[seen.length - 1];
    expect(final[0]).toBe('audit.folder.create');
    expect(final[1]?.name).toBe(isolateText('Finance'));
    expect(final[1]?.actor).toBe('audit.someone');
  });

  // i18n isolates every parameter in a right-to-left language (decision 67), so the probe that finds the leading word
  // starts with an isolateText mark, not with the marker. The lead must still be found.
  it('finds the leading word when the translator isolates every parameter', () => {
    const iso = (s: string) => `⁨${s}⁩`;
    const translate = (key: string, params?: Record<string, string>) => {
      if (key === 'audit.someone') return 'someone';
      return `${iso(params?.actor ?? '')} added ${iso(params?.name ?? '')}`;
    };
    const out = renderSentence(
      { key: 'audit.node.create', params: { actor: { key: 'audit.someone' }, name: { text: 'a.txt' } } },
      translate,
    );
    expect(out.startsWith(iso('Someone'))).toBe(true);
  });

  it('isolates an empty name as nothing', () => {
    expect(isolateText('')).toBe('');
  });

  // The templates folder: the server writes its node ID when it is set, and an empty node when it is cleared (decision 105).
  it('says a templates folder is set when a node is named, and cleared when it is empty or missing', () => {
    expect(sentence(entry({ action: 'templates.folder', details: { node: 'nod_x' } }), names).key).toBe(
      'audit.templates.folder.set',
    );
    expect(sentence(entry({ action: 'templates.folder', details: { node: '' } }), names).key).toBe(
      'audit.templates.folder.clear',
    );
    expect(sentence(entry({ action: 'templates.folder' }), names).key).toBe('audit.templates.folder.clear');
  });
});
