// SPDX-License-Identifier: AGPL-3.0-or-later

// The slice s4a checks: the grid's thumbnails and the zip download, in the browser. Two real PNG files go up through the
// API, so the server decodes them in its own child process (decision 33). The member then sees a thumbnail in the tiles,
// and downloads the folder as a zip. The browser saves the zip, and the check unpacks it and compares every entry with
// the bytes that were uploaded.
import { mkdir, readFile, readdir } from 'node:fs/promises';
import { deflateSync, inflateRawSync } from 'node:zlib';
import { join } from 'node:path';

const q = (v) => JSON.stringify(v);

// CRC-32, as PNG chunks and zip entries use it.
const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();
export function crc32(buf) {
  let c = 0xffffffff;
  for (const b of buf) c = CRC_TABLE[(c ^ b) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function pngChunk(type, data) {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length);
  const body = Buffer.concat([Buffer.from(type, 'latin1'), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body));
  return Buffer.concat([len, body, crc]);
}

// pngOf encodes a w by h picture, eight bits a channel, RGB, with no filter; pixel(x, y) gives [r, g, b].
export function pngOf(w, h, pixel) {
  const row = w * 3 + 1;
  const raw = Buffer.alloc(row * h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const [r, g, b] = pixel(x, y);
      const o = y * row + 1 + x * 3;
      raw[o] = r;
      raw[o + 1] = g;
      raw[o + 2] = b;
    }
  }
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0);
  ihdr.writeUInt32BE(h, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 2; // colour type: RGB
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    pngChunk('IHDR', ihdr),
    pngChunk('IDAT', deflateSync(raw)),
    pngChunk('IEND', Buffer.alloc(0)),
  ]);
}

// readZip reads the entries of a zip the server made, from its central directory. Each entry is inflated, and its CRC-32
// must match the one the directory records. It returns a map from entry name to contents.
export function readZip(buf) {
  let eocd = -1;
  for (let i = buf.length - 22; i >= Math.max(0, buf.length - 22 - 65557); i--) {
    if (buf.readUInt32LE(i) === 0x06054b50) {
      eocd = i;
      break;
    }
  }
  if (eocd < 0) throw new Error('the zip has no end of central directory');
  const count = buf.readUInt16LE(eocd + 10);
  let p = buf.readUInt32LE(eocd + 16);
  const entries = new Map();
  for (let n = 0; n < count; n++) {
    if (buf.readUInt32LE(p) !== 0x02014b50) throw new Error('the zip central directory is damaged');
    const method = buf.readUInt16LE(p + 10);
    const crc = buf.readUInt32LE(p + 16);
    const csize = buf.readUInt32LE(p + 20);
    const nlen = buf.readUInt16LE(p + 28);
    const xlen = buf.readUInt16LE(p + 30);
    const clen = buf.readUInt16LE(p + 32);
    const local = buf.readUInt32LE(p + 42);
    const name = buf.toString('utf8', p + 46, p + 46 + nlen);
    p += 46 + nlen + xlen + clen;
    const start = local + 30 + buf.readUInt16LE(local + 26) + buf.readUInt16LE(local + 28);
    const data = buf.subarray(start, start + csize);
    if (method !== 0 && method !== 8) throw new Error(`entry ${name} uses method ${method}`);
    const body = method === 0 ? data : inflateRawSync(data);
    if (crc32(body) !== crc) throw new Error(`entry ${name} fails its CRC-32`);
    entries.set(name, body);
  }
  return entries;
}

// waitForZip waits until the browser has saved a zip in dir, and returns its path. A partial download has another name.
async function waitForZip(dir, timeoutMs = 20000) {
  for (let i = 0; i < timeoutMs / 200; i++) {
    const zips = (await readdir(dir)).filter((f) => f.endsWith('.zip'));
    if (zips.length > 0) return join(dir, zips[0]);
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error('the browser saved no zip');
}

export async function s4aChecks(h, { page, base, accountKey, teamA, dir }) {
  const { check } = h;
  const red = pngOf(96, 64, (x, y) => [220, 40 + x, 40]);
  const blue = pngOf(64, 64, (x, y) => [30, 60 + y, 200]);
  const notes = Buffer.from('notes kept beside the pictures\n');
  await h.api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: '/s4a-photos', parents: false });
  await h.rawPut(base, `/api/v1/folders/${teamA}/files?path=%2Fs4a-photos%2Fred.png`, accountKey, red, 'image/png');
  await h.rawPut(base, `/api/v1/folders/${teamA}/files?path=%2Fs4a-photos%2Fblue.png`, accountKey, blue, 'image/png');
  await h.rawPut(base, `/api/v1/folders/${teamA}/files?path=%2Fs4a-photos%2Fnotes.txt`, accountKey, notes, 'text/plain');

  // The page is loaded again, so the listing reads the folder that was just made. A full load also sets the window back
  // to desktop width, where the earlier checks left it at phone width.
  await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
  // The list is the view the rows are found in; the page keeps the view the person chose, so it is set first.
  await page.goto(`${page.base}/files/${teamA}`);
  await h.clickButton(page, 'List');
  await h.rowAppears(page, 's4a-photos');
  await h.openRow(page, 's4a-photos');
  await h.rowAppears(page, 'red.png');
  // Tiles show the thumbnails: a picture the server made, decoded by the browser, in the grid.
  await h.clickButton(page, 'Tiles');
  await page.waitFor(
    `(() => { const i = document.querySelector('[role="option"][aria-label="red.png"] img'); return !!i && i.complete && i.naturalWidth > 0; })()`,
    30000,
  );
  const thumb = await page.evaluate(`(() => { const i = document.querySelector('[role="option"][aria-label="red.png"] img'); return { src: i.getAttribute('src'), w: i.naturalWidth, h: i.naturalHeight }; })()`);
  check(thumb.src.startsWith('blob:') && thumb.w > 0 && thumb.h > 0, `a tile shows its thumbnail (${thumb.w} by ${thumb.h})`);
  const noteTile = await page.evaluate(`!!document.querySelector('[role="option"][aria-label="notes.txt"] img')`);
  check(!noteTile, 'a text file shows its icon, not an image');

  // Download as zip: the folder's contents come back unchanged, and nothing is left out. The folder is selected at the
  // top of Team A, where its row is, and the browser saves the zip into a folder of its own.
  await page.goto(`${page.base}/files/${teamA}`);
  await h.clickButton(page, 'List');
  await h.rowAppears(page, 's4a-photos');
  const downloads = join(dir, 'zip-downloads');
  await mkdir(downloads, { recursive: true });
  await page.send('Page.setDownloadBehavior', { behavior: 'allow', downloadPath: downloads });
  await h.selectRow(page, 's4a-photos');
  await h.clickButton(page, 'Download as zip');
  await page.waitFor(`!!document.querySelector('.ant-modal [role="status"]') && document.body.innerText.includes(${q('The zip is ready')})`, 30000);
  const saved = await waitForZip(downloads);
  const entries = readZip(await readFile(saved));
  // The folder is an entry of its own, so an empty folder survives the zip (decision 35).
  const want = new Map([
    ['s4a-photos/', Buffer.alloc(0)],
    ['s4a-photos/red.png', red],
    ['s4a-photos/blue.png', blue],
    ['s4a-photos/notes.txt', notes],
  ]);
  const names = [...entries.keys()].sort();
  check(
    JSON.stringify(names) === JSON.stringify([...want.keys()].sort()),
    `the zip holds the folder's files and nothing else (${names.join(', ')})`,
  );
  const same = [...want].every(([name, body]) => entries.get(name)?.equals(body));
  check(same, 'every file in the zip has the bytes that were uploaded');
  await h.clickButton(page, 'Close');

  // Left out: a shortcut inside the folder is not zipped, and the server counts it as left out. The zip holds the same
  // entries as before, and the dialog says one item was left out.
  const photos = (await h.api(base, 'GET', `/api/v1/folders/${teamA}/children?path=%2Fs4a-photos`, accountKey)).items;
  const photosNode = (await h.api(base, 'GET', `/api/v1/folders/${teamA}/children?path=%2F`, accountKey)).items.find((n) => n.name === 's4a-photos');
  const notesNode = photos.find((n) => n.name === 'notes.txt');
  await h.api(base, 'POST', `/api/v1/nodes/${photosNode.id}/shortcuts`, accountKey, { target: notesNode.id, name: 'notes-link.txt' });
  await page.goto(`${page.base}/files/${teamA}`);
  await h.clickButton(page, 'List');
  await h.rowAppears(page, 's4a-photos');
  const downloadsAgain = join(dir, 'zip-downloads-left');
  await mkdir(downloadsAgain, { recursive: true });
  await page.send('Page.setDownloadBehavior', { behavior: 'allow', downloadPath: downloadsAgain });
  await h.selectRow(page, 's4a-photos');
  await h.clickButton(page, 'Download as zip');
  const leftOut = 'One item was left out. You cannot download it.';
  const noticeSel = `(() => { const s = document.querySelector('.ant-modal [role="status"]'); return !!s && s.innerText.includes(${q(leftOut)}); })()`;
  await page.waitFor(noticeSel, 30000);
  check(await page.evaluate(noticeSel), 'the left-out notice is in the dialog\'s live status region');
  const leftZip = readZip(await readFile(await waitForZip(downloadsAgain)));
  check(
    JSON.stringify([...leftZip.keys()].sort()) === JSON.stringify([...want.keys()].sort()) &&
      [...want].every(([name, body]) => leftZip.get(name)?.equals(body)),
    'the left-out shortcut is not in the zip, and the other entries are unchanged',
  );
  await h.clickButton(page, 'Close');

  // A new version of red.png: the tile shows the new picture, not the one it showed before. The new picture has other
  // dimensions, so the tile's image size tells the two apart.
  const redNode = photos.find((n) => n.name === 'red.png');
  const red2 = pngOf(40, 40, (x, y) => [20, 200 - x * 3, 60 + y]);
  const put = await fetch(`${base}/api/v1/nodes/${redNode.id}/content`, {
    method: 'PUT',
    headers: { Authorization: `Bearer ${accountKey}`, 'Content-Type': 'image/png', 'If-Match': redNode.etag },
    body: red2,
  });
  if (!put.ok) throw new Error(`PUT red.png content: ${put.status} ${await put.text()}`);
  await page.goto(`${page.base}/files/${teamA}`);
  await h.clickButton(page, 'List');
  await h.rowAppears(page, 's4a-photos');
  await h.openRow(page, 's4a-photos');
  await h.rowAppears(page, 'red.png');
  await h.clickButton(page, 'Tiles');
  await page.waitFor(
    `(() => { const i = document.querySelector('[role="option"][aria-label="red.png"] img'); return !!i && i.complete && i.naturalWidth === 40; })()`,
    30000,
  );
  const again = await page.evaluate(`(() => { const i = document.querySelector('[role="option"][aria-label="red.png"] img'); return { w: i.naturalWidth, h: i.naturalHeight }; })()`);
  check(again.w === 40 && again.h === 40, `a new version shows its new thumbnail (${again.w} by ${again.h}, was ${thumb.w} by ${thumb.h})`);
  // The checks after this one read the rows of the list, so the view goes back to the list.
  await h.clickButton(page, 'List');
}
