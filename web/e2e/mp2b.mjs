// SPDX-License-Identifier: AGPL-3.0-or-later

// Choosing a subtitle file from the drive in the player's Subtitles menu (slice mp2b), run inside the shared end-to-end
// run (run.mjs) on the shared page, server and accounts, as the member: a video and a two-cue WebVTT file (and a text
// file that is not a subtitle) are put in a folder of My files; the video opens, Subtitles > Choose from my files… opens
// the picker on My files, which lists the .vtt and no non-subtitle file; picking it and pressing Use adds a track named
// after the file, the menu shows it as the active one, and each of its cues shows on the stage at its own time. The
// fixtures are the member's own, made through the page's API with its CSRF token as pk1 does, and removed afterwards.
// Run it alone with SELFCLOUD_E2E_ONLY=mp2b. The checks end signed in as the member, which the rest of the run expects;
// the interface's language and theme and the listing's view are put back afterwards. The section adds no CDP listener,
// so there is none to remove.
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

// putFileIn uploads the bytes as the signed-in member, with the session's CSRF token, the way the page's own upload does.
function putFileIn(page, path, b64, type) {
  return page.evaluate(`(async () => {
    const me = await fetch('/api/v1/me', { credentials: 'same-origin', headers: { Accept: 'application/json' } }).then((x) => x.json());
    const bin = Uint8Array.from(atob(${q(b64)}), (c) => c.charCodeAt(0));
    const r = await fetch(${q(path)}, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': ${q(type)}, 'X-CSRF-Token': me.csrf_token ?? '' }, body: bin });
    return { status: r.status };
  })()`);
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

// openSubs clicks the bar's Subtitles button (a programmatic click wakes the bar even while it is hidden). pickOption is
// the expression for an item in the open Subtitles menu found by its label.
const openSubs = `document.querySelector('[data-testid="media-subs"]').click()`;
const pickOption = (name) => `[...document.querySelectorAll('.ant-dropdown-menu-item')].find((x) => x.textContent.trim() === ${q(name)})`;

// modalButtonEl finds a button of the open dialog by its exact text; treeRowSel names a row of the picker's tree.
const modalButtonEl = (text) => `[...document.querySelectorAll('.ant-modal button')].find((b) => b.textContent.trim() === ${q(text)})`;
const treeRowSel = (name) => `[...document.querySelectorAll('.ant-modal .ant-tree-treenode')].find((r) => r.textContent.includes(${q(name)}))`;

// cueAt says the showing track has an active cue holding this text: the browser draws it on the stage at its time.
const cueAt = (text) => `(() => { const t = [...${MEDIA}.textTracks].find((x) => x.mode === 'showing'); return !!t && [...t.activeCues].some((c) => c.text.includes(${q(text)})); })()`;

export async function mp2bChecks(h, { page, base }) {
  const { check } = h;
  const signIn = (who) => h.signIn(page, who);
  const signOut = () => h.signOut(page);
  // The shared run's interface settings and listing view, put back when the checks end.
  const kept = await page.evaluate(`(() => ({ lang: localStorage.getItem('selfcloud.lang'), theme: localStorage.getItem('selfcloud.theme'), files: localStorage.getItem('selfcloud.files') }))()`);
  const stamp = Date.now().toString(36);
  const dirName = `mp2b-${stamp}`;
  const videoName = `clip-${stamp}.webm`;
  const vttName = `subs-${stamp}.vtt`;
  const notesName = `aaa-notes-${stamp}.txt`;
  // The two-cue WebVTT: the first cue shows at 2 s, the second at 5 s.
  const VTT = 'WEBVTT\n\n00:00:01.000 --> 00:00:03.000\nFirst drive cue\n\n00:00:04.000 --> 00:00:06.000\nSecond drive cue\n';
  let pf = null;
  let dirId = null;
  let videoId = null;
  let vttId = null;
  let notesId = null;

  await signOut();
  try {
    await signIn(MEMBER);
    // The words these checks read are the English ones; the theme is the light one.
    await page.goto(base + '/');
    await page.evaluate(`localStorage.setItem('selfcloud.lang', 'en'); localStorage.setItem('selfcloud.theme', 'light'); true`);
    await page.goto(base + '/');
    await until(page, `!!document.querySelector('[data-testid="account-menu"]')`, 15000, 'the shell');

    // The fixtures, all the member's own: a folder of My files with the video, the two-cue .vtt and a text file.
    const me = await apiIn(page, 'GET', '/api/v1/me');
    pf = me.pf;
    check(!!pf, 'the member has a personal folder');
    const mk = await apiIn(page, 'POST', `/api/v1/folders/${pf}/directories`, { path: '/' + dirName, parents: false });
    check(mk.status === 201 || mk.status === 200, `the member made the folder (${mk.status})`);
    dirId = mk.body?.id ?? null;
    const clip = await readFile(join(media, 'clip.webm'));
    const upVideo = await putFileIn(page, `/api/v1/folders/${pf}/files?path=${encodeURIComponent('/' + dirName + '/' + videoName)}`, clip.toString('base64'), 'video/webm');
    const upVtt = await putFileIn(page, `/api/v1/folders/${pf}/files?path=${encodeURIComponent('/' + dirName + '/' + vttName)}`, Buffer.from(VTT).toString('base64'), 'text/vtt');
    const upNotes = await putFileIn(page, `/api/v1/folders/${pf}/files?path=${encodeURIComponent('/' + dirName + '/' + notesName)}`, Buffer.from('not a subtitle\n').toString('base64'), 'text/plain');
    check(upVideo.status === 201 || upVideo.status === 200, `the member uploaded the video (${upVideo.status})`);
    check(upVtt.status === 201 || upVtt.status === 200, `the member uploaded the subtitles (${upVtt.status})`);
    check(upNotes.status === 201 || upNotes.status === 200, `the member uploaded the notes (${upNotes.status})`);
    const kids = await apiIn(page, 'GET', `/api/v1/folders/${pf}/children?path=${encodeURIComponent('/' + dirName)}&limit=200`);
    videoId = kids.body.items.find((n) => n.name === videoName)?.id ?? null;
    vttId = kids.body.items.find((n) => n.name === vttName)?.id ?? null;
    notesId = kids.body.items.find((n) => n.name === notesName)?.id ?? null;
    check(!!videoId && !!vttId && !!notesId, 'the three uploads are listed');
    if (!dirId) {
      const root = await apiIn(page, 'GET', `/api/v1/folders/${pf}/children?path=%2F&limit=200`);
      dirId = root.body.items.find((n) => n.name === dirName && n.kind === 'dir')?.id ?? null;
    }
    check(!!dirId, 'the folder is listed');

    await step('the video opens and plays', async () => {
      await openFolder(page, `${base}/files/${pf}/${dirId}`, videoName);
      await dblClick(page, await rowPoint(page, videoName));
      await until(page, viewerShown, 15000, 'the viewer opens');
      await playing(page);
      await until(page, `!!document.querySelector('[data-testid="media-subs"]')`, 15000, 'the Subtitles button');
    });

    await step('Choose from my files… opens the picker on My files, which lists the .vtt and no non-subtitle file', async () => {
      await page.evaluate(openSubs);
      await until(page, `!!${pickOption('Choose from my files…')}`, 5000, 'the Choose item');
      await page.evaluate(`${pickOption('Choose from my files…')}.click()`);
      await until(page, `document.querySelector('.ant-modal-title')?.textContent.trim() === ${q('Choose subtitles')}`, 15000, 'the picker dialog');
      await until(page, `!!document.querySelector('.ant-modal .ant-tree')`, 10000, 'the tree');
      check(await page.evaluate(`(() => { const r = ${treeRowSel('My files')}; return !!r && r.className.includes('switcher-open'); })()`), 'My files is opened at once');
      // The folder of the fixtures is at My files' root; the root may page, so Load more is pressed until it shows.
      for (let i = 0; i < 20; i++) {
        if (await page.evaluate(`!!${treeRowSel(dirName)}`)) break;
        const more = await page.evaluate(`(() => { const b = ${modalButtonEl('Load more')}; if (!b || b.disabled) return false; b.click(); return true; })()`);
        if (!more) break;
        await sleep(300);
      }
      await until(page, `!!${treeRowSel(dirName)}`, 15000, 'the folder in the picker');
      await page.evaluate(`${treeRowSel(dirName)}?.querySelector('.ant-tree-switcher')?.click()`);
      await until(page, `!!${treeRowSel(vttName)}`, 15000, 'the .vtt in the picker');
      check(await page.evaluate(`!!${treeRowSel(vttName)}`), 'the .vtt is listed');
      check(await page.evaluate(`!${treeRowSel(notesName)}`), 'the text file is not listed');
      check(await page.evaluate(`!${treeRowSel(videoName)}`), 'the video is not listed');
    });

    await step('picking the .vtt and Use adds the active track, and its cues show at their times', async () => {
      await page.evaluate(`${treeRowSel(vttName)}?.querySelector('.ant-tree-node-content-wrapper')?.click()`);
      await until(page, `(() => { const b = ${modalButtonEl('Use')}; return !!b && !b.disabled; })()`, 5000, 'Use turns on');
      await page.evaluate(`${modalButtonEl('Use')}?.click()`);
      await until(page, `!document.querySelector('.ant-modal .ant-tree')`, 10000, 'the picker closes');
      // The menu lists the file as the active track.
      await page.evaluate(openSubs);
      await until(page, `!!${pickOption(vttName)}`, 5000, 'the track in the menu');
      check(await page.evaluate(`${pickOption(vttName)}?.getAttribute('aria-checked') === 'true'`), 'the track is the active one');
      await page.evaluate(`document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', keyCode: 27, bubbles: true })), true`);
      // Each cue shows on the stage at its own time.
      await page.evaluate(`(() => { const v = ${MEDIA}; v.pause(); v.currentTime = 2; return true; })()`);
      await until(page, cueAt('First drive cue'), 10000, 'the first cue');
      check(await page.evaluate(cueAt('First drive cue')), 'the first cue shows at its time');
      await page.evaluate(`(() => { const v = ${MEDIA}; v.currentTime = 5; return true; })()`);
      await until(page, cueAt('Second drive cue'), 10000, 'the second cue');
      check(await page.evaluate(cueAt('Second drive cue')), 'the second cue shows at its time');
    });
  } finally {
    // The fixtures are the member's own; the uploads and their folder are removed.
    for (const id of [videoId, vttId, notesId, dirId]) {
      if (id !== null) await apiIn(page, 'DELETE', `/api/v1/nodes/${id}`).catch(() => {});
    }
    const restore = [['selfcloud.lang', kept.lang], ['selfcloud.theme', kept.theme], ['selfcloud.files', kept.files]];
    await page.evaluate(`(() => { for (const [k, v] of ${q(restore)}) { if (v === null) localStorage.removeItem(k); else localStorage.setItem(k, v); } return true; })()`);
  }
  await page.goto(base + '/');
  await until(page, `!!document.querySelector('[data-testid="account-menu"]') || location.pathname === '/login'`, 15000, 'the shell after the checks');
  await signOut();
  await signIn(MEMBER);
  console.log('mp2b checks passed');
}
