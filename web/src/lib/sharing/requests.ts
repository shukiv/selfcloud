// SPDX-License-Identifier: AGPL-3.0-or-later

import { ApiError } from '../apiError';

// RequestOutcome is what the access-request form shows after it sends. The server answers 202 whether or
// not the item exists, so every answer about the item itself reads "sent". Only a rate limit, or a failure
// to reach the server at all, is reported differently: those say nothing about the item.
export type RequestOutcome = 'sent' | 'limited' | 'failed';

export function requestOutcome(err: unknown): RequestOutcome {
  if (err === undefined) return 'sent';
  if (err instanceof ApiError) {
    if (err.status === 0) return 'failed';
    if (err.code === 'rate_limited') return 'limited';
    return 'sent';
  }
  return 'failed';
}

// The text of the confirmation is one key, whatever the response was.
export const REQUEST_SENT_KEY = 'share.request.sent';
