// SPDX-License-Identifier: AGPL-3.0-or-later

const ORIGIN = 'http://selfcloud.invalid';
const SIGN_IN_PATH = '/login';
const MAX_LENGTH = 2048;

// safeNext returns the path a sign-in should go to. Only a path on this site is accepted: it starts
// with a single slash, has no backslash and no control character or space (browsers strip some of
// those, which could turn "/\t/host" into "//host"), and resolves to this origin. Anything else goes
// to the home page. The normalised result is checked again, because dot segments can turn a path
// that starts with one slash into one that starts with two.
export function safeNext(raw: string | null | undefined): string {
  if (typeof raw !== 'string' || raw.length === 0 || raw.length > MAX_LENGTH) return '/';
  if (!raw.startsWith('/') || raw.startsWith('//')) return '/';
  if (raw.includes('\\')) return '/';
  if (/[\u0000-\u0020\u007f]/.test(raw)) return '/';
  try {
    const url = new URL(raw, ORIGIN);
    if (url.origin !== ORIGIN) return '/';
    // Dot segments can normalise to a protocol-relative path: "/.//host" becomes "//host".
    const out = url.pathname + url.search + url.hash;
    if (out.startsWith('//') || new URL(out, ORIGIN).origin !== ORIGIN) return '/';
    return out;
  } catch {
    return '/';
  }
}

// afterSignIn is where a signed-in person goes from a sign-in page: safeNext, except that a path
// back to the sign-in page goes home. Without this, /login?next=/login sends a signed-in person to
// the sign-in page again, and then home only because the second hop has no next.
export function afterSignIn(raw: string | null | undefined): string {
  const path = safeNext(raw);
  return path.startsWith(SIGN_IN_PATH) ? '/' : path;
}
