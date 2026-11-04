// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { ApiError } from './api';
import { statusZeroKey } from './errors';

// A request that never got a status is a network failure, unless it timed out as a write: then the server may have saved the
// change, and the page must say so (guard: a write's timeout is not the network message).
describe('the message for a request with no status', () => {
  it('says the change may have been saved when a write timed out', () => {
    expect(statusZeroKey(new ApiError(0, 'timeout', { write: true }, 0))).toBe('error.timeout');
  });

  it('says the server did not answer when a read timed out, and the network when the connection failed', () => {
    expect(statusZeroKey(new ApiError(0, 'timeout', { write: false }, 0))).toBe('error.timeoutRead');
    expect(statusZeroKey(new ApiError(0, 'network', {}, 0))).toBe('error.network');
  });
});
