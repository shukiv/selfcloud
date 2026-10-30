// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { namesOf, personLabel, statusTone } from './people';
import type { Person, Space } from './types';

const person = (over: Partial<Person>): Person => ({
  id: 'usr_1',
  external_id: null,
  email: 'dana@example.com',
  display_name: '',
  locale: 'en',
  role: 'member',
  status: 'active',
  verified_at: null,
  created_at: '',
  updated_at: '',
  ...over,
});

describe('personLabel', () => {
  it('shows the display name, else the email address', () => {
    expect(personLabel(person({ display_name: 'Dana' }))).toBe('Dana');
    expect(personLabel(person({ display_name: '  ' }))).toBe('dana@example.com');
  });
});

describe('namesOf', () => {
  it('looks people and team spaces up by ID, and says nothing about the rest', () => {
    const space = { id: 'fld_1', name: 'Finance' } as Space;
    const names = namesOf([person({ id: 'usr_1', display_name: 'Dana' })], [space]);
    expect(names.person('usr_1')).toBe('Dana');
    expect(names.person('usr_2')).toBeUndefined();
    expect(names.space('fld_1')).toBe('Finance');
    expect(names.space('fld_2')).toBeUndefined();
  });
});

describe('statusTone', () => {
  it('colours each status, and leaves an unknown one plain', () => {
    expect(statusTone('active')).toBe('ok');
    expect(statusTone('pending')).toBe('warn');
    expect(statusTone('disabled')).toBe('danger');
    expect(statusTone('weird')).toBe('neutral');
  });
});
