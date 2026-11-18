// SPDX-License-Identifier: AGPL-3.0-or-later

// The Playlists tab's New playlist and Add files (slice pk1), run inside the shared end-to-end run (run.mjs) on the shared
// page, server and accounts, as the member: New playlist names an empty playlist through the dialog and opens its empty
// table; Add files opens the file picker on My files, where a non-media file is not listed, and checking two audio files
// adds them in the order they were checked; adding one of them again makes no duplicate. The fixtures are the member's own,
// made through the page's API with its CSRF token as the media sections do, and removed afterwards. Run it alone with
// SELFCLOUD_E2E_ONLY=pk1. The checks end signed in as the member, which the rest of the run expects; the interface's language
// and theme and the listing's view are put back afterwards. The section adds no CDP listener, so there is none to remove.
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

// clickTab presses one of the Media page's tabs by its label.
async function clickTab(page, label) {
  await page.evaluate(`[...document.querySelectorAll('[role="tab"]')].find((t) => t.textContent.trim() === ${q(label)})?.click()`);
}

// rowSel names a row inside its region: the left table is "Your playlists", the detail is the chosen list's name.
const rowSel = (region, name) => `[role="region"][aria-label=${JSON.stringify(region)}] [role="row"][aria-label=${JSON.stringify(name)}]`;

// modalButton finds a button of the open dialog by its exact text.
const modalButtonEl = (text) => `[...document.querySelectorAll('.ant-modal button')].find((b) => b.textContent.trim() === ${q(text)})`;

// treeRowSel names a row of the picker's tree by the file or place it shows.
const treeRowSel = (name) => `[...document.querySelectorAll('.ant-modal .ant-tree-treenode')].find((r) => r.textContent.includes(${q(name)}))`;

export async function pk1Checks(h, { page, base }) {
  const { check } = h;
  const signIn = (who) => h.signIn(page, who);
  const signOut = () => h.signOut(page);
  // The shared run's interface settings and listing view, put back when the checks end.
  const kept = await page.evaluate(`(() => ({ lang: localStorage.getItem('selfcloud.lang'), theme: localStorage.getItem('selfcloud.theme'), files: localStorage.getItem('selfcloud.files') }))()`);
  const stamp = Date.now().toString(36);
  // The notes file sorts before the two audio files, so once they are on a loaded page it is too, and its absence is the
  // picker's accept at work, not a page that was never read.
  const notesName = `pk1-aaa-notes-${stamp}.txt`;
  const oneName = `pk1-one-${stamp}.ogg`;
  const twoName = `pk1-two-${stamp}.ogg`;
  const listName = 'Road trip';
  let notesId = null;
  let oneId = null;
  let twoId = null;
  let playlistId = null;

  await signOut();
  try {
    await signIn(MEMBER);
    // The words these checks read are the English ones; the theme is the light one.
    await page.goto(base + '/');
    await page.evaluate(`localStorage.setItem('selfcloud.lang', 'en'); localStorage.setItem('selfcloud.theme', 'light'); true`);
    await page.goto(base + '/');
    await until(page, `!!document.querySelector('[data-testid="account-menu"]')`, 15000, 'the shell');

    // The fixtures, all the member's own: two audio files and one text file at the root of My files.
    const me = await apiIn(page, 'GET', '/api/v1/me');
    const pf = me.pf;
    check(!!pf, 'the member has a personal folder');
    const tone = await readFile(join(media, 'tone.ogg'));
    for (const name of [oneName, twoName]) {
      const up = await putFileIn(page, `/api/v1/folders/${pf}/files?path=${encodeURIComponent('/' + name)}`, tone.toString('base64'), 'audio/ogg');
      check(up.status === 201 || up.status === 200, `the member uploaded ${name} (${up.status})`);
    }
    const upNotes = await putFileIn(page, `/api/v1/folders/${pf}/files?path=${encodeURIComponent('/' + notesName)}`, Buffer.from('not media\n').toString('base64'), 'text/plain');
    check(upNotes.status === 201 || upNotes.status === 200, `the member uploaded the notes (${upNotes.status})`);
    const kids = await apiIn(page, 'GET', `/api/v1/folders/${pf}/children?path=%2F&limit=200`);
    notesId = kids.body.items.find((n) => n.name === notesName)?.id ?? null;
    oneId = kids.body.items.find((n) => n.name === oneName)?.id ?? null;
    twoId = kids.body.items.find((n) => n.name === twoName)?.id ?? null;
    check(!!notesId && !!oneId && !!twoId, 'the three uploads are listed');

    await step('New playlist names an empty playlist and opens its empty table', async () => {
      await h.clickNav(page, 'Media');
      await until(page, `location.pathname === '/media'`, 10000, 'the Media page');
      await until(page, `[...document.querySelectorAll('[role="tab"]')].some((t) => t.textContent.trim() === 'Playlists')`, 15000, 'the Playlists tab');
      await clickTab(page, 'Playlists');
      await until(page, `location.search.includes('tab=playlists')`, 10000, 'the Playlists tab');
      await until(page, `!!document.querySelector('[data-testid="media-pl-layout"]')`, 15000, 'the two tables');
      await until(page, `!!document.querySelector('button[aria-label="New playlist"]')`, 15000, 'the New playlist button');
      await page.evaluate(`document.querySelector('button[aria-label="New playlist"]').click()`);
      await until(page, `!!document.querySelector('.ant-modal input')`, 10000, 'the name dialog');
      check(await page.evaluate(`document.querySelector('.ant-modal-title')?.textContent.trim() === 'New playlist'`), 'the dialog is the new playlist');
      // The Create button stays off until the name is worth keeping.
      check(await page.evaluate(`(() => { const b = ${modalButtonEl('Create')}; return !!b && b.disabled; })()`), 'Create is off with no name');
      await page.setInput('.ant-modal input', listName);
      await until(page, `(() => { const b = ${modalButtonEl('Create')}; return !!b && !b.disabled; })()`, 5000, 'Create turns on');
      await page.evaluate(`${modalButtonEl('Create')}?.click()`);
      await until(page, `!!document.querySelector(${q(rowSel('Your playlists', listName))})`, 15000, 'the new playlist row');
      await until(page, `location.search.includes('list=')`, 10000, 'the new list in the address');
      playlistId = await page.evaluate(`new URLSearchParams(location.search).get('list')`);
      check(!!playlistId && playlistId !== 'continue', `the address names the new playlist (${playlistId})`);
      await until(page, `document.querySelector('.media-pl-name')?.textContent.trim() === ${q(listName)}`, 10000, 'the detail is named for it');
      await until(page, `document.body.innerText.includes('This playlist is empty')`, 10000, 'the empty table');
      check(true, 'the new playlist opens on its empty table');
    });

    await step('Add files opens the picker on My files, hides the non-media file, and adds two audio files in order', async () => {
      await until(page, `!!document.querySelector('button[aria-label="Add files"]')`, 10000, 'the Add files button');
      await page.evaluate(`document.querySelector('button[aria-label="Add files"]').click()`);
      await until(page, `document.querySelector('.ant-modal-title')?.textContent.trim() === ${q('Add files to ' + listName)}`, 15000, 'the picker dialog');
      // My files is opened and read at once: its row is expanded and the uploads show without a press. The root may page,
      // so Load more is pressed until both audio files are on a loaded page.
      await until(page, `!!document.querySelector('.ant-modal .ant-tree')`, 10000, 'the tree');
      check(await page.evaluate(`(() => { const r = ${treeRowSel('My files')}; return !!r && r.className.includes('switcher-open'); })()`), 'My files is opened at once');
      for (let i = 0; i < 20; i++) {
        const both = await page.evaluate(`!!${treeRowSel(oneName)} && !!${treeRowSel(twoName)}`);
        if (both) break;
        const more = await page.evaluate(`(() => { const b = ${modalButtonEl('Load more')}; if (!b || b.disabled) return false; b.click(); return true; })()`);
        if (!more) break;
        await sleep(300);
      }
      await until(page, `!!${treeRowSel(oneName)} && !!${treeRowSel(twoName)}`, 15000, 'the two audio files in the picker');
      check(await page.evaluate(`!${treeRowSel(notesName)}`), 'the non-media file is not listed');
      check(await page.evaluate(`(() => { const m = [...document.querySelectorAll('.ant-modal')].find((x) => x.querySelector('.ant-tree')); return !!m && !m.textContent.includes(${q(notesName)}); })()`), 'the notes file is nowhere in the picker');
      // Only the file leaves carry a checkbox; the places do not.
      check(await page.evaluate(`(() => { const r = ${treeRowSel('My files')}; return !!r && !r.querySelector('.ant-tree-checkbox'); })()`), 'My files has no checkbox');
      check(await page.evaluate(`(() => { const r = ${treeRowSel(oneName)}; return !!r && !!r.querySelector('.ant-tree-checkbox'); })()`), 'the audio file has a checkbox');
      // Check the two in this order; the button counts them.
      await page.evaluate(`${treeRowSel(oneName)}?.querySelector('.ant-tree-checkbox')?.click()`);
      await page.evaluate(`${treeRowSel(twoName)}?.querySelector('.ant-tree-checkbox')?.click()`);
      await until(page, `(() => { const b = ${modalButtonEl('Add (2)')}; return !!b && !b.disabled; })()`, 5000, 'Add (2)');
      check(true, 'the button reads Add (2)');
      await page.evaluate(`${modalButtonEl('Add (2)')}?.click()`);
      await until(page, `!document.querySelector('.ant-modal .ant-tree')`, 10000, 'the picker closes');
      await until(page, `!!document.querySelector(${q(rowSel(listName, oneName))}) && !!document.querySelector(${q(rowSel(listName, twoName))})`, 15000, 'the two entries');
      const order = await page.evaluate(`[...document.querySelectorAll(${q(`[role="region"][aria-label=${JSON.stringify(listName)}] [role="row"][aria-label]`)})].map((r) => r.getAttribute('aria-label'))`);
      check(order.indexOf(oneName) >= 0 && order.indexOf(twoName) >= 0 && order.indexOf(oneName) < order.indexOf(twoName), `the table shows the two in the order checked (${order.join(', ')})`);
      const d = await apiIn(page, 'GET', `/api/v1/playlists/${playlistId}`);
      check(d.body.entries.length === 2 && d.body.entries[0].node_id === oneId && d.body.entries[1].node_id === twoId, 'the server holds the two in order');
    });

    await step('adding one of them again makes no duplicate', async () => {
      await page.evaluate(`document.querySelector('button[aria-label="Add files"]').click()`);
      await until(page, `document.querySelector('.ant-modal-title')?.textContent.trim() === ${q('Add files to ' + listName)}`, 15000, 'the picker dialog');
      for (let i = 0; i < 20; i++) {
        const found = await page.evaluate(`!!${treeRowSel(oneName)}`);
        if (found) break;
        const more = await page.evaluate(`(() => { const b = ${modalButtonEl('Load more')}; if (!b || b.disabled) return false; b.click(); return true; })()`);
        if (!more) break;
        await sleep(300);
      }
      await until(page, `!!${treeRowSel(oneName)}`, 15000, 'the audio file in the picker');
      await page.evaluate(`${treeRowSel(oneName)}?.querySelector('.ant-tree-checkbox')?.click()`);
      await until(page, `(() => { const b = ${modalButtonEl('Add (1)')}; return !!b && !b.disabled; })()`, 5000, 'Add (1)');
      await page.evaluate(`${modalButtonEl('Add (1)')}?.click()`);
      await until(page, `!document.querySelector('.ant-modal .ant-tree')`, 10000, 'the picker closes');
      await sleep(500);
      const rows = await page.evaluate(`[...document.querySelectorAll(${q(`[role="region"][aria-label=${JSON.stringify(listName)}] [role="row"][aria-label]`)})].map((r) => r.getAttribute('aria-label'))`);
      check(rows.filter((n) => n === oneName).length === 1 && rows.filter((n) => n === twoName).length === 1, `no duplicate after adding one again (${rows.join(', ')})`);
      const d = await apiIn(page, 'GET', `/api/v1/playlists/${playlistId}`);
      check(d.body.entries.length === 2, `the server still holds two entries (${d.body.entries.length})`);
    });
  } finally {
    // The fixtures are the member's own; the playlist (if a check left it, or an earlier run did) and the uploads are removed.
    const lists = await apiIn(page, 'GET', '/api/v1/playlists').catch(() => ({ body: { items: [] } }));
    for (const pl of lists.body?.items ?? []) {
      if (pl.name === listName) await apiIn(page, 'DELETE', `/api/v1/playlists/${pl.id}`).catch(() => {});
    }
    for (const id of [notesId, oneId, twoId]) {
      if (id !== null) await apiIn(page, 'DELETE', `/api/v1/nodes/${id}`).catch(() => {});
    }
    const restore = [['selfcloud.lang', kept.lang], ['selfcloud.theme', kept.theme], ['selfcloud.files', kept.files]];
    await page.evaluate(`(() => { for (const [k, v] of ${q(restore)}) { if (v === null) localStorage.removeItem(k); else localStorage.setItem(k, v); } return true; })()`);
  }
  await page.goto(base + '/');
  await until(page, `!!document.querySelector('[data-testid="account-menu"]') || location.pathname === '/login'`, 15000, 'the shell after the checks');
  await signOut();
  await signIn(MEMBER);
  console.log('pk1 checks passed');
}
