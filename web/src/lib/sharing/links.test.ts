// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  captureSecret,
  forgetSecret,
  inheritedLinks,
  linkRoleOptions,
  openStep,
  ownLink,
  peekSecret,
  pendingKey,
  secretFromAddress,
  type Keep,
  type ShareLink,
} from './links';

// memoryKeep is a session storage stand-in. A throwing one stands for a browser that refuses storage.
function memoryKeep(): Keep & { data: Record<string, string> } {
  const data: Record<string, string> = {};
  return {
    data,
    getItem: (k) => (k in data ? data[k] : null),
    setItem: (k, v) => {
      data[k] = v;
    },
    removeItem: (k) => {
      delete data[k];
    },
  };
}

const refusing: Keep = {
  getItem: () => {
    throw new Error('denied');
  },
  setItem: () => {
    throw new Error('denied');
  },
  removeItem: () => {
    throw new Error('denied');
  },
};

const secret = 'A'.repeat(43);

describe('linkRoleOptions', () => {
  it('offers nothing to a caller that may not share', () => {
    expect(linkRoleOptions(null)).toEqual([]);
  });

  it('caps the roles at the caller, and never offers manager', () => {
    expect(linkRoleOptions('editor')).toEqual(['viewer', 'commenter', 'contributor', 'editor']);
    expect(linkRoleOptions('contributor')).toEqual(['viewer', 'commenter', 'contributor']);
    expect(linkRoleOptions('manager')).toEqual(['viewer', 'commenter', 'contributor', 'editor']);
  });
});

describe('ownLink and inheritedLinks', () => {
  const on = (id: string, node: string, from: string | null): ShareLink => ({
    id,
    node_id: node,
    audience: 'organisation',
    role: 'viewer',
    expires_at: null,
    created_at: '2026-10-29T00:00:00Z',
    inherited_from: from,
  });

  it('finds the link on the node, and the ones above it', () => {
    const links = [on('shl_a', 'nod_x', null), on('shl_b', 'nod_up', 'nod_up')];
    expect(ownLink(links, 'nod_x')?.id).toBe('shl_a');
    expect(ownLink(links, 'nod_y')).toBeNull();
    expect(inheritedLinks(links, 'nod_x').map((l) => l.id)).toEqual(['shl_b']);
  });
});

describe('secretFromAddress', () => {
  it('reads a secret of the server shape from the fragment', () => {
    expect(secretFromAddress(`#${secret}`)).toBe(secret);
    expect(secretFromAddress(secret)).toBe(secret);
  });

  it('refuses anything else', () => {
    expect(secretFromAddress('')).toBe('');
    expect(secretFromAddress('#')).toBe('');
    expect(secretFromAddress('#token=' + secret)).toBe('');
    expect(secretFromAddress(`#${'A'.repeat(42)}`)).toBe('');
    expect(secretFromAddress(`#${'A'.repeat(43)}=`)).toBe('');
    expect(secretFromAddress(`#${'A'.repeat(42)}/`)).toBe('');
  });
});

describe('captureSecret', () => {
  it('moves the secret into storage and strips the address at once', () => {
    const keep = memoryKeep();
    const replaced: string[] = [];
    const found = captureSecret(`#${secret}`, '/s', '', keep, (u) => replaced.push(u));
    expect(found).toBe(true);
    expect(JSON.parse(keep.data[pendingKey]).secret).toBe(secret);
    expect(replaced).toEqual(['/s']);
  });

  it('strips the address even when there is no secret, and stores nothing', () => {
    const keep = memoryKeep();
    const replaced: string[] = [];
    expect(captureSecret('#nope', '/s', '?x=1', keep, (u) => replaced.push(u))).toBe(false);
    expect(keep.data).toEqual({});
    expect(replaced).toEqual(['/s?x=1']);
  });

  it('reports no secret when storage refuses the write', () => {
    expect(captureSecret(`#${secret}`, '/s', '', refusing, () => {})).toBe(false);
  });
});

describe('peekSecret and forgetSecret', () => {
  it('reads a waiting secret without removing it, and forgets it once used', () => {
    const keep = memoryKeep();
    keep.data[pendingKey] = JSON.stringify({ secret, at: Date.now() });
    expect(peekSecret(keep)).toBe(secret);
    expect(keep.data[pendingKey]).toBeDefined();
    forgetSecret(keep);
    expect(peekSecret(keep)).toBe('');
  });

  it('drops a secret waiting more than ten minutes, so it does not outlive the visit', () => {
    const keep = memoryKeep();
    const start = 1_000_000;
    keep.data[pendingKey] = JSON.stringify({ secret, at: start });
    expect(peekSecret(keep, start + 9 * 60_000)).toBe(secret);
    expect(peekSecret(keep, start + 10 * 60_000 + 1)).toBe('');
    expect(keep.data[pendingKey]).toBeUndefined();
  });

  it('ignores a stored value of the wrong shape, and a refusing storage', () => {
    const keep = memoryKeep();
    keep.data[pendingKey] = 'short';
    expect(peekSecret(keep)).toBe('');
    keep.data[pendingKey] = JSON.stringify({ secret: 'short', at: Date.now() });
    expect(peekSecret(keep)).toBe('');
    expect(peekSecret(refusing)).toBe('');
    expect(() => forgetSecret(refusing)).not.toThrow();
  });
});

describe('openStep', () => {
  it('opens for a signed-in visitor, asks a signed-out one to sign in, and is dead without a secret', () => {
    expect(openStep(secret, true)).toBe('open');
    expect(openStep(secret, false)).toBe('sign-in');
    expect(openStep('', true)).toBe('dead');
    expect(openStep('', false)).toBe('dead');
  });
});
