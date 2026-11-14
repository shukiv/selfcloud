// SPDX-License-Identifier: AGPL-3.0-or-later

// The Media page checks (slice mp1, decision 172), run inside the shared end-to-end run (run.mjs) on the shared page,
// server and accounts: the sidebar's Media entry right after Starred, the page's empty state with its way back to My
// files, a file played from My files that goes on in the mini player and then on the Media page — the same element,
// marked, never made again — with the queue panel below it and no mini player beside it, the mini player back on My
// files, the page's Stop and close that ends the session, and the page fitting a phone with no sideways scroll. Run it
// alone with SELFCLOUD_E2E_ONLY=mp1. The checks end signed in as the member, which the rest of the run expects; the
// interface's language and theme and the listing's view are put back afterwards.
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const media = fileURLToPath(new URL('./media/', import.meta.url));
const PASSWORD = 'correct horse battery staple';
// The same people as the shared run's owner and member (run.mjs), who sign in with these.
const OWNER = { email: 'olivia@example.com', name: 'Olivia Owner', password: PASSWORD };
const MEMBER = { email: 'mateo@example.com', name: 'Mateo Member', password: PASSWORD };
const q = (v) => JSON.stringify(v);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function step(name, fn) {
  console.log(`-- ${name}`);
  await fn();
}

// until polls a page expression with a bound, and returns when it is true.
async function until(page, expr, ms = 15000, what = expr) {
  const deadline = Date.now() + ms;
  for (;;) {
    if (await page.evaluate(`!!(${expr})`).catch(() => false)) return;
    if (Date.now() > deadline) {
      const st = await page.evaluate(`JSON.stringify({ path: location.pathname, text: document.body.innerText.slice(0, 300) })`).catch((e) => `state-unavailable ${e}`);
      const unended = [...(page.inflight?.values() ?? [])].map((r) => `${r.url.startsWith(page.base) ? r.url.slice(page.base.length) : r.url.slice(0, 80)} status ${r.status} for ${Date.now() - r.at} ms`).join(' ; ') || 'none';
      throw new Error(`timed out (${ms} ms) waiting for: ${what} | DIAG ${st} | requests not ended: ${unended}`);
    }
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

// openFolder shows a folder's listing in List view, with nothing selected.
async function openFolder(page, url, firstName) {
  await page.goto(url);
  await page.evaluate(`localStorage.setItem('selfcloud.files', JSON.stringify({ view: 'list', sort: { key: 'name', dir: 'asc' } }))`);
  await page.goto(url);
  await until(page, `!!document.querySelector('button[data-view]')`, 15000, 'the view toggle');
  await page.evaluate(`(() => { const b = document.querySelector('button[data-view]'); if (b.dataset.view === 'list') b.click(); })()`);
  await until(page, `!!document.querySelector('[role="row"][aria-label=${JSON.stringify(firstName)}]')`, 15000, `row ${firstName}`);
}

// The element the session plays, wherever its box is: the viewer's screen, the page's stage or the hidden viewer.
const MEDIA = `document.querySelector('[data-testid="media-screen"] video, [data-testid="media-screen"] audio')`;

async function playing(page, ms = 20000) {
  await until(page, `(() => { const v = ${MEDIA}; return !!v && v.readyState >= 2; })()`, ms, 'the file has data');
  await until(page, `(() => { const v = ${MEDIA}; return !!v && !v.paused && v.currentTime > 0.2; })()`, 15000, 'the file plays');
}

// viewerShown says the viewer's dialog is on screen (its test id is only borne while it is the dialog).
const viewerShown = `(() => { const v = document.querySelector('[data-testid="preview"]'); return !!v && getComputedStyle(v).display !== 'none'; })()`;

async function closeViewer(page) {
  await page.evaluate(`document.querySelector('[data-testid="preview"] header button').click()`);
  await until(page, `!(${viewerShown})`, 10000, 'the viewer closes');
}

// setPhone is the phone these checks measure: 390 by 844, a touch screen.
async function setPhone(page, on) {
  if (on) {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
  } else {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: false });
  }
}

// stopOnPage presses the page's Stop and close, the X of the session's header on the page's stage.
async function stopOnPage(page) {
  await page.evaluate(`[...document.querySelectorAll('[data-testid="media-page-stage"] button')].find((b) => b.getAttribute('aria-label') === 'Stop and close')?.click()`);
}

export async function mp1Checks(h, { page, base, accountKey, accountID }) {
  const { api, rawPut, check } = h;
  const signIn = (who) => h.signIn(page, who);
  const signOut = () => h.signOut(page);
  // The shared run's interface settings and listing view, put back when the checks end.
  const kept = await page.evaluate(`(() => ({ lang: localStorage.getItem('selfcloud.lang'), theme: localStorage.getItem('selfcloud.theme'), files: localStorage.getItem('selfcloud.files') }))()`);
  // The fixtures: a folder of the owner's own with two audio files, one of them played and marked.
  const folder = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'MP1 media' })).id;
  const tone = await readFile(join(media, 'tone.ogg'));
  for (const name of ['track-one.ogg', 'track-two.ogg']) {
    await rawPut(base, `/api/v1/folders/${folder}/files?path=${encodeURIComponent(`/${name}`)}`, accountKey, tone, 'audio/ogg');
  }
  const folderURL = `${base}/files/${folder}`;

  await signOut();
  try {
    await signIn(OWNER);
    // The words these checks read are the English ones; the theme is the light one.
    await page.goto(base + '/');
    await page.evaluate(`localStorage.setItem('selfcloud.lang', 'en'); localStorage.setItem('selfcloud.theme', 'light'); true`);
    await page.goto(base + '/');

    await step('Media sits right after Starred in the sidebar', async () => {
      await until(page, `!!document.querySelector('[data-testid="account-menu"]')`, 15000, 'the shell');
      const texts = await page.evaluate(`[...document.querySelectorAll('[role="menu"] [role="menuitem"]')].map((x) => x.textContent.trim())`);
      const i = texts.indexOf('Starred');
      check(i >= 0 && texts[i + 1] === 'Media', `Media is right after Starred (${texts.join(', ')})`);
    });

    await step('the Media page with no session offers the way back to My files', async () => {
      await h.clickNav(page, 'Media');
      await until(page, `location.pathname === '/media'`, 10000, 'the Media page');
      await until(page, `document.body.innerText.includes('Nothing is playing')`, 15000, 'the empty state');
      check(await page.evaluate(`document.body.innerText.includes('Play a video or a song from your files and it shows here.')`), 'the empty state says what to do');
      await page.evaluate(`[...document.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Go to My files').click()`);
      // My files opens at /files and settles on the root folder's own address.
      await until(page, `location.pathname === '/files' || location.pathname.startsWith('/files/')`, 10000, 'My files');
    });

    await step('a file played from the folder goes on in the mini player, marked', async () => {
      await openFolder(page, folderURL, 'track-one.ogg');
      await dblClick(page, await rowPoint(page, 'track-one.ogg'));
      await until(page, viewerShown, 15000, 'the viewer opens');
      await playing(page);
      await closeViewer(page);
      await until(page, `!!document.querySelector('[data-testid="media-mini"]')`, 10000, 'the mini player');
      await page.evaluate(`(() => { const el = document.querySelector('audio'); el.__mp1 = 1; window.__mp1el = el; return true; })()`);
    });

    await step('the Media page hosts the same element, with the queue below and no mini player', async () => {
      await h.clickNav(page, 'Media');
      await until(page, `location.pathname === '/media'`, 10000, 'the Media page');
      await until(page, `!!document.querySelector('[data-testid="media-page-stage"] audio')`, 15000, 'the player on the page stage');
      check(await page.evaluate(`document.querySelector('[data-testid="media-page-stage"] audio') === window.__mp1el && window.__mp1el.__mp1 === 1`), 'the same marked element is on the page stage');
      check(await page.evaluate(`!!document.querySelector('[data-testid="media-page-queue"] [data-testid="media-queue"]')`), 'the queue panel is in the page queue host');
      check(await page.evaluate(`!document.querySelector('[data-testid="media-mini"]') && !document.querySelector('[data-testid="preview"]')`), 'no mini player and no dialog beside the page');
      check(await page.evaluate(`!window.__mp1el.paused`), 'the file plays on');
      const t1 = await page.evaluate(`window.__mp1el.currentTime`);
      await sleep(1200);
      const t2 = await page.evaluate(`window.__mp1el.currentTime`);
      check(t2 > t1 + 0.5, `the place goes on (${t1.toFixed(2)} s, then ${t2.toFixed(2)} s), not restarted`);
    });

    await step('My files brings the mini player back with the same element', async () => {
      await h.clickNav(page, 'My files');
      await until(page, `location.pathname === '/files' || location.pathname.startsWith('/files/')`, 10000, 'My files');
      await until(page, `!!document.querySelector('[data-testid="media-mini"]')`, 10000, 'the mini player');
      check(await page.evaluate(`document.querySelector('audio') === window.__mp1el && window.__mp1el.__mp1 === 1 && !window.__mp1el.paused`), 'the same marked element plays on in the mini player');
    });

    await step("the page's Stop and close ends the session and shows the empty state", async () => {
      await h.clickNav(page, 'Media');
      await until(page, `!!document.querySelector('[data-testid="media-page-stage"] audio')`, 15000, 'the player on the page');
      await stopOnPage(page);
      await until(page, `!document.querySelector('audio') && document.body.innerText.includes('Nothing is playing')`, 10000, 'the session ends and the empty state shows');
      check(await page.evaluate(`!document.querySelector('[data-testid="media-mini"]')`), 'no mini player is left');
    });

    await step('the Media page fits a phone with no sideways scroll', async () => {
      await openFolder(page, folderURL, 'track-one.ogg');
      await dblClick(page, await rowPoint(page, 'track-one.ogg'));
      await until(page, viewerShown, 15000, 'the viewer opens');
      await playing(page);
      await closeViewer(page);
      await h.clickNav(page, 'Media');
      await until(page, `!!document.querySelector('[data-testid="media-page-stage"] audio')`, 15000, 'the player on the page');
      await setPhone(page, true);
      await sleep(400);
      const over = await page.evaluate(`Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) - window.innerWidth`);
      check(over <= 0, `no horizontal overflow at 390x844 (overflow ${over}px)`);
      await stopOnPage(page);
      await until(page, `!document.querySelector('audio')`, 10000, 'the session ends');
    });
  } finally {
    await setPhone(page, false);
    const restore = [['selfcloud.lang', kept.lang], ['selfcloud.theme', kept.theme], ['selfcloud.files', kept.files]];
    await page.evaluate(`(() => { for (const [k, v] of ${q(restore)}) { if (v === null) localStorage.removeItem(k); else localStorage.setItem(k, v); } return true; })()`);
  }
  // The phone size was just left: a fresh load of the shell lets the account menu open at the desktop size.
  await page.goto(base + '/');
  await until(page, `!!document.querySelector('[data-testid="account-menu"]') || location.pathname === '/login'`, 15000, 'the shell after the phone size');
  await signOut();
  await signIn(MEMBER);
  console.log('mp1 checks passed');
}
