// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it } from 'vitest';
// Through the alias in vite.config.ts, this import is the shim, not rc-util's own module.
import { updateCSS, removeCSS, clearContainerCache } from 'rc-util/es/Dom/dynamicCSS';
import { readNonce } from './nonce';

function setMeta(nonce: string | null): void {
  document.querySelectorAll('meta[property="csp-nonce"]').forEach((m) => m.remove());
  if (nonce === null) return;
  const meta = document.createElement('meta');
  meta.setAttribute('property', 'csp-nonce');
  meta.setAttribute('nonce', nonce);
  document.head.appendChild(meta);
}

afterEach(() => {
  setMeta(null);
  removeCSS('shim-test');
  clearContainerCache();
});

describe('readNonce', () => {
  it('is empty when the page has no nonce meta', () => {
    setMeta(null);
    expect(readNonce()).toBe('');
  });

  it('reads the nonce from the meta tag', () => {
    setMeta('abc123==');
    expect(readNonce()).toBe('abc123==');
  });
});

describe('dynamicCSS shim', () => {
  it('fills in the page nonce when the caller passes none', () => {
    setMeta('pagenonce');
    const node = updateCSS('.x{}', 'shim-test') as HTMLStyleElement & { nonce?: string };
    expect(node.nonce).toBe('pagenonce');
    expect(node.getAttribute('rc-util-key')).toBe('shim-test');
  });

  it('keeps a nonce the caller passes', () => {
    setMeta('pagenonce');
    const node = updateCSS('.x{}', 'shim-test', { csp: { nonce: 'callernonce' } }) as HTMLStyleElement & {
      nonce?: string;
    };
    expect(node.nonce).toBe('callernonce');
  });

  it('writes no nonce when the page has none', () => {
    setMeta(null);
    const node = updateCSS('.x{}', 'shim-test') as HTMLStyleElement & { nonce?: string };
    expect(node.nonce ?? '').toBe('');
  });
});
