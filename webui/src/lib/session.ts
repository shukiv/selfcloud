// SPDX-License-Identifier: AGPL-3.0-or-later

import { get, writable } from './store';
import type { Account, Me, SignedIn, User } from './types';

export interface Session {
  user: User;
  account: Account | null;
  // csrf is kept in memory only. It is not in storage, so another page cannot read it.
  csrf: string;
  // publicLinks is the account's switch for links that anyone can open.
  publicLinks: boolean;
}

export const session = writable<Session | null>(null);

// flash is a one-line notice shown on the next page, such as "password changed".
export const flash = writable<string | null>(null);

export function currentCsrf(): string | null {
  return get(session)?.csrf ?? null;
}

export function setSignedIn(s: SignedIn): void {
  session.set({ user: s.user, account: null, csrf: s.csrf_token, publicLinks: false });
}

export function setMe(me: Me): void {
  session.set({ user: me.user, account: me.account, csrf: me.csrf_token, publicLinks: me.public_links === true });
}

// sessionKey names the session a request was made under: its person and its CSRF token. It is '' with no session.
// A change of person or of token (a sign-out, then a sign-in) gives a new key.
export function sessionKey(s: Session | null): string {
  return s ? `${s.user.id}\n${s.csrf}` : '';
}

// sessionChanged starts watching the session. The function it returns reports whether the session has since ended
// or changed (a sign-out, or a sign-in as someone else, or with a new token). A page's async load checks it after each
// await, so it never sends a request under a session that has gone.
export function sessionChanged(): () => boolean {
  const started = sessionKey(get(session));
  return () => sessionKey(get(session)) !== started;
}

// ending counts the sign-outs under way. The session is still set while a sign-out call is in flight, so a request a
// page starts then would be sent under a session the server is ending, and be answered 401. api refuses those requests
// while the count is above zero, except the sign-out call itself. A counter, not a flag: two sign-outs overlap, and
// the first to finish must not clear the second's guard.
let ending = 0;

export function beginSignOut(): void {
  ending++;
}

export function endSignOut(): void {
  ending = Math.max(0, ending - 1);
}

export function signingOut(): boolean {
  return ending > 0;
}

export function clearSession(): void {
  session.set(null);
}

// isAdmin is true for an installation admin, which has no account of its own.
export function isAdmin(user: User | undefined): boolean {
  return user?.role === 'admin';
}

// isOwner is true for an owner of the signed-in account.
export function isOwner(user: User | undefined): boolean {
  return user?.role === 'owner';
}
