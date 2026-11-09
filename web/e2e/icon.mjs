// SPDX-License-Identifier: AGPL-3.0-or-later

// The e2e brand's icon file (decision 119): a square PNG with a green edge and a red square in the middle. The harness writes it
// with Node's zlib and a CRC-32 of its own, so it needs no image library. pwa.mjs checks that Chromium draws the app icons and the
// favicon from it.
import { deflateSync, inflateSync } from 'node:zlib';

export const ICON_SIZE = 512;
// ICON_INNER is the middle of the picture, and ICON_OUTER its edge.
export const ICON_INNER = [220, 0, 0];
export const ICON_OUTER = [0, 170, 0];

const CRC_TABLE = (() => {
  const table = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    table[n] = c >>> 0;
  }
  return table;
})();

function crc32(bytes) {
  let c = 0xffffffff;
  for (const b of bytes) c = CRC_TABLE[(c ^ b) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

// chunk is one PNG chunk: its length, its type and data, and the CRC of the type and data.
function chunk(type, data) {
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length);
  const typed = Buffer.concat([Buffer.from(type, 'latin1'), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(typed));
  return Buffer.concat([length, typed, crc]);
}

// pngPixel reads one pixel of a non-interlaced 8-bit RGB or RGBA PNG, as [r, g, b, a]. The check of a screenshot uses it: a
// screenshot is read here, not by the page, so a picture the page cannot draw still shows what the screen showed.
export function pngPixel(png, x, y) {
  let offset = 8;
  let width = 0;
  let height = 0;
  let colour = 0;
  const idat = [];
  while (offset < png.length) {
    const length = png.readUInt32BE(offset);
    const type = png.toString('latin1', offset + 4, offset + 8);
    const data = png.subarray(offset + 8, offset + 8 + length);
    if (type === 'IHDR') {
      width = data.readUInt32BE(0);
      height = data.readUInt32BE(4);
      if (data[8] !== 8 || (data[9] !== 2 && data[9] !== 6) || data[12] !== 0) throw new Error('pngPixel: not an 8-bit RGB or RGBA PNG');
      colour = data[9];
    }
    if (type === 'IDAT') idat.push(data);
    offset += 12 + length;
  }
  const bpp = colour === 6 ? 4 : 3;
  const stride = width * bpp;
  const raw = inflateSync(Buffer.concat(idat));
  const out = Buffer.alloc(stride * height);
  for (let row = 0; row < height; row++) {
    const filter = raw[row * (stride + 1)];
    for (let i = 0; i < stride; i++) {
      const input = raw[row * (stride + 1) + 1 + i];
      const left = i >= bpp ? out[row * stride + i - bpp] : 0;
      const up = row > 0 ? out[(row - 1) * stride + i] : 0;
      const upLeft = row > 0 && i >= bpp ? out[(row - 1) * stride + i - bpp] : 0;
      let value;
      switch (filter) {
        case 0: value = input; break;
        case 1: value = input + left; break;
        case 2: value = input + up; break;
        case 3: value = input + ((left + up) >> 1); break;
        case 4: {
          const p = left + up - upLeft;
          const pa = Math.abs(p - left);
          const pb = Math.abs(p - up);
          const pc = Math.abs(p - upLeft);
          value = input + (pa <= pb && pa <= pc ? left : pb <= pc ? up : upLeft);
          break;
        }
        default: throw new Error(`pngPixel: filter ${filter}`);
      }
      out[row * stride + i] = value & 0xff;
    }
  }
  const at = y * stride + x * bpp;
  return [out[at], out[at + 1], out[at + 2], bpp === 4 ? out[at + 3] : 255];
}

// brandIconPng is the icon file: an 8-bit RGB PNG, size pixels square, with the inner colour in the middle half of each side.
export function brandIconPng(size = ICON_SIZE) {
  const rows = [];
  for (let y = 0; y < size; y++) {
    // Each row starts with its filter type, 0 for none.
    const row = Buffer.alloc(1 + size * 3);
    for (let x = 0; x < size; x++) {
      const inner = x >= size / 4 && x < (3 * size) / 4 && y >= size / 4 && y < (3 * size) / 4;
      const colour = inner ? ICON_INNER : ICON_OUTER;
      row[1 + x * 3] = colour[0];
      row[2 + x * 3] = colour[1];
      row[3 + x * 3] = colour[2];
    }
    rows.push(row);
  }
  const header = Buffer.alloc(13);
  header.writeUInt32BE(size, 0);
  header.writeUInt32BE(size, 4);
  header[8] = 8; // bit depth
  header[9] = 2; // colour type: RGB. Compression, filter and interlace stay 0.
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', header),
    chunk('IDAT', deflateSync(Buffer.concat(rows))),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}
