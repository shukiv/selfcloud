// SPDX-License-Identifier: AGPL-3.0-or-later

import { ApiError } from './apiError';
import { clearSession, currentCsrf, forcePasswordChange, session, sessionKey, signingOut, trackRequest } from './session';
import { get } from './store';
import { go, signInURL } from './router';

export { ApiError } from './apiError';

export interface RequestOptions {
  body?: unknown;
  // raw sends the bytes as they are, with their own content type, in place of a JSON body (a picture upload).
  raw?: { body: Blob; type: string };
  // headers adds request headers, such as If-Match. The CSRF header is set by api itself.
  headers?: Record<string, string>;
  // noRedirect is for calls where a 401 is an answer, not an expired session: the start-up check,
  // and the sign-in and link pages themselves.
  noRedirect?: boolean;
  // allowWhileSigningOut is for the sign-out call itself; every other request is refused while a sign-out runs.
  allowWhileSigningOut?: boolean;
  // signal aborts the request when it fires (a sign-out gives its call a time limit, and a cancelled dialog its write).
  signal?: AbortSignal;
  // timeoutMs is how long the call may wait for its answer, headers and body both; 0 turns the limit off.
  timeoutMs?: number;
}

// REQUEST_TIMEOUT_MS is the default wait for an answer. A call here is small JSON that the server answers in well under
// a second, so a minute with no answer means the server or the network has stopped. Uploads and downloads do not go
// through api(), so they have no such limit.
export const REQUEST_TIMEOUT_MS = 60_000;
const EXPIRED = Symbol('expired');

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
  let body: string | Blob | undefined;
  if (opts.raw !== undefined) {
    headers['Content-Type'] = opts.raw.type;
    body = opts.raw.body;
  } else if (opts.body !== undefined) {
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
  // The timer covers the answer's headers and its body. When it fires, the request is aborted and the call fails as a timeout.
  let timedOut = false;
  let expire: () => void = () => undefined;
  const expired = new Promise<typeof EXPIRED>((resolve) => {
    expire = () => resolve(EXPIRED);
  });
  // An abort (a cancel, a sign-out, or the timer) ends the body read at once, whether or not the body has started.
  ctl.signal.addEventListener('abort', () => expire(), { once: true });
  if (ctl.signal.aborted) expire();
  const limit = opts.timeoutMs ?? REQUEST_TIMEOUT_MS;
  const timer = limit > 0 ? window.setTimeout(() => {
    timedOut = true;
    ctl.abort();
    expire();
  }, limit) : undefined;
  // A timeout says whether the call was a write: only a write may have been saved (the message says so).
  const failed = (): ApiError =>
    timedOut
      ? new ApiError(0, 'timeout', { write: method !== 'GET' }, 0)
      : new ApiError(0, ctl.signal.aborted && signingOut() ? 'signing_out' : opts.signal?.aborted ? 'cancelled' : 'network', {}, 0);
  let res: Response;
  try {
    res = await fetch(path, { method, headers, body, credentials: 'same-origin', cache: 'no-store', signal: ctl.signal });
  } catch {
    window.clearTimeout(timer);
    throw failed();
  } finally {
    forget();
  }

  if (res.status === 204) {
    window.clearTimeout(timer);
    return undefined as T;
  }

  // The body is read against the same timer: a body that never ends is a timeout, whether or not the fetch honours the abort.
  let data: unknown = null;
  try {
    const got: unknown = await Promise.race([res.json().catch(() => null), expired]);
    // A timeout or a cancel during the body read is a failure, never an answer with no body (a null would read as success).
    if (got === EXPIRED || timedOut || ctl.signal.aborted) throw failed();
    data = got;
  } finally {
    window.clearTimeout(timer);
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
