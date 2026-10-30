// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { ApiError } from './apiError';
import { looksLikeToken, signInTarget, splitParts, switchPromptOf, tokenFromFragment } from './signin';

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

describe('switchPromptOf', () => {
  const conflict = (details: Record<string, unknown>, code = 'conflict') =>
    new ApiError(409, code, details, 0);
  const good = {
    reason: 'signed_in_as_other',
    current: { name: 'Ann', email: 'ann@example.com' },
    target: { name: 'Ben', email: 'ben@example.com' },
  };

  it('reads both names from a signed_in_as_other answer', () => {
    expect(switchPromptOf(conflict(good))).toEqual({ current: 'Ann', target: 'Ben' });
  });

  it('shows the email of a user without a name', () => {
    const d = { ...good, current: { name: '', email: 'ann@example.com' }, target: { email: 'ben@example.com' } };
    expect(switchPromptOf(conflict(d))).toEqual({ current: 'ann@example.com', target: 'ben@example.com' });
  });

  it('is null for another reason, another code, or any other error', () => {
    expect(switchPromptOf(conflict({ ...good, reason: 'other' }))).toBeNull();
    expect(switchPromptOf(conflict(good, 'invalid_request'))).toBeNull();
    expect(switchPromptOf(new Error('boom'))).toBeNull();
    expect(switchPromptOf(null)).toBeNull();
  });

  it('is null when a name is missing', () => {
    expect(switchPromptOf(conflict({ ...good, target: undefined }))).toBeNull();
    expect(switchPromptOf(conflict({ ...good, current: { name: '', email: '' } }))).toBeNull();
    expect(switchPromptOf(conflict({ ...good, target: 'Ben' }))).toBeNull();
  });
});

describe('splitParts', () => {
  it('cuts the sentence at each name, keeping the text around it', () => {
    expect(
      splitParts("You're signed in as {current}. This link signs you in as {target} instead.", {
        current: 'Ann',
        target: 'Ben',
      }),
    ).toEqual([
      { text: "You're signed in as ", name: false },
      { text: 'Ann', name: true },
      { text: '. This link signs you in as ', name: false },
      { text: 'Ben', name: true },
      { text: ' instead.', name: false },
    ]);
  });

  it('leaves unknown placeholders in the text and shows names as text, never as HTML', () => {
    expect(splitParts('a {other} b {current}', { current: '<img src=x>' })).toEqual([
      { text: 'a {other} b ', name: false },
      { text: '<img src=x>', name: true },
    ]);
  });

  it('is one text part when there is no placeholder', () => {
    expect(splitParts('Stay', { current: 'Ann' })).toEqual([{ text: 'Stay', name: false }]);
  });
});
