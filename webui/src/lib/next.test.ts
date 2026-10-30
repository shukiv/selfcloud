// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { afterSignIn, safeNext } from './next';

describe('safeNext', () => {
  it('keeps a same-site path, with its query and fragment', () => {
    expect(safeNext('/files')).toBe('/files');
    expect(safeNext('/files/a%20b?x=1#top')).toBe('/files/a%20b?x=1#top');
    expect(safeNext('/shared')).toBe('/shared');
  });

  it('sends anything else home', () => {
    const refused = [
      null,
      undefined,
      '',
      'files',
      '//evil.example',
      '//evil.example/files',
      '///evil.example',
      '/\\evil.example',
      '/\\evil.example/files',
      '/\t/files',
      '\\\\evil.example',
      '/\t/evil.example',
      '/\n/evil.example',
      ' /files',
      '/files\u0000',
      'https://evil.example/',
      'http://evil.example',
      'javascript:alert(1)',
      'data:text/html,hi',
      'mailto:a@b.example',
      '/' + 'a'.repeat(2048),
      '/.//evil.example',
      '/a/..//evil.example',
      '/x/./..//evil.example',
    ];
    for (const raw of refused) {
      expect(safeNext(raw as string | null | undefined), String(raw)).toBe('/');
    }
  });
});

describe('afterSignIn', () => {
  it('goes where safeNext says, except back to a sign-in page', () => {
    expect(afterSignIn('/files?x=1')).toBe('/files?x=1');
    expect(afterSignIn('/login')).toBe('/');
    expect(afterSignIn('/login?next=/files')).toBe('/');
    expect(afterSignIn('/login/')).toBe('/');
    expect(afterSignIn('/sign-in?x=1')).toBe('/sign-in?x=1');
    expect(afterSignIn(null)).toBe('/');
  });
});
