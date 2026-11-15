// SPDX-License-Identifier: AGPL-3.0-or-later

// The installation's outside sign-in page (decision 185). The server names it in GET /api/v1/info's sign_in, and
// answers GET /login with a redirect to it; this module keeps what the last info read said, and moves the browser the
// same way for the moves the interface makes itself: a person with no session goes to the outside page, and a sign-out
// goes to the signed-out URL, or to the signed-out page when only the sign-in page is named.

import { go, location, signInURL } from './router';
import { get } from './store';
import { safeNext } from './next';

// SignInInfo is what the installation says of sign-in: the outside sign-in page (empty for none), the page a sign-out
// goes to (empty for none), and whether account users may sign in with a password. Installation admins always may.
export interface SignInInfo {
  externalUrl: string;
  signedOutUrl: string;
  password: boolean;
}

const NONE: SignInInfo = { externalUrl: '', signedOutUrl: '', password: true };

let info: SignInInfo = NONE;

// setSignInInfo keeps the sign-in settings of the last info read; null puts the defaults back, as an unread info leaves them.
export function setSignInInfo(next: SignInInfo | null): void {
  info = next ?? NONE;
}

export function signInInfo(): SignInInfo {
  return info;
}

// SIGNED_OUT_PATH is the page a signed-out person sees when the loop guard refuses another redirect, or when a sign-out
// has no signed-out URL to go to while an outside sign-in page is set. It never redirects by itself.
export const SIGNED_OUT_PATH = '/signed-out';

// outsideSignIn is the outside sign-in page for the path given, or null when the installation names none. The path is
// checked as a sign-in's next is, and encoded into the URL's {next} placeholder.
export function outsideSignIn(next: string): string | null {
  if (info.externalUrl === '') return null;
  return info.externalUrl.replaceAll('{next}', encodeURIComponent(safeNext(next)));
}

// The loop guard: a sign-in that never lands must not send the browser round for good. Two redirects within a minute
// are let through; a third shows the signed-out page instead. The count lives in the tab's sessionStorage, so it
// survives the page loads a redirect causes; a storage that cannot be read or written lets the redirect go.
const HOPS_KEY = 'selfcloud.outsideSignInHops';
const HOP_WINDOW_MS = 60_000;

function hopAllowed(): boolean {
  try {
    const now = Date.now();
    const raw = window.sessionStorage.getItem(HOPS_KEY);
    const parsed: unknown = raw === null ? [] : JSON.parse(raw);
    const hops = (Array.isArray(parsed) ? parsed : []).filter((t): t is number => typeof t === 'number' && now - t < HOP_WINDOW_MS);
    if (hops.length >= 2) return false;
    hops.push(now);
    window.sessionStorage.setItem(HOPS_KEY, JSON.stringify(hops));
    return true;
  } catch {
    return true;
  }
}

// goOutside assigns the outside page, or shows the signed-out page when the loop guard refuses a third hop in a minute.
export function goOutside(url: string): void {
  if (hopAllowed()) {
    window.location.assign(url);
    return;
  }
  go(SIGNED_OUT_PATH, { replace: true });
}

// sendToSignIn moves a person with no session to sign-in: the outside page when the installation names one, else the
// app's own sign-in page, remembering the path the person was on as today.
export function sendToSignIn(from: string = get(location).path + get(location).search): void {
  const url = outsideSignIn(from);
  if (url !== null) {
    goOutside(url);
    return;
  }
  go(signInURL(from), { replace: true });
}

// goSignedOut is where the browser goes after an explicit sign-out: the signed-out URL when one is set, else the
// signed-out page when an outside sign-in page is set, else today's sign-in page.
export function goSignedOut(): void {
  if (info.signedOutUrl !== '') {
    window.location.assign(info.signedOutUrl);
    return;
  }
  if (info.externalUrl !== '') {
    go(SIGNED_OUT_PATH, { replace: true });
    return;
  }
  go('/login', { replace: true });
}