// SPDX-License-Identifier: AGPL-3.0-or-later

// SHA-256 for the upload client. Two uses:
//   - the fingerprint of an upload's first MiB, which a resume must match. It is always computed: with
//     crypto.subtle where the page has it (HTTPS or localhost), otherwise with sha256Pure below.
//   - each piece's Upload-Checksum. It is sent only with crypto.subtle: pure JavaScript over 8 MiB or more per
//     piece would slow large uploads, so without it no checksum header is sent and the server still checks
//     the length and the offsets.

export const FINGERPRINT_BYTES = 1024 * 1024;

const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

function rotr(x: number, n: number): number {
  return (x >>> n) | (x << (32 - n));
}

// sha256Pure is SHA-256 (FIPS 180-4) in plain TypeScript. It is the fallback where crypto.subtle is missing.
// Its answers match crypto.subtle's; the tests check that on the padding edges.
export function sha256Pure(data: Uint8Array): Uint8Array {
  const h = new Uint32Array([
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
  ]);
  // The message, a 0x80 byte, zeros, and the bit length as 64 bits, to a whole number of 64-byte blocks.
  const total = ((data.length + 9 + 63) >> 6) << 6;
  const padded = new Uint8Array(total);
  padded.set(data);
  padded[data.length] = 0x80;
  const view = new DataView(padded.buffer);
  view.setUint32(total - 8, Math.floor(data.length / 0x20000000));
  view.setUint32(total - 4, (data.length * 8) >>> 0);

  const w = new Uint32Array(64);
  for (let off = 0; off < total; off += 64) {
    for (let i = 0; i < 16; i++) w[i] = view.getUint32(off + i * 4);
    for (let i = 16; i < 64; i++) {
      const s0 = rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >>> 3);
      const s1 = rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >>> 10);
      w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }
    let a = h[0];
    let b = h[1];
    let c = h[2];
    let d = h[3];
    let e = h[4];
    let f = h[5];
    let g = h[6];
    let hh = h[7];
    for (let i = 0; i < 64; i++) {
      const S1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
      const ch = (e & f) ^ (~e & g);
      const t1 = (hh + S1 + ch + K[i] + w[i]) >>> 0;
      const S0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
      const maj = (a & b) ^ (a & c) ^ (b & c);
      const t2 = (S0 + maj) >>> 0;
      hh = g;
      g = f;
      f = e;
      e = (d + t1) >>> 0;
      d = c;
      c = b;
      b = a;
      a = (t1 + t2) >>> 0;
    }
    h[0] += a;
    h[1] += b;
    h[2] += c;
    h[3] += d;
    h[4] += e;
    h[5] += f;
    h[6] += g;
    h[7] += hh;
  }
  const out = new Uint8Array(32);
  const ov = new DataView(out.buffer);
  for (let i = 0; i < 8; i++) ov.setUint32(i * 4, h[i]);
  return out;
}

function subtle(): SubtleCrypto | null {
  return globalThis.crypto?.subtle ?? null;
}

function toBase64(bytes: Uint8Array): string {
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}

// sha256Base64 is the base64 SHA-256 of a piece, for Upload-Checksum. It is null where crypto.subtle is missing,
// and then no checksum is sent.
export async function sha256Base64(piece: Blob): Promise<string | null> {
  const s = subtle();
  if (!s) return null;
  return toBase64(new Uint8Array(await s.digest('SHA-256', await piece.arrayBuffer())));
}

// headFingerprint is the lowercase hex SHA-256 of the first MiB of a file. It is always computed: natively when
// the page has crypto.subtle, and otherwise with sha256Pure.
export async function headFingerprint(file: Blob): Promise<string> {
  const bytes = new Uint8Array(await file.slice(0, FINGERPRINT_BYTES).arrayBuffer());
  const s = subtle();
  const d = s ? new Uint8Array(await s.digest('SHA-256', bytes)) : sha256Pure(bytes);
  return Array.from(d, (b) => b.toString(16).padStart(2, '0')).join('');
}
