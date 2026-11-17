// SPDX-License-Identifier: AGPL-3.0-or-later

// The album-page player at phone width (slice mp7), for audio only, run inside the shared end-to-end run (run.mjs) on the
// shared page, server and accounts. At a window no wider than 599 px an audio file in the signed-in session is laid out as an
// album page in both the viewer and the Media page's Now playing: a header naming the album (else the folder) with the artist
// under it and Share when allowed, a full-width square cover, the seek row, one centred line, a large transport, an action row
// (Playlist, Download, More), then the queue as a table below a dashed divider. A video at phone width keeps the player it had
// before the album page — the bar that hides while it plays, the count header, the Playlist toggle and the drawer — and is
// judged so here. The checks judge the layout, the sizes, the absence of the bar's own transport at phone width for audio, the
// queue as the table, no sideways scroll from 320 to 599 px, and — the heart of it — the one element that plays, marked, never
// made again across a width change or a move between the viewer and the page. Over 599 px the bar and the drawer are as they
// were. Run it alone with SELFCLOUD_E2E_ONLY=mp7. The checks end signed in as the member, which the rest of the run expects;
// the interface's language and theme and the listing's view are put back afterwards.
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const media = fileURLToPath(new URL('./media/', import.meta.url));
const PASSWORD = 'correct horse battery staple';
const OWNER = { email: 'olivia@example.com', name: 'Olivia Owner', password: PASSWORD };
const MEMBER = { email: 'mateo@example.com', name: 'Mateo Member', password: PASSWORD };
const q = (v) => JSON.stringify(v);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// MORE_OPEN_STILL is true once the phone More menu's own dropdown is open and still: a dropdown is in the document before it
// is shown and while it animates open (and a closed one stays in it hidden), so a row of the menu is read only when the
// .ant-dropdown that holds .media-phone-menu carries no ant-dropdown-hidden, its submenu title's box is wider and taller than
// 0, and no animation runs anywhere in the dropdown. The motion's classes must be gone too: the appear draws one frame at full
// size before its animation starts and one scaled to nothing right after, and neither carries an animation yet, so "no
// animation" alone can meet either frame (the slide rows of the file menus wait out their motion the same way, decision 200).
export const MORE_OPEN_STILL = `(() => {
  const d = [...document.querySelectorAll('.ant-dropdown')].find((x) => (x.classList.contains('media-phone-menu') || x.querySelector('.media-phone-menu')) && !x.classList.contains('ant-dropdown-hidden'));
  if (!d) return false;
  if ([...d.classList].some((c) => c.includes('-appear') || c.includes('-enter') || c.includes('-leave'))) return false;
  const t = d.querySelector('.ant-dropdown-menu-submenu-title');
  if (!t) return false;
  const r = t.getBoundingClientRect();
  return r.width > 0 && r.height > 0 && d.getAnimations({ subtree: true }).length === 0;
})()`;

async function step(name, fn) {
  console.log(`-- ${name}`);
  await fn();
}

async function until(page, expr, ms = 15000, what = expr) {
  const deadline = Date.now() + ms;
  for (;;) {
    if (await page.evaluate(`!!(${expr})`).catch(() => false)) return;
    if (Date.now() > deadline) {
      const st = await page.evaluate(`JSON.stringify({ path: location.pathname, text: document.body.innerText.slice(0, 300) })`).catch((e) => `state-unavailable ${e}`);
      throw new Error(`timed out (${ms} ms) waiting for: ${what} | DIAG ${st}`);
    }
    await sleep(100);
  }
}

async function dblClick(page, { x, y }) {
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
  for (const clickCount of [1, 2]) {
    await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', buttons: 1, clickCount });
    await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', buttons: 0, clickCount });
    await sleep(100);
  }
}

async function clickAt(page, { x, y }) {
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
  await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', buttons: 1, clickCount: 1 });
  await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', buttons: 0, clickCount: 1 });
}

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

async function openFolder(page, url, firstName) {
  await page.goto(url);
  await page.evaluate(`localStorage.setItem('selfcloud.files', JSON.stringify({ view: 'list', sort: { key: 'name', dir: 'asc' } }))`);
  await page.goto(url);
  await until(page, `!!document.querySelector('button[data-view]')`, 15000, 'the view toggle');
  await page.evaluate(`(() => { const b = document.querySelector('button[data-view]'); if (b.dataset.view === 'list') b.click(); })()`);
  await until(page, `!!document.querySelector('[role="row"][aria-label=${JSON.stringify(firstName)}]')`, 15000, `row ${firstName}`);
}

const MEDIA = `document.querySelector('[data-testid="media-screen"] video, [data-testid="media-screen"] audio')`;

async function playing(page, ms = 20000) {
  await until(page, `(() => { const v = ${MEDIA}; return !!v && v.readyState >= 2; })()`, ms, 'the file has data');
  await until(page, `(() => { const v = ${MEDIA}; return !!v && !v.paused && v.currentTime > 0.2; })()`, 15000, 'the file plays');
}

const viewerShown = `(() => { const v = document.querySelector('[data-testid="preview"]'); return !!v && getComputedStyle(v).display !== 'none'; })()`;

async function closeViewer(page) {
  await page.evaluate(`document.querySelector('[data-testid="preview"] header button').click()`);
  await until(page, `!(${viewerShown})`, 10000, 'the viewer closes');
}

// setWidth emulates a window of the given width (a touch screen at phone width), as a person's resize does.
async function setWidth(page, width, phone) {
  await page.send('Emulation.setDeviceMetricsOverride', { width, height: phone ? 844 : 800, deviceScaleFactor: 1, mobile: phone });
  await page.send('Emulation.setTouchEmulationEnabled', phone ? { enabled: true, maxTouchPoints: 1 } : { enabled: false });
  await sleep(350);
}

// mark carries an identity mark on the element that plays, so a later check judges the same one.
async function mark(page, sel) {
  await page.evaluate(`(() => { const el = document.querySelector(${q(sel)}); el.__mp7 = 1; window.__mp7el = el; return true; })()`);
}

const overflow = `Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) - window.innerWidth`;

// phoneHead is the album-page header; barButtons is the bar's own transport row (not drawn at phone width).
const phoneHead = `[data-testid="media-phone-head"]`;
const barButtons = `[data-testid="media-controls"] .media-buttons`;

export async function mp7Checks(h, { page, base, accountKey, accountID }) {
  const { api, rawPut, check } = h;
  const signIn = (who) => h.signIn(page, who);
  const signOut = () => h.signOut(page);
  const kept = await page.evaluate(`(() => ({ lang: localStorage.getItem('selfcloud.lang'), theme: localStorage.getItem('selfcloud.theme'), files: localStorage.getItem('selfcloud.files') }))()`);
  // The fixtures: a folder of the owner's own with a tagged song (album, artist, title, cover), two untagged tracks and a
  // video. The tagged song runs half a minute, so it keeps playing through every resize below and the one element is judged
  // across the width change, not a next file the queue moved to.
  const folder = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'MP7 media' })).id;
  const tagged = await readFile(join(media, 'tag-long.mp3'));
  await rawPut(base, `/api/v1/folders/${folder}/files?path=${encodeURIComponent('/tag-long.mp3')}`, accountKey, tagged, 'audio/mpeg');
  const tone = await readFile(join(media, 'tone-long.ogg'));
  for (const name of ['track-one.ogg', 'track-two.ogg']) {
    await rawPut(base, `/api/v1/folders/${folder}/files?path=${encodeURIComponent(`/${name}`)}`, accountKey, tone, 'audio/ogg');
  }
  // The video sorts after the songs, so the queue's second row is the second song, which the tap below plays.
  const clip = await readFile(join(media, 'clip.webm'));
  await rawPut(base, `/api/v1/folders/${folder}/files?path=${encodeURIComponent('/zz-clip.webm')}`, accountKey, clip, 'video/webm');
  const folderURL = `${base}/files/${folder}`;

  await signOut();
  try {
    await signIn(OWNER);
    await page.goto(base + '/');
    await page.evaluate(`localStorage.setItem('selfcloud.lang', 'en'); localStorage.setItem('selfcloud.theme', 'light'); true`);
    await page.goto(base + '/');

    await step('the viewer at phone width is the album page: header, cover, seek, line, transport, actions, queue', async () => {
      await openFolder(page, folderURL, 'tag-long.mp3');
      await dblClick(page, await rowPoint(page, 'tag-long.mp3'));
      await until(page, viewerShown, 15000, 'the viewer opens');
      await playing(page);
      await mark(page, '[data-testid="media-screen"] audio');
      await setWidth(page, 390, true);
      await until(page, `!!document.querySelector(${q(phoneHead)})`, 10000, 'the phone header');
      // The header names the album from the tags, with the artist under it, and Share for the owner who may share.
      await until(page, `document.querySelector(${q(phoneHead)}).textContent.includes('Album X')`, 10000, 'the album in the header');
      check(await page.evaluate(`document.querySelector(${q(phoneHead)}).textContent.includes('by Artist One')`), 'the header says the artist under the album');
      check(await page.evaluate(`!!document.querySelector(${q(phoneHead)} + ' button[aria-label="Share"]')`), 'Share shows for the owner');
      check(await page.evaluate(`!!document.querySelector(${q(phoneHead)} + ' button[aria-label="Close"]')`), 'the viewer keeps its Close at the start');
      // The cover is a full-width square; the bar's own transport is not drawn; the seek row is.
      const cover = await page.evaluate(`(() => { const c = document.querySelector('[data-testid="media-screen"] .media-cover-box'); if (!c) return null; const r = c.getBoundingClientRect(); return { w: r.width, h: r.height, vw: window.innerWidth }; })()`);
      check(!!cover && Math.abs(cover.w - cover.h) <= 2 && cover.w >= cover.vw - 24, `the cover is a full-width square (${JSON.stringify(cover)})`);
      check(await page.evaluate(`!document.querySelector(${q(barButtons)})`), "the bar's own transport is not drawn at phone width");
      check(await page.evaluate(`!!document.querySelector('[data-testid="media-controls"] .media-seek-row')`), 'the seek row stays, full width');
      // The line is the place, the artist and the tag title.
      const line = await page.evaluate(`document.querySelector('[data-testid="media-phone-line"]').textContent`);
      check(/^\d+\. Artist One - /.test(line), `the line is the place, artist and title (${line})`);
      // The transport: play 64 px, the steps 48 px, each at least 44.
      const sizes = await page.evaluate(`(() => {
        const play = document.querySelector('[data-testid="media-phone-play"]');
        const prev = document.querySelector('[data-testid="media-phone-prev"]');
        const next = document.querySelector('[data-testid="media-phone-next"]');
        const box = (b) => { const r = b.getBoundingClientRect(); return { w: r.width, h: r.height }; };
        return { play: box(play), prev: box(prev), next: box(next) };
      })()`);
      check(Math.round(sizes.play.w) === 64 && Math.round(sizes.play.h) === 64, `the play button is 64 px (${JSON.stringify(sizes.play)})`);
      check(Math.round(sizes.prev.w) === 48 && Math.round(sizes.next.w) === 48 && sizes.prev.h >= 44 && sizes.next.h >= 44, `the steps are 48 px, at least 44 tall (${JSON.stringify(sizes)})`);
      // The action row: Playlist, Download (the owner may download), More.
      for (const name of ['Playlist', 'Download', 'More']) {
        check(await page.evaluate(`!![...document.querySelectorAll('[data-testid="media-phone-actions"] button')].find((b) => b.textContent.trim() === ${q(name)})`), `the action row has ${name}`);
      }
      // The queue is the table in the flow below the player, not an inert drawer.
      await until(page, `!!document.querySelector('[data-testid="media-screen"] .media-drawer [data-testid="media-queue-table"]')`, 15000, 'the queue table in the viewer');
      check(await page.evaluate(`(() => { const d = document.querySelector('[data-testid="media-screen"] .media-drawer'); return !!d && !d.hasAttribute('inert') && !d.classList.contains('media-drawer--closed'); })()`), 'the queue is in view, not a closed drawer');
      check(await page.evaluate(`!document.querySelector('[data-testid="media-screen"] .media-drawer [data-testid="media-queue"]')`), 'the drawer panel is not drawn at phone width');
      check(await page.evaluate(`getComputedStyle(document.querySelector('[data-testid="media-screen"] .media-drawer')).position === 'static'`), 'the queue sits in the flow');
      // No sideways scroll at 320, 390 and 599.
      for (const w of [320, 390, 599]) {
        await setWidth(page, w, true);
        const over = await page.evaluate(overflow);
        check(over <= 0, `no horizontal overflow at ${w}px (overflow ${over}px)`);
      }
      await setWidth(page, 390, true);
    });

    await step('the same element plays across the width change, and the bar returns over 599 px', async () => {
      const plays = await page.evaluate(`window.__mp7el.currentTime`);
      await setWidth(page, 1280, false);
      await until(page, `!document.querySelector(${q(phoneHead)})`, 10000, 'the phone header goes over 599 px');
      check(await page.evaluate(`!!document.querySelector(${q(barButtons)})`), "the bar's own transport is back on desktop");
      check(await page.evaluate(`!!document.querySelector('[data-testid="media-screen"] .media-drawer [data-testid="media-queue"]')`), 'the drawer panel is back on desktop');
      check(await page.evaluate(`document.querySelector('[data-testid="media-screen"] audio') === window.__mp7el && window.__mp7el.__mp7 === 1`), 'the same marked element on desktop');
      await setWidth(page, 390, true);
      await until(page, `!!document.querySelector(${q(phoneHead)})`, 10000, 'the phone header returns');
      check(await page.evaluate(`document.querySelector('[data-testid="media-screen"] audio') === window.__mp7el && window.__mp7el.__mp7 === 1 && window.__mp7el.isConnected`), 'the same marked element back at phone width');
      check(await page.evaluate(`document.querySelectorAll('audio').length === 1`), 'one audio element');
      const now = await page.evaluate(`window.__mp7el.currentTime`);
      check(now > plays, `the place went on across the resize (${plays.toFixed(2)} s, then ${now.toFixed(2)} s)`);
    });

    await step('the phone transport drives the one element, and a queue row plays through it', async () => {
      // Pause and play through the large transport.
      await page.evaluate(`document.querySelector('[data-testid="media-phone-play"]').click()`);
      await until(page, `window.__mp7el.paused`, 5000, 'the transport pauses');
      await page.evaluate(`document.querySelector('[data-testid="media-phone-play"]').click()`);
      await until(page, `!window.__mp7el.paused`, 5000, 'the transport plays');
      check(await page.evaluate(`document.querySelector('[data-testid="media-screen"] audio') === window.__mp7el`), 'still the one element');
      // A tap on the queue's second row plays it through the session's one player. The rows are the folder's name order,
      // so the second is track-one.ogg, not the tagged file that plays now.
      const second = await page.evaluate(`(() => {
        const rows = document.querySelectorAll('[data-testid="media-screen"] .media-drawer [data-testid="media-qrow"]');
        const row = rows[1];
        if (!row) return null;
        row.scrollIntoView({ block: 'center' });
        const r = (row.querySelector('bdi') ?? row).getBoundingClientRect();
        return { x: r.left + r.width / 2, y: r.top + r.height / 2, name: row.getAttribute('aria-label') };
      })()`);
      check(!!second && second.name === 'track-one.ogg', `the queue's second row is track-one (${second && second.name})`);
      await clickAt(page, second);
      await until(page, `(() => { const v = ${MEDIA}; return !!v && v.getAttribute('aria-label') === 'track-one.ogg' && v.readyState >= 2; })()`, 20000, 'row 2 has data');
      await playing(page);
      check(await page.evaluate(`document.querySelectorAll('audio').length === 1`), 'one player plays row 2');
      await mark(page, '[data-testid="media-screen"] audio');
    });

    await step('the More menu holds Speed, Shuffle, Repeat, Details and Open folder', async () => {
      await page.evaluate(`document.querySelector('[data-testid="media-phone-more"]').click()`);
      // The rows below are read, so the menu must be open and still before they are (the player's phone check waits the same).
      await until(page, MORE_OPEN_STILL, 5000, 'the More menu');
      for (const name of ['Shuffle', 'Details', 'Open folder']) {
        check(await page.evaluate(`!![...document.querySelectorAll('[role="menuitem"], [role="menuitemcheckbox"]')].find((x) => x.textContent.trim() === ${q(name)} && !x.closest('.ant-dropdown-hidden'))`), `More has ${name}`);
      }
      check(await page.evaluate(`!![...document.querySelectorAll('[role="menuitem"]')].find((x) => x.textContent.trim().startsWith('Repeat') && !x.closest('.ant-dropdown-hidden'))`), 'More has Repeat');
      // Speed opens its rates and sets the element's.
      await page.evaluate(`[...document.querySelectorAll('[role="menuitem"]')].find((x) => x.textContent.trim().startsWith('Speed') && !x.closest('.ant-dropdown-hidden')).click()`);
      await until(page, `!![...document.querySelectorAll('[role="menuitemradio"]')].find((x) => x.textContent.trim() === '1.5×' && !x.closest('.ant-dropdown-hidden, .ant-dropdown-menu-submenu-hidden'))`, 5000, 'the speed rates');
      await page.evaluate(`[...document.querySelectorAll('[role="menuitemradio"]')].find((x) => x.textContent.trim() === '1.5×' && !x.closest('.ant-dropdown-hidden, .ant-dropdown-menu-submenu-hidden')).click()`);
      await until(page, `window.__mp7el.playbackRate === 1.5`, 5000, 'the rate is set');
      check(await page.evaluate(`window.__mp7el.playbackRate === 1.5`), 'Speed sets the element rate');
      await page.evaluate(`document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', keyCode: 27, bubbles: true })), true`);
    });

    await step('the Playlist action opens the saved-playlists flow', async () => {
      await page.evaluate(`document.querySelector('[data-testid="media-phone-playlist"]').click()`);
      await until(page, `document.body.innerText.includes('Saved playlists') && !![...document.querySelectorAll('button')].find((b) => b.getAttribute('aria-label') === 'Save queue')`, 10000, 'the saved-playlists dialog');
      check(await page.evaluate(`document.body.innerText.includes('Saved playlists')`), 'the dialog names the saved playlists');
      await page.evaluate(`document.querySelector('.ant-modal-close')?.click()`);
      await until(page, `!document.body.innerText.includes('Saved playlists')`, 5000, 'the dialog closes');
    });

    await step('the Media page at phone width has no back, the tab bar above, and the queue in its host', async () => {
      await closeViewer(page);
      // The sidebar's Media entry is reached at desktop width, as a person widens the window to use it; the page itself is
      // judged at phone width, and the one element must survive the move and the narrowing both.
      await setWidth(page, 1280, false);
      await h.clickNav(page, 'Media');
      await until(page, `location.pathname === '/media'`, 10000, 'the Media page');
      await until(page, `!!document.querySelector('[data-testid="media-page-stage"] audio')`, 15000, 'the player on the page stage');
      check(await page.evaluate(`document.querySelector('[data-testid="media-page-stage"] audio') === window.__mp7el && window.__mp7el.__mp7 === 1`), 'the same marked element moved to the page');
      await setWidth(page, 390, true);
      await until(page, `!!document.querySelector(${q(phoneHead)})`, 10000, 'the phone header on the page');
      check(await page.evaluate(`!document.querySelector(${q(phoneHead)} + ' button[aria-label="Close"]') && !document.querySelector(${q(phoneHead)} + ' button[aria-label="Stop and close"]')`), 'the page header has no back');
      check(await page.evaluate(`!![...document.querySelectorAll('[role="tab"]')].find((t) => t.textContent.trim() === 'Now playing')`), 'the tab bar stays above');
      await until(page, `!!document.querySelector('[data-testid="media-page-queue"].media-page-queue--phone [data-testid="media-queue-table"]')`, 15000, 'the queue table in the page host');
      check(await page.evaluate(`!!document.querySelector('[data-testid="media-page-queue"].media-page-queue--phone')`), 'the page queue host takes the phone divider');
      check(await page.evaluate(`!document.querySelector('[data-testid="media-mini"]') && !document.querySelector('[data-testid="preview"]')`), 'no mini player and no dialog beside the page');
      const over = await page.evaluate(overflow);
      check(over <= 0, `the Media page fits the phone (overflow ${over}px)`);
    });

    await step('a video at phone width keeps the player it had: no album page, and the bar hides while it plays', async () => {
      // End the audio session, then open the video from the folder. The page's Stop and close is the desktop header's X;
      // the phone header has no back, so widen first to reach it.
      await setWidth(page, 1280, false);
      await until(page, `!document.querySelector(${q(phoneHead)})`, 10000, 'the phone header goes before the stop');
      await page.evaluate(`[...document.querySelectorAll('[data-testid="media-page-stage"] button')].find((b) => b.getAttribute('aria-label') === 'Stop and close')?.click()`);
      await until(page, `!document.querySelector('audio')`, 10000, 'the audio session ends');
      await openFolder(page, folderURL, 'zz-clip.webm');
      await dblClick(page, await rowPoint(page, 'zz-clip.webm'));
      await until(page, viewerShown, 15000, 'the viewer opens');
      await until(page, `!!document.querySelector('[data-testid="media-screen"] video')`, 15000, 'the video element');
      await playing(page);
      await mark(page, '[data-testid="media-screen"] video');
      // At phone width the video is not the album page: no album header and no album part, and the screen keeps its wide
      // layout. The width change must not make the element again.
      await setWidth(page, 390, true);
      check(await page.evaluate(`!document.querySelector(${q(phoneHead)})`), 'a video at phone width has no album header');
      check(await page.evaluate(`!document.querySelector('[data-testid="media-phone-line"]') && !document.querySelector('[data-testid="media-phone-transport"]') && !document.querySelector('[data-testid="media-phone-actions"]')`), 'a video at phone width draws no album part');
      check(await page.evaluate(`!document.querySelector('[data-testid="media-screen"]').classList.contains('media-screen--phone')`), 'the screen is not the album layout for a video');
      check(await page.evaluate(`!document.querySelector('.media-box--album')`), 'the album class is not on the box for a video');
      // The old header: the count and the Playlist toggle; the bar keeps its own transport; the queue is the drawer panel.
      check(await page.evaluate(`!!document.querySelector('[data-testid="media-count"]')`), 'the video keeps the count header');
      check(await page.evaluate(`!!document.querySelector('.media-playlist-toggle')`), 'the video keeps the Playlist toggle');
      check(await page.evaluate(`!!document.querySelector(${q(barButtons)})`), "the video's bar keeps its own transport at phone width");
      check(await page.evaluate(`!!document.querySelector('[data-testid="media-screen"] .media-drawer [data-testid="media-queue"]')`), 'the queue is the drawer panel for a video');
      check(await page.evaluate(`!document.querySelector('[data-testid="media-queue-table"]')`), 'the album queue table is not drawn for a video');
      // The bar hides while the video plays, and a tap on the picture shows it again.
      await until(page, `document.querySelector('[data-testid="media-controls"]').classList.contains('media-controls--hidden')`, 6000, 'the bar hides while the video plays');
      await page.evaluate(`document.querySelector('[data-testid="media-stage"]').dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, cancelable: true }))`);
      await until(page, `!document.querySelector('[data-testid="media-controls"]').classList.contains('media-controls--hidden')`, 5000, 'a tap on the picture shows the bar');
      check(await page.evaluate(`document.querySelector('[data-testid="media-screen"] video') === window.__mp7el && window.__mp7el.__mp7 === 1`), 'the same marked video element');
      // No album header and no sideways scroll at 375 and 390 px.
      for (const w of [375, 390]) {
        await setWidth(page, w, true);
        check(await page.evaluate(`!document.querySelector(${q(phoneHead)})`), `no album header for the video at ${w}px`);
        const over = await page.evaluate(overflow);
        check(over <= 0, `the video player fits the phone at ${w}px (overflow ${over}px)`);
      }
      // Over 599 px the video is unchanged, the same one element.
      await setWidth(page, 1280, false);
      check(await page.evaluate(`document.querySelector('[data-testid="media-screen"] video') === window.__mp7el && window.__mp7el.__mp7 === 1`), 'the same video on desktop');
      check(await page.evaluate(`document.querySelectorAll('video').length === 1`), 'one video element');
      await closeViewer(page);
      await page.evaluate(`[...document.querySelectorAll('[data-testid="media-mini"] button')].find((b) => b.getAttribute('aria-label') === 'Stop and close')?.click()`);
      await until(page, `!document.querySelector('video')`, 10000, 'the video session ends');
    });
  } finally {
    await setWidth(page, 1280, false);
    const restore = [['selfcloud.lang', kept.lang], ['selfcloud.theme', kept.theme], ['selfcloud.files', kept.files]];
    await page.evaluate(`(() => { for (const [k, v] of ${q(restore)}) { if (v === null) localStorage.removeItem(k); else localStorage.setItem(k, v); } return true; })()`);
  }
  await page.goto(base + '/');
  await until(page, `!!document.querySelector('[data-testid="account-menu"]') || location.pathname === '/login'`, 15000, 'the shell after the phone size');
  await signOut();
  await signIn(MEMBER);
  console.log('mp7 checks passed');
}