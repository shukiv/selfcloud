// SPDX-License-Identifier: AGPL-3.0-or-later

// ApiError is a refused request. Its message is never shown: the interface shows text by code. The one exception is the
// server's own English text, kept in serverMessage, which a caller shows only where the text is the point: the reason a
// picture was refused (the Branding card).
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: Record<string, unknown>;
  // retryAfter is the Retry-After header in seconds, or 0.
  readonly retryAfter: number;
  // serverMessage is the message the server sent with the refusal, or an empty text.
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
