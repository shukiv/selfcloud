// SPDX-License-Identifier: AGPL-3.0-or-later

// ApiError is a refused request. Its code is what the interface shows, never the server's English text. The one
// exception is serverMessage, kept only when the call asks for it (api's nameBadValue): where the server names the
// bad value itself (an embed origin that is not one), the page shows that sentence under the field. Every other
// refusal leaves it empty.
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: Record<string, unknown>;
  // retryAfter is the Retry-After header in seconds, or 0.
  readonly retryAfter: number;
  // serverMessage is the server's own message for the refusal, or "" when it sent none.
  readonly serverMessage: string;

  constructor(status: number, code: string, details: Record<string, unknown>, retryAfter: number, serverMessage = '') {
    super(code);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.details = details;
    this.retryAfter = retryAfter;
    this.serverMessage = serverMessage;
  }
}
