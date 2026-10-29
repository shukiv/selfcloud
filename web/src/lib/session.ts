// SPDX-License-Identifier: AGPL-3.0-or-later

import { get, writable } from 'svelte/store';
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
