// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../apiError';
import { childrenPage, statOrNull } from './api';
import { locate } from './location';
import type { Node } from './types';

vi.mock('./api', () => ({
  childrenPage: vi.fn(),
  statOrNull: vi.fn(),
}));

const childrenMock = vi.mocked(childrenPage);
const statMock = vi.mocked(statOrNull);

const page = { items: [] as Node[], next_cursor: undefined };

describe('locate reads the root once', () => {
  beforeEach(() => {
    childrenMock.mockReset();
    statMock.mockReset();
  });

  // The folder page reads its root listing anyway. A listing it hands to locate is the reachability check, so the root
  // is not read a second time (perf-1 3.2: locate's probe downloaded a full page and threw it away).
  it('uses the listing the page reads as the reachability check, with no request of its own', async () => {
    const here = await locate('fld_a', null, [], Promise.resolve(page));
    expect(here).not.toBeNull();
    expect(here?.rootReachable).toBe(true);
    expect(childrenMock).not.toHaveBeenCalled();
  });

  it('is missing when the listing it is given answers not_found', async () => {
    const refused = Promise.reject(new ApiError(404, 'not_found', {}, 0));
    refused.catch(() => undefined);
    await expect(locate('fld_a', null, [], refused)).resolves.toBeNull();
    expect(childrenMock).not.toHaveBeenCalled();
  });

  it('keeps the probe for a caller that gives no listing', async () => {
    childrenMock.mockResolvedValueOnce(page);
    const here = await locate('fld_a', null, []);
    expect(here?.rootReachable).toBe(true);
    expect(childrenMock).toHaveBeenCalledTimes(1);
  });
});
