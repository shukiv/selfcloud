// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { ApiError } from './apiError';
import { errorMessage, type Context } from './errors';

const invalid = (details: Record<string, unknown> = {}) => new ApiError(400, 'invalid_request', details, 0);

describe('errorMessage for invalid_request', () => {
  it('names a server without mail wherever the request was made, except a link page', () => {
    const err = invalid({ reason: 'mail_not_configured' });
    expect(errorMessage(err)).toEqual({ key: 'error.mailNotConfigured' });
    for (const ctx of ['login', 'password', 'admin', 'generic'] as const) {
      expect(errorMessage(err, ctx), ctx).toEqual({ key: 'error.mailNotConfigured' });
    }
  });

  it('keeps a plain invalid_request as the entered-fields sentence', () => {
    expect(errorMessage(invalid())).toEqual({ key: 'error.invalid' });
    expect(errorMessage(invalid({ reason: 'something_else' }))).toEqual({ key: 'error.invalid' });
  });

  it('leaves the link pages unchanged', () => {
    expect(errorMessage(invalid({ reason: 'mail_not_configured' }), 'link' as Context)).toEqual({ key: 'error.linkInvalid' });
  });
});