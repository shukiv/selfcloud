// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Names } from '../audit/sentence';
import type { Person, Space } from './types';

// personLabel is the name shown for a person: the display name, else the email address.
export function personLabel(p: Pick<Person, 'display_name' | 'email'>): string {
  return p.display_name.trim() !== '' ? p.display_name : p.email;
}

// namesOf builds the lookup the audit sentences use, from the people and team spaces the page has read.
export function namesOf(people: Person[], spaces: Space[]): Names {
  const who = new Map(people.map((p) => [p.id, personLabel(p)]));
  const where = new Map(spaces.map((s) => [s.id, s.name]));
  return {
    person: (id) => who.get(id),
    space: (id) => where.get(id),
  };
}

// statusTone is the colour a status badge takes. The word itself is always shown with it.
export function statusTone(status: string): 'ok' | 'warn' | 'danger' | 'neutral' {
  switch (status) {
    case 'active':
      return 'ok';
    case 'pending':
      return 'warn';
    case 'disabled':
    case 'suspended':
      return 'danger';
    default:
      return 'neutral';
  }
}
