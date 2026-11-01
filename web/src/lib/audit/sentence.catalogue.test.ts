// SPDX-License-Identifier: AGPL-3.0-or-later

import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { renderSentence, sentence, type AuditEntry, type Names } from './sentence';

// Every key the sentences name is in all seven catalogues, and the rows of this slice read as who did what to whom
// (decision 41): the admin by name, a removed admin as "an installation admin", never a raw ID or "Someone".
const LANGS = ['en', 'fr', 'es', 'pt', 'ru', 'ar', 'he'] as const;
const catalogues = Object.fromEntries(
  LANGS.map((l) => [l, JSON.parse(readFileSync(join(process.cwd(), 'src/i18n', `${l}.json`), 'utf8')) as Record<string, string>]),
) as Record<(typeof LANGS)[number], Record<string, string>>;

const source = readFileSync(join(process.cwd(), 'src/lib/audit/sentence.ts'), 'utf8');

describe('audit sentence catalogues', () => {
  it('every audit key the sentences name is in all seven catalogues', () => {
    const keys = new Set([...source.matchAll(/'(audit\.[A-Za-z0-9_.]+)'/g)].map((m) => m[1]));
    for (const m of source.matchAll(/`(audit\.[A-Za-z0-9_.]*)\$\{/g)) keys.add(`${m[1]}`);
    // The dynamic keys are the reasons and ways, listed here so each one is checked by name.
    for (const k of [
      'audit.impersonation.end.returned', 'audit.impersonation.end.expired', 'audit.impersonation.end.adminSignedOut',
      'audit.impersonation.end.adminEnded', 'audit.impersonation.end.accountChanged', 'audit.impersonation.end.other',
      'audit.admin.password_refused.signInAs', 'audit.admin.password_refused.typedPassword',
      'audit.admin.password_refused.resetLink', 'audit.admin.password_refused.other',
    ]) keys.add(k);
    const missing: string[] = [];
    for (const k of keys) {
      if (k === 'audit.' || k.endsWith('.')) continue;
      for (const l of LANGS) if (!(k in catalogues[l])) missing.push(`${l}: ${k}`);
    }
    expect(missing).toEqual([]);
  });
});

// The English catalogue filled in the way the page fills it, so the sentences are read as the owner reads them.
const translate = (key: string, params?: Record<string, string>): string => {
  const text = catalogues.en[key] ?? key;
  return text.replace(/\{(\w+)\}/g, (_, k: string) => params?.[k] ?? '');
};
const iso = (t: string) => `⁨${t}⁩`;

const names: Names = {
  person: (id) => (id === 'usr_mateo' ? 'Mateo Member' : undefined),
  space: () => undefined,
};

const row = (over: Partial<AuditEntry>): AuditEntry => ({
  action: 'impersonation.start',
  actor: { kind: 'user', id: 'usr_ada' },
  target: 'usr_mateo',
  details: {},
  ...over,
});

const read = (e: AuditEntry) => renderSentence(sentence(e, names), translate);

describe('rows of an installation admin', () => {
  it('a sign-in names the admin and the member', () => {
    const e = row({ details: { actor_admin: true, actor_name: 'Ada Admin' } });
    expect(read(e)).toBe(`${iso('Ada Admin')} signed in as ${iso('Mateo Member')}.`);
  });

  it('a refused sign-in reads as refused, not as a sign-in', () => {
    const e = row({
      action: 'admin.password_refused',
      details: { way: 'sign_in_as', actor_admin: true, actor_name: 'Ada Admin' },
    });
    expect(read(e)).toBe(`${iso('Ada Admin')}’s sign-in as ${iso('Mateo Member')} was refused: wrong password.`);
  });

  it('a refused typed password and a refused reset link say what was refused', () => {
    const typed = row({ action: 'admin.password_refused', details: { way: 'typed_password', actor_admin: true, actor_name: 'Ada Admin' } });
    expect(read(typed)).toBe(`${iso('Ada Admin')} could not set a password for ${iso('Mateo Member')}: wrong password.`);
    const link = row({ action: 'admin.password_refused', details: { way: 'reset_link', actor_admin: true, actor_name: 'Ada Admin' } });
    expect(read(link)).toBe(`${iso('Ada Admin')} could not make a reset link for ${iso('Mateo Member')}: wrong password.`);
  });

  it('going back names the admin and says it went back', () => {
    const e = row({ action: 'impersonation.end', details: { reason: 'returned', actor_admin: true, actor_name: 'Ada Admin' } });
    expect(read(e)).toBe(`${iso('Ada Admin')} went back from signing in as ${iso('Mateo Member')}.`);
  });

  it('an ended sign-in gives its reason', () => {
    const reasons: Record<string, string> = {
      expired: 'ended after an hour',
      admin_signed_out: 'ended when the admin signed out',
      admin_removed: 'ended when the admin’s own session ended or the admin was removed',
      password_set: 'ended because',
    };
    for (const [reason, text] of Object.entries(reasons)) {
      const e = row({ action: 'impersonation.end', details: { reason, actor_admin: true, actor_name: 'Ada Admin' } });
      expect(read(e), reason).toContain(text);
    }
  });

  it('a password the admin set reads as set', () => {
    const e = row({ action: 'user.password_set', details: { actor_admin: true, actor_name: 'Ada Admin' } });
    expect(read(e)).toBe(`${iso('Ada Admin')} set a new password for ${iso('Mateo Member')}.`);
  });

  it('a reset link the admin made reads as a link', () => {
    const e = row({ action: 'verification.send', details: { purpose: 'reset', method: 'link', actor_admin: true, actor_name: 'Ada Admin' } });
    expect(read(e)).toBe(`${iso('Ada Admin')} made a one-time password reset link for ${iso('Mateo Member')}.`);
  });

  it('a removed admin is named by its role, never by its ID or as "Someone"', () => {
    const e = row({ action: 'impersonation.end', details: { reason: 'admin_removed', actor_admin: true, actor_name: '' } });
    const text = read(e);
    expect(text).toContain('an installation admin');
    expect(text).not.toContain('usr_ada');
    expect(text).not.toContain('Someone');
  });

  it('an ordinary person who is not in the lookup still reads as Someone', () => {
    const e = row({ action: 'node.trash', details: {} });
    expect(read(e)).toContain('Someone');
  });
});
