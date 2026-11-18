// SPDX-License-Identifier: AGPL-3.0-or-later

// Bookmarks in the media player (slice mp6b), run inside the shared end-to-end run (run.mjs) on the shared page, server and
// accounts. A signed-in person marks a place in the file that plays: More > Add a bookmark posts the place and a tick joins the
// seek bar; More > Bookmarks (n) opens the list; a row is renamed, survives a reload, and Go to lands back on its place; Delete
// empties the list; and at phone width the album page's More carries the same two items. Run it alone with SELFCLOUD_E2E_ONLY=mp6b.
// The checks end signed in as the member, which the rest of the run expects; the interface's language and theme and the listing's
// view are put back afterwards.
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { MORE_OPEN_STILL } from './mp7.mjs';

const media = fileURLToPath(new URL('./media/', import.meta.url));
const PASSWORD = 'correct horse battery staple';
const OWNER = { email: 'olivia@example.com', name: 'Olivia Owner', password: PASSWORD };
const MEMBER = { email: 'mateo@example.com', name: 'Mateo Member', password: PASSWORD };
const q = (v) => JSON.stringify(v);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

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

async function setWidth(page, width, phone) {
  await page.send('Emulation.setDeviceMetricsOverride', { width, height: phone ? 844 : 800, deviceScaleFactor: 1, mobile: phone });
  await page.send('Emulation.setTouchEmulationEnabled', phone ? { enabled: true, maxTouchPoints: 1 } : { enabled: false });
  await sleep(350);
}

// moreNode names a visible desktop More-menu entry by its text (mp1's helper): not inside a hidden dropdown or submenu.
const moreNode = (text) => `[...document.querySelectorAll('[role="menuitem"]')].find((x) => x.textContent.trim() === ${q(text)} && !x.closest('.ant-dropdown-hidden, .ant-dropdown-menu-submenu-hidden'))`;

async function openMore(page) {
  await page.evaluate(`document.querySelector('[data-testid="media-controls"] button[aria-label="More options"]').click()`);
  await until(page, `!!(${moreNode('A-B loop')})`, 5000, 'the More menu');
}

async function pickMore(page, text) {
  await until(page, `!!(${moreNode(text)})`, 5000, `the menu item ${text}`);
  await page.evaluate(`(${moreNode(text)}).click()`);
}

const TICKS = `document.querySelectorAll('[data-testid="media-bookmark-tick"]').length`;
const ROWS = `document.querySelectorAll('[data-testid="bookmark-row"]').length`;
const modalOpen = `!!document.querySelector('.ant-modal')`;

async function closeDialog(page) {
  await page.evaluate(`[...document.querySelectorAll('.ant-modal button')].find((b) => b.textContent.trim() === 'Close')?.click()`);
  await until(page, `!(${modalOpen})`, 5000, 'the dialog closes');
}

export async function mp6bChecks(h, { page, base, accountKey, accountID }) {
  const { api, rawPut, check } = h;
  const signIn = (who) => h.signIn(page, who);
  const signOut = () => h.signOut(page);
  const kept = await page.evaluate(`(() => ({ lang: localStorage.getItem('selfcloud.lang'), theme: localStorage.getItem('selfcloud.theme'), files: localStorage.getItem('selfcloud.files') }))()`);
  // The fixture: a folder of the owner's own with one tagged song that runs half a minute, so it keeps playing through the checks.
  const folder = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'MP6B media' })).id;
  const tagged = await readFile(join(media, 'tag-long.mp3'));
  await rawPut(base, `/api/v1/folders/${folder}/files?path=${encodeURIComponent('/tag-long.mp3')}`, accountKey, tagged, 'audio/mpeg');
  const folderURL = `${base}/files/${folder}`;

  await signOut();
  try {
    await signIn(OWNER);
    await page.goto(base + '/');
    await page.evaluate(`localStorage.setItem('selfcloud.lang', 'en'); localStorage.setItem('selfcloud.theme', 'light'); true`);
    await page.goto(base + '/');

    await openFolder(page, folderURL, 'tag-long.mp3');
    await dblClick(page, await rowPoint(page, 'tag-long.mp3'));
    await until(page, viewerShown, 15000, 'the viewer opens');
    await playing(page);
    await setWidth(page, 1280, false);

    let place = 0;
    await step('More > Add a bookmark shows the message and one tick on the seek bar', async () => {
      await until(page, `(() => { const v = ${MEDIA}; return !!v && v.currentTime > 3; })()`, 15000, 'the file plays on a little');
      await page.evaluate(`${MEDIA}.pause()`);
      await until(page, `${MEDIA}.paused`, 5000, 'the file pauses');
      place = Math.round((await page.evaluate(`${MEDIA}.currentTime`)) * 10) / 10;
      await openMore(page);
      await pickMore(page, 'Add a bookmark');
      await until(page, `!!document.querySelector('.ant-message-success')`, 5000, 'the added message');
      check(await page.evaluate(`document.querySelector('.ant-message-success').textContent.includes('Bookmark added at')`), 'the message names the place');
      await until(page, `${TICKS} === 1`, 5000, 'one tick');
      check(await page.evaluate(`${TICKS} === 1`), 'one tick on the seek bar');
    });

    await step('More > Bookmarks (1) opens the dialog with one row', async () => {
      await openMore(page);
      await pickMore(page, 'Bookmarks (1)');
      await until(page, `${modalOpen} && ${ROWS} === 1`, 5000, 'the dialog with one row');
      check(await page.evaluate(`${ROWS} === 1`), 'the dialog lists the one bookmark');
    });

    await step('Rename to Intro shows in the row', async () => {
      await page.evaluate(`document.querySelector('[data-testid="bookmark-row"] button[aria-label="Rename"]').click()`);
      await until(page, `!!document.querySelector('[data-testid="bookmark-rename"]')`, 5000, 'the rename input');
      await page.setInput('[data-testid="bookmark-rename"]', 'Intro');
      await page.evaluate(`document.querySelector('[data-testid="bookmark-rename"]').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', keyCode: 13, bubbles: true }))`);
      await until(page, `document.querySelector('[data-testid="bookmark-row"]').textContent.includes('Intro')`, 5000, 'the row shows Intro');
      check(await page.evaluate(`document.querySelector('[data-testid="bookmark-row"]').textContent.includes('Intro')`), 'the row shows the new label');
      await closeDialog(page);
    });

    await step('a reload and reopen keeps the row', async () => {
      await openFolder(page, folderURL, 'tag-long.mp3');
      await dblClick(page, await rowPoint(page, 'tag-long.mp3'));
      await until(page, viewerShown, 15000, 'the viewer reopens');
      await playing(page);
      await openMore(page);
      await pickMore(page, 'Bookmarks (1)');
      await until(page, `${modalOpen} && ${ROWS} === 1 && document.querySelector('[data-testid="bookmark-row"]').textContent.includes('Intro')`, 5000, 'the persisted row');
      check(await page.evaluate(`document.querySelector('[data-testid="bookmark-row"]').textContent.includes('Intro')`), 'the bookmark survived the reload');
    });

    await step('Go to after playing on lands within a second of the place and closes the dialog', async () => {
      await page.evaluate(`${MEDIA}.play()`);
      await until(page, `(() => { const v = ${MEDIA}; return !!v && v.currentTime > ${place} + 2; })()`, 15000, 'the file plays on past the place');
      await page.evaluate(`document.querySelector('[data-testid="bookmark-row"] button[aria-label="Go to"]').click()`);
      await until(page, `Math.abs(${MEDIA}.currentTime - ${place}) < 1`, 5000, 'back at the place');
      check(await page.evaluate(`Math.abs(${MEDIA}.currentTime - ${place}) < 1`), 'Go to lands within a second of the bookmark');
      check(await page.evaluate(`!(${modalOpen})`), 'Go to closes the dialog');
    });

    await step('Delete leaves the empty state', async () => {
      await openMore(page);
      await pickMore(page, 'Bookmarks (1)');
      await until(page, `${modalOpen} && ${ROWS} === 1`, 5000, 'the dialog with the row');
      await page.evaluate(`document.querySelector('[data-testid="bookmark-row"] button[aria-label="Delete"]').click()`);
      await until(page, `document.body.innerText.includes('No bookmarks yet')`, 5000, 'the empty state');
      check(await page.evaluate(`document.body.innerText.includes('No bookmarks yet') && ${ROWS} === 0`), 'Delete empties the list');
      await closeDialog(page);
    });

    await step('at phone width the album page More has both bookmark items', async () => {
      await setWidth(page, 390, true);
      await until(page, `!!document.querySelector('[data-testid="media-phone-more"]')`, 10000, 'the phone More');
      await page.evaluate(`document.querySelector('[data-testid="media-phone-more"]').click()`);
      await until(page, MORE_OPEN_STILL, 5000, 'the phone More menu');
      check(await page.evaluate(`!![...document.querySelectorAll('[role="menuitem"]')].find((x) => x.textContent.trim() === 'Add a bookmark' && !x.closest('.ant-dropdown-hidden'))`), 'the phone More has Add a bookmark');
      check(await page.evaluate(`!![...document.querySelectorAll('[role="menuitem"]')].find((x) => x.textContent.trim() === 'Bookmarks (0)' && !x.closest('.ant-dropdown-hidden'))`), 'the phone More has Bookmarks (0)');
      await page.evaluate(`document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', keyCode: 27, bubbles: true })), true`);
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
  console.log('mp6b checks passed');
}
