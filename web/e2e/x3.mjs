// SPDX-License-Identifier: AGPL-3.0-or-later

// The tags and covers checks of the player (slice x3, decision 56), run inside the shared end-to-end run (run.mjs) on the
// shared page and server: an audio file's title and artist show in the viewer and the mini player, in Hebrew with dir="auto";
// its cover shows once the server has made it, and the cover's box keeps its size while the tags and the cover arrive; a file
// with no tags shows its file name and a plain icon; a corrupt file shows its name and does not stop the player from playing the
// next file. With SELFCLOUD_X3_SHOTS set to a folder, the screenshots of desktop and phone, light and dark, English and Hebrew
// are written there. Run it alone with SELFCLOUD_E2E_ONLY=x3. The checks end signed in as the member, as the shared run expects.
import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const media = fileURLToPath(new URL('./media/', import.meta.url));
const shots = process.env.SELFCLOUD_X3_SHOTS;
const PASSWORD = 'correct horse battery staple';
const MEMBER = { email: 'mateo@example.com', name: 'Mateo Member', password: PASSWORD };
const q = (v) => JSON.stringify(v);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// The title the Hebrew fixture carries (internal/audio/testdata/tag24.mp3, made with ffmpeg).
const HEBREW_TITLE = 'שלום עולם';

async function step(name, fn) {
  console.log(`-- ${name}`);
  await fn();
}

// until polls a page expression with a bound, and returns when it is true.
async function until(page, expr, ms = 15000, what = expr) {
  const deadline = Date.now() + ms;
  for (;;) {
    if (await page.evaluate(`!!(${expr})`).catch(() => false)) return;
    if (Date.now() > deadline) throw new Error(`timed out (${ms} ms) waiting for: ${what}`);
    await sleep(100);
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

// tap is one touch on a phone.
async function tap(page, { x, y }) {
  await page.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] });
  await sleep(60);
  await page.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
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

// setPhone sets the phone's size and touch, or puts the desktop back.
async function setPhone(page, on) {
  if (on) {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 800, deviceScaleFactor: 1, mobile: true });
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
  } else {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: false });
  }
}

async function shot(page, name) {
  if (!shots) return;
  const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
  await writeFile(join(shots, `${name}.png`), Buffer.from(data, 'base64'));
  console.log(`shot ${join(shots, `${name}.png`)}`);
}

// COVER reads the cover box of the player on screen: its size, and whether a picture is in it.
const COVER = `(() => { const c = document.querySelector('[data-testid="preview"] [data-testid="tag-cover"]'); if (!c) return null; const r = c.getBoundingClientRect(); return { w: Math.round(r.width), h: Math.round(r.height), cover: c.dataset.cover, img: !!c.querySelector('img'), icon: !!c.querySelector('svg') }; })()`;
// SAMPLER records the header at every change of the page while the viewer is open: the title, the cover box, the header's height
// and where the title line sits in it. The window keeps the samples for the check to read.
const SAMPLER = `(() => {
  window.__x3Samples = [];
  window.__x3Watch = new MutationObserver(() => {
    const v = document.querySelector('[data-testid="preview"] header');
    const c = v?.querySelector('[data-testid="tag-cover"]');
    const t = v?.querySelector('bdi');
    if (!c || !t) return;
    const r = c.getBoundingClientRect();
    const hr = v.getBoundingClientRect();
    window.__x3Samples.push({ title: t.textContent, cover: c.dataset.cover, box: Math.round(r.width) + 'x' + Math.round(r.height), header: Math.round(hr.height), top: Math.round(t.getBoundingClientRect().top - hr.top) });
  });
  window.__x3Watch.observe(document.documentElement, { subtree: true, childList: true, characterData: true, attributes: true });
  return true;
})()`;
const VIEW_TITLE = `document.querySelector('[data-testid="preview"] header bdi')?.textContent ?? null`;


// lineUp checks the viewer's two lines start at the same edge in the page's direction, and the playlist row's title and artist are
// apart: dir="auto" gives each its own direction, which must not pull them away from each other or against each other.
async function lineUp(page, check, what) {
  const m = await page.evaluate(`(() => {
    const rtl = document.documentElement.dir === 'rtl';
    const t = document.querySelector('[data-testid="preview"] header bdi').getBoundingClientRect();
    const a = document.querySelector('[data-testid="media-artist"]').getBoundingClientRect();
    const row = [...document.querySelectorAll('[data-testid="media-row"]')].find((x) => x.querySelector('.media-row-artist'));
    const rt = row.querySelector('.media-title bdi').getBoundingClientRect();
    const ra = row.querySelector('.media-row-artist').getBoundingClientRect();
    return { rtl, apart: Math.round(rtl ? t.right - a.right : t.left - a.left), gap: Math.round(rtl ? rt.left - ra.right : ra.left - rt.right), titleCut: row.querySelector('.media-title bdi').scrollWidth > row.querySelector('.media-title bdi').clientWidth };
  })()`);
  check(m.apart === 0, `${what}: the title and the artist start at the same edge (${q(m)})`);
  check(m.gap >= 6, `${what}: the row's title and artist have a gap (${q(m)})`);
  check(!m.titleCut, `${what}: the artist gives way before a short title is cut (${q(m)})`);
}

// rowFit checks the playlist rows at this width. The playing row keeps its state mark and its More button inside the row and
// apart from each other, so neither is clipped at an edge. The long row's title keeps at least MIN_TITLE px of room, so a long
// artist does not squeeze it to a few letters; a cut title ends in an ellipsis; and the whole title and artist are in their
// title attributes, so the cut is not the only way to read them.
const MIN_TITLE = 96;
const MIN_MARK_GAP = 8;
const MIN_ARTIST = 60;
// The failures are collected in fits and reported once the matrix is done, so one run shows every size's measures.
const fits = [];
async function rowFit(page, what) {
  await until(page, `!!document.querySelector('[data-testid="media-row"] .media-title[title="zz-long.mp3"]')`, 15000, 'the long row in the playlist');
  await until(page, `!!document.querySelector('[data-testid="media-row"] .media-title[title="tag24.mp3"]')`, 15000, 'the Hebrew row in the playlist');
  const m = await page.evaluate(`(() => {
    const inside = (el, row) => { const a = el.getBoundingClientRect(), b = row.getBoundingClientRect(); return a.left >= b.left - 0.5 && a.right <= b.right + 0.5; };
    const apart = (a, b) => { const x = a.getBoundingClientRect(), y = b.getBoundingClientRect(); return x.right <= y.left + 0.5 || y.right <= x.left + 0.5; };
    const rowNamed = (name) => [...document.querySelectorAll('[data-testid="media-row"]')].find((r) => r.querySelector('.media-title')?.title === name);
    const on = document.querySelector('[data-testid="media-row"][aria-current="true"]');
    const state = on.querySelector('.media-state');
    const menu = on.querySelector('.media-row-buttons .ant-btn');
    const long = rowNamed('zz-long.mp3');
    const short = rowNamed('tag24.mp3');
    const lt = long.querySelector('.media-title bdi');
    const la = long.querySelector('.media-row-artist');
    const st = short.querySelector('.media-title bdi');
    // The painted text of the label and of the short title, not only their boxes: a clipped glyph is outside its box's text.
    const painted = (el) => { const rg = document.createRange(); rg.selectNodeContents(el); return rg.getBoundingClientRect(); };
    const lab = painted(state);
    const shortNatural = painted(st).width;
    return {
      rowW: Math.round(on.getBoundingClientRect().width),
      stateIn: inside(state, on),
      labelTextIn: lab.left >= on.getBoundingClientRect().left - 0.5 && lab.right <= on.getBoundingClientRect().right + 0.5,
      menuIn: inside(menu, on),
      apart: apart(state, menu),
      markGap: Math.round(Math.min(Math.abs(menu.getBoundingClientRect().left - state.getBoundingClientRect().right), Math.abs(state.getBoundingClientRect().left - menu.getBoundingClientRect().right))),
      labelW: Math.round(lab.width),
      longTitleBox: Math.round(lt.getBoundingClientRect().width),
      longCut: lt.scrollWidth > lt.clientWidth,
      longEllipsis: getComputedStyle(lt).textOverflow === 'ellipsis',
      titleFull: lt.title === lt.textContent,
      artistFull: la !== null && la.title === la.textContent,
      shortTitleBox: Math.round(st.getBoundingClientRect().width),
      shortNatural: Math.round(shortNatural),
      artistBox: Math.round(on.querySelector('.media-row-artist')?.getBoundingClientRect().width ?? 0),
      artistNatural: Math.round(on.querySelector('.media-row-artist') ? painted(on.querySelector('.media-row-artist')).width : 0),
    };
  })()`);
  console.log(`fit ${what} ${q(m)}`);
  const fail = (ok, msg) => {
    if (!ok) fits.push(`${what}: ${msg} (${q(m)})`);
  };
  fail(m.stateIn && m.labelTextIn && m.menuIn && m.apart, "the playing row's mark, its text and More button are inside the row and apart");
  fail(m.markGap >= MIN_MARK_GAP, `the playing mark keeps ${MIN_MARK_GAP} px from the More button`);
  fail(m.longTitleBox >= MIN_TITLE, `a long artist leaves the long title at least ${MIN_TITLE} px`);
  fail(!m.longCut || m.longEllipsis, 'a cut long title ends in an ellipsis');
  fail(m.titleFull && m.artistFull, 'the whole title and artist are in their title attributes');
  fail(m.shortTitleBox >= Math.min(m.shortNatural, MIN_TITLE), 'a short title keeps its whole width while its row has room');
  fail(m.artistBox >= Math.min(m.artistNatural, MIN_ARTIST), `the playing row's artist keeps ${MIN_ARTIST} px (or its whole width)`);
}

// holdPlaying makes the file on screen repeat: the fixture is one second long, and the playlist would otherwise move on from it.
async function holdPlaying(page) {
  await until(page, `(() => { const v = document.querySelector('[data-testid="media-screen"] audio'); if (!v) return false; v.loop = true; return true; })()`, 15000, 'the audio element');
}

// x3Checks runs the checks. h is the shared run's helpers (api, rawPut, check, signIn, signOut); base and accountKey as in run.mjs.
export async function x3Checks(h, { page, base, accountKey, accountID, memberID }) {
  const { api, rawPut, check } = h;
  const signIn = (who) => h.signIn(page, who);
  const signOut = () => h.signOut(page);
  const kept = await page.evaluate(`(() => ({ lang: localStorage.getItem('selfcloud.lang'), theme: localStorage.getItem('selfcloud.theme'), files: localStorage.getItem('selfcloud.files') }))()`);
  // A folder of its own for the run's member (write): the Hebrew tagged song, an untagged track, and a corrupt file.
  const shared = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'X3 music' })).id;
  await api(base, 'PUT', `/api/v1/accounts/${accountID}/folders/${shared}/grants/${memberID}`, accountKey, { level: 'write' });
  await api(base, 'POST', `/api/v1/folders/${shared}/directories`, accountKey, { path: '/songs', parents: false });
  const put = (name, bytes, type) =>
    rawPut(base, `/api/v1/folders/${shared}/files?path=${encodeURIComponent(`/songs/${name}`)}`, accountKey, bytes, type);
  await put('tag24.mp3', await readFile(join(media, 'tag24.mp3')), 'audio/mpeg');
  await put('plain.ogg', await readFile(join(media, 'tone.ogg')), 'audio/ogg');
  await put('broken.flac', Buffer.from('fLaC\x80\x00\x00\x10' + '\x00'.repeat(16) + 'not audio'), 'audio/flac');
  // A long title and a long artist: the name sorts last, so the other rows keep their places in the playlist.
  await put('zz-long.mp3', await readFile(join(media, 'zz-long.mp3')), 'audio/mpeg');
  const children = (await api(base, 'GET', `/api/v1/folders/${shared}/children?path=%2F`, accountKey)).items;
  const dirID = children.find((n) => n.name === 'songs')?.id;
  const songsURL = `${base}/files/${shared}/${dirID}`;

  await signOut();
  try {
    await step('the member signs in', async () => {
      await signIn(MEMBER);
    });

    await step('the Hebrew title and artist show in the player, with dir="auto", and nothing moves while they arrive', async () => {
      await setPrefs(page, songsURL + '?lang=en', 'en', 'light');
      await openFolder(page, songsURL + '?lang=en', 'tag24.mp3');
      // The sampler reads the header at every change of the page, from the moment the viewer opens, so the state before the tags
      // is measured too.
      await page.evaluate(SAMPLER);
      await dblClick(page, await rowPoint(page, 'tag24.mp3'));
      await holdPlaying(page);
      await until(page, `${VIEW_TITLE} === ${q(HEBREW_TITLE)}`, 20000, 'the Hebrew title in the player');
      await until(page, `!!document.querySelector('[data-testid="preview"] [data-testid="tag-cover"][data-cover="yes"] img')`, 25000, 'the cover picture');
      await sleep(300);
      const samples = await page.evaluate(`(() => { window.__x3Watch.disconnect(); return window.__x3Samples; })()`);
      const distinct = (k) => [...new Set(samples.map((s) => s[k]))];
      check(samples.some((s) => s.title === 'tag24.mp3' && s.cover === 'no'), `the file name and the icon show before the tags and the cover (${samples.length} samples, titles ${q(distinct('title'))})`);
      check(distinct('title').includes(HEBREW_TITLE) && samples.at(-1).cover === 'yes', `the title and the cover arrive (covers ${q(distinct('cover'))})`);
      check(distinct('box').length === 1 && distinct('box')[0] === '48x48', `the cover box stays 48 by 48 px throughout (${q(distinct('box'))})`);
      check(distinct('header').length === 1, `the header keeps its height throughout (${q(distinct('header'))})`);
      check(distinct('top').length === 1, `the title line does not move in the header (${q(distinct('top'))})`);
      const dirAuto = await page.evaluate(`document.querySelector('[data-testid="preview"] header bdi')?.getAttribute('dir')`);
      check(dirAuto === 'auto', 'the title is set with dir="auto"');
      const artist = await page.evaluate(`(() => { const a = document.querySelector('[data-testid="media-artist"]'); return a ? { text: a.textContent, dir: a.getAttribute('dir') } : null; })()`);
      check(artist?.text === 'Artist One' && artist?.dir === 'auto', `the artist shows under the title with dir="auto" (${q(artist)})`);
      const row = await page.evaluate(`(() => { const r = [...document.querySelectorAll('[data-testid="media-row"]')].find((x) => x.querySelector('.media-title')?.title === 'tag24.mp3'); return r ? { title: r.querySelector('.media-title bdi')?.textContent, artist: r.querySelector('.media-row-artist')?.textContent } : null; })()`);
      check(row?.title === HEBREW_TITLE && row?.artist === 'Artist One', `the playlist row shows the title and the artist (${q(row)})`);
      await until(page, `[...document.querySelectorAll('[data-testid="media-row"]')].find((x) => x.querySelector('.media-title')?.title === 'tag24.mp3')?.querySelector('[data-testid="tag-cover"]')?.dataset.cover === 'yes'`, 15000, 'the cover in the playlist row');
      check(true, 'the playlist row shows the cover');
      const plainRow = await page.evaluate(`(() => { const r = [...document.querySelectorAll('[data-testid="media-row"]')].find((x) => x.querySelector('.media-title')?.title === 'plain.ogg'); return r ? { title: r.querySelector('.media-title bdi')?.textContent, cover: r.querySelector('[data-testid="tag-cover"]')?.dataset.cover, icon: !!r.querySelector('[data-testid="tag-cover"] svg') } : null; })()`);
      check(plainRow?.title === 'plain.ogg' && plainRow?.cover === 'no' && plainRow?.icon, `a row without tags shows its file name and the icon (${q(plainRow)})`);
      await until(page, `navigator.mediaSession?.metadata?.title === ${q(HEBREW_TITLE)} && navigator.mediaSession.metadata.artwork.length === 1`, 10000, 'the Media Session with the cover');
      const lock = await page.evaluate(`(() => { const m = navigator.mediaSession?.metadata; return m ? { title: m.title, artist: m.artist, album: m.album, art: m.artwork.length } : null; })()`);
      check(lock?.title === HEBREW_TITLE && lock?.artist === 'Artist One' && lock?.album === 'Album X' && lock?.art === 1, `the Media Session has the title, artist, album and cover (${q(lock)})`);
    });

    await step('the mini player keeps the title and the cover after the viewer closes', async () => {
      await page.evaluate(`document.querySelector('[data-testid="preview"] header button').click()`);
      await until(page, `!!document.querySelector('[data-testid="media-mini"] .media-mini-name')`, 10000, 'the mini player');
      await until(page, `document.querySelector('[data-testid="media-mini"] .media-mini-name bdi')?.textContent === ${q(HEBREW_TITLE)}`, 10000, 'the title in the mini player');
      const artist = await page.evaluate(`document.querySelector('[data-testid="media-mini"] .media-mini-artist')?.textContent ?? null`);
      check(artist === 'Artist One', `the mini player shows the artist (${artist})`);
      await until(page, `!!document.querySelector('[data-testid="media-mini"] [data-testid="tag-cover"]')`, 10000, 'the mini cover');
      await page.evaluate(`[...document.querySelectorAll('.media-mini .media-mini-btn')].pop().click()`);
    });

    await step('an untagged file shows its name and a plain icon', async () => {
      await openFolder(page, songsURL + '?lang=en', 'plain.ogg');
      await dblClick(page, await rowPoint(page, 'plain.ogg'));
      await until(page, `${VIEW_TITLE} === 'plain.ogg'`, 15000, 'the file name for an untagged file');
      await until(page, `document.querySelector('[data-testid="preview"] [data-testid="tag-cover"]')?.dataset.cover === 'no'`, 15000, 'no cover');
      const box = await page.evaluate(COVER);
      check(box.icon && !box.img && box.w === 48, `an untagged file has the icon in the box (${JSON.stringify(box)})`);
      await page.evaluate(`document.querySelector('[data-testid="preview"] header button').click()`);
    });

    await step('a corrupt file never blocks playback: the player moves on and plays the next file', async () => {
      await openFolder(page, songsURL + '?lang=en', 'broken.flac');
      await dblClick(page, await rowPoint(page, 'broken.flac'));
      // The playlist skips a file that cannot play, and the next one is plain.ogg, sorted after it.
      await until(page, `${VIEW_TITLE} === 'plain.ogg'`, 20000, 'the next file after the corrupt one');
      await until(page, `(() => { const v = document.querySelector('[data-testid="media-screen"] audio'); return !!v && v.readyState >= 2 && !v.paused && v.currentTime > 0.2; })()`, 20000, 'the next file plays');
      check(true, 'a corrupt file does not stop the player: the next file plays');
      const none = await page.evaluate(`(() => { const r = [...document.querySelectorAll('[data-testid="media-row"]')].find((x) => x.querySelector('.media-title')?.title === 'broken.flac'); return r ? { title: r.querySelector('.media-title bdi')?.textContent, cover: r.querySelector('[data-testid="tag-cover"]')?.dataset.cover } : null; })()`);
      check(none?.title === 'broken.flac' && none?.cover === 'no', `the corrupt file's row keeps its name and the icon (${q(none)})`);
      await page.evaluate(`document.querySelector('[data-testid="preview"] header button').click()`);
    });

    // The screenshot matrix (SELFCLOUD_X3_SHOTS set): the player and the mini bar on the desktop and the phone, in English and
    // Hebrew, light and dark. The URL carries ?lang= because the owner's account locale outranks the saved choice.
    if (shots) {
      await step('the screenshot matrix: desktop and phone, light and dark, English and Hebrew', async () => {
        for (const [lang, theme] of [['en', 'light'], ['en', 'dark'], ['he', 'light'], ['he', 'dark']]) {
          const tag = `${lang}-${theme}`;
          const u = songsURL + `?lang=${lang}`;
          await setPrefs(page, base + '/', lang, theme);
          await openFolder(page, u, 'tag24.mp3');
          await dblClick(page, await rowPoint(page, 'tag24.mp3'));
          await holdPlaying(page);
          await until(page, `${VIEW_TITLE} === ${q(HEBREW_TITLE)}`, 20000, 'the Hebrew title');
          await until(page, `!!document.querySelector('[data-testid="preview"] [data-testid="tag-cover"][data-cover="yes"] img')`, 25000, 'the cover');
          await lineUp(page, check, `desktop ${tag}`);
          await rowFit(page, `desktop ${tag}`);
          await shot(page, `player-desktop-${tag}`);
          await page.evaluate(`document.querySelector('[data-testid="preview"] header button').click()`);
          await until(page, `!!document.querySelector('[data-testid="media-mini"]')`, 10000, 'the mini player');
          await shot(page, `mini-desktop-${tag}`);
          await page.evaluate(`[...document.querySelectorAll('.media-mini .media-mini-btn')].pop().click()`);
          await setPhone(page, true);
          await openFolder(page, u, 'tag24.mp3');
          await tap(page, await rowPoint(page, 'tag24.mp3'));
          await until(page, `!!document.querySelector('[data-testid="preview"]')`, 15000, 'the phone viewer');
          await holdPlaying(page);
          await until(page, `${VIEW_TITLE} === ${q(HEBREW_TITLE)}`, 20000, 'the Hebrew title on the phone');
          await sleep(500);
          await lineUp(page, check, `phone ${tag}`);
          await rowFit(page, `phone ${tag}`);
          await shot(page, `player-phone-${tag}`);
          await page.evaluate(`document.querySelector('[data-testid="preview"] header button').click()`);
          await until(page, `!!document.querySelector('[data-testid="media-mini"]')`, 10000, 'the phone mini player');
          await sleep(300);
          await shot(page, `mini-phone-${tag}`);
          await page.evaluate(`[...document.querySelectorAll('.media-mini .media-mini-btn')].pop().click()`);
          await setPhone(page, false);
        }
        check(fits.length === 0, `the playlist rows fit: ${fits.join(' | ')}`);
      });
    }
  } finally {
    await setPhone(page, false);
    const restore = [['selfcloud.lang', kept.lang], ['selfcloud.theme', kept.theme], ['selfcloud.files', kept.files]];
    await page.evaluate(`(() => { for (const [k, v] of ${q(restore)}) { if (v === null) localStorage.removeItem(k); else localStorage.setItem(k, v); } return true; })()`);
  }
  await page.goto(base + '/');
  await until(page, `!!document.querySelector('[data-testid="account-menu"]') || location.pathname === '/login'`, 15000, 'the shell after the phone size');
  await signOut();
  await signIn(MEMBER);
  console.log('x3 checks passed');
}

