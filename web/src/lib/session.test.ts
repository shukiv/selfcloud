// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { session } from './session';
import { beginSignOut, endSignOut, sessionChanged, sessionKey, signingOut, type Session } from './session';

// signedIn returns a session for a person with the given token.
function signedIn(userID: string, csrf: string): Session {
  return {
    user: { id: userID } as Session['user'],
    account: null,
    csrf,
    publicLinks: false,
  };
}

describe('sessionKey', () => {
  it('is empty with no session', () => {
    expect(sessionKey(null)).toBe('');
  });

  it('is the same for the same person and token, whatever the object', () => {
    expect(sessionKey(signedIn('usr_a', 'x'))).toBe(sessionKey(signedIn('usr_a', 'x')));
  });

  it('changes when the person or the token changes, so a stale answer is recognised', () => {
    const before = sessionKey(signedIn('usr_a', 'x'));
    expect(sessionKey(null)).not.toBe(before);
    expect(sessionKey(signedIn('usr_b', 'x'))).not.toBe(before);
    expect(sessionKey(signedIn('usr_a', 'y'))).not.toBe(before);
  });
});

describe('the sign-out flag', () => {
  it('is set from the start of a sign-out until it ends', () => {
    expect(signingOut()).toBe(false);
    beginSignOut();
    expect(signingOut()).toBe(true);
    endSignOut();
    expect(signingOut()).toBe(false);
  });
});

describe('the sign-out guard counts sign-outs', () => {
  it('stays up while any sign-out is under way', () => {
    beginSignOut();
    beginSignOut();
    endSignOut();
    expect(signingOut()).toBe(true);
    endSignOut();
    expect(signingOut()).toBe(false);
  });

  it('never goes below zero', () => {
    endSignOut();
    expect(signingOut()).toBe(false);
    beginSignOut();
    expect(signingOut()).toBe(true);
    endSignOut();
  });
});

describe('sessionChanged', () => {
  it('reports nothing while the same session stays, and a sign-out', () => {
    session.set(signedIn('usr_a', 'tok-1'));
    const changed = sessionChanged();
    expect(changed()).toBe(false);
    session.set(null);
    expect(changed()).toBe(true);
    session.set(null);
  });

  it('reports a sign-in as someone else, or with a new token, as a change', () => {
    session.set(signedIn('usr_a', 'tok-1'));
    const changed = sessionChanged();
    session.set(signedIn('usr_b', 'tok-2'));
    expect(changed()).toBe(true);
    session.set(null);
  });
});
