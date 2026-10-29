// SPDX-License-Identifier: AGPL-3.0-or-later

import { clearSession, currentCsrf } from './session';
import { go, signInURL } from './router';

// ApiError is a refused request. Its message is never shown: the interface shows text by code.
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: Record<string, unknown>;
  // retryAfter is the Retry-After header in seconds, or 0.
  readonly retryAfter: number;

  constructor(status: number, code: string, details: Record<string, unknown>, retryAfter: number) {
    super(code);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.details = details;
    this.retryAfter = retryAfter;
  }
}

export interface RequestOptions {
  body?: unknown;
  // noRedirect is for calls where a 401 is an answer, not an expired session: the start-up check,
  // and the sign-in and link pages themselves.
  noRedirect?: boolean;
}

export type Method = 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE';

// api calls the Selfcloud API on this origin. Cookies go with the request only to this site. Every
// request that changes something carries the CSRF token from the session.
export async function api<T = void>(method: Method, path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  let body: string | undefined;
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(opts.body);
  }
  if (method !== 'GET') {
    const csrf = currentCsrf();
    if (csrf) headers['X-CSRF-Token'] = csrf;
  }

  let res: Response;
  try {
    res = await fetch(path, { method, headers, body, credentials: 'same-origin', cache: 'no-store' });
  } catch {
    throw new ApiError(0, 'network', {}, 0);
  }

  if (res.status === 401 && !opts.noRedirect) {
    clearSession();
    go(signInURL(), { replace: true });
  }
  if (res.status === 204) return undefined as T;

  let data: unknown = null;
  try {
    data = await res.json();
  } catch {
    data = null;
  }
  if (!res.ok) {
    const env = data as { error?: { code?: unknown; details?: unknown } } | null;
    const code = typeof env?.error?.code === 'string' ? env.error.code : 'internal';
    const raw = env?.error?.details;
    const details = typeof raw === 'object' && raw !== null ? (raw as Record<string, unknown>) : {};
    const retry = Number(res.headers.get('Retry-After') ?? 0);
    throw new ApiError(res.status, code, details, Number.isFinite(retry) ? retry : 0);
  }
  return data as T;
}
