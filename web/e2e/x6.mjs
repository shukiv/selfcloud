// SPDX-License-Identifier: AGPL-3.0-or-later

// The player's screen and cover checks (slice x6, decision 63), run inside the shared end-to-end run (run.mjs) on the shared page
// and server, or alone with SELFCLOUD_E2E_ONLY=x6. The video fills the viewer's width and height at 1920, 1280 and 390 px; the
// playlist is a drawer, closed by default, that opens from its toggle, comes from the inline end (the left in Hebrew), and is
// closed by Escape before the viewer is. An audio file shows its cover large, the folder's picture for a file with none (one read
// of the folder), and the next track's cover replaces the last without a flash. With SELFCLOUD_E2E_SHOTS set to a folder, the
// screenshots are written there, each taken once the transitions have settled.
import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { inflateSync } from 'node:zlib';

// decodePng reads an 8-bit RGB or RGBA PNG (what the browser's screenshots are), for the pixel checks.
function decodePng(buf) {
  let pos = 8, w = 0, h = 0, ct = 0; const idat = [];
  while (pos < buf.length) {
    const len = buf.readUInt32BE(pos); const type = buf.toString('ascii', pos + 4, pos + 8); const data = buf.subarray(pos + 8, pos + 8 + len);
    if (type === 'IHDR') { w = data.readUInt32BE(0); h = data.readUInt32BE(4); ct = data[9]; }
    if (type === 'IDAT') idat.push(data);
    pos += 12 + len;
  }
  const bpp = ct === 6 ? 4 : 3; const raw = inflateSync(Buffer.concat(idat)); const stride = w * bpp;
  const out = Buffer.alloc(stride * h); let prev = Buffer.alloc(stride);
  for (let y = 0; y < h; y++) {
    const f = raw[y * (stride + 1)]; const line = raw.subarray(y * (stride + 1) + 1, (y + 1) * (stride + 1)); const cur = out.subarray(y * stride, (y + 1) * stride);
    for (let i = 0; i < stride; i++) {
      const a = i >= bpp ? cur[i - bpp] : 0; const b = prev[i]; const c = i >= bpp ? prev[i - bpp] : 0; let v = line[i];
      if (f === 1) v += a; else if (f === 2) v += b; else if (f === 3) v += (a + b) >> 1;
      else if (f === 4) { const pp = a + b - c; const pa = Math.abs(pp - a), pb = Math.abs(pp - b), pc = Math.abs(pp - c); v += pa <= pb && pa <= pc ? a : pb <= pc ? b : c; }
      cur[i] = v & 255;
    }
    prev = cur;
  }
  return { w, h, bpp, px: (x, y) => { const o = y * stride + x * bpp; return [out[o], out[o + 1], out[o + 2]]; } };
}

const media = fileURLToPath(new URL('./media/', import.meta.url));
const shots = process.env.SELFCLOUD_E2E_SHOTS;
const PASSWORD = 'correct horse battery staple';
const MEMBER = { email: 'mateo@example.com', name: 'Mateo Member', password: PASSWORD };
const q = (v) => JSON.stringify(v);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// The antd transitions and the drawer's slide run for 0.2 s; a shot waits this long after the last change.
const SETTLE_MS = 600;
const ALBUM_DIR = '2003 - Demo Album (Sample Edition)';
const ALBUM_IMAGE = 'Demo Album (Sample Edition).jpg';

async function step(name, fn) {
  console.log(`-- ${name}`);
  await fn();
}

// until polls a page expression with a bound, and returns when it is true.
// unendedOf names the page's requests that have not ended, with their status and age: a listing that never shows says
// whether its read is still out (gate stfu4d: twice "Loading the folder" in the player sections).
const unendedOf = (page) =>
  [...(page.inflight?.values() ?? [])].map((r) => `${r.url.startsWith(page.base) ? r.url.slice(page.base.length) : r.url.slice(0, 80)} status ${r.status} for ${Date.now() - r.at} ms`).join(' ; ') || 'none';

async function until(page, expr, ms = 15000, what = expr) {
  const deadline = Date.now() + ms;
  for (;;) {
    if (await page.evaluate(`!!(${expr})`).catch(() => false)) return;
    if (Date.now() > deadline) { const st = await page.evaluate(`JSON.stringify({ path: location.pathname, title: document.title, nav: performance.getEntriesByType("navigation")[0]?.type, alerts: [...document.querySelectorAll("[role=alert]")].map((a) => a.innerText), text: document.body.innerText.slice(0, 400) })`).catch((e) => "state-unavailable " + e); throw new Error(`timed out (${ms} ms) waiting for: ${what} | DIAG ${st} | requests not ended: ${unendedOf(page)}`); }
    await sleep(100);
  }
}

// READS is a page or link read, the requests a drawer must not add.
const READS = /\/(links|children|content|thumbnail|subtitles|resume|tags)\b|\/signed/;

// quiet waits until the page has sent no request for a moment: the listing and tree reads that follow a page load start
// before a mark, so a later count sees only what the action itself asks for. A request from a page already left can stay
// in the network log without an end, so the log's length, not the open requests, says whether the page has settled.
async function quiet(page, ms = 600) {
  const deadline = Date.now() + 15000;
  let last = page.sent.length;
  let since = Date.now();
  for (;;) {
    await sleep(100);
    if (page.sent.length !== last) {
      last = page.sent.length;
      since = Date.now();
    } else if (Date.now() - since >= ms) return;
    if (Date.now() > deadline) throw new Error(`the page kept reading: ${unendedOf(page)}`);
  }
}

// dblClick sends a real mouse double click at a point, the way Chrome sends it for a person.
async function dblClick(page, { x, y }) {
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
  for (const clickCount of [1, 2]) {
    await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', buttons: 1, clickCount });
    await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', buttons: 0, clickCount });
    await sleep(100);
  }
}

// clickAt is one real click on the centre of a control (a pointer press, not a script call).
async function clickAt(page, selector) {
  const p = await page.evaluate(`(() => { const el = document.querySelector(${q(selector)}); if (!el) return null; el.scrollIntoView({ block: 'center' }); const r = el.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
  if (!p) throw new Error(`no control ${selector}`);
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: p.x, y: p.y });
  await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: p.x, y: p.y, button: 'left', buttons: 1, clickCount: 1 });
  await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: p.x, y: p.y, button: 'left', buttons: 0, clickCount: 1 });
}

// tap is one touch on a phone.
async function tap(page, { x, y }) {
  await page.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] });
  await sleep(60);
  await page.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
}

// clickRowText is one real click on a visitor's listing row, by its name.
async function clickRowText(page, name) {
  const p = await page.evaluate(`(() => { const b = [...document.querySelectorAll('.rows button')].find((x) => x.textContent === ${q(name)}); if (!b) return null; b.scrollIntoView({ block: 'center' }); const r = b.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
  if (!p) throw new Error(`no row ${name}`);
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: p.x, y: p.y });
  await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: p.x, y: p.y, button: 'left', buttons: 1, clickCount: 1 });
  await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: p.x, y: p.y, button: 'left', buttons: 0, clickCount: 1 });
}

// press sends one key, the way the keyboard does.
async function press(page, key) {
  const vk = { Escape: 27, ArrowRight: 39, ArrowLeft: 37, ' ': 32 }[key];
  const code = key === ' ' ? 'Space' : key;
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key, code, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key, code, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk });
}

// rowPoint is the centre of a listing row's name, scrolled into view.
async function rowPoint(page, name) {
  const p = await page.evaluate(`(() => {
    const row = document.querySelector(${q(`[role="row"][aria-label=${JSON.stringify(name)}]`)});
    if (!row) return null;
    row.scrollIntoView({ block: 'center' });
    const r = (row.querySelector('bdi') ?? row).getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  })()`);
  if (!p) throw new Error(`no row named ${name}`);
  return p;
}

// openFolder shows a folder's listing in List view, with the first row given.
async function openFolder(page, url, firstName) {
  await page.goto(url);
  await page.evaluate(`localStorage.setItem('selfcloud.files', JSON.stringify({ view: 'list', sort: { key: 'name', dir: 'asc' } }))`);
  await page.goto(url);
  await until(page, `!!document.querySelector('button[data-view]')`, 15000, 'the view toggle');
  await page.evaluate(`(() => { const b = document.querySelector('button[data-view]'); if (b.dataset.view === 'list') b.click(); })()`);
  await until(page, `!!document.querySelector('[role="row"][aria-label=${JSON.stringify(firstName)}]')`, 15000, `row ${firstName}`);
}

// setPrefs sets the interface language and theme the way the page keeps them, then reloads the page to take them.
async function setPrefs(page, url, lang, theme) {
  await page.goto(url);
  await page.evaluate(`localStorage.setItem('selfcloud.lang', ${q(lang)}); localStorage.setItem('selfcloud.theme', ${q(theme)}); true`);
  await page.goto(url);
}

// setViewport sets the window's size. A phone is 390 px wide with a finger for a pointer; the desktop sizes are a mouse.
async function setViewport(page, width, height = 800) {
  const phone = width <= 600;
  await page.send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: phone });
  if (phone) await page.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
  else await page.send('Emulation.setTouchEmulationEnabled', { enabled: false });
}

async function shot(page, name) {
  if (!shots) return;
  await sleep(SETTLE_MS);
  const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
  await writeFile(join(shots, `${name}.png`), Buffer.from(data, 'base64'));
  console.log(`shot ${join(shots, `${name}.png`)}`);
}

// HEAD_ROW reads the viewer's head: each button (its top, its height, its name), the head's height, and the sideways overflow.
const HEAD_ROW = `(() => {
  const h = document.querySelector('[data-testid="preview"] header');
  const buttons = Array.from(h.querySelectorAll('button')).map((b) => { const r = b.getBoundingClientRect(); return { label: b.getAttribute('aria-label') || b.textContent.trim(), top: Math.round(r.top * 10) / 10, height: Math.round(r.height), width: Math.round(r.width) }; });
  const n = h.querySelector('.media-head-name');
  const nr = n ? n.getBoundingClientRect() : null;
  return { buttons, headHeight: Math.round(h.getBoundingClientRect().height), overflow: h.scrollWidth - h.clientWidth, name: n ? { title: n.getAttribute('title') ?? '', textOverflow: getComputedStyle(n).textOverflow, width: Math.round(nr.width) } : null };
})()`;
// FIT reads the cover box and the picture drawn in it: their sizes, and the picture's natural size.
const FIT = `(() => { const c = document.querySelector('[data-testid="media-cover"]'); if (!c) return null; const b = c.getBoundingClientRect(); const i = c.querySelector('img'); const r = i ? i.getBoundingClientRect() : null; return { source: c.dataset.source, box: { w: Math.round(b.width), h: Math.round(b.height) }, img: r ? { w: Math.round(r.width), h: Math.round(r.height), natural: [i.naturalWidth, i.naturalHeight] } : null }; })()`;
// VIEWER says the viewer is on screen, and PLAYER holds the element the viewer plays.
const VIEWER = `(() => { const v = document.querySelector('[data-testid="preview"]'); if (!v) return false; const w = v.querySelector('.ant-modal-wrap') ?? v; return getComputedStyle(w).display !== 'none'; })()`;
const MEDIA = `document.querySelector('[data-testid="media-screen"] video, [data-testid="media-screen"] audio')`;
// DRAWER reads the playlist drawer: its open state, its box, and its own visibility.
const DRAWER = `(() => { const d = document.querySelector('[data-media-drawer]'); if (!d) return null; const r = d.getBoundingClientRect(); const sc = document.querySelector('[data-testid="media-screen"]').getBoundingClientRect(); return { open: d.dataset.open, left: r.left, right: r.right, width: r.width, visibility: getComputedStyle(d).visibility, inert: d.inert, screenLeft: sc.left, screenRight: sc.right, innerWidth: window.innerWidth }; })()`;
// COVER reads the stage's cover: its source, its box, and the picture's size.
const COVER = `(() => { const c = document.querySelector('[data-testid="media-cover"]'); if (!c) return null; const r = c.getBoundingClientRect(); const img = c.querySelector('img'); const ir = img?.getBoundingClientRect(); return { cover: c.dataset.cover, source: c.dataset.source, box: { w: Math.round(r.width), h: Math.round(r.height) }, icon: !!c.querySelector('svg'), img: img ? { w: Math.round(ir.width), h: Math.round(ir.height), natural: [img.naturalWidth, img.naturalHeight], src: img.getAttribute('src') } : null, name: document.querySelector('.media-art-name bdi')?.textContent ?? null }; })()`;

// playing waits until the file on screen has data and plays. The fixtures are short, so the file repeats while the checks look.
async function holdPlaying(page) {
  await until(page, `(() => { const v = ${MEDIA}; if (!v) return false; v.loop = true; return true; })()`, 15000, 'the player element');
  await until(page, `(() => { const v = ${MEDIA}; return !!v && v.readyState >= 2 && !v.paused && v.currentTime > 0.05; })()`, 20000, 'the file plays');
}

// openPlayer opens a file of the listing in the viewer, and waits until its player is on screen.
async function openPlayer(page, url, name) {
  await openFolder(page, url, name);
  await dblClick(page, await rowPoint(page, name));
  await until(page, VIEWER, 15000, 'the viewer');
  await until(page, `!!${MEDIA}`, 15000, 'the player element');
}

// samplerCover records the cover and the name at every change of the stage while the watch runs: a flash of another file's
// picture would show as a sample with the new name and the old picture's source.
const SAMPLER = `(() => {
  window.__x6Samples = [];
  window.__x6Watch = new MutationObserver(() => {
    const c = document.querySelector('[data-testid="media-cover"]');
    const name = document.querySelector('.media-art-name bdi')?.textContent;
    if (!c || name === undefined) return;
    window.__x6Samples.push({ name, cover: c.dataset.cover, source: c.dataset.source, src: c.querySelector('img')?.getAttribute('src') ?? null });
  });
  window.__x6Watch.observe(document.documentElement, { subtree: true, childList: true, characterData: true, attributes: true });
  return true;
})()`;

// boxPixels reads the fallback box's pixels from a screenshot of the box, and says whether the box is one colour, and whether the
// icon is a different one. The box is sampled on a grid that leaves out the icon.
async function boxPixels(page) {
  const r = await page.evaluate(`(() => { const e = document.querySelector('.media-cover-empty'); const b = e.getBoundingClientRect(); const i = e.querySelector('svg').getBoundingClientRect(); return { box: [b.left, b.top, b.width, b.height], icon: [i.left, i.top, i.width, i.height] }; })()`);
  const [bx, by, bw, bh] = r.box.map((v) => Math.floor(v));
  const [ix, iy, iw, ih] = r.icon;
  const { data } = await page.send('Page.captureScreenshot', { format: 'png', clip: { x: bx, y: by, width: bw, height: bh, scale: 1 } });
  const img = decodePng(Buffer.from(data, 'base64'));
  const inIcon = (x, y) => x >= ix - 4 && x <= ix + iw + 4 && y >= iy - 4 && y <= iy + ih + 4;
  const boxColours = new Set();
  let iconPixels = 0;
  const iconColours = new Set();
  for (let gy = 0; gy < 24; gy++) {
    for (let gx = 0; gx < 24; gx++) {
      const x = Math.min(bw - 1, Math.floor(((gx + 0.5) * bw) / 24));
      const y = Math.min(bh - 1, Math.floor(((gy + 0.5) * bh) / 24));
      const px = img.px(x, y).join(',');
      if (inIcon(bx + x, by + y)) {
        iconColours.add(px);
        iconPixels++;
      } else {
        boxColours.add(px);
      }
    }
  }
  return { boxColours: [...boxColours], iconColours: [...iconColours], iconPixels };
}

export async function x6Checks(h, { page, base, accountKey, accountID, memberID }) {
  const { api, rawPut, check } = h;
  const signIn = (who) => h.signIn(page, who);
  const signOut = () => h.signOut(page);
  const kept = await page.evaluate(`(() => ({ lang: localStorage.getItem('selfcloud.lang'), theme: localStorage.getItem('selfcloud.theme'), files: localStorage.getItem('selfcloud.files') }))()`);
  // The fixtures: a folder of its own for the run's member (write). Each folder is one case of the checks.
  const shared = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'X6 player' })).id;
  await api(base, 'PUT', `/api/v1/accounts/${accountID}/folders/${shared}/grants/${memberID}`, accountKey, { level: 'write' });
  for (const d of ['/video', '/embedded', '/cover', '/neither', '/small', `/${ALBUM_DIR}`]) {
    await api(base, 'POST', `/api/v1/folders/${shared}/directories`, accountKey, { path: d, parents: false });
  }
  const put = (dir, name, bytes, type) =>
    rawPut(base, `/api/v1/folders/${shared}/files?path=${encodeURIComponent(`/${dir}/${name}`)}`, accountKey, bytes, type);
  const tagged = await readFile(join(media, 'tag24.mp3'));
  const long = await readFile(join(media, 'zz-long.mp3'));
  const plain = await readFile(join(media, 'tone.ogg'));
  const picture = await readFile(join(media, 'cover.jpg'));
  // A 300 by 300 picture: a small one, which the stage scales up to its box.
  const small = await readFile(join(media, 'cover-300.jpg'));
  await put('video', 'bright.webm', await readFile(join(media, 'clip.webm')), 'video/webm');
  // An album whose tracks carry the picture inside the file: the first track has a cover of its own, the second has none.
  await put('embedded', 'a-tagged.mp3', tagged, 'audio/mpeg');
  await put('embedded', 'b-plain.ogg', plain, 'audio/ogg');
  // A folder with a picture beside its tracks, and a folder with none.
  await put('cover', 'a-plain.ogg', plain, 'audio/ogg');
  await put('cover', 'b-long.mp3', long, 'audio/mpeg');
  await put('cover', 'cover.jpg', picture, 'image/jpeg');
  await put('neither', 'a-plain.ogg', plain, 'audio/ogg');
  await put('small', 'a-plain.ogg', plain, 'audio/ogg');
  await put('small', 'cover.jpg', small, 'image/jpeg');
  await put('neither', 'b-long.mp3', long, 'audio/mpeg');
  // The shape the owner's album has: fourteen tracks with no picture, and one image named after the album, in a folder whose name
  // carries the album's name.
  for (let i = 1; i <= 14; i++) await put(ALBUM_DIR, `${String(i).padStart(2, '0')} Track.mp3`, long, 'audio/mpeg');
  await put(ALBUM_DIR, ALBUM_IMAGE, picture, 'image/jpeg');

  // The cover inside a-tagged.mp3 is drawn by the server's job queue. In a full run the queue still holds the work the earlier
  // checks started, so the run asks for it here and waits until the server has it. The player's own wait (202, then ask again)
  // is the unit tests' matter; these checks are about the layout.
  {
    const listed = await api(base, 'GET', `/api/v1/folders/${shared}/children?path=${encodeURIComponent('/embedded')}`, accountKey);
    const id = listed.items.find((n) => n.name === 'a-tagged.mp3').id;
    // Both sizes the page asks for: the stage's 1024 first, and the 256 the row and the lock screen use.
    for (const size of [1024, 256]) {
      const started = Date.now();
      let status = 0;
      while (Date.now() - started < 180000) {
        const res = await fetch(`${base}/api/v1/nodes/${id}/thumbnail?size=${size}`, {
          headers: { Authorization: `Bearer ${accountKey}` },
          redirect: 'manual',
        });
        status = res.status;
        await res.arrayBuffer().catch(() => undefined);
        if (status !== 202 && status !== 429) break;
        await new Promise((r) => setTimeout(r, 1000));
      }
      console.log(`    the server's ${size} px cover of a-tagged.mp3: ${status} after ${Math.round((Date.now() - started) / 1000)} s`);
      if (status !== 200 && (status < 300 || status > 399)) throw new Error(`the server has no ${size} px cover for a-tagged.mp3 (${status})`);
    }
  }

  const children = (await api(base, 'GET', `/api/v1/folders/${shared}/children?path=%2F`, accountKey)).items;
  const dirOf = (name) => children.find((n) => n.name === name)?.id;
  const urlOf = (name) => `${base}/files/${shared}/${dirOf(name)}`;
  const albumDir = dirOf(ALBUM_DIR);

  await signOut();
  try {
    await step('the member signs in', async () => {
      await signIn(MEMBER);
    });

    await step('the video fills the viewer at 1920, 1280 and 390 px, with no page scroll', async () => {
      for (const width of [1920, 1280, 390]) {
        await setViewport(page, width, width <= 600 ? 800 : 900);
        await setPrefs(page, urlOf('video') + '?lang=en', 'en', 'light');
        if (width <= 600) {
          await openFolder(page, urlOf('video') + '?lang=en', 'bright.webm');
          await tap(page, await rowPoint(page, 'bright.webm'));
        } else {
          await openPlayer(page, urlOf('video') + '?lang=en', 'bright.webm');
        }
        await until(page, VIEWER, 15000, 'the viewer');
        await until(page, `!!${MEDIA}`, 15000, 'the player');
        await sleep(300);
        const m = await page.evaluate(`(() => {
          const v = document.querySelector('[data-testid="media-screen"] video').getBoundingClientRect();
          const p = document.querySelector('[data-testid="preview"]').getBoundingClientRect();
          const viewer = document.querySelector('[data-testid="preview"]');
          return { w: v.width, h: v.height, bottom: v.bottom, viewerW: p.width, viewerBottom: p.bottom, pageW: document.documentElement.scrollWidth - innerWidth, pageH: viewer.scrollHeight - viewer.clientHeight, viewerW2: viewer.scrollWidth - viewer.clientWidth };
        })()`);
        // The Playlist button is on the viewer's top row, beside Download; the screen, and so the video, starts right under the head.
        const head = await page.evaluate(`(() => {
          const t = document.querySelector('.media-playlist-toggle');
          const h = document.querySelector('[data-testid="preview"] header');
          const scr = document.querySelector('[data-testid="media-screen"]').getBoundingClientRect();
          return { inHeader: h.contains(t), h: t.getBoundingClientRect().height, labelShown: t.textContent.trim() !== '', aria: t.getAttribute('aria-label'), gap: scr.top - h.getBoundingClientRect().bottom, screenTop: scr.top, videoTop: document.querySelector('[data-testid="media-screen"] video').getBoundingClientRect().top, rows: document.querySelectorAll('[data-testid="preview"] header').length };
        })()`);
        // Decision 68: the Playlist button is the icon at every width, with its name as its label, so it shows no text at any width.
        check(head.inHeader && !head.labelShown && head.aria === 'Playlist', `${width} px: the Playlist button is on the viewer's top row, as an icon with its name in aria-label ${JSON.stringify(head)}`);
        check(head.gap >= 0 && head.gap <= 13 && Math.abs(head.videoTop - head.screenTop) <= 1, `${width} px: the video starts right under the header, ${head.gap.toFixed(0)} px below it`);
        if (width <= 600) check(head.h >= 44, `390 px: the Playlist button is 44 px tall on a touch screen (${head.h.toFixed(0)} px)`);
        check(Math.abs(m.w - (m.viewerW - 24)) <= 1, `${width} px: the video is the viewer's full width (${m.w.toFixed(1)} of ${(m.viewerW - 24).toFixed(1)})`);
        check(m.bottom <= m.viewerBottom - 11 && m.h > 200, `${width} px: the video reaches down the viewer, no empty page below it (${m.h.toFixed(1)} px tall, bottom ${m.bottom.toFixed(1)})`);
        check(m.pageW <= 0 && m.pageH <= 0 && m.viewerW2 <= 0, `${width} px: no page scroll (${m.pageW} sideways, ${m.pageH} down in the viewer, ${m.viewerW2} across it)`);
        const probe = await page.evaluate(`(() => { const box = (sel) => { const el = document.querySelector(sel); if (!el) return null; const r = el.getBoundingClientRect(); return [Math.round(r.left), Math.round(r.top), Math.round(r.width), Math.round(r.height)]; }; return { viewer: box('[data-testid="preview"]'), playlist: box('.media-playlist'), main: box('.media-main'), toolbar: box('.media-toolbar'), screen: box('[data-testid="media-screen"]'), stage: box('[data-testid="media-stage"]'), video: box('[data-testid="media-screen"] video'), controls: box('[data-testid="media-controls"]'), inner: window.innerWidth }; })()`);
        console.log(`    probe ${width}: ${JSON.stringify(probe)}`);
        await shot(page, `video-${width}`);
        await page.evaluate(`document.querySelector('[data-testid="preview"] header button').click()`);
        await until(page, `!(${VIEWER})`, 10000, 'the viewer closes');
      }
    });

    await step('the viewer\'s head is one row on a phone, at 390 and 320 px, in English and Hebrew: every button has the same top', async () => {
      for (const [width, lang] of [[390, 'en'], [320, 'en'], [390, 'he'], [320, 'he']]) {
        await setViewport(page, width, 700);
        await setPrefs(page, urlOf('video') + `?lang=${lang}`, lang, 'light');
        await openFolder(page, urlOf('video') + `?lang=${lang}`, 'bright.webm');
        await tap(page, await rowPoint(page, 'bright.webm'));
        await until(page, VIEWER, 15000, 'the viewer');
        await until(page, `!!${MEDIA}`, 15000, 'the player');
        await sleep(300);
        const head = await page.evaluate(HEAD_ROW);
        const tops = head.buttons.map((b) => b.top);
        const sameTop = tops.length > 0 && Math.max(...tops) - Math.min(...tops) <= 1;
        check(sameTop && head.buttons.every((b) => b.height >= 44), `${width} px, ${lang}: every head button has the same top and is 44 px tall (${JSON.stringify(head.buttons.map((b) => b.label))}, tops ${JSON.stringify(tops)})`);
        check(head.headHeight <= 64 && head.overflow <= 0, `${width} px, ${lang}: the head is one row, and nothing overflows it (${head.headHeight} px tall, ${head.overflow} px sideways)`);
        check(head.name !== null && head.name.title.length > 0 && head.name.textOverflow === 'ellipsis' && head.name.width >= 24, `${width} px, ${lang}: the name is one line with an ellipsis and its full text in the title (${JSON.stringify(head.name)})`);
        if (width === 320 && lang === 'en') await shot(page, 'video-320');
        await press(page, 'Escape');
        await until(page, `!(${VIEWER})`, 10000, 'the viewer closes');
      }
      await setViewport(page, 1280, 800);
    });

    await step('the drawer is closed by default, off the screen and hidden, and its toggle opens it', async () => {
      await setViewport(page, 1280, 900);
      await setPrefs(page, urlOf('video') + '?lang=en', 'en', 'light');
      await openPlayer(page, urlOf('video') + '?lang=en', 'bright.webm');
      await holdPlaying(page);
      const closed = await page.evaluate(DRAWER);
      const dprobe = await page.evaluate(`(() => { const d = document.querySelector('[data-media-drawer]'); const cs = getComputedStyle(d); const op = d.offsetParent; const r = op ? op.getBoundingClientRect() : null; return { transform: cs.transform, position: cs.position, insetEnd: cs.insetInlineEnd, parentClass: op?.className ?? null, parentRect: r ? [Math.round(r.left), Math.round(r.right)] : null, dir: document.documentElement.dir, cls: d.className }; })()`);
      console.log(`    drawer probe: ${JSON.stringify(dprobe)}`);
      check(closed?.open === 'false' && closed.inert === true, `the drawer is closed at opening, and inert: out of the focus order and the accessibility tree (${JSON.stringify(closed)})`);
      check(closed.left >= closed.screenRight - 1, `the closed drawer is off the video at the inline end (left ${closed.left.toFixed(0)}, the video ends at ${closed.screenRight.toFixed(0)})`);
      const shown = await page.evaluate(`(() => { const v = document.querySelector('[data-testid="media-screen"] video').getBoundingClientRect(); const s = document.querySelector('[data-testid="media-screen"]').getBoundingClientRect(); return { right: v.right, screenRight: s.right, width: v.width }; })()`);
      check(Math.abs(shown.right - shown.screenRight) <= 1 && shown.width > 800, `with the drawer closed, the video is full width (${shown.width.toFixed(0)} px)`);
      await clickAt(page, '.media-playlist-toggle');
      await sleep(400);
      const open = await page.evaluate(DRAWER);
      check(open.open === 'true' && open.visibility === 'visible' && Math.abs(open.width - 380) <= 1, `the toggle opens the drawer, 380 px wide (${open.width.toFixed(0)} px)`);
      // On a wide window the screen gives the drawer its width: the drawer sits beside the video, not over it, and no control of
      // the player is under it (decision 63, the drawer takes the stage's width).
      const beside = await page.evaluate(`(() => {
        const d = document.querySelector('[data-media-drawer]').getBoundingClientRect();
        const v = document.querySelector('[data-testid="media-screen"] video').getBoundingClientRect();
        const under = Array.from(document.querySelectorAll('[data-testid="preview"] button, [data-testid="preview"] [role="slider"], [data-testid="preview"] input, [data-testid="preview"] .ant-select-selector, [data-testid="preview"] .ant-select'))
          .filter((el) => !el.closest('[data-media-drawer]') && !el.closest('[inert]') && el.getBoundingClientRect().width > 0)
          .filter((el) => { const r = el.getBoundingClientRect(); return r.right > d.left + 0.5 && r.left < d.right - 0.5 && r.bottom > d.top + 0.5 && r.top < d.bottom - 0.5; })
          .map((el) => (el.getAttribute('aria-label') ?? el.textContent.trim()).slice(0, 30));
        return { videoRight: v.right, videoLeft: v.left, videoTop: v.top, videoH: v.height, drawerLeft: d.left, drawerRight: d.right, under };
      })()`);
      check(open.width === 380 && beside.videoRight <= beside.drawerLeft + 1 && beside.videoLeft >= 0, `the open drawer takes its 380 px beside the video, not over it (video to ${beside.videoRight.toFixed(0)}, drawer from ${beside.drawerLeft.toFixed(0)})`);
      check(beside.under.length === 0, `no control of the player is under the open drawer (${JSON.stringify(beside.under)})`);
      check(await page.evaluate(`document.querySelector('.media-playlist-toggle').getAttribute('aria-expanded') === 'true'`), 'the toggle says the drawer is open');
      // The focus moves into the drawer on the same click, before anything else happens to the page.
      const at = await page.evaluate(`(() => { const a = document.activeElement; return a ? a.tagName + ' ' + (a.getAttribute('aria-label') ?? a.textContent.trim().slice(0, 30)) : 'none'; })()`);
      check(at.includes('Close playlist'), `the focus moves into the drawer on opening (focus is on ${at})`);
      await shot(page, 'video-drawer-1920');
      await page.send('Emulation.setDeviceMetricsOverride', { width: 1920, height: 900, deviceScaleFactor: 1, mobile: false });
      await sleep(400);
      await shot(page, 'video-drawer-1920-wide');
    });

    await step('the drawer opens with focus in it; its own X and the toggle close it, and the focus goes back to the toggle', async () => {
      const focused = await page.evaluate(`(() => { const a = document.activeElement; return a ? a.tagName + ' ' + (a.getAttribute('aria-label') ?? a.textContent.trim().slice(0, 30)) : 'none'; })()`);
      console.log(`    focus after the page was shown again: ${focused}`);
      await clickAt(page, '.media-drawer-close');
      await sleep(400);
      check((await page.evaluate(DRAWER)).open === 'false', 'the X closes the drawer');
      await clickAt(page, '.media-playlist-toggle');
      await sleep(300);
      check((await page.evaluate(DRAWER)).open === 'true', 'the toggle opens the drawer again');
      await clickAt(page, '.media-playlist-toggle');
      await sleep(400);
      check((await page.evaluate(DRAWER)).open === 'false', 'the same toggle, clicked again, closes the drawer');
      check(await page.evaluate(`document.activeElement?.classList.contains('media-playlist-toggle')`), 'the focus goes back to the toggle when the drawer closes');
    });

    await step('playback goes on while the drawer is open, and Escape closes the drawer first, then the viewer', async () => {
      await clickAt(page, '.media-playlist-toggle');
      await sleep(300);
      const t0 = await page.evaluate(`${MEDIA}.currentTime`);
      await sleep(1200);
      const t1 = await page.evaluate(`(() => { const v = ${MEDIA}; return { t: v.currentTime, paused: v.paused }; })()`);
      check(!t1.paused && t1.t !== t0, `the video still plays while the drawer is open (${t0.toFixed(2)} s, then ${t1.t.toFixed(2)} s)`);
      await press(page, 'Escape');
      await sleep(400);
      check((await page.evaluate(DRAWER)).open === 'false', 'Escape closes the drawer');
      check(await page.evaluate(VIEWER), 'the viewer is still open after the first Escape');
      await press(page, 'Escape');
      await until(page, `!(${VIEWER})`, 10000, 'the viewer closes on the second Escape');
      check(true, 'the second Escape closes the viewer');
    });

    await step('opening the drawer asks for no page read and no link', async () => {
      await openPlayer(page, urlOf('video') + '?lang=en', 'bright.webm');
      await holdPlaying(page);
      await quiet(page);
      const mark = page.sent.length;
      await clickAt(page, '.media-playlist-toggle');
      await sleep(500);
      // A read is a GET: the place is saved with a PUT as the file plays, and a save is not a read.
      const asked = page.sent.slice(mark).filter((r) => r.method === 'GET' && READS.test(r.url));
      check(asked.length === 0, `opening the drawer asks for no page or link (${asked.map((r) => r.url.replace(/^https?:\/\/[^/]+/, '')).join(' ; ') || 'none'})`);
      await press(page, 'Escape');
      await press(page, 'Escape');
      await until(page, `!(${VIEWER})`, 10000, 'the viewer closes');
    });

    await step('the drawer is closed again on every opening of the viewer', async () => {
      await openPlayer(page, urlOf('video') + '?lang=en', 'bright.webm');
      await clickAt(page, '.media-playlist-toggle');
      await sleep(300);
      await page.evaluate(`document.querySelector('[data-testid="preview"] header button').click()`);
      await until(page, `!(${VIEWER})`, 10000, 'the viewer closes');
      await openPlayer(page, urlOf('video') + '?lang=en', 'bright.webm');
      await sleep(300);
      check((await page.evaluate(DRAWER)).open === 'false', 'a second opening starts with the drawer closed');
      await press(page, 'Escape');
      await until(page, `!(${VIEWER})`, 10000, 'the viewer closes');
    });

    await step('the drawer comes from the left in Hebrew, and is closed off the left edge', async () => {
      await setViewport(page, 1280, 900);
      await setPrefs(page, urlOf('video') + '?lang=he', 'he', 'light');
      await openPlayer(page, urlOf('video') + '?lang=he', 'bright.webm');
      await holdPlaying(page);
      const closed = await page.evaluate(DRAWER);
      check(closed.right <= closed.screenLeft + 1, `Hebrew: the closed drawer sits off the video's left edge (right ${closed.right.toFixed(0)}, the video starts at ${closed.screenLeft.toFixed(0)})`);
      await clickAt(page, '.media-playlist-toggle');
      await sleep(400);
      const open = await page.evaluate(DRAWER);
      check(Math.abs(open.left - open.screenLeft) <= 1 && Math.abs(open.right - (open.screenLeft + 380)) <= 1, `Hebrew: the open drawer covers the video's left edge, 380 px (${open.left.toFixed(0)} to ${open.right.toFixed(0)}, the video starts at ${open.screenLeft.toFixed(0)})`);
      await shot(page, 'video-hebrew-drawer');
      await press(page, 'Escape');
      await press(page, 'Escape');
      await until(page, `!(${VIEWER})`, 10000, 'the viewer closes');
    });

    await step('the drawer keeps its list, scroll and order between openings', async () => {
      await setPrefs(page, urlOf('video') + '?lang=en', 'en', 'light');
      await openPlayer(page, urlOf('video') + '?lang=en', 'bright.webm');
      await clickAt(page, '.media-playlist-toggle');
      await sleep(300);
      const rows = await page.evaluate(`[...document.querySelectorAll('[data-testid="media-row"]')].map((r) => r.querySelector('.media-title')?.title ?? '')`);
      await clickAt(page, '.media-drawer-close');
      await sleep(300);
      await clickAt(page, '.media-playlist-toggle');
      await sleep(300);
      const again = await page.evaluate(`[...document.querySelectorAll('[data-testid="media-row"]')].map((r) => r.querySelector('.media-title')?.title ?? '')`);
      check(JSON.stringify(rows) === JSON.stringify(again) && rows.length > 0, `the queue keeps its rows and order between openings (${rows.length} rows)`);
      await press(page, 'Escape');
      await press(page, 'Escape');
      await until(page, `!(${VIEWER})`, 10000, 'the viewer closes');
    });

    await step('a file in an album with its cover inside the file shows that cover, large and not upscaled', async () => {
      await setViewport(page, 1280, 800);
      await setPrefs(page, urlOf('embedded') + '?lang=en', 'en', 'light');
      await openPlayer(page, urlOf('embedded') + '?lang=en', 'a-tagged.mp3');
      await until(page, `document.querySelector('[data-testid="media-cover"]')?.dataset.cover === 'yes'`, 25000, 'the embedded cover');
      await sleep(300);
      const c = await page.evaluate(COVER);
      check(c.source === 'file', `the file's own cover is used (${c.source})`);
      // The picture fills the cover box at any natural size (decision 63): a picture smaller than the box is scaled up to it.
      check(Math.abs(c.img.w - c.box.w) <= 1 && Math.abs(c.img.h - c.box.h) <= 1, `the cover fills its box (shown ${c.img.w}x${c.img.h} in a ${c.box.w}x${c.box.h} box, natural ${c.img.natural})`);
      check(c.box.h >= 200, `the cover area is large on the screen (${c.box.w}x${c.box.h})`);
      console.log(`    the embedded cover: natural ${c.img.natural} px, shown ${c.img.w} by ${c.img.h} px`);
      await shot(page, 'audio-cover-1280');
    });

    await step('the next track replaces the cover on the step, with no flash of the wrong one', async () => {
      await page.evaluate(SAMPLER);
      await page.evaluate(`document.querySelector('[aria-label="Next file"]')?.click()`);
      await until(page, `document.querySelector('.media-art-name bdi')?.textContent === 'b-plain.ogg'`, 15000, 'the next track');
      await until(page, `document.querySelector('[data-testid="media-cover"]')?.dataset.cover === 'no'`, 15000, 'the next track without a cover');
      await sleep(300);
      const samples = await page.evaluate(`(() => { window.__x6Watch.disconnect(); return window.__x6Samples; })()`);
      const flash = samples.filter((s) => s.name === 'b-plain.ogg' && (s.source === 'file' || s.source === 'folder'));
      check(flash.length === 0, `no picture of the first track shows under the second's name (${flash.length} samples)`);
    });

    await step('a file with no picture of its own takes the folder picture; a folder with none shows the music icon', async () => {
      await setPrefs(page, urlOf('cover') + '?lang=en', 'en', 'light');
      await openPlayer(page, urlOf('cover') + '?lang=en', 'a-plain.ogg');
      await until(page, `document.querySelector('[data-testid="media-cover"]')?.dataset.cover === 'yes'`, 25000, 'the folder picture');
      const c = await page.evaluate(COVER);
      check(c.source === 'folder' && c.img, `a file with no cover of its own shows the folder's picture (${c.source})`);
      await shot(page, 'audio-folder-cover-1280');
      await setPrefs(page, urlOf('neither') + '?lang=en', 'en', 'light');
      await openPlayer(page, urlOf('neither') + '?lang=en', 'a-plain.ogg');
      await until(page, `document.querySelector('[data-testid="media-cover"]')?.dataset.cover === 'no'`, 25000, 'no cover');
      await sleep(300);
      const n = await page.evaluate(COVER);
      check(n.icon && !n.img && n.name === 'a-plain.ogg', `a folder with no picture shows the music icon and the file's name (${JSON.stringify(n.name)})`);
      await shot(page, 'audio-without-1280');
      // The box is one colour, and the icon another: no square or shade behind the icon (decision 63, fix 2).
      const px = await boxPixels(page);
      check(px.boxColours.length === 1 && px.iconPixels > 0 && px.iconColours.some((c) => !px.boxColours.includes(c)), `the fallback box is one colour, and the icon is another (${px.boxColours.length} colour(s) in the box: ${JSON.stringify(px.boxColours)}; ${px.iconColours.length} in the icon)`);
    });

    await step('a picture fills the cover box at 1440 by 900, scaled up or down to it: drawn at the box size, and the box is at least 500 px', async () => {
      await setViewport(page, 1440, 900);
      // A folder picture of 300 by 300.
      await setPrefs(page, urlOf('small') + '?lang=en', 'en', 'light');
      await openPlayer(page, urlOf('small') + '?lang=en', 'a-plain.ogg');
      await until(page, `document.querySelector('[data-testid="media-cover"]')?.dataset.cover === 'yes'`, 25000, 'the folder picture');
      await sleep(400);
      const fit = await page.evaluate(FIT);
      check(fit.source === 'folder' && fit.img && fit.img.natural[0] === 300 && Math.abs(fit.img.w - fit.box.w) <= 1 && Math.abs(fit.img.h - fit.box.h) <= 1 && fit.box.w >= 500 && fit.box.h >= 500, `a 300 by 300 folder picture is drawn at the box's size, ${fit.img?.w} by ${fit.img?.h} in a ${fit.box.w} by ${fit.box.h} box (natural ${fit.img?.natural})`);
      await shot(page, 'audio-small-picture-1440');
      await press(page, 'Escape');
      await until(page, `!(${VIEWER})`, 10000, 'the viewer closes');
      // A file's own picture (embedded in the file) fills the box the same way.
      await setPrefs(page, urlOf('embedded') + '?lang=en', 'en', 'light');
      await openPlayer(page, urlOf('embedded') + '?lang=en', 'a-tagged.mp3');
      await until(page, `document.querySelector('[data-testid="media-cover"]')?.dataset.cover === 'yes'`, 25000, 'the embedded picture');
      await sleep(400);
      const own = await page.evaluate(FIT);
      check(own.source === 'file' && own.img && Math.abs(own.img.w - own.box.w) <= 1 && Math.abs(own.img.h - own.box.h) <= 1 && own.box.w >= 500 && own.box.h >= 500, `a file's own picture is drawn at the box's size too, ${own.img?.w} by ${own.img?.h} in a ${own.box.w} by ${own.box.h} box`);
      await press(page, 'Escape');
      await until(page, `!(${VIEWER})`, 10000, 'the viewer closes');
    });

    await step('an album of fourteen tracks with one image beside them reads its folder once, and every track has the cover', async () => {
      await setPrefs(page, `${base}/files/${shared}/${albumDir}?lang=en`, 'en', 'light');
      const mark = page.sent.length;
      await openPlayer(page, `${base}/files/${shared}/${albumDir}?lang=en`, '01 Track.mp3');
      // The album's files are short: the playlist would run to its end while the picture is read. The file repeats while the checks look.
      await holdPlaying(page);
      await until(page, `document.querySelector('[data-testid="media-cover"]')?.dataset.cover === 'yes'`, 25000, 'the album picture');
      const sources = [];
      for (let i = 0; i < 3; i++) {
        await holdPlaying(page);
        // The picture of the track on screen is read from the folder, which takes a moment on a slow machine: the check waits for it.
        await until(page, `(() => { const s = document.querySelector('[data-testid="media-cover"]')?.dataset.source; return s === 'folder' || s === 'file'; })()`, 15000, 'the picture of the track on screen');
        sources.push(await page.evaluate(COVER).then((c) => c.source));
        await page.evaluate(`document.querySelector('[aria-label="Next file"]')?.click()`);
        await sleep(900);
      }
      check(sources.every((s) => s === 'folder'), `each track of the album shows the folder's picture (${sources.join(', ')})`);
      const reads = page.sent.slice(mark).filter((r) => r.method === 'GET' && r.url.includes(`/nodes/${albumDir}/children`) && !r.url.includes('sort='));
      check(reads.length === 1, `the album's folder is read once for its covers (${reads.length} reads)`);
      await press(page, 'Escape');
      await until(page, `!(${VIEWER})`, 10000, 'the viewer closes');
    });

    await step('44 px targets at rest, hovered and focused, on a phone', async () => {
      await setViewport(page, 390, 800);
      await setPrefs(page, urlOf('video') + '?lang=en', 'en', 'light');
      await openFolder(page, urlOf('video') + '?lang=en', 'bright.webm');
      await tap(page, await rowPoint(page, 'bright.webm'));
      await until(page, VIEWER, 15000, 'the phone viewer');
      await until(page, `!!${MEDIA}`, 15000, 'the phone player');
      const sizes = (sel) => `(() => { const el = document.querySelector(${q(sel)}); if (!el) return null; const r = el.getBoundingClientRect(); return { w: r.width, h: r.height }; })()`;
      const targets = { toggle: '.media-playlist-toggle', close: '[data-testid="preview"] header button' };
      const ok = (s) => s && s.w >= 43.5 && s.h >= 43.5;
      for (const [name, sel] of Object.entries(targets)) {
        const rest = await page.evaluate(sizes(sel));
        check(ok(rest), `${name} is 44 px at rest (${rest?.w.toFixed(1)} by ${rest?.h.toFixed(1)})`);
        await page.evaluate(`document.querySelector(${q(sel)}).focus()`);
        await sleep(300);
        const focus = await page.evaluate(sizes(sel));
        check(ok(focus), `${name} is 44 px when focused (${focus?.w.toFixed(1)} by ${focus?.h.toFixed(1)})`);
        const r = await page.evaluate(`(() => { const r = document.querySelector(${q(sel)}).getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
        await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: r.x, y: r.y });
        await sleep(300);
        const hover = await page.evaluate(sizes(sel));
        check(ok(hover), `${name} is 44 px when hovered (${hover?.w.toFixed(1)} by ${hover?.h.toFixed(1)})`);
      }
      await clickAt(page, '.media-playlist-toggle');
      await sleep(400);
      const x = await page.evaluate(sizes('.media-drawer-close'));
      check(ok(x), `the drawer's close button is 44 px (${x?.w.toFixed(1)} by ${x?.h.toFixed(1)})`);
      await shot(page, 'video-phone-drawer');
      await press(page, 'Escape');
      await press(page, 'Escape');
      await until(page, `!(${VIEWER})`, 10000, 'the phone viewer closes');
      await setViewport(page, 1280, 800);
    });

    await step('the phone shots: the video, the audio with its cover, and the drawer in Hebrew and in dark', async () => {
      await setViewport(page, 390, 800);
      await setPrefs(page, urlOf('video') + '?lang=en', 'en', 'light');
      await openFolder(page, urlOf('video') + '?lang=en', 'bright.webm');
      await tap(page, await rowPoint(page, 'bright.webm'));
      await until(page, VIEWER, 15000, 'the phone viewer');
      await holdPlaying(page);
      await shot(page, 'video-phone');
      await press(page, 'Escape');
      await until(page, `!(${VIEWER})`, 10000, 'the phone viewer closes');
      await setPrefs(page, urlOf('embedded') + '?lang=en', 'en', 'light');
      await openFolder(page, urlOf('embedded') + '?lang=en', 'a-tagged.mp3');
      await tap(page, await rowPoint(page, 'a-tagged.mp3'));
      await until(page, VIEWER, 15000, 'the phone audio viewer');
      await until(page, `document.querySelector('[data-testid="media-cover"]')?.dataset.cover === 'yes'`, 25000, 'the phone cover');
      await shot(page, 'audio-phone');
      await press(page, 'Escape');
      await until(page, `!(${VIEWER})`, 10000, 'the phone audio viewer closes');
      await setPrefs(page, urlOf('video') + '?lang=he', 'he', 'dark');
      await openFolder(page, urlOf('video') + '?lang=he', 'bright.webm');
      await tap(page, await rowPoint(page, 'bright.webm'));
      await until(page, VIEWER, 15000, 'the Hebrew phone viewer');
      await holdPlaying(page);
      await clickAt(page, '.media-playlist-toggle');
      await sleep(400);
      await shot(page, 'video-phone-drawer-hebrew-dark');
      await press(page, 'Escape');
      await press(page, 'Escape');
      await until(page, `!(${VIEWER})`, 10000, 'the viewer closes');
      await setViewport(page, 1280, 800);
    });

    await step('the desktop shots in dark: the video with the drawer, and the audio without a cover', async () => {
      await setPrefs(page, urlOf('video') + '?lang=en', 'en', 'dark');
      await openPlayer(page, urlOf('video') + '?lang=en', 'bright.webm');
      await holdPlaying(page);
      await clickAt(page, '.media-playlist-toggle');
      await sleep(400);
      await shot(page, 'video-dark-drawer');
      await press(page, 'Escape');
      await press(page, 'Escape');
      await until(page, `!(${VIEWER})`, 10000, 'the viewer closes');
      await setPrefs(page, urlOf('neither') + '?lang=en', 'en', 'dark');
      await openPlayer(page, urlOf('neither') + '?lang=en', 'a-plain.ogg');
      await until(page, `document.querySelector('[data-testid="media-cover"]')?.dataset.cover === 'no'`, 25000, 'no cover');
      await shot(page, 'audio-dark-without');
      await press(page, 'Escape');
      await until(page, `!(${VIEWER})`, 10000, 'the viewer closes');
    });

    await step('a share link opens an audio file and a video, signed out: the cover is large, the video fills the window, and the drawer sits beside it', async () => {
      // The share page is a visitor's page: links that anyone can open must be on, and the folder must allow links. The switch
      // is put back as it was at the end of this step, and the two links are deleted: the sharing checks after these turn the
      // switch on themselves and count the account's links.
      const wasOn = (await api(base, 'GET', `/api/v1/accounts/${accountID}/sharing-policy`, accountKey)).public_links === true;
      await api(base, 'PATCH', `/api/v1/accounts/${accountID}/sharing-policy`, accountKey, { public_links: true });
      await api(base, 'PATCH', `/api/v1/folders/${shared}/settings`, accountKey, { allow_links: true });
      const linkIDs = [];
      const makeLink = async (dir) => {
        const made = await api(base, 'POST', `/api/v1/nodes/${dirOf(dir)}/share-links`, accountKey, { audience: 'anyone', role: 'viewer' });
        linkIDs.push(made.link.id);
        return made.url;
      };
      const audioURL = await makeLink('cover');
      const videoURL = await makeLink('video');
      await signOut();
      await setViewport(page, 1280, 900);
      await page.goto(audioURL);
      await until(page, `!!document.querySelector('.rows button')`, 20000, 'the share listing');
      await page.evaluate(`[...document.querySelectorAll('.rows button')].find((b) => b.textContent === 'a-plain.ogg').click(), true`);
      await until(page, `!!document.querySelector('[data-testid="media-cover"]')`, 20000, 'the share player');
      await sleep(400);
      const ca = await page.evaluate(`(() => { const e = document.querySelector('.media-cover-empty'); const r = e ? e.getBoundingClientRect() : null; return { icon: !!e, w: r ? r.width : 0, h: r ? r.height : 0, bottom: r ? r.bottom : 0, innerH: innerHeight, cover: document.querySelector('[data-testid="media-cover"]').dataset.cover }; })()`);
      check(ca.icon && ca.w >= 200 && ca.h >= 200 && ca.bottom <= ca.innerH, `a share visitor's audio file shows its icon large, inside the window (${ca.w.toFixed(0)} x ${ca.h.toFixed(0)} of ${ca.innerH} px tall)`);
      check(ca.cover === 'no', `a share visitor asks for no picture (cover ${ca.cover})`);
      // A second share differs only in its secret, so the page is left first: a hash change alone does not load the link.
      await page.goto(base + '/login');
      await page.goto(videoURL);
      await until(page, `!!document.querySelector('.rows button')`, 20000, 'the share listing of the video');
      // A real click: a script's click is no gesture, and the browser then refuses the video's play.
      await clickRowText(page, 'bright.webm');
      await until(page, `!!${MEDIA}`, 20000, 'the share video');
      await holdPlaying(page);
      await sleep(400);
      const vh = await page.evaluate(`(() => { const v = document.querySelector('[data-testid="media-screen"] video').getBoundingClientRect(); const t = document.querySelector('.media-playlist-toggle'); return { top: v.top, h: v.height, bottom: v.bottom, innerH: innerHeight, toggleInHead: !!t && !!t.closest('.media-toggle-slot'), toggleH: t ? t.getBoundingClientRect().height : 0 }; })()`);
      check(vh.h >= 300 && vh.bottom <= vh.innerH && vh.toggleInHead, `a share visitor's video fills the window's height under the head, with the Playlist button in the head (${vh.h.toFixed(0)} px of ${vh.innerH}; ${JSON.stringify(vh)})`);
      await clickAt(page, '.media-playlist-toggle');
      await sleep(400);
      const both = await page.evaluate(`(() => { const d = document.querySelector('[data-media-drawer]').getBoundingClientRect(); const v = document.querySelector('[data-testid="media-screen"] video').getBoundingClientRect(); return { open: document.querySelector('[data-media-drawer]').dataset.open, dw: d.width, dl: d.left, vr: v.right }; })()`);
      check(both.open === 'true' && Math.abs(both.dw - 380) <= 1 && both.vr <= both.dl + 1, `on a share page the drawer opens beside the video, 380 px (video to ${both.vr.toFixed(0)}, drawer from ${both.dl.toFixed(0)})`);
      await clickAt(page, '.media-playlist-toggle');
      await sleep(300);
      // On a phone the share page's head is one row as well: the Playlist button and Download share one top, 44 px tall each.
      await setViewport(page, 390, 800);
      await sleep(400);
      const shareHead = await page.evaluate(`(() => { const row = document.querySelector('.media-toggle-slot').parentElement; const bs = Array.from(row.querySelectorAll('button')).map((b) => { const r = b.getBoundingClientRect(); return { label: b.getAttribute('aria-label') || b.textContent.trim(), top: Math.round(r.top * 10) / 10, height: Math.round(r.height) }; }); return { bs, rowH: Math.round(row.getBoundingClientRect().height) }; })()`);
      const sTops = shareHead.bs.map((b) => b.top);
      check(shareHead.bs.length === 2 && Math.max(...sTops) - Math.min(...sTops) <= 1 && shareHead.bs.every((b) => b.height >= 44), `on a share page at 390 px the Playlist button and Download are one row, 44 px tall (${JSON.stringify(shareHead.bs)})`);
      await setViewport(page, 1280, 900);
      // The visitor is signed out again for the rest of the checks.
      await signIn(MEMBER);
      for (const id of linkIDs) await api(base, 'DELETE', `/api/v1/share-links/${id}`, accountKey);
      await api(base, 'PATCH', `/api/v1/accounts/${accountID}/sharing-policy`, accountKey, { public_links: wasOn });
      const after = await api(base, 'GET', `/api/v1/accounts/${accountID}/sharing-policy`, accountKey);
      check(after.public_links === wasOn, `the account's switch for links that anyone can open is back as it was (${after.public_links})`);
    });
  } finally {
    await setViewport(page, 1280, 800);
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    const restore = [['selfcloud.lang', kept.lang], ['selfcloud.theme', kept.theme], ['selfcloud.files', kept.files]];
    await page.evaluate(`(() => { for (const [k, v] of ${q(restore)}) { if (v === null) localStorage.removeItem(k); else localStorage.setItem(k, v); } return true; })()`);
  }
  await page.goto(base + '/');
  await until(page, `!!document.querySelector('[data-testid="account-menu"]') || location.pathname === '/login'`, 15000, 'the shell after the checks');
  await signOut();
  await signIn(MEMBER);
  console.log('x6 checks passed');
}
