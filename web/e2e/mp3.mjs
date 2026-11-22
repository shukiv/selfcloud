// SPDX-License-Identifier: AGPL-3.0-or-later

// The Media page's Playlists tab (slice mp3), run inside the shared end-to-end run (run.mjs) on the shared page, server and
// accounts, as the member: the tab sits right after Now playing; the automatic Continue watching lists a file stopped part
// way with the place and the time left, and plays it through the page's one session; Recently added, the second automatic
// list, holds the uploaded file at list=recent; a saved playlist's detail plays, is renamed and deleted through the page
// (the delete asks first), and an empty detail falls back to Continue watching; the two tables fit a phone with no sideways
// scroll. Run it alone with SELFCLOUD_E2E_ONLY=mp3. The checks end signed in as the member, which the rest of the run
// expects; the interface's language and theme and the listing's view are put back afterwards.
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const media = fileURLToPath(new URL('./media/', import.meta.url));
const PASSWORD = 'correct horse battery staple';
// The same member as the shared run (run.mjs), who signs in with this.
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
      throw new Error(`timed out (${ms} ms) waiting for: ${what} | DIAG ${st}`);
    }
    await sleep(100);
  }
}

// apiIn calls the signed-in person's own API as the page does, with the session's CSRF token, and returns the status and JSON.
function apiIn(page, method, path, body) {
  return page.evaluate(`(async () => {
    const me = await fetch('/api/v1/me', { credentials: 'same-origin', headers: { Accept: 'application/json' } }).then((x) => x.json());
    const headers = { Accept: 'application/json', 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrf_token ?? '' };
    const r = await fetch(${q(path)}, { method: ${q(method)}, credentials: 'same-origin', headers, body: ${body === undefined ? 'undefined' : q(JSON.stringify(body))} });
    return { status: r.status, body: r.status === 204 ? null : await r.json().catch(() => null), pf: me.personal_folder_id };
  })()`);
}

// putAudioIn uploads the bytes as the signed-in member, with the session's CSRF token, the way the page's own upload does.
function putAudioIn(page, path, b64) {
  return page.evaluate(`(async () => {
    const me = await fetch('/api/v1/me', { credentials: 'same-origin', headers: { Accept: 'application/json' } }).then((x) => x.json());
    const bin = Uint8Array.from(atob(${q(b64)}), (c) => c.charCodeAt(0));
    const r = await fetch(${q(path)}, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'audio/ogg', 'X-CSRF-Token': me.csrf_token ?? '' }, body: bin });
    return { status: r.status };
  })()`);
}

// The element the session plays, wherever its box is: the page's stage or the hidden viewer.
const MEDIA = `document.querySelector('[data-testid="media-page-stage"] video, [data-testid="media-page-stage"] audio, [data-testid="media-screen"] video, [data-testid="media-screen"] audio')`;

async function playing(page, ms = 20000) {
  await until(page, `(() => { const v = ${MEDIA}; return !!v && v.readyState >= 2; })()`, ms, 'the file has data');
  await until(page, `(() => { const v = ${MEDIA}; return !!v && !v.paused && v.currentTime > 0.2; })()`, 15000, 'the file plays');
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

// clickTab presses one of the Media page's tabs by its label.
async function clickTab(page, label) {
  await page.evaluate(`[...document.querySelectorAll('[role="tab"]')].find((t) => t.textContent.trim() === ${q(label)})?.click()`);
}

// rowSel names a row inside its region: the left table is "Your playlists", the detail is the chosen list's name.
const rowSel = (region, name) => `[role="region"][aria-label=${JSON.stringify(region)}] [role="row"][aria-label=${JSON.stringify(name)}]`;
async function clickRow(page, region, name) {
  await until(page, `!!document.querySelector(${q(rowSel(region, name))})`, 15000, `the row ${name} in ${region}`);
  await page.evaluate(`document.querySelector(${q(rowSel(region, name))})?.click()`);
}

// clickDetail presses one of the detail header's buttons by its text (Play, Shuffle and play, Rename, Delete). Picking a
// playlist reads its detail again, and until it is read the heading already shows the name while Rename and Delete are
// disabled, where a click does nothing; so it waits for the button to be enabled first.
async function clickDetail(page, text) {
  const button = `[...document.querySelectorAll('.media-pl-actions button')].find((b) => b.textContent.trim() === ${q(text)})`;
  await until(page, `(() => { const b = ${button}; return !!b && !b.disabled; })()`, 10000, `the ${text} button`);
  await page.evaluate(`${button}?.click()`);
}

// realClickEl sends a real mouse click at the element the expression names, the way Chrome sends it for a person. A play
// needs it: the browser only lets a file sound after a real gesture, and a scripted .click() is not one.
async function realClickEl(page, elExpr) {
  const p = await page.evaluate(`(() => { const el = ${elExpr}; if (!el) return null; el.scrollIntoView({ block: 'center' }); const r = el.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
  if (!p) throw new Error(`no element to click for: ${elExpr}`);
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: p.x, y: p.y });
  await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: p.x, y: p.y, button: 'left', buttons: 1, clickCount: 1 });
  await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: p.x, y: p.y, button: 'left', buttons: 0, clickCount: 1 });
}
const tabEl = (label) => `[...document.querySelectorAll('[role="tab"]')].find((t) => t.textContent.trim() === ${q(label)})`;
const detailButtonEl = (text) => `[...document.querySelectorAll('.media-pl-actions button')].find((b) => b.textContent.trim() === ${q(text)})`;

export async function mp3Checks(h, { page, base }) {
  const { check } = h;
  const signIn = (who) => h.signIn(page, who);
  const signOut = () => h.signOut(page);
  // The shared run's interface settings and listing view, put back when the checks end.
  const kept = await page.evaluate(`(() => ({ lang: localStorage.getItem('selfcloud.lang'), theme: localStorage.getItem('selfcloud.theme'), files: localStorage.getItem('selfcloud.files') }))()`);
  const stamp = Date.now().toString(36);
  const toneName = `mp3-tone-${stamp}.ogg`;
  const savedName = `MP3 saved ${stamp}`;
  const renamedName = `MP3 renamed ${stamp}`;
  let toneId = null;
  let playlistId = null;

  await signOut();
  try {
    await signIn(MEMBER);
    // The words these checks read are the English ones; the theme is the light one.
    await page.goto(base + '/');
    await page.evaluate(`localStorage.setItem('selfcloud.lang', 'en'); localStorage.setItem('selfcloud.theme', 'light'); true`);
    await page.goto(base + '/');
    await until(page, `!!document.querySelector('[data-testid="account-menu"]')`, 15000, 'the shell');

    // The fixtures, all the member's own: an audio file in My files, a place stopped part way in it, and a saved playlist
    // holding it. Continue watching reads the place directly, so no search index is waited for.
    const me = await apiIn(page, 'GET', '/api/v1/me');
    const pf = me.pf;
    check(!!pf, 'the member has a personal folder');
    const tone = await readFile(join(media, 'tone.ogg'));
    const up = await putAudioIn(page, `/api/v1/folders/${pf}/files?path=${encodeURIComponent('/' + toneName)}`, tone.toString('base64'));
    check(up.status === 201 || up.status === 200, `the member uploaded the audio (${up.status})`);
    const kids = await apiIn(page, 'GET', `/api/v1/folders/${pf}/children?path=%2F`);
    toneId = kids.body.items.find((n) => n.name === toneName)?.id ?? null;
    check(!!toneId, 'the uploaded audio is listed');
    // A place worth resuming: past the start rule and well before the end (6 s of a declared 30 s).
    const res = await apiIn(page, 'PUT', `/api/v1/nodes/${toneId}/resume`, { position_seconds: 6, duration_seconds: 30 });
    check(res.status === 204, `the place was saved (${res.status})`);
    const made = await apiIn(page, 'POST', '/api/v1/playlists', { name: savedName, nodes: [toneId] });
    playlistId = made.body?.id ?? null;
    check(!!playlistId, `the saved playlist was made (${made.status})`);

    await step('the Playlists tab sits right after Now playing', async () => {
      await h.clickNav(page, 'Media');
      await until(page, `location.pathname === '/media'`, 10000, 'the Media page');
      // The library tabs draw once the page's media catalogue is ready, a tick after the address turns.
      await until(page, `[...document.querySelectorAll('[role="tab"]')].some((t) => t.textContent.trim() === 'Playlists')`, 15000, 'the Playlists tab');
      const texts = await page.evaluate(`[...document.querySelectorAll('[role="tab"]')].map((t) => t.textContent.trim())`);
      const i = texts.indexOf('Now playing');
      check(i >= 0 && texts[i + 1] === 'Playlists', `Playlists is right after Now playing (${texts.join(', ')})`);
      // A real click opens the tab and gives the page the gesture a later play needs.
      await realClickEl(page, tabEl('Playlists'));
      await until(page, `location.search.includes('tab=playlists')`, 10000, 'the Playlists tab');
      await until(page, `!!document.querySelector('[data-testid="media-pl-layout"]')`, 15000, 'the two tables');
    });

    await step('Continue watching lists the stopped file with its place', async () => {
      // The left table names Continue watching first, tagged Automatic, then the saved playlist.
      await until(page, `!!document.querySelector(${q(rowSel('Your playlists', 'Continue watching'))})`, 15000, 'the Continue watching row');
      check(await page.evaluate(`(() => { const r = document.querySelector(${q(rowSel('Your playlists', 'Continue watching'))}); return !!r && r.textContent.includes('Automatic'); })()`), 'Continue watching is tagged Automatic');
      await until(page, `!!document.querySelector(${q(rowSel('Your playlists', savedName))})`, 15000, 'the saved playlist row');
      // The detail defaults to Continue watching and shows the file with the place stopped at and the time left.
      await until(page, `!!document.querySelector(${q(rowSel('Continue watching', toneName))})`, 15000, 'the stopped file in the detail');
      const txt = await page.evaluate(`document.querySelector(${q(rowSel('Continue watching', toneName))}).textContent`);
      check(txt.includes('At 0:06') && txt.includes('0:24 left'), `the place and the time left show (${txt})`);
      check(await page.evaluate(`document.querySelector('.media-pl-name').textContent.trim() === 'Continue watching'`), 'the detail is named Continue watching');
    });

    await step('playing from Continue watching runs the page’s one session on Now playing', async () => {
      await until(page, `!!document.querySelector(${q(rowSel('Continue watching', toneName))})`, 15000, 'the stopped file row');
      await realClickEl(page, `document.querySelector(${q(rowSel('Continue watching', toneName))})`);
      await until(page, `!!document.querySelector('[data-testid="media-page-stage"] audio')`, 15000, 'the player on the page stage');
      await until(page, `!location.search.includes('tab=')`, 10000, 'back to Now playing');
      await playing(page);
      check(await page.evaluate(`document.querySelectorAll('audio').length === 1`), 'one audio element on the page');
      check(await page.evaluate(`!document.querySelector('[data-testid="media-mini"]')`), 'no mini player beside the page');
      await stopOnPage(page);
      await until(page, `!document.querySelector('audio')`, 10000, 'the session ends');
    });

    await step('Recently added lists the uploaded file as the second automatic list', async () => {
      await clickTab(page, 'Playlists');
      await until(page, `location.search.includes('tab=playlists')`, 10000, 'the Playlists tab');
      await clickRow(page, 'Your playlists', 'Recently added');
      await until(page, `location.search.includes('list=recent')`, 10000, 'Recently added in the address');
      check(await page.evaluate(`[...document.querySelectorAll('.media-pl-head .ant-tag')].some((t) => t.textContent.trim() === 'Automatic playlist')`), 'the detail is tagged Automatic');
      // The search lists the file once the index holds it; the wait covers the indexing job.
      await until(page, `!!document.querySelector(${q(rowSel('Recently added', toneName))})`, 15000, 'the uploaded file in Recently added');
    });

    await step('the saved playlist’s detail plays its file', async () => {
      await clickTab(page, 'Playlists');
      await until(page, `location.search.includes('tab=playlists')`, 10000, 'the Playlists tab');
      await clickRow(page, 'Your playlists', savedName);
      await until(page, `location.search.includes(${q('list=' + playlistId)})`, 10000, 'the saved list in the address');
      await until(page, `!!document.querySelector(${q(rowSel(savedName, toneName))})`, 15000, 'the entry in the detail');
      check(await page.evaluate(`document.querySelector('.media-pl-name').textContent.trim() === ${q(savedName)}`), 'the detail is named for the playlist');
      check(await page.evaluate(`[...document.querySelectorAll('.media-pl-head .ant-tag')].some((t) => t.textContent.trim() === 'Playlist')`), 'the detail is tagged a saved Playlist');
      await realClickEl(page, detailButtonEl('Play'));
      await until(page, `!!document.querySelector('[data-testid="media-page-stage"] audio')`, 15000, 'the player on the page stage');
      await playing(page);
      check(await page.evaluate(`document.querySelectorAll('audio').length === 1`), 'one audio element on the page');
      await stopOnPage(page);
      await until(page, `!document.querySelector('audio')`, 10000, 'the session ends');
    });

    await step('the saved playlist is renamed through the page', async () => {
      await clickTab(page, 'Playlists');
      await until(page, `!!document.querySelector(${q(rowSel('Your playlists', savedName))})`, 15000, 'the saved playlist row');
      await clickRow(page, 'Your playlists', savedName);
      await until(page, `document.querySelector('.media-pl-name')?.textContent.trim() === ${q(savedName)}`, 10000, 'the saved detail');
      await clickDetail(page, 'Rename');
      await until(page, `!!document.querySelector('.ant-modal input')`, 10000, 'the rename dialog');
      check(await page.evaluate(`document.querySelector('.ant-modal-title')?.textContent.trim() === 'Rename playlist'`), 'the dialog is the rename');
      await page.setInput('.ant-modal input', renamedName);
      await page.evaluate(`[...document.querySelectorAll('.ant-modal button')].find((b) => b.textContent.trim() === 'Save')?.click()`);
      await until(page, `!!document.querySelector(${q(rowSel('Your playlists', renamedName))})`, 15000, 'the renamed row');
      check(await page.evaluate(`document.querySelector('.media-pl-name')?.textContent.trim() === ${q(renamedName)}`), 'the detail bears the new name');
    });

    await step('the saved playlist is deleted after asking, and the detail falls back', async () => {
      await clickDetail(page, 'Delete');
      await until(page, `!!document.querySelector('.ant-popover:not(.ant-popover-hidden)')`, 10000, 'the delete confirmation');
      check(await page.evaluate(`document.querySelector('.ant-popover:not(.ant-popover-hidden)').textContent.includes('Delete this playlist? The files stay')`), 'the confirmation says the files stay');
      await page.evaluate(`[...document.querySelectorAll('.ant-popover:not(.ant-popover-hidden) button')].find((b) => b.textContent.trim() === 'Delete')?.click()`);
      await until(page, `!document.querySelector(${q(rowSel('Your playlists', renamedName))})`, 15000, 'the saved row is gone');
      await until(page, `document.querySelector('.media-pl-name')?.textContent.trim() === 'Continue watching'`, 10000, 'the detail falls back to Continue watching');
      // The file itself is kept: the delete only removed the playlist.
      const still = await apiIn(page, 'GET', `/api/v1/nodes/${toneId}`);
      check(still.status === 200, `the file is kept after the playlist delete (${still.status})`);
      playlistId = null;
    });

    await step('the two tables fit a phone with no sideways scroll', async () => {
      await setPhone(page, true);
      await sleep(400);
      const over = await page.evaluate(`Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) - window.innerWidth`);
      check(over <= 0, `no horizontal overflow at 390x844 (overflow ${over}px)`);
      check(await page.evaluate(`document.querySelector('[data-testid="media-pl-layout"]').classList.contains('media-pl-layout--phone')`), 'the layout stacks on a phone');
    });
  } finally {
    await setPhone(page, false);
    // The fixtures are the member's own; the playlist (if a check left it) and the audio are removed, the place with the file.
    if (playlistId !== null) await apiIn(page, 'DELETE', `/api/v1/playlists/${playlistId}`).catch(() => {});
    if (toneId !== null) await apiIn(page, 'DELETE', `/api/v1/nodes/${toneId}`).catch(() => {});
    const restore = [['selfcloud.lang', kept.lang], ['selfcloud.theme', kept.theme], ['selfcloud.files', kept.files]];
    await page.evaluate(`(() => { for (const [k, v] of ${q(restore)}) { if (v === null) localStorage.removeItem(k); else localStorage.setItem(k, v); } return true; })()`);
  }
  // The phone size was just left: a fresh load of the shell lets the account menu open at the desktop size.
  await page.goto(base + '/');
  await until(page, `!!document.querySelector('[data-testid="account-menu"]') || location.pathname === '/login'`, 15000, 'the shell after the phone size');
  await signOut();
  await signIn(MEMBER);
  console.log('mp3 checks passed');
}
