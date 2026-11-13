// SPDX-License-Identifier: AGPL-3.0-or-later

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
