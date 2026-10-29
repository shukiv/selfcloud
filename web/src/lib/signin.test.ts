// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { looksLikeToken, signInTarget, tokenFromFragment } from './signin';

const TOKEN = 'a'.repeat(43);

describe('tokenFromFragment', () => {
  it('reads the token from the fragment, with or without the #', () => {
    expect(tokenFromFragment(`#token=${TOKEN}`)).toBe(TOKEN);
    expect(tokenFromFragment(`token=${TOKEN}`)).toBe(TOKEN);
  });

  it('is empty when the fragment has no token', () => {
    expect(tokenFromFragment('')).toBe('');
    expect(tokenFromFragment('#other=1')).toBe('');
  });
});

describe('looksLikeToken', () => {
  it('accepts a 43-character base64url token', () => {
    expect(looksLikeToken(TOKEN)).toBe(true);
    expect(looksLikeToken('-_'.repeat(21) + 'x')).toBe(true);
  });

  it('refuses any other shape', () => {
    expect(looksLikeToken('')).toBe(false);
    expect(looksLikeToken('a'.repeat(42))).toBe(false);
    expect(looksLikeToken('a'.repeat(44))).toBe(false);
    expect(looksLikeToken(`${'a'.repeat(42)}+`)).toBe(false);
    expect(looksLikeToken(`${'a'.repeat(42)}/`)).toBe(false);
    expect(looksLikeToken(`${'a'.repeat(42)} `)).toBe(false);
  });
});

describe('signInTarget', () => {
  it('opens the path the server gave', () => {
    expect(signInTarget('/files/x')).toBe('/files/x');
    expect(signInTarget('/shared?tab=1')).toBe('/shared?tab=1');
  });

  it('checks the path again and falls back to files', () => {
    for (const bad of ['//evil.example', 'https://evil.example', '/\\evil', '/a\tb', '', null, undefined, '/']) {
      expect(signInTarget(bad)).toBe('/files');
    }
  });
});
