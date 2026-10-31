// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { renderSentence, sentence, type AuditEntry, type Names } from './sentence';

// The viewers say who acted for an installation admin who was signed in as the user (decision 41, F6). The owner and
// the admin both see the admin's name, and a row whose admin is gone says so in general words.
const names: Names = {
  person: (id) => (id === 'usr_dana' ? 'Dana' : undefined),
  space: () => undefined,
};

const translate = (key: string, params?: Record<string, string>): string => {
  const fill = (s: string) => s.replace(/\{(\w+)\}/g, (_, k: string) => params?.[k] ?? '');
  const catalogue: Record<string, string> = {
    'audit.node.trash': '{actor} moved a file to the trash.',
    'audit.someone': 'Someone',
    'audit.viaAdmin': 'Done by the installation admin {admin}, signed in as this user.',
    'audit.anAdmin': 'an installation admin',
  };
  return fill(catalogue[key] ?? key);
};

// Text names are wrapped in directional isolate marks by the renderer.
const iso = (t: string) => `\u2068${t}\u2069`;

const entry = (over: Partial<AuditEntry>): AuditEntry => ({
  action: 'node.trash',
  actor: { kind: 'user', id: 'usr_dana' },
  target: 'nod_1',
  ...over,
});

describe('impersonated audit rows', () => {
  it('names the admin after the sentence', () => {
    const e = entry({ details: { impersonated_by: 'usr_root', impersonated_by_name: 'root@example.com' } });
    expect(renderSentence(sentence(e, names), translate)).toBe(
      `${iso('Dana')} moved a file to the trash. Done by the installation admin ${iso('root@example.com')}, signed in as this user.`,
    );
  });

  it('uses the general word when the admin no longer exists', () => {
    const e = entry({ details: { impersonated_by: 'usr_gone', impersonated_by_name: '' } });
    expect(renderSentence(sentence(e, names), translate)).toBe(
      `${iso('Dana')} moved a file to the trash. Done by the installation admin an installation admin, signed in as this user.`,
    );
  });

  it('adds nothing to an ordinary row', () => {
    expect(sentence(entry({}), names).via).toBeUndefined();
    expect(renderSentence(sentence(entry({}), names), translate)).toBe(`${iso('Dana')} moved a file to the trash.`);
  });
});
