// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { typedNameMatches } from './confirm';

describe('typedNameMatches', () => {
  it('needs the name exactly, apart from the spaces at its ends', () => {
    expect(typedNameMatches('Finance', 'Finance')).toBe(true);
    expect(typedNameMatches('  Finance ', 'Finance')).toBe(true);
    expect(typedNameMatches('finance', 'Finance')).toBe(false);
    expect(typedNameMatches('Fin', 'Finance')).toBe(false);
    expect(typedNameMatches('', 'Finance')).toBe(false);
  });

  it('never matches an empty name', () => {
    expect(typedNameMatches('', '')).toBe(false);
  });
});
