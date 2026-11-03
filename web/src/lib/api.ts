// SPDX-License-Identifier: AGPL-3.0-or-later

import { ApiError } from './apiError';
import { clearSession, currentCsrf, forcePasswordChange, session, sessionKey, signingOut, trackRequest } from './session';
import { get } from './store';
import { go, signInURL } from './router';

export { ApiError } from './apiError';

export interface RequestOptions {
  body?: unknown;
  // headers adds request headers, such as If-Match. The CSRF header is set by api itself.
  headers?: Record<string, string>;
  // noRedirect is for calls where a 401 is an answer, not an expired session: the start-up check,
  // and the sign-in and link pages themselves.
  noRedirect?: boolean;
  // allowWhileSigningOut is for the sign-out call itself; every other request is refused while a sign-out runs.
  allowWhileSigningOut?: boolean;
  // signal aborts the request when it fires (a sign-out gives its call a time limit).
  signal?: AbortSignal;
}

export type Method = 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE';

// endedHandler is set by lib/impersonation.ts: it restores the admin's session when a sign-in as has ended. It is a
// registered function, not an import, so the two modules do not import each other.
let endedHandler: ((expired: boolean) => void) | null = null;
export function onSignInAsEnded(fn: (expired: boolean) => void): void {
  endedHandler = fn;
}

// api calls the Selfcloud API on this origin. Cookies go with the request only to this site. Every
// request that changes something carries the CSRF token from the session.
export async function api<T = void>(method: Method, path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json', ...opts.headers };
  let body: string | undefined;
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(opts.body);
  }
  if (method !== 'GET') {
    const csrf = currentCsrf();
    if (csrf) headers['X-CSRF-Token'] = csrf;
  }

  // The session the request is made under. A request sent while signed in that is answered 401 after the session
  // has ended (a sign-out, or a sign-in as someone else) is a stale answer: it must not clear the session that
  // now exists, nor send the person to sign-in from the page they have moved on to. A request sent with no session
  // is left to the app's own guard, which sends a signed-out person to sign-in.
  const issued = sessionKey(get(session));

  // A sign-out is under way: no other request is sent, since it would reach the server after the session ended.
  if (signingOut() && !opts.allowWhileSigningOut) {
    throw new ApiError(0, 'signing_out', {}, 0);
  }

  // Every call but the sign-out's own is recorded while it is on its way, so a sign-out can abort it (session.ts).
  const ctl = new AbortController();
  opts.signal?.addEventListener('abort', () => ctl.abort(), { once: true });
  if (opts.signal?.aborted) ctl.abort();
  const forget = opts.allowWhileSigningOut ? () => undefined : trackRequest(ctl);
  let res: Response;
  try {
    res = await fetch(path, { method, headers, body, credentials: 'same-origin', cache: 'no-store', signal: ctl.signal });
  } catch {
    throw new ApiError(0, ctl.signal.aborted && signingOut() ? 'signing_out' : 'network', {}, 0);
  } finally {
    forget();
  }

  if (res.status === 204) return undefined as T;

  let data: unknown = null;
  try {
    data = await res.json();
  } catch {
    data = null;
  }
  const env = data as { error?: { code?: unknown; details?: unknown } } | null;
  const code = typeof env?.error?.code === 'string' ? env.error.code : 'internal';
  const raw = env?.error?.details;
  const details = typeof raw === 'object' && raw !== null ? (raw as Record<string, unknown>) : {};

  // The one-hour sign-in as has ended: the admin's own session is restored in place of it, and the page carries on
  // with that session. The request that found out fails with its code; the page shows nothing more for it.
  if (res.status === 401 && !opts.noRedirect && details.reason === 'impersonation_ended') {
    endedHandler?.(true);
    throw new ApiError(401, 'impersonation_ended', details, 0);
  }
  // Turning admin.tenant_access off ends a sign-in as at once (decision 41): the admin's session is restored the same way,
  // without the hour's message.
  if (res.status === 401 && !opts.noRedirect && details.reason === 'tenant_access_off') {
    endedHandler?.(false);
    throw new ApiError(401, code, details, 0);
  }
  if (res.status === 401 && !opts.noRedirect && issued !== '' && sessionKey(get(session)) === issued) {
    clearSession();
    go(signInURL(), { replace: true });
  }
  // The person must change the password first: every route refuses until then, so the change-password page is opened.
  if (res.status === 403 && !opts.noRedirect && details.reason === 'password_change_required') {
    forcePasswordChange();
    go('/change-password', { replace: true });
  }
  if (!res.ok) {
    const retry = Number(res.headers.get('Retry-After') ?? 0);
    throw new ApiError(res.status, code, details, Number.isFinite(retry) ? retry : 0);
  }
  return data as T;
}
