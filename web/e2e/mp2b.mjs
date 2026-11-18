// SPDX-License-Identifier: AGPL-3.0-or-later

// Choosing a subtitle file from the drive in the player's Subtitles menu (slice mp2b), run inside the shared end-to-end
// run (run.mjs) on the shared page, server and accounts, as the member: a video of My files opens (one an earlier
// section left there, found by its type as the media sections find theirs, or the run's clip uploaded for this section
// alone), Subtitles > Choose from my files… opens the picker on My files, which lists the two-cue WebVTT file the
// section put at the root and no non-subtitle file; picking it and pressing Use adds a track named after the file, the
// menu shows it as the active one, and each of its cues shows on the stage at its own time. The fixtures are the
// member's own, made through the page's API with its CSRF token as pk1 does. Everything the section made is removed
// afterwards, to the trash and then out of it for good, so the member's used bytes and tree are as it found them; the
// used bytes are read before and after and checked to be equal. Run it alone with SELFCLOUD_E2E_ONLY=mp2b. The checks
// end signed in as the member, which the rest of the run expects; the interface's language and theme and the listing's
// view are put back afterwards. The section adds no CDP listener, so there is none to remove.
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
  const vttName = `subs-${stamp}.vtt`;
  const notesName = `aaa-notes-${stamp}.txt`;
  // The two-cue WebVTT: the first cue shows at 2 s, the second at 5 s.
  const VTT = 'WEBVTT\n\n00:00:01.000 --> 00:00:03.000\nFirst drive cue\n\n00:00:04.000 --> 00:00:06.000\nSecond drive cue\n';
  let pf = null;
  let videoName = null;
  let videoId = null;
  // videoOurs marks a video this section uploaded: one an earlier section left is not this section's to remove.
  let videoOurs = false;
  let vttId = null;
  let notesId = null;
  // The member's used bytes as the section finds them, read as the app reads them (the storage answer of /me).
  let usedBefore = null;
  let failed = false;

  await signOut();
  try {
    await signIn(MEMBER);
    // The words these checks read are the English ones; the theme is the light one.
    await page.goto(base + '/');
    await page.evaluate(`localStorage.setItem('selfcloud.lang', 'en'); localStorage.setItem('selfcloud.theme', 'light'); true`);
    await page.goto(base + '/');
    await until(page, `!!document.querySelector('[data-testid="account-menu"]')`, 15000, 'the shell');

    // The fixtures, all the member's own, at the root of My files: the video, the two-cue .vtt and a text file.
    const me = await apiIn(page, 'GET', '/api/v1/me?storage=breakdown');
    pf = me.pf;
    check(!!pf, 'the member has a personal folder');
    usedBefore = me.body?.storage?.used_bytes ?? null;
    check(usedBefore !== null, `the member's used bytes are read (${usedBefore})`);
    // The video: one already in My files if an earlier section left one there; otherwise the run's clip, uploaded
    // here and removed for good afterwards, so a run of this section alone has a video too.
    const root = await apiIn(page, 'GET', `/api/v1/folders/${pf}/children?path=%2F&limit=200`);
    const found = (root.body?.items ?? []).find((n) => n.kind === 'file' && n.type === 'video');
    if (found) {
      videoName = found.name;
    } else {
      videoName = `clip-${stamp}.webm`;
      const clip = await readFile(join(media, 'clip.webm'));
      const upVideo = await putFileIn(page, `/api/v1/folders/${pf}/files?path=${encodeURIComponent('/' + videoName)}`, clip.toString('base64'), 'video/webm');
      check(upVideo.status === 201 || upVideo.status === 200, `the member uploaded the video (${upVideo.status})`);
      videoOurs = true;
    }
    const upVtt = await putFileIn(page, `/api/v1/folders/${pf}/files?path=${encodeURIComponent('/' + vttName)}`, Buffer.from(VTT).toString('base64'), 'text/vtt');
    const upNotes = await putFileIn(page, `/api/v1/folders/${pf}/files?path=${encodeURIComponent('/' + notesName)}`, Buffer.from('not a subtitle\n').toString('base64'), 'text/plain');
    check(upVtt.status === 201 || upVtt.status === 200, `the member uploaded the subtitles (${upVtt.status})`);
    check(upNotes.status === 201 || upNotes.status === 200, `the member uploaded the notes (${upNotes.status})`);
    const kids = await apiIn(page, 'GET', `/api/v1/folders/${pf}/children?path=%2F&limit=200`);
    videoId = kids.body.items.find((n) => n.name === videoName)?.id ?? null;
    vttId = kids.body.items.find((n) => n.name === vttName)?.id ?? null;
    notesId = kids.body.items.find((n) => n.name === notesName)?.id ?? null;
    check(!!videoId && !!vttId && !!notesId, 'the video, the subtitles and the notes are listed');

    await step('the video opens and plays', async () => {
      await openFolder(page, `${base}/files/${pf}`, videoName);
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
      // The fixtures are at My files' root; the root may page, so Load more is pressed until the .vtt shows. The notes
      // file's name sorts before it, so once the .vtt is on a loaded page the notes file is too, and its absence is the
      // picker's accept at work, not a page that was never read.
      for (let i = 0; i < 20; i++) {
        if (await page.evaluate(`!!${treeRowSel(vttName)}`)) break;
        const more = await page.evaluate(`(() => { const b = ${modalButtonEl('Load more')}; if (!b || b.disabled) return false; b.click(); return true; })()`);
        if (!more) break;
        await sleep(300);
      }
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
  } catch (err) {
    failed = true;
    throw err;
  } finally {
    // Everything the section made goes: first to the trash, then out of it for good (the member manages its own My
    // files), so the used bytes come down and the tree is as before. The names are read anew, so a check that failed
    // mid-upload leaves nothing behind either. A video an earlier section left is not this section's to remove.
    if (pf !== null) {
      const left = await apiIn(page, 'GET', `/api/v1/folders/${pf}/children?path=%2F&limit=200`).catch(() => null);
      for (const name of [vttName, notesName, ...(videoOurs ? [videoName] : [])]) {
        const id = (left?.body?.items ?? []).find((n) => n.name === name)?.id;
        if (!id) continue;
        await apiIn(page, 'DELETE', `/api/v1/nodes/${id}`).catch(() => {});
        await apiIn(page, 'DELETE', `/api/v1/trash/${id}`).catch(() => {});
      }
    }
    const restore = [['selfcloud.lang', kept.lang], ['selfcloud.theme', kept.theme], ['selfcloud.files', kept.files]];
    await page.evaluate(`(() => { for (const [k, v] of ${q(restore)}) { if (v === null) localStorage.removeItem(k); else localStorage.setItem(k, v); } return true; })()`);
    // The member's used bytes are as the section found them. A failed check is not masked by this one: it is logged
    // instead, and the cleanup above ran all the same.
    if (usedBefore !== null) {
      const after = await apiIn(page, 'GET', '/api/v1/me?storage=breakdown').catch(() => null);
      const usedAfter = after?.body?.storage?.used_bytes ?? null;
      if (failed) console.log(`INFO the member's used bytes: ${usedBefore} before, ${usedAfter} after the failed checks`);
      else check(usedAfter === usedBefore, `the member's used bytes are as the section found them (${usedBefore} before, ${usedAfter} after)`);
    }
  }
  await page.goto(base + '/');
  await until(page, `!!document.querySelector('[data-testid="account-menu"]') || location.pathname === '/login'`, 15000, 'the shell after the checks');
  await signOut();
  await signIn(MEMBER);
  console.log('mp2b checks passed');
}
