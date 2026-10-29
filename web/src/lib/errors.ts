// SPDX-License-Identifier: AGPL-3.0-or-later

import { ApiError } from './api';

// Context is where a request was made, which decides what a generic code means.
export type Context = 'login' | 'link' | 'password' | 'generic';

// Message is a catalogue key to show. plural, when set, is the count for a plural key.
export interface Message {
  key: string;
  plural?: number;
}

// errorMessage maps an API error code to a message. The server's English text is never shown.
export function errorMessage(err: unknown, ctx: Context = 'generic'): Message {
  if (!(err instanceof ApiError)) return { key: 'error.generic' };
  switch (err.code) {
    case 'network':
      return { key: 'error.network' };
    case 'unauthenticated':
      return { key: ctx === 'login' ? 'error.unauthenticated' : 'error.sessionEnded' };
    case 'user_not_verified':
      return { key: 'error.notVerified' };
    case 'user_disabled':
      return { key: 'error.userDisabled' };
    case 'account_suspended':
      return { key: 'error.accountSuspended' };
    case 'rate_limited':
      return { key: 'error.rateLimited', plural: Math.max(1, Math.ceil(err.retryAfter / 60)) };
    case 'invalid_request':
      // On the link pages the server answers every bad link with invalid_request.
      return { key: ctx === 'link' ? 'error.linkInvalid' : 'error.invalid' };
    case 'token_expired':
      return { key: 'error.linkInvalid' };
    case 'not_found':
      return { key: ctx === 'link' ? 'error.linkInvalid' : 'error.notFound' };
    case 'forbidden':
      return { key: ctx === 'password' ? 'error.currentPassword' : 'error.forbidden' };
    case 'conflict':
      return { key: 'error.conflict' };
    case 'internal':
    case 'unavailable':
      return { key: 'error.server' };
    default:
      return { key: 'error.generic' };
  }
}

// isLinkFailure reports whether a link (verify or reset) is no longer usable, so the page shows no
// form.
export function isLinkFailure(err: unknown): boolean {
  return (
    err instanceof ApiError &&
    (err.code === 'invalid_request' || err.code === 'token_expired' || err.code === 'not_found')
  );
}
