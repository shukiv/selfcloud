// SPDX-License-Identifier: AGPL-3.0-or-later

import { safeNext } from './next';

// tokenFromFragment reads the sign-in token from a URL fragment such as "#token=...". It returns ""
// when there is none.
export function tokenFromFragment(hash: string): string {
  return new URLSearchParams(hash.startsWith('#') ? hash.slice(1) : hash).get('token') ?? '';
}

// looksLikeToken reports whether s has the shape the server issues: 32 random bytes in unpadded
// base64url. A value of any other shape is refused without a request.
export function looksLikeToken(s: string): boolean {
  return /^[A-Za-z0-9_-]{43}$/.test(s);
}

// signInTarget is the page a sign-in opens: the path the server gave, checked again here, or the
// home page of files when that path is not usable or is the root.
export function signInTarget(next: string | null | undefined): string {
  const path = safeNext(next ?? '');
  return path === '/' ? '/files' : path;
}
