// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { signIn } from './auth';

// signIn posts the sign-in body the server reads. The "Keep me signed in" choice travels as a JSON boolean when the
// page has one, and is left out when it has none, so the server keeps the session as it did before the box (decision 75).
describe('signIn request body', () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn(async () => new Response(JSON.stringify({ user: { id: 'usr_1' }, csrf_token: 'csrf' }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function sentBody(): Record<string, unknown> {
    const init = fetchMock.mock.calls[0][1] as { body: string };
    return JSON.parse(init.body) as Record<string, unknown>;
  }

  it('sends remember true as a JSON boolean', async () => {
    await signIn('ann@example.com', 'a passphrase', undefined, true);
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/auth/login');
    expect(sentBody()).toEqual({ email: 'ann@example.com', password: 'a passphrase', remember: true });
    expect(typeof sentBody().remember).toBe('boolean');
  });

  it('sends remember false as a JSON boolean', async () => {
    await signIn('ann@example.com', 'a passphrase', undefined, false);
    expect(sentBody()).toEqual({ email: 'ann@example.com', password: 'a passphrase', remember: false });
  });

  it('leaves remember out when the caller gives none', async () => {
    await signIn('ann@example.com', 'a passphrase');
    expect('remember' in sentBody()).toBe(false);
  });

  it('sends the account with the choice', async () => {
    await signIn('ann@example.com', 'a passphrase', 'acc_1', true);
    expect(sentBody()).toEqual({ email: 'ann@example.com', password: 'a passphrase', account: 'acc_1', remember: true });
  });
});
