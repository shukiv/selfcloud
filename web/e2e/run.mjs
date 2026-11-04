// SPDX-License-Identifier: AGPL-3.0-or-later

// The browser smoke test of the React interface. It starts a server on a temporary data directory,
// makes an account with an owner and a member, and drives headless Chrome through the pages built so
// far: sign-in, the shell at desktop and phone width, the language and theme choices, the placeholder
// of a page not built yet, and a sign-in link. Every content-policy violation counts as an error.
//
// Run with make e2e, which builds the binary and the interface first.
import { execFile, spawn } from 'node:child_process';
import { access, constants, mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { randomBytes } from 'node:crypto';
import { tmpdir } from 'node:os';
import { delimiter, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { freePort, launchBrowser, Page } from './cdp.mjs';
import { linkChecks } from './sharePublic.mjs';
import { sharingChecks } from './shareDialog.mjs';
import * as owner from './owner.mjs';
import * as p3 from './p3.mjs';
import * as p4 from './p4.mjs';
import * as p5 from './p5.mjs';
import * as s4a from './s4a.mjs';
import * as s4b from './s4b.mjs';
import { startConvertFake } from './convertFake.mjs';
import { refusedOnPurpose } from './refusals.mjs';
import { sortChecks } from './sort.mjs';
import { accountName, E2E_ACCENT, mediaChecks, setTheme } from './media.mjs';
import { mailSink, waitForMail } from './mailsink.mjs';
import { w1Checks } from './w1.mjs';
import { w2Checks } from './w2.mjs';
import { x2Checks } from './x2.mjs';
import { q4Checks } from './q4.mjs';
import { r7Checks } from './r7.mjs';
import { r7bChecks } from './r7b.mjs';
import { pwaChecks } from './pwa.mjs';
import { x3Checks } from './x3.mjs';
import { x4Checks } from './x4.mjs';
import { x6Checks } from './x6.mjs';
import { d1Checks } from './d1.mjs';
import { d2Checks } from './d2.mjs';
import { i1Checks } from './i1.mjs';
import { d3Checks } from './d3.mjs';
import { l1Checks } from './l1.mjs';
import { ic2Checks } from './ic2.mjs';
import { l4Checks } from './l4.mjs';
import { ksChecks } from './ks.mjs';
import { fu3aChecks } from './fu3a.mjs';
import { pickMenuEntry } from './menu.mjs';

const execFileP = promisify(execFile);
const repo = fileURLToPath(new URL('../../', import.meta.url));
const bin = process.env.SELFCLOUD_BIN ?? join(repo, 'bin', 'selfcloud');

const OWNER = { email: 'olivia@example.com', name: 'Olivia Owner', password: 'correct horse battery staple' };
const MEMBER = { email: 'mateo@example.com', name: 'Mateo Member', password: 'correct horse battery staple' };
const ADMIN = { email: 'ada@example.com', name: 'Ada Admin', password: 'correct horse battery staple' };
const BRAND = { name: 'E2E Drive', accent: E2E_ACCENT };
const INVITEE = { email: 'dana@example.com', name: 'Dana Invited', password: 'correct horse battery staple' };
const TENANT = { name: 'Acme Ltd', owner: { email: 'owen@example.com', name: 'Owen Owner' } };

const results = [];
function check(ok, what) {
  results.push({ ok, what });
  console.log(`${ok ? 'PASS' : 'FAIL'} ${what}`);
  if (!ok) throw new Error(`check failed: ${what}`);
}

// The node IDs the s4b section refuses on purpose. The section fills it right before each refusal, so a 415 counts as
// expected only for a node the section refused.
const s4bRefused = new Set();

// The answers the run causes on purpose: the start-up probe of an anonymous visitor, and a sign-in link
// used a second time. The browser logs each as a failed request; the run counts them instead.
const EXPECTED = [
  // A share link the visitor cannot open: a dead link is a 404, a wrong or missing password a 401 (both counted by name).
  {
    name: 'dead link',
    matches: (e) => e.source === 'network' && /\/api\/v1\/share\/open$/.test(e.url ?? '') && /status of 404\b/.test(e.text ?? ''),
  },
  {
    name: 'password refused',
    matches: (e) => e.source === 'network' && /\/api\/v1\/share\/open$/.test(e.url ?? '') && /status of 401\b/.test(e.text ?? ''),
  },
  // The sharing checks refuse a dead, removed, password-protected or wrong link on purpose, and a node a caller
  // cannot reach: each refusal is logged as a failed request, and counted here.
  {
    name: 'refused share link',
    matches: (e) => e.source === 'network' && /\/api\/v1\/share\/open$/.test(e.url ?? '') && /status of (401|404|429)\b/.test(e.text ?? ''),
  },
  {
    name: 'node the caller cannot reach',
    matches: (e) => e.source === 'network' && /\/api\/v1\/nodes\/[^/]+$/.test(e.url ?? '') && /status of 404\b/.test(e.text ?? ''),
  },
  // An audio file without a cover answers 404 when the player or a tile asks for its cover (decision 56); the icon stays.
  {
    name: 'no cover',
    matches: (e) => e.source === 'network' && /\/api\/v1\/nodes\/[^/]+\/thumbnail\b/.test(e.url ?? '') && /status of 404\b/.test(e.text ?? ''),
  },
  // A cover the server cannot start now answers 429 with Retry-After: its queue is full, or the caller asked for more new
  // thumbnails than a minute allows. The page waits that long and asks again (lib/files/thumbnail.ts). The run's playlists ask
  // for the covers of fifty tracks at once while earlier checks still fill the queue, so some runs meet it.
  {
    name: 'thumbnail busy',
    matches: (e) => e.source === 'network' && /\/api\/v1\/nodes\/[^/]+\/thumbnail\b/.test(e.url ?? '') && /status of 429\b/.test(e.text ?? ''),
  },
  {
    name: 'start-up probe',
    matches: (e) => e.source === 'network' && /\/api\/v1\/me$/.test(e.url ?? '') && /status of 401\b/.test(e.text ?? ''),
  },
  {
    name: 'used sign-in link',
    matches: (e) =>
      e.source === 'network' && /\/api\/v1\/auth\/sign-in-link$/.test(e.url ?? '') && /status of 400\b/.test(e.text ?? ''),
  },
  // A link for another user: the browser is asked before it signs in as that user, and the page logs the 409 it got.
  {
    name: 'switch prompt',
    matches: (e) => e.source === 'network' && /\/api\/v1\/auth\/sign-in-link$/.test(e.url ?? '') && /status of 409\b/.test(e.text ?? ''),
  },
  // A signed link after its end: the browser's next range for it is refused, and the viewer asks for a new link (mediaChecks).
  {
    name: 'link ended',
    matches: (e) => e.source === 'network' && /\/api\/v1\/links\//.test(e.url ?? '') && /status of 410\b/.test(e.text ?? ''),
  },
  // The search page's limit: the run sends more searches than the server allows a minute, on purpose (q4Checks).
  {
    name: 'rate limited search',
    matches: (e) => e.source === 'network' && /\/api\/v1\/search/.test(e.url ?? '') && /status of 429\b/.test(e.text ?? ''),
  },
  // A 403 from the comments or the who-viewed route is the answer a switch gives on purpose: the panel
  // reads it to hide the tab, and the run turns the switch off to check that.
  {
    name: 'comments switch off',
    matches: (e) => e.source === 'network' && /\/api\/v1\/nodes\/[^/]+\/comments(\?[^/]*)?$/.test(e.url ?? '') && /status of 403\b/.test(e.text ?? ''),
  },
  {
    name: 'who-viewed switch off',
    matches: (e) => e.source === 'network' && /\/api\/v1\/nodes\/[^/]+\/viewers(\?[^/]*)?$/.test(e.url ?? '') && /status of 403\b/.test(e.text ?? ''),
  },
  // The member's upload over its limit is refused by the server.
  {
    name: 'over the limit',
    matches: (e) => e.source === 'network' && /\/api\/v1\/uploads$/.test(e.url ?? '') && /status of (403|413|507)\b/.test(e.text ?? ''),
  },
  // The upload that meets a taken name is refused once, and asked about; the connection the run drops is refused
  // by the browser while it is down.
  {
    name: 'taken name',
    matches: (e) => e.source === 'network' && /\/api\/v1\/uploads$/.test(e.url ?? '') && /status of 409\b/.test(e.text ?? ''),
  },
  {
    name: 'dropped connection',
    matches: (e) => e.source === 'network' && /ERR_INTERNET_DISCONNECTED/.test(e.text ?? ''),
  },
  // The conversions the s4b checks refuse on purpose: a refused preview or export, and a file type the converter does not
  // take. Only a 415 on a node s4b refused is expected; any other 415 on a preview or an export fails the run.
  {
    name: 'conversion refused',
    matches: (e) => refusedOnPurpose(e, s4bRefused),
  },
];

async function findBrowser() {
  const candidates = [process.env.SELFCLOUD_E2E_BROWSER, 'chromium', 'chromium-browser', 'google-chrome-stable', 'google-chrome'];
  for (const c of candidates.filter(Boolean)) {
    if (c.includes('/')) {
      try {
        await access(c, constants.X_OK);
        return c;
      } catch {
        continue;
      }
    }
    for (const dir of (process.env.PATH ?? '').split(delimiter)) {
      try {
        await access(join(dir, c), constants.X_OK);
        return join(dir, c);
      } catch {
        // not here
      }
    }
  }
  throw new Error('no browser found: set SELFCLOUD_E2E_BROWSER to a Chrome or Chromium binary');
}

async function waitForServer(base) {
  for (let i = 0; ; i++) {
    try {
      if ((await fetch(base + '/healthz')).ok) return;
    } catch {
      // not up yet
    }
    if (i > 100) throw new Error('the server did not start');
    await new Promise((r) => setTimeout(r, 100));
  }
}

async function api(base, method, path, key, body) {
  const headers = {};
  if (key) headers.Authorization = `Bearer ${key}`;
  if (body) headers['Content-Type'] = 'application/json';
  const res = await fetch(base + path, { method, headers, body: body ? JSON.stringify(body) : undefined });
  const text = await res.text();
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${text}`);
  return text ? JSON.parse(text) : null;
}

const q = (v) => JSON.stringify(v);

// clickOn waits for the element the selector names, then clicks it.
async function clickOn(page, selector, timeout = 10000) {
  await page.waitFor(
    `(() => { const e = document.querySelector(${q(selector)}); if (!e) return false; e.click(); return true; })()`,
    timeout,
  );
}

// clickText clicks the first element of the selector whose text is exactly text.
async function clickText(page, selector, text, timeout = 10000) {
  await page.waitFor(
    `(() => { const e = [...document.querySelectorAll(${q(selector)})].find((x) => x.textContent.trim() === ${q(text)}); if (!e) return false; e.click(); return true; })()`,
    timeout,
  );
}

async function signIn(page, who) {
  await page.goto(page.base + '/login');
  await page.waitFor(`!!document.getElementById('login-email')`);
  await page.setInput('#login-email', who.email);
  await page.setInput('#login-password', who.password);
  await clickOn(page, 'form button[type="submit"]');
  await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]')`, 15000);
}

async function signOut(page) {
  // A signed-in visitor on a public page has no account menu: the shell is opened first, through a link click.
  if (!(await page.evaluate(`!!document.querySelector('[data-testid="account-menu"]')`))) {
    // A visitor who is not signed in has nothing to sign out of: the sign-in page is what it sees.
    if (await page.evaluate(`location.pathname === '/login'`)) return;
    await navigateTo(page, '/');
    await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]') || location.pathname === '/login'`, 15000);
    if (!(await page.evaluate(`!!document.querySelector('[data-testid="account-menu"]')`))) return;
  }
  await clickOn(page, '[data-testid="account-menu"]');
  await clickText(page, '.ant-dropdown-menu-item', 'Sign out');
  await page.waitFor(`location.pathname === '/login' && !!document.getElementById('login-email')`, 15000);
}

// rawPut uploads a small file as the account key, for setup the browser does not do.
async function rawPut(base, path, key, body, type = 'text/plain') {
  const res = await fetch(base + path, {
    method: 'POST',
    headers: { Authorization: `Bearer ${key}`, 'Content-Type': type },
    body,
  });
  if (!res.ok) throw new Error(`POST ${path}: ${res.status} ${await res.text()}`);
}

// rowSel selects the listing row of the item with this name.
const rowSel = (name) => `[role="row"][aria-label="${name.replace(/["\\]/g, '\\$&')}"]`;

// clickMenu clicks the sidebar entry with exactly this text, once it is on the page.
async function clickMenu(page, text) {
  await page.waitFor(
    `(() => { const i = [...document.querySelectorAll('[role="menuitem"], ul[aria-label] > li > a, [data-testid="folder-tree"] .tree-row a')].find((x) => x.textContent.trim() === ${q(text)}); if (!i) return false; i.click(); return true; })()`,
    10000,
  );
}

// clickButton clicks the enabled button whose text is exactly text, once it is on the page.
async function clickButton(page, text) {
  // The selection panel's icons carry their names as labels: one that is there is pressed directly.
  const panelButton = `[...document.querySelectorAll('[data-testid="selection-panel"] button')].find((x) => x.getAttribute('aria-label') === ${q(text)} && !x.disabled)`;
  if (await page.evaluate(`!!${panelButton}`)) {
    await page.evaluate(`${panelButton}.click()`);
    return;
  }
  // Some toolbar actions live in the New and More menus: those are chosen from the menu that holds them. Any other button
  // is waited for on the page, as before (a dialog's, or a row's).
  const NEW_MENU = ['New folder', 'Upload files', 'Upload folder'];
  const MENU_ONLY = [...NEW_MENU, 'Details', 'Versions', 'Add shortcut', 'Cut', 'Copy', 'Paste', 'Move to…', 'Copy to…', 'Download as zip'];
  if (!MENU_ONLY.includes(text)) {
    await page.waitFor(
      // An icon button has its name as the label (the picture viewer's Close).
      `(() => { const all = [...document.querySelectorAll('button')].filter((x) => !x.disabled); const b = all.find((x) => x.textContent.trim() === ${q(text)}) ?? all.find((x) => x.getAttribute('aria-label') === ${q(text)}); if (!b) return false; b.click(); return true; })()`,
      10000,
    );
    return;
  }
  const holder = NEW_MENU.includes(text) ? 'New' : 'More';
  // The panel's More is an icon with its name as the label; the toolbar's New and More are words.
  const toolbarButton = `[...document.querySelectorAll('[data-testid="browser-toolbar"] button, [data-testid="selection-panel"] button, [data-testid="new-button"]')].find((x) => x.textContent.trim() === ${q(holder)} || x.getAttribute('aria-label') === ${q(holder)})`;
  await page.waitFor(`!!${toolbarButton}`, 10000);
  await page.evaluate(`${toolbarButton}.click()`);
  // The entry is picked by its label; one in a submenu (decision 70) opens its submenu first.
  await pickMenuEntry(page, text);
}

// rowAppears and rowGone wait for a row of the listing.
async function rowAppears(page, name, timeout = 15000) {
  await page.waitFor(`!!document.querySelector(${q(rowSel(name))})`, timeout);
}

async function rowGone(page, name, timeout = 15000) {
  await page.waitFor(`!document.querySelector(${q(rowSel(name))})`, timeout);
}

// selectRow clicks a row and moves focus to it, as a person would.
async function selectRow(page, name) {
  await rowAppears(page, name);
  await page.evaluate(`(() => { const el = document.querySelector(${q(rowSel(name))}); el.focus(); el.click(); return true; })()`);
}

// openRow opens a folder or a preview by double click.
async function openRow(page, name) {
  await rowAppears(page, name);
  await page.evaluate(`document.querySelector(${q(rowSel(name))}).dispatchEvent(new MouseEvent('dblclick', { bubbles: true })), true`);
}

// press sends a key to the element the selector names, as the page's own key handlers see it.
async function press(page, selector, key) {
  // keyCode is set too: the dialogs' Escape handler reads it.
  const keyCode = key === 'Escape' ? 27 : 0;
  await page.evaluate(`(() => { const el = document.querySelector(${q(selector)}); el.focus(); el.dispatchEvent(new KeyboardEvent('keydown', { key: ${q(key)}, keyCode: ${keyCode}, bubbles: true })); return true; })()`);
}

// buttonTexts lists the text of every button on the page.
async function buttonTexts(page) {
  // An icon button has no text: its name (aria-label) is read too (decision 68), so a check for a missing button still reads it.
  return page.evaluate(`[...document.querySelectorAll('button')].flatMap((b) => [b.textContent.trim(), b.getAttribute('aria-label')]).filter(Boolean)`);
}

// sizeCell reads the size column of a row. The column is found by its header, because the cells before it depend on the pointer:
// a finger has a check box column, a mouse has the check box in the name cell (decision 70).
const sizeCell = (name) =>
  `(() => { const row = document.querySelector(${q(rowSel(name))}); const heads = [...(row.closest('.ant-table')?.querySelectorAll('thead th') ?? [])]; const i = heads.findIndex((h) => h.textContent.trim().startsWith('Size')); return i < 0 ? null : row.querySelectorAll('td')[i].textContent.trim(); })()`;

// chooseLanguage picks a language from the language menu of the top bar.
async function chooseLanguage(page, name) {
  await clickOn(page, '[data-testid="language"]');
  await clickText(page, '.ant-dropdown-menu-item', name);
}

// fileBrowserChecks is the file browser's end-to-end path, as the member signed in. Its check descriptions are the
// Svelte suite's, which this one replaced, word for word, so the PASS lists before and after the flip match.
async function fileBrowserChecks(page, { base, dir, teamA, teamB }) {
  const small = join(dir, 'hello.txt');
  const bigPath = join(dir, 'big.bin');
  // The replacement has the same name as the file it replaces, in a folder of its own.
  await mkdir(join(dir, 'second'), { recursive: true });
  const replacement = join(dir, 'second', 'hello.txt');
  const png = join(dir, 'pic.png');
  await writeFile(small, 'hello from the e2e\n');
  await writeFile(replacement, 'a second version, longer than the first one\n');
  await writeFile(bigPath, randomBytes(3 * 1024 * 1024));
  await writeFile(png, Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=', 'base64'));
  const UPLOAD = '[role="toolbar"] input[type="file"]:not([webkitdirectory])';

  // The language is English for this part.
  await chooseLanguage(page, 'English');
  await page.waitFor(`document.documentElement.lang === 'en'`);

  // My files opens the personal folder; then Team A, where the member can edit.
  await clickMenu(page, 'My files');
  await page.waitFor(`/^\\/files\\/fld_/.test(location.pathname)`);
  check(true, 'My files opens the personal folder');
  await clickMenu(page, 'Team A');
  await page.waitFor(`location.pathname === '/files/${teamA}'`);
  // Wide windows: the sidebar holds New (decision 66), and the toolbar has none.
  await page.waitFor(`document.querySelector('#browser-title')?.textContent === 'Team A' && !!document.querySelector('.shell-sider [data-testid="new-button"]:not([disabled])')`);
  const buttons = await buttonTexts(page);
  check(buttons.includes('New'), `an editor sees the New menu in the sidebar (${JSON.stringify(buttons)})`);
  // The label alone says nothing about what the menu holds: it is opened, its items are read, and it is closed again.
  await page.evaluate(`document.querySelector('.shell-sider [data-testid="new-button"]').click()`);
  await page.waitFor(`[...document.querySelectorAll('.ant-dropdown-menu-item')].some((i) => i.textContent.trim() === 'New folder')`, 10000);
  const newItems = await page.evaluate(`[...document.querySelectorAll('.ant-dropdown-menu-item')].map((i) => i.textContent.trim())`);
  check(newItems.includes('New folder') && newItems.includes('Upload files'), `the New menu holds New folder and Upload files (${JSON.stringify(newItems)})`);
  await press(page, 'body', 'Escape');
  await page.waitFor(`![...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden)')].length`, 10000);

  // New folder, with the server's name checks.
  await clickButton(page, 'New folder');
  await page.waitFor(`!!document.getElementById('newfolder-name')`);
  await page.setInput('#newfolder-name', 'Reports');
  await page.evaluate(`document.querySelector('#newfolder-name').form.requestSubmit(), true`);
  await rowAppears(page, 'Reports');
  check(true, 'a new folder is created and listed');

  // Open it. Upload a small file, then the 3 MiB file, which must go up in pieces.
  await openRow(page, 'Reports');
  await page.waitFor(`location.pathname.split('/').length === 4`);
  await page.setFiles(UPLOAD, [small]);
  await rowAppears(page, 'hello.txt');
  check(true, 'a small file uploads and is listed');

  const before = page.sent.filter((r) => r.method === 'PATCH' && r.url.includes('/api/v1/uploads/')).length;
  await page.setFiles(UPLOAD, [bigPath]);
  await rowAppears(page, 'big.bin', 30000);
  const pieces = page.sent.filter((r) => r.method === 'PATCH' && r.url.includes('/api/v1/uploads/')).length - before;
  check(pieces >= 3, `the 3 MiB file went up in ${pieces} pieces of 1 MiB`);

  // Rename, then undo the rename.
  await selectRow(page, 'hello.txt');
  await press(page, rowSel('hello.txt'), 'F2');
  await page.waitFor(`!!document.querySelector('input.rename')`);
  await page.setInput('input.rename', 'greeting.txt');
  await press(page, 'input.rename', 'Enter');
  await rowAppears(page, 'greeting.txt');
  check(true, 'rename works');
  await clickButton(page, 'Undo');
  await rowAppears(page, 'hello.txt');
  await rowGone(page, 'greeting.txt');
  check(true, 'undo puts the name back');

  // Delete, then undo the delete.
  await selectRow(page, 'hello.txt');
  await press(page, rowSel('hello.txt'), 'Delete');
  await clickButton(page, 'Move to trash');
  await rowGone(page, 'hello.txt');
  await clickButton(page, 'Undo');
  await rowAppears(page, 'hello.txt');
  check(true, 'delete goes to the trash and undo brings it back');

  // Delete again, then restore from the trash.
  await selectRow(page, 'hello.txt');
  await press(page, rowSel('hello.txt'), 'Delete');
  await clickButton(page, 'Move to trash');
  await rowGone(page, 'hello.txt');
  await clickMenu(page, 'Trash');
  await page.waitFor(`location.pathname === '/trash' && [...document.querySelectorAll('.trash-item .name')].some((n) => n.textContent.includes('hello.txt'))`);
  await page.evaluate(`(() => { const item = [...document.querySelectorAll('.trash-item')].find((li) => li.textContent.includes('hello.txt')); [...item.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Restore' || b.getAttribute('aria-label') === 'Restore: hello.txt').click(); return true; })()`);
  await page.waitFor(`document.querySelector('.ant-alert-success') && document.querySelector('.ant-alert-success').textContent.includes('hello.txt')`);
  check(true, 'the trash restores a deleted file');
  await page.evaluate(`history.back(), true`);
  await page.waitFor(`location.pathname === '/files/${teamA}/' + location.pathname.split('/')[3]`);
  await rowAppears(page, 'hello.txt');

  // Replace the content by uploading under the same name: the batch question says replace, and the old
  // content is kept as a version.
  await selectRow(page, 'hello.txt');
  await page.evaluate(`(window.__oldSize = ${sizeCell('hello.txt')}, true)`);
  await page.setFiles(UPLOAD, [replacement]);
  await clickButton(page, 'Replace it');
  await page.waitFor(`(() => { const r = document.querySelector(${q(rowSel('hello.txt'))}); return !!r && ${sizeCell('hello.txt')} !== window.__oldSize; })()`, 20000);
  check(true, 'an upload with replace replaces the content');

  // Versions: the old content is listed, and restoring it makes it current again.
  await selectRow(page, 'hello.txt');
  await clickButton(page, 'Versions');
  await page.waitFor(`document.querySelectorAll('[data-testid="version"]').length >= 2`);
  // A version's buttons are icons named "verb: version" (decision 72), so the old version's Restore is found by that name.
  await page.waitFor(`(() => { const b = [...document.querySelectorAll('[data-testid="version"] button')].find((x) => !x.disabled && (x.getAttribute('aria-label') ?? '').startsWith('Restore this version: ')); if (!b) return false; b.click(); return true; })()`, 10000);
  await page.waitFor(`document.body.textContent.includes('Restored.')`);
  await page.waitFor(`(() => { const r = document.querySelector(${q(rowSel('hello.txt'))}); return !!r && ${sizeCell('hello.txt')} === window.__oldSize; })()`, 20000);
  check(true, 'restoring an earlier version makes it current again');
  // The drawer's own X is the only close control (Versions has no footer button).
  await page.waitFor(`(() => { const b = document.querySelector('[data-testid="versions"] .ant-drawer-close'); if (!b) return false; b.click(); return true; })()`, 10000);
  await page.waitFor(`!document.querySelector('[data-testid="versions"]')`);

  // An image opens in the preview, loaded from a short signed link.
  await page.setFiles(UPLOAD, [png]);
  await rowAppears(page, 'pic.png');
  await selectRow(page, 'pic.png');
  await press(page, rowSel('pic.png'), 'Enter');
  await page.waitFor(`(() => { const i = document.querySelector('[data-testid="preview"] img'); return !!i && i.complete && i.naturalWidth > 0; })()`, 15000);
  check(true, 'an image opens in the preview');
  await clickButton(page, 'Close');
  await page.waitFor(`!document.querySelector('[data-testid="preview"]')`);

  // Hebrew: the list turns right to left and the actions are translated. The selection is dropped first: while one is
  // selected the selection panel takes the toolbar's row, and the New button is not there.
  await page.evaluate(`document.querySelector('[data-testid="selection-clear"]')?.click()`);
  await page.waitFor(`!document.querySelector('[data-testid="selection-panel"]')`, 10000);
  await chooseLanguage(page, 'עברית');
  await page.waitFor(`document.documentElement.dir === 'rtl' && [...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'חדש')`);
  const rtlRows = await page.evaluate(`!!document.querySelector('.ant-table') && getComputedStyle(document.querySelector('.ant-table')).direction === 'rtl' || document.documentElement.dir === 'rtl'`);
  check(rtlRows, 'in Hebrew the list is right to left and the actions are translated');
  await chooseLanguage(page, 'English');
  await page.waitFor(`[...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'New' || b.getAttribute('aria-label') === 'New')`);

  // A viewer of Team B: no rename, no delete, no upload.
  await clickMenu(page, 'Team B');
  await page.waitFor(`location.pathname === '/files/${teamB}'`);
  await rowAppears(page, 'readme.txt');
  await selectRow(page, 'readme.txt');
  // The panel's icons have no text: their names (aria-label) are read too.
  const viewer = [...(await buttonTexts(page)), ...(await page.evaluate(`[...document.querySelectorAll('[data-testid="selection-panel"] button')].map((b) => b.getAttribute('aria-label'))`))];
  for (const hidden of ['Rename', 'Delete']) {
    check(!viewer.includes(hidden), `a viewer of Team B has no ${hidden}`);
  }
  check(viewer.includes('Download'), 'a viewer of Team B can download');
  // The sidebar's New is on the folder shown: a viewer's three entries are disabled, with the reason, and not hidden (decision 66).
  await page.evaluate(`document.querySelector('.shell-sider [data-testid="new-button"]').click()`);
  await page.waitFor(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].some((i) => i.textContent.trim() === 'New folder')`, 10000);
  const viewerNew = await page.evaluate(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].map((i) => ({ text: i.textContent.trim(), disabled: i.classList.contains('ant-dropdown-menu-item-disabled') }))`);
  const viewerEntries = viewerNew.filter((i) => ['New folder', 'Upload files', 'Upload folder'].includes(i.text));
  check(viewerEntries.length === 3 && viewerEntries.every((i) => i.disabled), `a viewer of Team B sees New folder, Upload files and Upload folder disabled (${JSON.stringify(viewerNew)})`);
  check(viewerNew.some((i) => i.text === 'You cannot add to this folder.'), 'the viewer is told why: the folder takes no additions');
  await press(page, 'body', 'Escape');
  await page.waitFor(`![...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden)')].length`, 10000);
}

// fileBrowserExtraChecks covers what the Svelte suite does not: a connection that drops in the middle of an upload,
// starring from the listing, and the listing at phone width. Its descriptions are the React suite's own.
async function fileBrowserExtraChecks(page, { dir, teamA }) {
  const UPLOAD = '[role="toolbar"] input[type="file"]:not([webkitdirectory])';
  const resumable = join(dir, 'resume.bin');
  await writeFile(resumable, randomBytes(3 * 1024 * 1024));
  await clickMenu(page, 'Team A');
  await page.waitFor(`location.pathname === '/files/${teamA}' && !!document.querySelector('[role="toolbar"]')`);
  const sentBefore = page.sent.filter((r) => r.method === 'PATCH' && r.url.includes('/api/v1/uploads/')).length;
  await page.setFiles(UPLOAD, [resumable]);
  // The connection drops once the first piece has gone up, and comes back a moment later.
  for (let i = 0; i < 300 && page.sent.filter((r) => r.method === 'PATCH' && r.url.includes('/api/v1/uploads/')).length <= sentBefore; i++) {
    await new Promise((r) => setTimeout(r, 20));
  }
  await page.send('Network.emulateNetworkConditions', { offline: true, latency: 0, downloadThroughput: 0, uploadThroughput: 0 });
  // The tray reports the failure. The connection stays down until it does: the uploader tries a dropped piece again after a
  // second, and a connection back by then let the upload finish on its own, with no failure to see (gate fu1b, under load).
  await page.waitFor(`document.querySelector('[data-testid="upload-tray"]')?.innerText.includes('resume.bin failed to upload.')`, 60000);
  await page.send('Network.emulateNetworkConditions', { offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
  // Retry sends the rest of the file from the offset the server has, not from the start.
  const beforeRetry = page.sent.filter((r) => r.method === 'PATCH' && r.url.includes('/api/v1/uploads/')).length;
  await clickButton(page, 'Retry: resume.bin');
  await rowAppears(page, 'resume.bin', 60000);
  const afterRetry = page.sent.filter((r) => r.method === 'PATCH' && r.url.includes('/api/v1/uploads/')).length - beforeRetry;
  check(afterRetry >= 1 && afterRetry <= 2, `an interrupted upload resumes and finishes, sending ${afterRetry} of its 3 pieces after the retry`);

  // Star is one of the icons at the end of a selected row (decision 70); once starred, the same icon says Unstar.
  await page.waitFor(`!!document.querySelector(${q(rowSel('Reports'))})`);
  await selectRow(page, 'Reports');
  await page.waitFor(
    `(() => { const b = document.querySelector(${q(rowSel('Reports'))})?.querySelector('.row-acts button[aria-label^="Star"]'); if (!b) return false; b.click(); return true; })()`,
    10000,
  );
  await page.waitFor(`!!document.querySelector(${q(rowSel('Reports'))})?.querySelector('.row-acts button[aria-label^="Unstar"]')`, 10000);
  check(true, 'a file can be starred from the listing');
  // Escape on the row clears the selection, so the steps after this one see the listing as before.
  await press(page, rowSel('Reports'), 'Escape');
  await page.waitFor(`!document.querySelector('[role="row"][aria-selected="true"]')`, 10000);

  await page.send('Emulation.setDeviceMetricsOverride', { width: 375, height: 812, deviceScaleFactor: 1, mobile: true });
  await page.waitFor(`document.querySelectorAll('.ant-table-thead th').length === 3`, 10000);
  const overflow = await page.evaluate(`document.documentElement.scrollWidth - document.documentElement.clientWidth`);
  check(overflow <= 0, `at phone width the listing keeps the name and the size, and the page does not scroll sideways (overflow ${overflow}px)`);
  await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
}

// menuTexts lists the entries of the sidebar menu, open or in the drawer.
const menuTexts = (page) => page.evaluate(`[...document.querySelectorAll('[role="menuitem"]')].map((i) => i.textContent.trim())`);

// clickMenuItemText clicks the sidebar entry whose text starts with this text (the Requests entry carries a count).
async function clickMenuItemText(page, text) {
  await page.waitFor(
    `(() => { const b = [...document.querySelectorAll('[role="menu"] [role="menuitem"], [data-testid="folder-tree"] .tree-row a')].find((x) => x.textContent.trim().startsWith(${q(text)})); if (!b) return false; b.click(); return true; })()`,
    10000,
  );
}

// navigateTo moves inside the app by a link click, so the router (not a page load) shows the page.
async function navigateTo(page, path) {
  await page.evaluate(
    `(() => { const a = document.createElement('a'); a.href = ${q(path)}; document.body.append(a); a.click(); a.remove(); return true; })()`,
  );
}

async function main() {
  const dir = await mkdtemp(join(tmpdir(), 'selfcloud-e2e-'));
  const port = await freePort();
  const base = `http://127.0.0.1:${port}`;
  const cfg = join(dir, 'selfcloud.yaml');
  const sink = await mailSink();
  // The document converter is a fake HTTPS server on this host (convertFake.mjs). The server trusts its certificate through
  // SSL_CERT_FILE, which is set for the server process only.
  const fake = await startConvertFake(dir, await freePort());
  await writeFile(
    cfg,
    [
      `listen: "127.0.0.1:${port}"`,
      `public_url: "${base}"`,
      `data_dir: "${join(dir, 'data')}"`,
      'log:',
      '  level: warn',
      'rate_limit:',
      '  per_ip_per_minute: 1000',
      '  per_email_per_minute: 1000',
      // Pieces are 1 MiB, so the 3 MiB file below must go up in several requests. The temporary directory
      // may be small, so the free-space floor is kept low (the default is 1 GiB).
      'upload:',
      '  max_chunk_size: "1MiB"',
      'storage:',
      '  min_free: "1MiB"',
      // Mail goes to the sink above, with no TLS and no login, as the server's smtp_tls none allows on localhost.
      'mail:',
      '  smtp_host: "127.0.0.1"',
      `  smtp_port: ${sink.port}`,
      '  smtp_tls: "none"',
      '  from: "Selfcloud <drive@example.com>"',
      // Document previews and exports go to the fake converter (decision 37).
      'office:',
      '  enabled: true',
      `  discovery_url: "${fake.origin}/hosting/discovery"`,
      'ui:',
      '  brand:',
      `    name: "${BRAND.name}"`,
      `    accent: "${BRAND.accent}"`,
      '',
    ].join('\n'),
  );

  const adminKey = (await execFileP(bin, ['admin', 'create-key', '-name', 'e2e', '-config', cfg])).stdout.trim();
  const made = await execFileP(bin, ['admin', 'create-account', '-name', 'E2E Company', '-config', cfg]);
  const accountID = /account_id=(\S+)/.exec(made.stdout)?.[1];
  const accountKey = /account_key=(\S+)/.exec(made.stdout)?.[1];
  if (!accountID || !accountKey || !adminKey) throw new Error('the CLI did not print the account');
  const adminMade = await execFileP(bin, ['admin', 'create-admin', '-email', ADMIN.email, '-name', ADMIN.name, '-config', cfg]);
  const adminToken = /^token=(\S+)/m.exec(adminMade.stdout)?.[1];
  if (!adminToken) throw new Error('the CLI did not print the admin token');

  const server = spawn(bin, ['serve', '-config', cfg], {
    stdio: ['ignore', 'pipe', 'pipe'],
    env: { ...process.env, SSL_CERT_FILE: fake.certFile },
  });
  let serverLog = '';
  server.stdout.on('data', (d) => (serverLog += d));
  server.stderr.on('data', (d) => (serverLog += d));
  let browser = null;
  // l1Browser is the list's own browser, while its checks run (see the l1 call).
  let l1Browser = null;
  let ic2Browser = null;
  // ksBrowser is the sign-in box's own browser, while its checks run (see the ks call).
  let ksBrowser = null;
  let page = null;
  let passed = false;
  try {
    await waitForServer(base);
    await api(base, 'POST', '/api/v1/auth/verify', null, { token: adminToken, password: ADMIN.password });

    let memberID = null;
    let ownerID = null;
    for (const u of [
      { ...OWNER, role: 'owner' },
      { ...MEMBER, role: 'member' },
    ]) {
      const created = await api(base, 'POST', `/api/v1/accounts/${accountID}/users`, accountKey, {
        email: u.email,
        display_name: u.name,
        role: u.role,
        verification: 'link',
      });
      if (u.role === 'member') memberID = created.id;
      if (u.role === 'owner') ownerID = created.id;
      await api(base, 'POST', '/api/v1/auth/verify', null, { token: created.verification.token, password: u.password });
    }
    check(true, 'account, owner and member created and verified through the API');
    const teamA = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'Team A' })).id;
    await api(base, 'PUT', `/api/v1/accounts/${accountID}/folders/${teamA}/grants/${memberID}`, accountKey, { level: 'write' });
    // Team B: the member only reads it, and it holds one file.
    const teamB = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'Team B' })).id;
    await api(base, 'PUT', `/api/v1/accounts/${accountID}/folders/${teamB}/grants/${memberID}`, accountKey, { level: 'read' });
    await rawPut(base, `/api/v1/folders/${teamB}/files?path=%2Freadme.txt`, accountKey, 'read me\n');
    check(true, 'two team spaces made; the member edits one and reads the other');
    await owner.p1Setup({ api, rawPut }, base, accountKey, memberID, randomBytes);
    const p3ids = await p3.p3Setup({ api, rawPut }, base, accountKey, { accountID, teamA, teamB });
    const p4ids = await p4.p4Setup({ api }, base, accountKey, { accountID, teamA });

    const launched = await launchBrowser(await findBrowser(), join(dir, 'chrome-profile'), { ignoreCertificateErrors: true });
    browser = launched.proc;
    page = new Page(launched.wsUrl);
    page.base = base;
    await page.start(EXPECTED);
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    // The helpers the share modules take as h. Their own helpers stay in their own files.
    const h = {
      check,
      api,
      rawPut,
      clickOn,
      clickButton,
      rowAppears,
      selectRow,
      openRow,
      press,
      signIn,
      signOut,
      // signInHere signs in on the sign-in page already open, so its ?next= goes on being used.
      signInHere: async (p, who) => {
        await p.waitFor(`!!document.getElementById('login-email')`);
        await p.setInput('#login-email', who.email);
        await p.setInput('#login-password', who.password);
        await clickOn(p, 'form button[type="submit"]');
        await p.waitFor(`!!document.querySelector('[data-testid="account-menu"]')`, 15000);
      },
      navigateTo,
      // clickNav opens a sidebar entry by its text, as a person would.
      clickNav: (p, text) => clickMenuItemText(p, text),
      setInput: (p, id, value) => p.setInput(`#${id}`, value),
      OWNER,
      MEMBER,
    };

    // Sign in as the owner, at desktop width.
    await signIn(page, OWNER);
    const who = await accountName(page);
    check(who.includes(OWNER.name), `the shell shows the signed-in name (${who.trim()})`);
    // The shell draws no name (decision 69): the configured name is in the browser tab, and the sidebar and the top bar show none.
    const tabTitle = await page.evaluate(`document.title`);
    check(tabTitle.includes(BRAND.name), 'the browser tab carries the configured brand name');
    const shellNames = await page.evaluate(`[document.querySelector('.shell-sider')?.textContent ?? '', document.querySelector('header')?.textContent ?? ''].join(' ')`);
    check(!shellNames.includes(BRAND.name), 'the sidebar and the top bar draw no brand name');
    const sider = await page.evaluate(`getComputedStyle(document.querySelector('.ant-layout-sider')).backgroundColor`);
    check(sider === 'rgb(15, 118, 110)', `the accent is the configured colour (${sider})`);
    // The shell's menu is antd items, not links: the owner's Manage section is its Overview entry and its people entry.
    const ownerItems = await menuTexts(page);
    check(ownerItems.includes('Overview') && ownerItems.includes('People'), 'an owner sees the Manage link');
    check(!ownerItems.includes('Tenants') && !ownerItems.includes('Admins'), 'an owner sees no Admin link: an owner is not an installation admin');
    const items = await menuTexts(page);
    check(items.includes('People'), 'an owner sees the owner pages in the menu');
    check(!items.includes('Tenants'), 'an owner sees no Tenants page: an owner is not an installation admin');

    // Hebrew: the page turns right to left, the navigation is translated, and the choice goes to the account.
    await clickOn(page, '[data-testid="language"]');
    await clickText(page, '.ant-dropdown-menu-item', 'עברית');
    await page.waitFor(`document.documentElement.dir === 'rtl' && document.documentElement.lang === 'he'`);
    check(true, 'Hebrew sets lang="he" and dir="rtl"');
    // My files is the first row of the folder tree in the sidebar (slice d1), and the menu holds the other pages.
    await page.waitFor(`[...document.querySelectorAll('[data-testid="folder-tree"] .tree-row bdi')].some((i) => i.textContent.includes('הקבצים שלי'))`);
    check(true, 'the navigation is translated into Hebrew');
    // The browser's saved choice is cleared first, so the reload can only learn Hebrew from the account.
    await page.evaluate(`localStorage.clear(), true`);
    await page.send('Page.reload');
    await page.waitFor(`document.documentElement.dir === 'rtl' && !!document.querySelector('[data-testid="account-menu"]')`, 15000);
    check(true, 'the Hebrew choice is kept after a reload, from the account');
    await clickOn(page, '[data-testid="language"]');
    await clickText(page, '.ant-dropdown-menu-item', 'English');
    await page.waitFor(`document.documentElement.dir === 'ltr'`);

    // Theme: dark paints the dark page and sets the attribute.
    await setTheme(page, 'Dark');
    await page.waitFor(`document.documentElement.dataset.theme === 'dark'`);
    const bg = await page.evaluate(`getComputedStyle(document.querySelector('.ant-layout')).backgroundColor`);
    check(bg === 'rgb(7, 21, 49)', `dark theme applies its background (${bg})`);
    await setTheme(page, 'Light');
    await page.waitFor(`document.documentElement.dataset.theme === 'light'`);
    check(true, 'the theme switch changes the page and the attribute');

    // Phone width: no horizontal scroll, and the sidebar opens as a drawer.
    await page.send('Emulation.setDeviceMetricsOverride', { width: 375, height: 812, deviceScaleFactor: 1, mobile: true });
    await page.waitFor(`!!document.querySelector('[data-testid="nav-open"]')`, 10000);
    const overflow = await page.evaluate(`document.documentElement.scrollWidth - document.documentElement.clientWidth`);
    check(overflow <= 0, `no horizontal scroll at 375px (overflow ${overflow}px)`);
    await clickOn(page, '[data-testid="nav-open"]');
    await page.waitFor(`!!document.querySelector('.ant-drawer-open [role="menuitem"]')`);
    check(true, 'at phone width the sidebar opens as a drawer');
    await clickOn(page, '.ant-drawer-close');
    // Every style element carries the page's nonce: the check that catches an unnonced style in any state.
    const unnonced = await page.evaluate(`[...document.querySelectorAll('style')].filter((s) => !s.nonce).length`);
    check(unnonced === 0, `every style element carries the page's nonce (${unnonced} without)`);
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });

    await signOut(page);
    check(true, 'sign out returns to the sign-in page');

    // A member sees no admin pages; a page not built yet shows its placeholder inside the shell.
    await signIn(page, MEMBER);
    const memberItems = await menuTexts(page);
    check(!memberItems.some((t) => t === 'Tenants' || t === 'Admins'), 'a member sees no admin pages');
    check(!memberItems.includes('Overview') && !memberItems.some((t) => t === 'Tenants' || t === 'Admins'), 'a member sees no Admin link');
    // SELFCLOUD_E2E_ONLY=media, w1, w2, x2, q4, r7, r7b, d1, d2, i1, l1, d3, x3, x4, x6, ks, fu3a or pwa runs that slice's checks alone, to work on them quickly; a full run never sets it.
    const only = process.env.SELFCLOUD_E2E_ONLY;
    // SELFCLOUD_E2E_ONLY takes one section or a comma list (d2,d3); the sections run in the order of the run.
    const onlySet = only ? new Set(only.split(',').map((x) => x.trim()).filter(Boolean)) : null;
    if (!only) {
      await fileBrowserChecks(page, { base, dir, teamA, teamB });
      await fileBrowserExtraChecks(page, { dir, teamA });
      await s4a.s4aChecks(h, { page, base, accountKey, teamA, dir });
      await sortChecks(h, { page, base, accountKey, teamA });
    }
    if (!only || onlySet.has('s4b')) await s4b.s4bChecks(h, { page, base, accountKey, teamA, dir, fake, refused: s4bRefused });
    if (!only || onlySet.has('media')) await mediaChecks(h, { page, base, accountKey, teamA });
    if (!only || onlySet.has('w1')) await w1Checks(h, { page, base, accountKey, teamA, teamB, dir });
    if (!only || onlySet.has('w2')) await w2Checks(h, { page, base, accountKey, accountID, memberID, teamA, teamB, dir });
    if (!only || onlySet.has('x2')) await x2Checks(h, { page, base, accountKey, accountID, memberID });
    if (!only || onlySet.has('q4')) await q4Checks(h, { page, base, accountKey, teamA });
    if (!only || onlySet.has('r7')) await r7Checks(h, { page, base, accountKey, accountID, memberID, teamA, dir });
    if (!only || onlySet.has('r7b')) await r7bChecks(h, { page, base, accountKey, accountID, memberID, teamA, dir });
    if (!only || onlySet.has('d1')) await d1Checks(h, { page, base, accountKey, teamA });
    if (!only || onlySet.has('d2')) await d2Checks(h, { page, base, accountKey, teamA, teamB, dir, accountID, memberID });
    if (!only || onlySet.has('i1')) await i1Checks(h, { page, base, accountKey, teamA, dir });
    // The list's checks need a mouse's hover, which a headless browser has only from its launch (see launchBrowser), and an earlier
    // section's touch emulation resets it. So l1 runs in its own browser, launched with a mouse, signed in as the member the shared page is signed in as before the sections (an editor), with the same
    // content-policy listening as the shared page; its errors count in this run.
    if (!only || onlySet.has('l1')) {
      const l1Launch = await launchBrowser(await findBrowser(), join(dir, 'l1-profile'), { mouse: true });
      l1Browser = l1Launch.proc;
      const l1Page = new Page(l1Launch.wsUrl);
      l1Page.base = base;
      try {
        await l1Page.start(EXPECTED);
        await l1Page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
        await signIn(l1Page, MEMBER);
        await l1Checks(h, { page: l1Page, base, accountKey, teamA, dir });
        check(l1Page.errors.length === 0, `the list's browser has no console errors or policy violations (${l1Page.errors.join(' | ') || 'none'})`);
        const l1Foreign = l1Page.requests.filter((u) => !u.startsWith(base + '/') && u !== base && !u.startsWith('data:') && !u.startsWith('blob:' + base + '/'));
        check(l1Foreign.length === 0, `the list's browser made no request to another origin (${l1Foreign.length} foreign)`);
      } finally {
        l1Page.close();
        l1Browser.kill();
        l1Browser = null;
      }
    }
    // Slice ic2 runs in its own browser with a mouse too (the tooltips open on hover), signed in as the owner for the trash.
    if (!only || onlySet.has('ic2')) {
      const ic2Launch = await launchBrowser(await findBrowser(), join(dir, 'ic2-profile'), { mouse: true });
      ic2Browser = ic2Launch.proc;
      const ic2Page = new Page(ic2Launch.wsUrl);
      ic2Page.base = base;
      try {
        await ic2Page.start(EXPECTED);
        await ic2Page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
        await ic2Checks(h, { page: ic2Page, base, accountKey, teamA, dir, signIn: (p, who) => signIn(p, who), OWNER });
        check(ic2Page.errors.length === 0, `the icon browser has no console errors or policy violations (${ic2Page.errors.join(' | ') || 'none'})`);
      } finally {
        ic2Page.close();
        ic2Browser.kill();
        ic2Browser = null;
      }
    }
    // l4 (decision 73): the editor from the member's menu, in the member's own browser, then the owner's break of the lock in the
    // owner's browser. The owner signs in on the shared page.
    if (!only || onlySet.has('l4')) {
      await l4Checks(h, { page, base, accountKey, teamA, teamB, fake, dir, ownerPage: page, signInAs: async (p) => { await signOut(p); await signIn(p, OWNER); } });
      await signOut(page);
      await signIn(page, MEMBER);
    }
    // The sign-in box's checks (ks) run in their own browser with their own profile, so the first sign-in there is a fresh browser's.
    if (!only || onlySet.has('ks')) {
      const ksLaunch = await launchBrowser(await findBrowser(), join(dir, 'ks-profile'));
      ksBrowser = ksLaunch.proc;
      const ksPage = new Page(ksLaunch.wsUrl);
      ksPage.base = base;
      try {
        await ksPage.start(EXPECTED);
        await ksChecks(h, { page: ksPage, base, MEMBER });
        check(ksPage.errors.length === 0, `the sign-in box's browser has no console errors or policy violations (${ksPage.errors.join(' | ') || 'none'})`);
      } finally {
        ksPage.close();
        ksBrowser.kill();
        ksBrowser = null;
      }
    }
    // The web follow-ups of fu3a (decision 76): the invitation dialog's focus, in its own browser.
    if (!only || onlySet.has('fu3a')) {
      const fuLaunch = await launchBrowser(await findBrowser(), join(dir, 'fu3a-profile'));
      const fuPage = new Page(fuLaunch.wsUrl);
      fuPage.base = base;
      try {
        await fuPage.start(EXPECTED);
        await fu3aChecks(h, { page: fuPage, base, OWNER });
        check(fuPage.errors.length === 0, `the fu3a browser has no console errors or policy violations (${fuPage.errors.join(' | ') || 'none'})`);
      } finally {
        fuPage.close();
        fuLaunch.proc.kill();
      }
    }
    if (!only || onlySet.has('d3')) await d3Checks(h, { page, ADMIN, MEMBER });
    if (!only || onlySet.has('x3')) await x3Checks(h, { page, base, accountKey, accountID, memberID });
    if (!only || onlySet.has('x4')) await x4Checks(h, { page, base, accountKey, accountID, memberID });
    if (!only || onlySet.has('x6')) await x6Checks(h, { page, base, accountKey, accountID, memberID });
    if (!only || onlySet.has('pwa')) await pwaChecks(h, { page, base, brand: { name: BRAND.name, accentUpper: BRAND.accent.toUpperCase(), member: MEMBER }, check, wsUrl: launched.wsUrl });
    if (only) {
      console.log(`only ${only}: passed`);
      passed = true;
      return;
    }
    const mine = await owner.myFilesChecks(page, h, { base, accountKey, memberID, ownerID, teamA });
    await sharingChecks(page, { base, dir, accountID, accountKey, memberID, teamB, sink, check, OWNER, MEMBER, waitForMail });

    // The owner's management pages, then the installation admin's, then a member who opens the owner's pages.
    await signOut(page);
    await signIn(page, OWNER);
    await owner.sharedWithOwnerChecks(page, h, mine);
    await owner.ownerChecks(page, h, { base, sink, waitForMail, INVITEE });
    await owner.p1OwnerChecks(page, h, { MEMBER });
    await p3.p3OwnerChecks(page, h, { teamA, projectsID: p3ids.projectsID });
    await p4.p4OwnerChecks(page, h, { api, base, accountKey, teamA, reportID: p4ids.reportID });
    await signOut(page);
    await signIn(page, ADMIN);
    await owner.adminChecks(page, h, { base, adminKey, TENANT });
    await signOut(page);
    await signIn(page, MEMBER);
    await owner.memberNotFound(page, h);
    await owner.p1MemberChecks(page, h, { dir, writeFile, randomBytes, join });
    await p3.p3MemberChecks(page, h, { teamA, teamB, ...p3ids });
    await p4.p4MemberChecks(page, h, { teamA, teamB });
    await owner.signInLinkChecks(page, h, { base, accountID, accountKey, memberID, teamA, MEMBER });
    await owner.switchChecks(page, h, { base, accountID, accountKey, OWNER, MEMBER, signIn });
    await linkChecks(page, { base, accountID, accountKey }, h);
    // Last, through the API: a restricted label and a move between team spaces. No page sees the moved file.
    await p5.p5Checks(h, { base, accountKey, memberID, accountID, teamA, teamB });

    // Policy: no console error and no content-policy violation (the nonce policy is enforced here),
    // and no request to another origin.
    check(page.errors.length === 0, `no console errors or policy violations (${page.errors.join(' | ') || 'none'})`);
    check(page.expectedAnswers.get('taken name')?.length === 1, 'exactly one upload met a taken name, and was asked about it');
    check(page.expectedAnswers.get('dead link')?.length === 4, 'the four dead links were refused once each: a signed-out open of a members\' link, a removed link, a deleted anyone link and a deleted drop link');
    check(page.expectedAnswers.get('password refused')?.length === 2, 'the password link was refused twice: asked for, then a wrong one');
    check(owner.switchPrompts() === 2, 'the two links for another user were each asked about, on a browser signed in as another');
    check(page.expectedAnswers.get('over the limit')?.length === 1, "the member's upload over its limit was refused once, by the server");
    check((page.expectedAnswers.get('node the caller cannot reach')?.length ?? 0) >= 2, 'the not-found views asked for access, and the browser logged their 404s');
    check(page.expectedAnswers.get('dropped connection')?.length >= 1, 'the connection the run dropped was refused while it was down, and counted');
    check(page.expectedAnswers.get('start-up probe')?.length >= 1, 'the start-up probe of an anonymous visitor was refused, and counted');
    check(page.expectedAnswers.get('used sign-in link')?.length === 1, 'the used sign-in link was refused once, on the second visit');
    // A blob: address is the page's own object (a thumbnail the page made from a same-origin answer), not a request elsewhere.
    // The browser's own PDF viewer is an extension inside the browser: a PDF in a frame loads its files from there, and nothing leaves the machine.
    const pdfViewer = 'chrome-extension://mhjfbmdgcfjbbpaeojofohoefgiehjai/';
    // The office editor is another server by design (decision 73): its page opens in a frame, posted to from this origin, and it
    // reaches this server back only through WOPISrc. Such a request is let through only when it names this server's WOPI address.
    const editorPage = (u) => {
      if (!u.startsWith(fake.origin + '/browser/')) return false;
      const src = new URL(u).searchParams.get('WOPISrc') ?? '';
      return src.startsWith(base + '/wopi/files/');
    };
    const foreign = page.requests.filter((u) => !u.startsWith(base + '/') && u !== base && !u.startsWith('data:') && !u.startsWith('blob:' + base + '/') && !u.startsWith(pdfViewer) && !editorPage(u));
    check(foreign.length === 0, `every request is same-origin (${foreign.length} foreign: ${foreign.join(' ')})`);
    console.log(`requests seen: ${page.requests.length}, all on ${base}`);
    console.log('e2e passed');
    passed = true;
  } finally {
    sink.close();
    page?.close();
    browser?.kill();
    l1Browser?.kill();
    ic2Browser?.kill();
    ksBrowser?.kill();
    server.kill();
    await fake.close();
    if (process.env.SELFCLOUD_E2E_KEEP !== '1') {
      await new Promise((r) => setTimeout(r, 200));
      // Chrome can still be writing its profile after the kill (gates d3d and fu1b failed a passed run on ENOTEMPTY), so
      // the removal tries again for a few seconds.
      await rm(dir, { recursive: true, force: true, maxRetries: 20, retryDelay: 250 });
    }
    if (!passed || results.some((r) => !r.ok)) {
      console.error(serverLog);
      if (page?.errors?.length) console.error(`browser errors: ${page.errors.join(' | ')}`);
    }
  }
}

main().catch((err) => {
  console.error(err.message);
  process.exit(1);
});
