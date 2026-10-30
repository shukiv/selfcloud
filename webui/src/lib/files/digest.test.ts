// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from 'vitest';
import { FINGERPRINT_BYTES, headFingerprint, sha256Base64, sha256Pure } from './digest';

function hex(b: Uint8Array): string {
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
}

// random returns n random bytes; crypto.getRandomValues takes at most 64 KiB a call.
function random(n: number): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(new ArrayBuffer(n));
  for (let off = 0; off < n; off += 65536) {
    crypto.getRandomValues(out.subarray(off, Math.min(n, off + 65536)));
  }
  return out;
}

// native is crypto.subtle's SHA-256, the reference the fallback must match.
async function native(b: BufferSource): Promise<Uint8Array> {
  return new Uint8Array(await crypto.subtle.digest('SHA-256', b));
}

describe('the pure SHA-256 fallback', () => {
  it('matches crypto.subtle on the empty input and on "abc"', async () => {
    const empty = new Uint8Array(0);
    const abc = new TextEncoder().encode('abc');
    expect(hex(sha256Pure(empty))).toBe(hex(await native(empty)));
    expect(hex(sha256Pure(abc))).toBe('ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad');
  });

  it('matches crypto.subtle on the padding edges: 55, 56, 63, 64 and 65 bytes', async () => {
    for (const n of [55, 56, 63, 64, 65]) {
      const b = random(n);
      expect(hex(sha256Pure(b)), `length ${n}`).toBe(hex(await native(b)));
    }
  });

  it('matches crypto.subtle on a 1 MiB random buffer', async () => {
    const b = random(FINGERPRINT_BYTES);
    expect(hex(sha256Pure(b))).toBe(hex(await native(b)));
  });
});

describe('upload digests', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('gives the base64 SHA-256 of a piece, as Upload-Checksum takes it', async () => {
    expect(await sha256Base64(new Blob(['abc']))).toBe('ungWv48Bz+pBQUDeXa4iI7ADYaOWF3qctBD/YfIAFa0=');
  });

  it('fingerprints the first MiB only, so a change past it leaves the fingerprint alone', async () => {
    const head = new Uint8Array(FINGERPRINT_BYTES).fill(7);
    const a = new File([head, 'x'], 'big.bin');
    const b = new File([head, 'y'], 'big.bin');
    const c = new File([new Uint8Array(FINGERPRINT_BYTES).fill(8), 'x'], 'big.bin');
    expect(await headFingerprint(a)).toBe(await headFingerprint(b));
    expect(await headFingerprint(a)).not.toBe(await headFingerprint(c));
    expect(await headFingerprint(a)).toMatch(/^[0-9a-f]{64}$/);
  });

  it('without crypto.subtle, the fingerprint is still the real one, and no piece checksum is made', async () => {
    const bytes = random(FINGERPRINT_BYTES + 5);
    const file = new File([bytes], 'big.bin');
    const withSubtle = await headFingerprint(file);
    expect(withSubtle).toBe(hex(await native(bytes.slice(0, FINGERPRINT_BYTES))));

    vi.stubGlobal('crypto', { getRandomValues: crypto.getRandomValues.bind(crypto) });
    expect(await headFingerprint(file)).toBe(withSubtle);
    expect(await sha256Base64(new Blob(['abc']))).toBeNull();
  });
});
