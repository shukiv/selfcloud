// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeAll, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../apiError';

// fileErrors reaches the API client, which reads the window location when it loads; the test gives it a stub first.
let fileErrorText: typeof import('./fileErrors').fileErrorText;
beforeAll(async () => {
  vi.stubGlobal('window', { location: { pathname: '/', search: '', hash: '' }, addEventListener: () => undefined });
  ({ fileErrorText } = await import('./fileErrors'));
});

const refused = (details: Record<string, unknown>) => new ApiError(507, 'quota_exceeded', details, 0);

describe('fileErrorText for a person limit', () => {
  it('names no numbers when the caller may not see them', () => {
    const text = fileErrorText(refused({ scope: 'user' }), 'en');
    expect(text).toBe('Your storage limit is full. Free some space or ask the account owner to raise your limit.');
    expect(text).not.toMatch(/\d/);
  });

  it('gives the numbers to the charged person', () => {
    const text = fileErrorText(refused({ scope: 'user', required_bytes: 2_000_000_000, available_bytes: 500_000_000 }), 'en');
    expect(text).toContain('your storage limit');
    expect(text).toContain('2 GB');
  });

  it('keeps the account wording for an account refusal', () => {
    expect(fileErrorText(refused({ scope: 'quota', required_bytes: 1000, available_bytes: 0 }), 'en')).toContain('your quota');
  });
});
