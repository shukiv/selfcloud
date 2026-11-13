// SPDX-License-Identifier: AGPL-3.0-or-later

import { QueryClient } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { LIMITS_STALE_MS, readLimits, seedLimits } from './api';

// The installation's upload limits come from /api/v1/info. The boot read seeds them; a read of the files page uses them while
// they are fresh (five minutes) and reads /info again once they are older, since the Settings page can change them
// (decision 127).
describe('readLimits uses the limits while they are fresh', () => {
  const fresh = { max_chunk_size: 8_388_608, max_file_size: 0 };
  const changed = { limits: { max_chunk_size: 5, max_file_size: 6 } };
  let qc: QueryClient;

  beforeEach(() => {
    qc = new QueryClient();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('answers a fresh seeded value with no request', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify(changed), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    seedLimits(qc, fresh, 0);
    await expect(readLimits(qc)).resolves.toEqual(fresh);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('reads /info again for a seeded value older than five minutes', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify(changed), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    seedLimits(qc, fresh, LIMITS_STALE_MS + 1000);
    await expect(readLimits(qc)).resolves.toEqual({ max_chunk_size: 5, max_file_size: 6 });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('reads /info when nothing was seeded, and keeps the answer while it is fresh', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify(changed), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    await expect(readLimits(qc)).resolves.toEqual({ max_chunk_size: 5, max_file_size: 6 });
    await expect(readLimits(qc)).resolves.toEqual({ max_chunk_size: 5, max_file_size: 6 });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
