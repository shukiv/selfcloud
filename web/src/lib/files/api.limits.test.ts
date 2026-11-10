// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { installLimits, serverLimits } from './api';

// The boot read of /api/v1/info sets the installation's upload limits; the files page sizes its uploads from them and does not
// read /info again (perf-1 3.2: /info was read twice in one page load).
describe('serverLimits reads /api/v1/info only when the boot read has not given the limits', () => {
  beforeEach(() => {
    installLimits.set(null);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    installLimits.set(null);
  });

  it('answers from the boot read, with no request', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ limits: { max_chunk_size: 1, max_file_size: 2 } }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    installLimits.set({ max_chunk_size: 8_388_608, max_file_size: 0 });
    await expect(serverLimits()).resolves.toEqual({ max_chunk_size: 8_388_608, max_file_size: 0 });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('reads /info when nothing has given the limits, and keeps the answer for the next call', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ limits: { max_chunk_size: 5, max_file_size: 6 } }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    await expect(serverLimits()).resolves.toEqual({ max_chunk_size: 5, max_file_size: 6 });
    await expect(serverLimits()).resolves.toEqual({ max_chunk_size: 5, max_file_size: 6 });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
