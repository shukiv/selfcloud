// SPDX-License-Identifier: AGPL-3.0-or-later

// The browser smoke test of the React interface. It starts a server on a temporary data directory,
// makes an account with an owner and a member, and drives headless Chrome through the pages built so
// far: sign-in, the shell at desktop and phone width, the language and theme choices, the placeholder
// of a page not built yet, and a sign-in link. Every content-policy violation counts as an error.
//
// Run with make e2e-react, which builds the binary with the reactui tag and the interface first.
import { execFile, spawn } from 'node:child_process';
import { access, constants, mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { randomBytes } from 'node:crypto';
import { tmpdir } from 'node:os';
import { delimiter, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { freePort, launchBrowser, Page } from './cdp.mjs';

const execFileP = promisify(execFile);
const repo = fileURLToPath(new URL('../../', import.meta.url));
const bin = process.env.SELFCLOUD_BIN ?? join(repo, 'bin', 'selfcloud');

const OWNER = { email: 'olivia@example.com', name: 'Olivia Owner', password: 'correct horse battery staple' };
const MEMBER = { email: 'mateo@example.com', name: 'Mateo Member', password: 'correct horse battery staple' };
const ADMIN = { email: 'ada@example.com', name: 'Ada Admin', password: 'correct horse battery staple' };
const BRAND = { name: 'E2E Drive', accent: '#0F766E' };

const results = [];
function check(ok, what) {
  results.push({ ok, what });
  console.log(`${ok ? 'PASS' : 'FAIL'} ${what}`);
  if (!ok) throw new Error(`check failed: ${what}`);
}

// The answers the run causes on purpose: the start-up probe of an anonymous visitor, and a sign-in link
// used a second time. The browser logs each as a failed request; the run counts them instead.
const EXPECTED = [
  {
    name: 'start-up probe',
    matches: (e) => e.source === 'network' && /\/api\/v1\/me$/.test(e.url ?? '') && /status of 401\b/.test(e.text ?? ''),
  },
  {
    name: 'used sign-in link',
    matches: (e) =>
      e.source === 'network' && /\/api\/v1\/auth\/sign-in-link$/.test(e.url ?? '') && /status of 400\b/.test(e.text ?? ''),
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
    `(() => { const i = [...document.querySelectorAll('[role="menuitem"]')].find((x) => x.textContent.trim() === ${q(text)}); if (!i) return false; i.click(); return true; })()`,
    10000,
  );
}

// clickButton clicks the enabled button whose text is exactly text, once it is on the page.
async function clickButton(page, text) {
  await page.waitFor(
    `(() => { const b = [...document.querySelectorAll('button')].find((x) => x.textContent.trim() === ${q(text)} && !x.disabled); if (!b) return false; b.click(); return true; })()`,
    10000,
  );
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
  await page.evaluate(`(() => { const el = document.querySelector(${q(selector)}); el.focus(); el.dispatchEvent(new KeyboardEvent('keydown', { key: ${q(key)}, bubbles: true })); return true; })()`);
}

// buttonTexts lists the text of every button on the page.
async function buttonTexts(page) {
  return page.evaluate(`[...document.querySelectorAll('button')].map((b) => b.textContent.trim())`);
}

// sizeCell reads the size column of a row: the check box is the first cell, the name the second.
const sizeCell = (name) => `document.querySelector(${q(rowSel(name))}).querySelectorAll('td')[2].textContent.trim()`;

// chooseLanguage picks a language from the language menu of the top bar.
async function chooseLanguage(page, name) {
  await clickOn(page, '[data-testid="language"]');
  await clickText(page, '.ant-dropdown-menu-item', name);
}

// fileBrowserChecks is the file browser's end-to-end path, as the member signed in. Its check descriptions are the
// Svelte build's (web/e2e/run.mjs), word for word, so the two PASS lists match for this part.
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
  await page.waitFor(`document.querySelector('#browser-title')?.textContent === 'Team A' && [...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'Upload files')`);
  const buttons = await buttonTexts(page);
  check(buttons.includes('New folder') && buttons.includes('Upload files'), `an editor sees New folder and Upload files (${JSON.stringify(buttons)})`);

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
  await page.evaluate(`(() => { const item = [...document.querySelectorAll('.trash-item')].find((li) => li.textContent.includes('hello.txt')); [...item.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Restore').click(); return true; })()`);
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
  await clickButton(page, 'Restore this version');
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

  // Hebrew: the list turns right to left and the actions are translated.
  await chooseLanguage(page, 'עברית');
  await page.waitFor(`document.documentElement.dir === 'rtl' && [...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'העלו קבצים')`);
  const rtlRows = await page.evaluate(`!!document.querySelector('.ant-table') && getComputedStyle(document.querySelector('.ant-table')).direction === 'rtl' || document.documentElement.dir === 'rtl'`);
  check(rtlRows, 'in Hebrew the list is right to left and the actions are translated');
  await chooseLanguage(page, 'English');
  await page.waitFor(`[...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'Upload files')`);

  // A viewer of Team B: no rename, no delete, no upload.
  await clickMenu(page, 'Team B');
  await page.waitFor(`location.pathname === '/files/${teamB}'`);
  await rowAppears(page, 'readme.txt');
  await selectRow(page, 'readme.txt');
  const viewer = await buttonTexts(page);
  for (const hidden of ['New folder', 'Upload files', 'Upload folder', 'Rename', 'Delete']) {
    check(!viewer.includes(hidden), `a viewer of Team B has no ${hidden}`);
  }
  check(viewer.includes('Download'), 'a viewer of Team B can download');
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
  await new Promise((r) => setTimeout(r, 1500));
  await page.send('Network.emulateNetworkConditions', { offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
  // The tray reports the failure; Retry sends the rest of the file from the offset the server has, not from the start.
  await page.waitFor(`document.querySelector('[data-testid="upload-tray"]')?.innerText.includes('resume.bin failed to upload.')`, 60000);
  const beforeRetry = page.sent.filter((r) => r.method === 'PATCH' && r.url.includes('/api/v1/uploads/')).length;
  await clickButton(page, 'Retry');
  await rowAppears(page, 'resume.bin', 60000);
  const afterRetry = page.sent.filter((r) => r.method === 'PATCH' && r.url.includes('/api/v1/uploads/')).length - beforeRetry;
  check(afterRetry >= 1 && afterRetry <= 2, `an interrupted upload resumes and finishes, sending ${afterRetry} of its 3 pieces after the retry`);

  await page.waitFor(`!!document.querySelector(${q(rowSel('Reports'))})`);
  await page.evaluate(`(() => { const b = document.querySelector(${q(rowSel('Reports'))}).querySelector('button[aria-pressed]'); b.click(); return true; })()`);
  await page.waitFor(`document.querySelector(${q(rowSel('Reports'))}).querySelector('button[aria-pressed]').getAttribute('aria-pressed') === 'true'`, 10000);
  check(true, 'a file can be starred from the listing');

  await page.send('Emulation.setDeviceMetricsOverride', { width: 375, height: 812, deviceScaleFactor: 1, mobile: true });
  await page.waitFor(`document.querySelectorAll('.ant-table-thead th').length === 3`, 10000);
  const overflow = await page.evaluate(`document.documentElement.scrollWidth - document.documentElement.clientWidth`);
  check(overflow <= 0, `at phone width the listing keeps the name and the size, and the page does not scroll sideways (overflow ${overflow}px)`);
  await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
}

// menuTexts lists the entries of the sidebar menu, open or in the drawer.
const menuTexts = (page) => page.evaluate(`[...document.querySelectorAll('[role="menuitem"]')].map((i) => i.textContent.trim())`);

async function main() {
  const dir = await mkdtemp(join(tmpdir(), 'selfcloud-e2e-react-'));
  const port = await freePort();
  const base = `http://127.0.0.1:${port}`;
  const cfg = join(dir, 'selfcloud.yaml');
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

  const server = spawn(bin, ['serve', '-config', cfg], { stdio: ['ignore', 'pipe', 'pipe'] });
  let serverLog = '';
  server.stdout.on('data', (d) => (serverLog += d));
  server.stderr.on('data', (d) => (serverLog += d));
  let browser = null;
  let page = null;
  let passed = false;
  try {
    await waitForServer(base);
    await api(base, 'POST', '/api/v1/auth/verify', null, { token: adminToken, password: ADMIN.password });

    let memberID = null;
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
      await api(base, 'POST', '/api/v1/auth/verify', null, { token: created.verification.token, password: u.password });
    }
    const teamA = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'Team A' })).id;
    await api(base, 'PUT', `/api/v1/accounts/${accountID}/folders/${teamA}/grants/${memberID}`, accountKey, { level: 'write' });
    // Team B: the member only reads it, and it holds one file.
    const teamB = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'Team B' })).id;
    await api(base, 'PUT', `/api/v1/accounts/${accountID}/folders/${teamB}/grants/${memberID}`, accountKey, { level: 'read' });
    await rawPut(base, `/api/v1/folders/${teamB}/files?path=%2Freadme.txt`, accountKey, 'read me\n');

    const launched = await launchBrowser(await findBrowser(), join(dir, 'chrome-profile'));
    browser = launched.proc;
    page = new Page(launched.wsUrl);
    page.base = base;
    await page.start(EXPECTED);
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });

    // Sign in as the owner, at desktop width.
    await signIn(page, OWNER);
    const who = await page.evaluate(`document.querySelector('[data-testid="account-menu"]').textContent`);
    check(who.includes(OWNER.name), `the shell shows the signed-in name (${who.trim()})`);
    const brandName = await page.evaluate(`document.querySelector('header a bdi')?.textContent ?? ''`);
    check(brandName.includes(BRAND.name), 'the header shows the configured brand name');
    const sider = await page.evaluate(`getComputedStyle(document.querySelector('.ant-layout-sider')).backgroundColor`);
    check(sider === 'rgb(15, 118, 110)', `the sidebar takes the configured accent (${sider})`);
    const items = await menuTexts(page);
    check(items.includes('People'), 'an owner sees the owner pages in the menu');
    check(!items.includes('Tenants'), 'an owner sees no Tenants page: an owner is not an installation admin');

    // Hebrew: the page turns right to left, the navigation is translated, and the choice goes to the account.
    await clickOn(page, '[data-testid="language"]');
    await clickText(page, '.ant-dropdown-menu-item', 'עברית');
    await page.waitFor(`document.documentElement.dir === 'rtl' && document.documentElement.lang === 'he'`);
    check(true, 'Hebrew sets lang="he" and dir="rtl"');
    await page.waitFor(`[...document.querySelectorAll('[role="menuitem"]')].some((i) => i.textContent.includes('הקבצים שלי'))`);
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
    await clickText(page, '[data-testid="theme"] .ant-segmented-item-label', 'Dark');
    await page.waitFor(`document.documentElement.dataset.theme === 'dark'`);
    const bg = await page.evaluate(`getComputedStyle(document.querySelector('.ant-layout')).backgroundColor`);
    check(bg === 'rgb(7, 21, 49)', `dark theme applies its background (${bg})`);
    await clickText(page, '[data-testid="theme"] .ant-segmented-item-label', 'Light');
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
    await fileBrowserChecks(page, { base, dir, teamA, teamB });
    await fileBrowserExtraChecks(page, { dir, teamA });
    await page.goto(base + '/storage');
    await page.waitFor(`document.body.textContent.includes('Coming soon')`);
    check(true, 'a page not built yet shows its placeholder inside the shell');

    // A sign-in link: the account's key makes one for the member; the browser opens it and lands signed in.
    await signOut(page);
    const link = await api(base, 'POST', `/api/v1/accounts/${accountID}/users/${memberID}/sign-in-links`, accountKey, {
      next: `/files/${teamA}`,
    });
    check(link.url.startsWith(base + '/sign-in#token='), 'the account key makes a sign-in link for the member');
    await page.goto(link.url);
    await page.waitFor(
      `location.pathname === '/files/${teamA}' && !location.hash && !!document.querySelector('[data-testid="account-menu"]')`,
      15000,
    );
    const signedIn = await page.evaluate(`document.querySelector('[data-testid="account-menu"]').textContent`);
    check(signedIn.includes(MEMBER.name), `the browser opens the link, lands on the requested path, signed in (${signedIn.trim()})`);
    await signOut(page);
    await page.goto(link.url);
    await page.waitFor(`document.body.textContent.includes('already used')`, 15000);
    check(
      !(await page.evaluate(`!!document.querySelector('[data-testid="account-menu"]')`)),
      'the same link a second time shows the expired message, and no shell',
    );

    // Policy: no console error and no content-policy violation (the nonce policy is enforced here),
    // and no request to another origin.
    check(page.errors.length === 0, `no console errors or policy violations (${page.errors.join(' | ') || 'none'})`);
    check(page.expectedAnswers.get('taken name')?.length === 1, 'exactly one upload met a taken name, and was asked about it');
    check(page.expectedAnswers.get('dropped connection')?.length >= 1, 'the connection the run dropped was refused while it was down, and counted');
    check(page.expectedAnswers.get('start-up probe')?.length >= 1, 'the start-up probe of an anonymous visitor was refused, and counted');
    check(page.expectedAnswers.get('used sign-in link')?.length === 1, 'the used sign-in link was refused once, on the second visit');
    const foreign = page.requests.filter((u) => !u.startsWith(base + '/') && u !== base && !u.startsWith('data:'));
    check(foreign.length === 0, `every request is same-origin (${foreign.length} foreign: ${foreign.join(' ')})`);
    console.log(`requests seen: ${page.requests.length}, all on ${base}`);
    console.log('e2e passed');
    passed = true;
  } finally {
    page?.close();
    browser?.kill();
    server.kill();
    if (process.env.SELFCLOUD_E2E_KEEP !== '1') {
      await new Promise((r) => setTimeout(r, 200));
      await rm(dir, { recursive: true, force: true });
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
