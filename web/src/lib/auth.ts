// SPDX-License-Identifier: AGPL-3.0-or-later

import { api, ApiError } from './api';
import { beginSignOut, clearSession, endSignOut, setMe, setSignedIn } from './session';
import type { Brand } from './brand';
import type { Me, SignedIn, SignedInLink } from './types';
import { sharingMaxExpiryDays } from './sharing/api';
import { markSignedOut } from "./pwa/cacheState";
import { purgeAllUnsaved } from "./code/unsaved";

export interface Info {
  brand: Brand | null;
  passwordMinLength: number;
  // sharingMaxExpiryDays is the longest a share may run, in days; 0 for no cap.
  sharingMaxExpiryDays: number;
  // features is the list of optional features the server has on, by name (see lib/files/convert.ts).
  features: unknown;
  // limits is the upload limits the server gives, or null when it gives none (the files page keeps its own default).
  limits: { max_chunk_size: number; max_file_size: number } | null;
}

// loadInfo reads the installation's name, colours and password rule; it needs no sign-in. A missing
// or odd minimum falls back to 8, the server's floor.
export async function loadInfo(): Promise<Info> {
  const info = await api<{
    brand?: Brand;
    password_min_length?: unknown;
    sharing_max_expiry_days?: unknown;
    features?: unknown;
    limits?: { max_chunk_size: number; max_file_size: number };
  }>('GET', '/api/v1/info', { noRedirect: true });
  const n = info.password_min_length;
  const min = typeof n === 'number' && Number.isInteger(n) && n >= 1 ? n : 8;
  return {
    brand: info.brand ?? null,
    passwordMinLength: min,
    sharingMaxExpiryDays: sharingMaxExpiryDays(info),
    features: info.features,
    limits: info.limits ?? null,
  };
}

// loadMe answers the signed-in user, or null when nobody is signed in.
export async function loadMe(): Promise<Me | null> {
  try {
    const me = await api<Me>('GET', '/api/v1/me', { noRedirect: true });
    setMe(me);
    return me;
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      clearSession();
      return null;
    }
    throw err;
  }
}

// SignInAnswer is the session, or, while the email's cap on failed sign-ins is spent from a browser this server does not
// know, the confirmation (sec2, decision 86): the browser must be confirmed by a link mailed to the person.
export type SignInAnswer = SignedIn | { confirm: 'email' };

// isConfirmByEmail reports whether a sign-in answer waits for the browser to be confirmed by email.
export function isConfirmByEmail(answer: SignInAnswer): answer is { confirm: 'email' } {
  return 'confirm' in answer;
}

// signIn checks the password. remember is the "Keep me signed in" box (decision 75): false makes a session the browser
// does not keep. Left out, the session is kept, as API clients have always had it.
export function signIn(email: string, password: string, account?: string, remember?: boolean): Promise<SignInAnswer> {
  const body: Record<string, string | boolean> = { email, password };
  if (account) body.account = account;
  if (remember !== undefined) body.remember = remember;
  return api<SignInAnswer>('POST', '/api/v1/auth/login', { body, noRedirect: true });
}

// confirmDevice redeems a device confirmation link in the browser that asked for it, and signs that browser in.
export function confirmDevice(token: string): Promise<SignedIn> {
  return api<SignedIn>('POST', '/api/v1/auth/device-confirm', { body: { token }, noRedirect: true });
}

// establish stores a new session from a sign-in answer, then loads the account around it.
export async function establish(signed: SignedIn): Promise<void> {
  setSignedIn(signed);
  await loadMe();
}

// verifyAccount sets the password and signs the account in. confirm is sent only when true: it accepts the switch from a
// browser signed in as another person (decision 74).
export function verifyAccount(token: string, password: string, confirm = false): Promise<SignedIn> {
  const body = confirm ? { token, password, confirm: true } : { token, password };
  return api<SignedIn>('POST', '/api/v1/auth/verify', { body, noRedirect: true });
}

// confirmEmailChange confirms a user's new address with the token its link carries (decision 89). It signs no one in.
export function confirmEmailChange(token: string): Promise<unknown> {
  return api<unknown>('POST', '/api/v1/auth/email-change/confirm', { body: { token }, noRedirect: true });
}

// signInWithLink redeems a sign-in link: the server starts a session and answers the path to open. confirm
// is sent only when true: it accepts the switch to the link's user from a browser signed in as another.
export function signInWithLink(token: string, confirm = false): Promise<SignedInLink> {
  const body = confirm ? { token, confirm: true } : { token };
  return api<SignedInLink>('POST', '/api/v1/auth/sign-in-link', { body, noRedirect: true });
}

// resetPassword chooses the password and signs the person in. confirm is sent only when true, as verifyAccount's is.
export function resetPassword(token: string, password: string, confirm = false): Promise<SignedIn> {
  const body = confirm ? { token, password, confirm: true } : { token, password };
  return api<SignedIn>('POST', '/api/v1/auth/password/reset', { body, noRedirect: true });
}

export function forgotPassword(email: string): Promise<void> {
  return api<void>('POST', '/api/v1/auth/password/forgot', { body: { email }, noRedirect: true });
}

export function changePassword(current: string, next: string): Promise<void> {
  return api<void>('POST', '/api/v1/me/password', {
    body: { current_password: current, new_password: next },
  });
}

export function setLocale(locale: string): Promise<Me> {
  return api<Me>('PATCH', '/api/v1/me', { body: { locale } });
}

// SIGN_OUT_TIMEOUT_MS bounds the sign-out call. The session is cleared either way, so a call that hangs must not hold the
// app's other requests back for longer than this.
export const SIGN_OUT_TIMEOUT_MS = 10_000;

export async function signOut(): Promise<void> {
  beginSignOut();
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), SIGN_OUT_TIMEOUT_MS);
  try {
    await api<void>('POST', '/api/v1/auth/logout', { allowWhileSigningOut: true, signal: ctl.signal });
  } catch {
    // The session is ended on this side whatever the server said, so a failed or timed-out call is not an error here.
  } finally {
    clearTimeout(timer);
    clearSession();
    // An explicit sign-out clears the unsaved editor text this tab kept, for every person (decision 133). The editor's watch
    // ignores this sign-out, so it does not keep the text again.
    purgeAllUnsaved();
    // The app's caches go too, so a shared computer keeps no app files after sign-out (decision 65).
    await markSignedOut();
    endSignOut();
  }
}
