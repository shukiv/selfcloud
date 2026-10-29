// SPDX-License-Identifier: AGPL-3.0-or-later

import { api, ApiError } from './api';
import { clearSession, setMe, setSignedIn } from './session';
import type { Brand } from './brand';
import type { Me, SignedIn, SignedInLink } from './types';
import { sharingMaxExpiryDays } from './sharing/api';

export interface Info {
  brand: Brand | null;
  passwordMinLength: number;
  // sharingMaxExpiryDays is the longest a share may run, in days; 0 for no cap.
  sharingMaxExpiryDays: number;
}

// loadInfo reads the installation's name, colours and password rule; it needs no sign-in. A missing
// or odd minimum falls back to 8, the server's floor.
export async function loadInfo(): Promise<Info> {
  const info = await api<{ brand?: Brand; password_min_length?: unknown; sharing_max_expiry_days?: unknown }>(
    'GET',
    '/api/v1/info',
    { noRedirect: true },
  );
  const n = info.password_min_length;
  const min = typeof n === 'number' && Number.isInteger(n) && n >= 1 ? n : 8;
  return {
    brand: info.brand ?? null,
    passwordMinLength: min,
    sharingMaxExpiryDays: sharingMaxExpiryDays(info),
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

export function signIn(email: string, password: string, account?: string): Promise<SignedIn> {
  const body: Record<string, string> = { email, password };
  if (account) body.account = account;
  return api<SignedIn>('POST', '/api/v1/auth/login', { body, noRedirect: true });
}

// establish stores a new session from a sign-in answer, then loads the account around it.
export async function establish(signed: SignedIn): Promise<void> {
  setSignedIn(signed);
  await loadMe();
}

export function verifyAccount(token: string, password: string): Promise<SignedIn> {
  return api<SignedIn>('POST', '/api/v1/auth/verify', { body: { token, password }, noRedirect: true });
}

// signInWithLink redeems a sign-in link: the server starts a session and answers the path to open.
export function signInWithLink(token: string): Promise<SignedInLink> {
  return api<SignedInLink>('POST', '/api/v1/auth/sign-in-link', { body: { token }, noRedirect: true });
}

export function resetPassword(token: string, password: string): Promise<SignedIn> {
  return api<SignedIn>('POST', '/api/v1/auth/password/reset', { body: { token, password }, noRedirect: true });
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

export async function signOut(): Promise<void> {
  try {
    await api<void>('POST', '/api/v1/auth/logout', {});
  } finally {
    clearSession();
  }
}
