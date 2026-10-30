// SPDX-License-Identifier: AGPL-3.0-or-later

// The end-to-end smoke test behind `make e2e`. It starts bin/selfcloud on a temporary data directory,
// makes an account, an owner, a member and an installation admin through the CLI and the API, and drives
// headless Chrome through the web interface: sign in, check the shell and the name, switch to Hebrew (and check the
// layout turns right to left and the choice is saved), switch the theme, and sign out. It then runs the owner's
// management pages (an invitation mailed to a local SMTP sink, a group, a team space), the admin's tenant pages
// (a new tenant, an API key shown once), and a member's not-found check. It fails on
// any console error, uncaught exception, policy violation, or request to another origin.
//
// Needs a Chrome or Chromium binary: set SELFCLOUD_E2E_BROWSER to its path.
import { execFile, spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { access, constants, mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { delimiter, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { freePort, launchBrowser, Page } from './cdp.mjs';
import { createServer } from 'node:net';

const execFileP = promisify(execFile);
const repo = fileURLToPath(new URL('../../', import.meta.url));
const bin = process.env.SELFCLOUD_BIN ?? join(repo, 'bin', 'selfcloud');

const OWNER = { email: 'olivia@example.com', name: 'Olivia Owner', password: 'correct horse battery staple' };
const MEMBER = { email: 'mateo@example.com', name: 'Mateo Member', password: 'correct horse battery staple' };
const BRAND = { name: 'E2E Drive', accent: '#0F766E' };
const ADMIN = { email: 'ada@example.com', name: 'Ada Admin', password: 'correct horse battery staple' };
const INVITEE = { email: 'dana@example.com', name: 'Dana Invited', password: 'correct horse battery staple' };
const TENANT = { name: 'Acme Ltd', owner: { email: 'owen@example.com', name: 'Owen Owner' } };

const results = [];
function check(ok, what) {
  results.push({ ok: Boolean(ok), what });
  console.log(`${ok ? 'PASS' : 'FAIL'} ${what}`);
  if (!ok) throw new Error(`check failed: ${what}`);
}

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

async function main() {
  const dir = await mkdtemp(join(tmpdir(), 'selfcloud-e2e-'));
  const port = await freePort();
  const base = `http://127.0.0.1:${port}`;
  const cfg = join(dir, 'selfcloud.yaml');
  const sink = await mailSink();
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
      `  smtp_host: "127.0.0.1"`,
      `  smtp_port: ${sink.port}`,
      '  smtp_tls: "none"',
      '  from: "Selfcloud <drive@example.com>"',
      'ui:',
      '  brand:',
      `    name: "${BRAND.name}"`,
      `    accent: "${BRAND.accent}"`,
      '',
    ].join('\n'),
  );

  const admin = await execFileP(bin, ['admin', 'create-key', '-name', 'e2e', '-config', cfg]);
  const adminKey = admin.stdout.trim();
  const made = await execFileP(bin, ['admin', 'create-account', '-name', 'E2E Company', '-config', cfg]);
  const accountID = /account_id=(\S+)/.exec(made.stdout)?.[1];
  const accountKey = /account_key=(\S+)/.exec(made.stdout)?.[1];
  if (!accountID || !accountKey || !adminKey) throw new Error('the CLI did not print the account');
  // The installation admin is made here, before the server starts, and verified through the API once it runs.
  const adminMade = await execFileP(bin, ['admin', 'create-admin', '-email', ADMIN.email, '-name', ADMIN.name, '-config', cfg]);
  const adminToken = /^token=(\S+)/m.exec(adminMade.stdout)?.[1];
  if (!adminToken) throw new Error('the CLI did not print the admin token');

  const server = spawn(bin, ['serve', '-config', cfg], { stdio: ['ignore', 'pipe', 'pipe'] });
  let serverLog = '';
  server.stdout.on('data', (d) => (serverLog += d));
  server.stderr.on('data', (d) => (serverLog += d));
  let browser = null;
  let page = null;
  // passed is set at the very end, so a wait that times out, which throws, also prints the logs below.
  let passed = false;
  try {
    await waitForServer(base);
    await api(base, 'POST', '/api/v1/auth/verify', null, { token: adminToken, password: ADMIN.password });

    // The users: an owner and a member, each verified through the link the API returns.
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
      await api(base, 'POST', '/api/v1/auth/verify', null, {
        token: created.verification.token,
        password: u.password,
      });
    }
    check(true, 'account, owner and member created and verified through the API');

    // Two team spaces: the member edits one (write, which is the editor role) and only reads the other.
    const teamA = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'Team A' })).id;
    const teamB = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'Team B' })).id;
    await api(base, 'PUT', `/api/v1/accounts/${accountID}/folders/${teamA}/grants/${memberID}`, accountKey, { level: 'write' });
    await api(base, 'PUT', `/api/v1/accounts/${accountID}/folders/${teamB}/grants/${memberID}`, accountKey, { level: 'read' });
    await rawPut(base, `/api/v1/folders/${teamB}/files?path=%2Freadme.txt`, accountKey, 'read me\n');
    check(true, 'two team spaces made; the member edits one and reads the other');
    await p1Setup(base, accountKey, memberID);

    const browserBin = await findBrowser();
    const launched = await launchBrowser(browserBin, join(dir, 'chrome-profile'));
    browser = launched.proc;
    page = new Page(launched.wsUrl);
    await page.start();
    await page.send('Emulation.setDeviceMetricsOverride', {
      width: 375,
      height: 812,
      deviceScaleFactor: 1,
      mobile: true,
    });

    // Sign in as the owner.
    await page.goto(base + '/login');
    await page.waitFor(`!!document.getElementById('login-email')`);
    await setInput(page, 'login-email', OWNER.email);
    await setInput(page, 'login-password', OWNER.password);
    await clickOn(page, `form button[type="submit"]`);
    await page.waitFor(`!!document.querySelector('.topbar')`);
    const who = await page.evaluate(`document.querySelector('.user-menu > button').textContent`);
    check(who.includes(OWNER.name), `the shell shows the signed-in name (${who.trim()})`);
    const brandName = await page.evaluate(`document.querySelector('.product-name').textContent`);
    check(brandName.includes(BRAND.name), 'the header shows the configured brand name');
    const accent = await page.evaluate(`document.documentElement.style.getPropertyValue('--brand-primary')`);
    check(accent === BRAND.accent, `the accent is the configured colour (${accent})`);
    const ownerLinks = await page.evaluate(`[...document.querySelectorAll('.sidenav a')].map((a) => a.getAttribute('href'))`);
    check(ownerLinks.includes('/manage'), 'an owner sees the Manage link');
    check(!ownerLinks.includes('/admin'), 'an owner sees no Admin link: an owner is not an installation admin');

    // Switch to Hebrew: the page turns right to left and the choice is saved to the account.
    await setSelect(page, '.topbar .control select', 'he');
    await page.waitFor(`document.documentElement.dir === 'rtl' && document.documentElement.lang === 'he'`);
    check(true, 'Hebrew sets lang="he" and dir="rtl"');
    await page.waitFor(`document.querySelector('.sidenav').textContent.includes('הקבצים שלי')`);
    check(true, 'the navigation is translated into Hebrew');
    // The browser's saved choice is cleared first, so the reload can only learn Hebrew from the account.
    await page.evaluate(`localStorage.clear(), true`);
    await page.send('Page.reload');
    await page.waitFor(`document.documentElement.dir === 'rtl' && !!document.querySelector('.topbar')`);
    check(true, 'the Hebrew choice is kept after a reload, from the account');

    // Theme: dark sets the data-theme attribute and the dark background.
    await clickOn(page, `input[name="theme"][value="dark"]`);
    await page.waitFor(`document.documentElement.dataset.theme === 'dark'`);
    const bg = await page.evaluate(`getComputedStyle(document.body).backgroundColor`);
    check(bg === 'rgb(11, 17, 32)', `dark theme applies its background (${bg})`);

    // The page fits the phone width: no horizontal scroll.
    const overflow = await page.evaluate(
      `document.documentElement.scrollWidth - document.documentElement.clientWidth`,
    );
    check(overflow <= 0, `no horizontal scroll at 375px (overflow ${overflow}px)`);

    // Sign out.
    await clickOn(page, `.user-menu > button`);
    await page.waitFor(`!!document.querySelector('.menu-item:last-child')`);
    await clickOn(page, `.menu-item:last-child`);
    await page.waitFor(`location.pathname === '/login' && !!document.getElementById('login-email')`);
    check(true, 'sign out returns to the sign-in page');

    // A member sees no Admin link.
    await setInput(page, 'login-email', MEMBER.email);
    await setInput(page, 'login-password', MEMBER.password);
    await clickOn(page, `form button[type="submit"]`);
    await page.waitFor(`!!document.querySelector('.topbar')`);
    const memberAdmin = await page.evaluate(
      `[...document.querySelectorAll('.sidenav a')].some((a) => a.getAttribute('href') === '/admin')`,
    );
    check(!memberAdmin, 'a member sees no Admin link');

    // The file browser, as a member who edits Team A and only reads Team B.
    await fileBrowserChecks(page, { base, dir, teamA, teamB });

    // Home, Starred and Recent, as the member: a star, an open, a clear; the owner then sees what was shared with it.
    const home = await homeChecks(page, { base, accountKey, memberID, ownerID, teamA });

    // Sharing, from both sides: the owner's share dialog and spaces, and the member's shared list and requests.
    await sharingChecks(page, { base, dir, accountID, accountKey, memberID, teamB, sink });

    // The owner's management pages, then the installation admin's, then a member who opens the owner's pages.
    await signOut(page);
    await signIn(page, OWNER);
    await homeOwnerChecks(page, home);
    await ownerChecks(page, { base, accountID, sink });
    await p1OwnerChecks(page);
    await signOut(page);
    await signIn(page, ADMIN);
    await adminChecks(page, { base, adminKey });
    await signOut(page);
    await signIn(page, MEMBER);
    await memberNotFound(page);
    await p1MemberChecks(page, { dir });

    // A sign-in link: the account's key makes one for the member, the browser opens it and lands signed in.
    await signInLinkChecks(page, { base, accountID, accountKey, memberID, teamA });
    await switchChecks(page, { base, accountID, accountKey });
    await linkChecks(page, { base, accountID, accountKey });

    // Policy: no console error, no CSP violation, no request to another origin.
    check(page.errors.length === 0, `no console errors or policy violations (${page.errors.join(' | ') || 'none'})`);
    check(page.expectedConflicts.length === 1, `exactly one upload met a taken name, and was asked about it (${page.expectedConflicts.length})`);
    check(page.expectedSignInRefusals.length === 1, `the used sign-in link was refused once, on the second visit (${page.expectedSignInRefusals.length})`);
    check(page.expectedDeadLinks.length === 4, `the four dead links were refused once each: a signed-out open of a members' link, a removed link, a deleted anyone link and a deleted drop link (${page.expectedDeadLinks.length})`);
    check(page.expectedPasswordRefusals.length === 2, `the password link was refused twice: asked for, then a wrong one (${page.expectedPasswordRefusals.length})`);
    check(page.expectedSwitchPrompts.length === 2, `the two links for another user were each asked about, on a browser signed in as another (${page.expectedSwitchPrompts.length})`);
    check(page.expectedQuotaRefusals.length === 1, `the member's upload over its limit was refused once, by the server (${page.expectedQuotaRefusals.length})`);
    check(page.expectedMissing.length >= 2, `the not-found views asked for access, and the browser logged their 404s (${page.expectedMissing.length})`);
    // A data: URL is inline, not a request: Chrome draws the calendar icon of a date input from one.
    const foreign = page.requests.filter((u) => !u.startsWith(base + '/') && u !== base && !u.startsWith('data:'));
    check(foreign.length === 0, `every request is same-origin (${foreign.length} foreign: ${foreign.join(' ')})`);
    console.log(`requests seen: ${page.requests.length}, all on ${base}`);
    console.log('e2e passed');
    passed = true;
  } finally {
    page?.close();
    browser?.kill();
    server.kill();
    sink.close();
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

// rawPut sends a body that is not JSON, as the small-file route takes it.
async function rawPut(base, path, key, body, type = 'text/plain') {
  const res = await fetch(base + path, {
    method: 'POST',
    headers: { Authorization: `Bearer ${key}`, 'Content-Type': type },
    body,
  });
  if (!res.ok) throw new Error(`POST ${path}: ${res.status} ${await res.text()}`);
}

const q = (v) => JSON.stringify(v);
// rowSel selects the list row of the item with this name.
const rowSel = (name) => `[role="row"][aria-label="${name.replace(/["\\]/g, '\\$&')}"]`;

// clickButton clicks the enabled button whose text is exactly text, once it is on the page.
async function clickButton(page, text) {
  await page.waitFor(
    `(() => { const b = [...document.querySelectorAll('button')].find((x) => x.textContent.trim() === ${q(text)} && !x.disabled); if (!b) return false; b.click(); return true; })()`,
    10000,
  );
}

// clickOn finds an element and clicks it in one step, waiting until it is there. A wait for one element followed by a
// click on another, in two calls, races a page that draws the second a moment after the first.
async function clickOn(page, selector, timeout = 10000) {
  await page.waitFor(`(() => { const e = document.querySelector(${q(selector)}); if (!e) return false; e.click(); return true; })()`, timeout);
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

// rootChild finds the item of a folder's root with this name, as the account key sees it, and returns its ID.
async function rootChild(base, folder, key, name) {
  const page = await api(base, 'GET', `/api/v1/folders/${folder}/children?path=%2F`, key);
  const found = (page.items ?? []).find((n) => n.name === name);
  if (!found) throw new Error(`${name} is not in the folder ${folder}`);
  return found.id;
}

// homeChecks is slice p2's end-to-end path, as the member: a file is starred in its listing and shows in Starred and on
// Home; opening it records it in Recent; Recent is cleared. It shares a file of the member's My files with the owner
// first, so the owner's Home can show it. It returns what the owner's check needs.
async function homeChecks(page, { base, accountKey, memberID, ownerID, teamA }) {
  const note = 'home-note.txt';
  const shared = 'home-shared.txt';
  await rawPut(base, `/api/v1/folders/${teamA}/files?path=%2F${note}`, accountKey, 'a note for home\n');
  const list = await api(base, 'GET', '/api/v1/folders', accountKey);
  const mine = (list.items ?? []).find((f) => f.kind === 'personal' && f.owner_user_id === memberID);
  if (!mine) throw new Error('the member has no personal folder in the listing');
  await rawPut(base, `/api/v1/folders/${mine.id}/files?path=%2F${shared}`, accountKey, 'shared with the owner\n');
  const sharedID = await rootChild(base, mine.id, accountKey, shared);
  await api(base, 'PUT', `/api/v1/nodes/${sharedID}/permissions/${ownerID}`, accountKey, { role: 'viewer', notify: false });
  const noteID = await rootChild(base, teamA, accountKey, note);

  // Star the note from its listing: the star button is pressed, and the item is marked.
  await navigateTo(page, `/files/${teamA}`);
  await rowAppears(page, note);
  await clickOn(page, `${rowSel(note)} .star-button`);
  await page.waitFor(`document.querySelector(${q(rowSel(note) + ' .star-button')})?.getAttribute('aria-pressed') === 'true'`);
  check(true, 'a member stars a file from the listing, and the star button is pressed');
  await page.waitFor(`!!document.querySelector(${q(rowSel(note) + ' .star-mark')})`);

  // Starred lists it, and so does Home.
  await clickOn(page, `.sidenav a[href="/starred"]`);
  await page.waitFor(`location.pathname === '/starred' && !!document.querySelector('.items a[href="/n/${noteID}"]')`);
  check(true, 'the starred file is listed in Starred');
  await clickOn(page, `.sidenav a[href="/"]`);
  await page.waitFor(`location.pathname === '/' && !!document.querySelector('[aria-labelledby="home-starred"] a[href="/n/${noteID}"]')`, 15000);
  check(true, 'the starred file shows on Home');

  // Opening the file (through its address) records it in Recent.
  await navigateTo(page, `/n/${noteID}`);
  await page.waitFor(`location.pathname.startsWith('/files/') || location.pathname === '/n/${noteID}'`, 15000);
  // The server writes views in batches every two seconds, so the view is in the database a moment after the open.
  await new Promise((resolve) => setTimeout(resolve, 2500));
  await clickOn(page, `.sidenav a[href="/recent"]`);
  await page.waitFor(`location.pathname === '/recent' && !!document.querySelector('.items a[href="/n/${noteID}"]')`, 15000);
  check(true, 'the opened file is listed in Recent');

  // Clear Recent after a confirmation: the list empties, and the file itself is still there.
  await clickButton(page, 'Clear all');
  await page.waitFor(`!!document.querySelector('[role="dialog"] .foot .danger-fill')`);
  await clickOn(page, `[role="dialog"] .foot .danger-fill`);
  await page.waitFor(`!document.querySelector('.items a[href="/n/${noteID}"]') && !!document.querySelector('.empty')`, 15000);
  check(true, 'Recent is cleared after the confirmation, and the file is kept');
  await navigateTo(page, `/files/${teamA}`);
  await rowAppears(page, note);
  check(true, 'the note is still in its folder after Recent is cleared');
  return { sharedID, shared };
}

// homeOwnerChecks: the owner's Home shows the item the member shared with it, in Recently shared.
async function homeOwnerChecks(page, { sharedID }) {
  await navigateTo(page, '/');
  await page.waitFor(`location.pathname === '/' && !!document.querySelector('[aria-labelledby="home-shared"] a[href="/n/${sharedID}"]')`, 15000);
  check(true, 'the owner sees a recently shared item on Home');
}

// fileBrowserChecks is the file browser's end-to-end path, as the member signed in.
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

  // The language is English for this part; the owner's Hebrew choice stays on the page until it is changed.
  await setSelect(page, '.topbar .control select', 'en');
  await page.waitFor(`document.documentElement.lang === 'en'`);

  // My files opens the personal folder; then Team A, where the member can edit.
  await clickOn(page, `.sidenav a[href="/files"]`);
  await page.waitFor(`/^\\/files\\/fld_/.test(location.pathname)`);
  check(true, 'My files opens the personal folder');
  await clickOn(page, `.teams a[href="/files/${teamA}"]`);
  await page.waitFor(`location.pathname === '/files/${teamA}'`);
  await page.waitFor(`!!document.querySelector('.toolbar')`);
  const buttons = await buttonTexts(page);
  check(buttons.includes('New folder') && buttons.includes('Upload files'), `an editor sees New folder and Upload files (${JSON.stringify(buttons)})`);

  // New folder, with the server's name checks.
  await clickButton(page, 'New folder');
  await page.waitFor(`!!document.getElementById('newfolder-name')`);
  await setInput(page, 'newfolder-name', 'Reports');
  await page.evaluate(`document.querySelector('#newfolder-name').form.requestSubmit(), true`);
  await rowAppears(page, 'Reports');
  check(true, 'a new folder is created and listed');

  // Open it. Upload a small file, then the 3 MiB file, which must go up in pieces.
  await openRow(page, 'Reports');
  await page.waitFor(`location.pathname.split('/').length === 4`);
  await page.setFiles('.toolbar input[type="file"]:not([webkitdirectory])', [small]);
  await rowAppears(page, 'hello.txt');
  check(true, 'a small file uploads and is listed');

  const before = page.sent.filter((r) => r.method === 'PATCH' && r.url.includes('/api/v1/uploads/')).length;
  await page.setFiles('.toolbar input[type="file"]:not([webkitdirectory])', [bigPath]);
  await rowAppears(page, 'big.bin', 30000);
  const pieces = page.sent.filter((r) => r.method === 'PATCH' && r.url.includes('/api/v1/uploads/')).length - before;
  check(pieces >= 3, `the 3 MiB file went up in ${pieces} pieces of 1 MiB`);

  // Rename, then undo the rename.
  await selectRow(page, 'hello.txt');
  await press(page, rowSel('hello.txt'), 'F2');
  await page.waitFor(`!!document.querySelector('input.rename')`);
  await setValue(page, 'input.rename', 'greeting.txt');
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
  await clickOn(page, `.sidenav a[href="/trash"]`);
  await page.waitFor(`location.pathname === '/trash' && [...document.querySelectorAll('.trash-item .name')].some((n) => n.textContent.includes('hello.txt'))`);
  await page.evaluate(`(() => { const item = [...document.querySelectorAll('.trash-item')].find((li) => li.textContent.includes('hello.txt')); [...item.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Restore').click(); return true; })()`);
  await page.waitFor(`document.querySelector('.alert.success') && document.querySelector('.alert.success').textContent.includes('hello.txt')`);
  check(true, 'the trash restores a deleted file');
  await page.evaluate(`history.back(), true`);
  await page.waitFor(`location.pathname === '/files/${teamA}/' + location.pathname.split('/')[3]`);
  await rowAppears(page, 'hello.txt');

  // Replace the content by uploading under the same name: the batch question says replace, and the old
  // content is kept as a version.
  await selectRow(page, 'hello.txt');
  await page.evaluate(`(window.__oldSize = document.querySelector(${q(rowSel('hello.txt'))}).querySelectorAll('[role="gridcell"]')[2].textContent.trim(), true)`);
  await page.setFiles('.toolbar input[type="file"]:not([webkitdirectory])', [replacement]);
  await clickButton(page, 'Replace it');
  await page.waitFor(`(() => { const r = document.querySelector(${q(rowSel('hello.txt'))}); return !!r && r.querySelectorAll('[role="gridcell"]')[2].textContent.trim() !== window.__oldSize; })()`, 20000);
  check(true, 'an upload with replace replaces the content');

  // Versions: the old content is listed, and restoring it makes it current again.
  await selectRow(page, 'hello.txt');
  await clickButton(page, 'Versions');
  await page.waitFor(`document.querySelectorAll('.versions .version').length >= 2`);
  await clickButton(page, 'Restore this version');
  await page.waitFor(`document.body.textContent.includes('Restored.')`);
  await page.waitFor(`(() => { const r = document.querySelector(${q(rowSel('hello.txt'))}); return !!r && r.querySelectorAll('[role="gridcell"]')[2].textContent.trim() === window.__oldSize; })()`, 20000);
  check(true, 'restoring an earlier version makes it current again');
  await clickButton(page, 'Close');
  await page.waitFor(`!document.querySelector('[role="dialog"][aria-labelledby="versions-title"]')`);

  // An image opens in the preview, loaded from a short signed link.
  await page.setFiles('.toolbar input[type="file"]:not([webkitdirectory])', [png]);
  await rowAppears(page, 'pic.png');
  await selectRow(page, 'pic.png');
  await press(page, rowSel('pic.png'), 'Enter');
  await page.waitFor(`(() => { const i = document.querySelector('.preview img'); return !!i && i.complete && i.naturalWidth > 0; })()`, 15000);
  check(true, 'an image opens in the preview');
  await press(page, '.preview', 'Escape');
  await page.waitFor(`!document.querySelector('.preview')`);

  // Hebrew: the list turns right to left and the actions are translated.
  await setSelect(page, '.topbar .control select', 'he');
  await page.waitFor(`document.documentElement.dir === 'rtl' && [...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'העלו קבצים')`);
  const rtlRows = await page.evaluate(`!!document.querySelector('[role="grid"]') && getComputedStyle(document.querySelector('[role="grid"]')).direction === 'rtl' || document.documentElement.dir === 'rtl'`);
  check(rtlRows, 'in Hebrew the list is right to left and the actions are translated');
  await setSelect(page, '.topbar .control select', 'en');
  await page.waitFor(`[...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'Upload files')`);

  // A viewer of Team B: no rename, no delete, no upload.
  await clickOn(page, `.teams a[href="/files/${teamB}"]`);
  await page.waitFor(`location.pathname === '/files/${teamB}'`);
  await rowAppears(page, 'readme.txt');
  await selectRow(page, 'readme.txt');
  const viewer = await buttonTexts(page);
  for (const hidden of ['New folder', 'Upload files', 'Upload folder', 'Rename', 'Delete']) {
    check(!viewer.includes(hidden), `a viewer of Team B has no ${hidden}`);
  }
  check(viewer.includes('Download'), 'a viewer of Team B can download');
}

// signInLinkChecks: the account's key makes a sign-in link for the member with a path to open. The browser opens
// it and lands on that path, signed in, with the token gone from the address bar. The same link a second time
// shows the expired message and no shell.
async function signInLinkChecks(page, { base, accountID, accountKey, memberID, teamA }) {
  await signOut(page);
  const made = await api(base, 'POST', `/api/v1/accounts/${accountID}/users/${memberID}/sign-in-links`, accountKey, {
    next: `/files/${teamA}`,
  });
  check(made.url.startsWith(base + '/sign-in#token='), 'the account key makes a sign-in link for the member');
  await page.goto(made.url);
  await page.waitFor(`location.pathname === '/files/${teamA}' && !location.hash && !!document.querySelector('.topbar')`, 15000);
  const who = await page.evaluate(`document.querySelector('.user-menu > button').textContent`);
  check(who.includes(MEMBER.name), `the browser opens the link, lands on the requested path, signed in (${who.trim()})`);

  await signOut(page);
  await page.goto(made.url);
  await page.waitFor(
    `!!document.querySelector('.alert') && document.querySelector('.alert').textContent.includes('already used')`,
    15000,
  );
  check(!(await page.evaluate(`!!document.querySelector('.topbar')`)), 'the same link a second time shows the expired message, and no shell');
}

// switchChecks: a browser signed in as the member that opens a link for the owner is asked first. Stay keeps the
// member signed in and the link unused. A new link, then Switch, signs the browser in as the owner.
async function switchChecks(page, { base, accountID, accountKey }) {
  const ownerID = (await api(base, 'GET', `/api/v1/accounts/${accountID}/users`, accountKey)).items.find(
    (u) => u.email === OWNER.email,
  )?.id;
  check(!!ownerID, 'the account lists the owner, so a link can be made for the owner');

  await page.goto(base + '/login');
  await signIn(page, MEMBER);
  const first = await api(base, 'POST', `/api/v1/accounts/${accountID}/users/${ownerID}/sign-in-links`, accountKey, {
    next: '/files',
  });
  await page.goto(first.url);
  await page.waitFor(`!!document.querySelector('[role="dialog"]')`, 15000);
  const asked = await page.evaluate(`document.querySelector('[role="dialog"]').textContent`);
  check(
    asked.includes(MEMBER.name) && asked.includes(OWNER.name),
    `a browser signed in as the member is asked before a link signs it in as the owner (${asked.trim().slice(0, 90)}...)`,
  );
  await clickButton(page, `Stay signed in as ${MEMBER.name}`);
  // /files moves on to My files at once, so the path only has to be under /files.
  await page.waitFor(`location.pathname.startsWith('/files') && !!document.querySelector('.topbar')`, 15000);
  const stayed = await page.evaluate(`document.querySelector('.user-menu > button').textContent`);
  check(stayed.includes(MEMBER.name), `Stay keeps the member signed in (${stayed.trim()})`);

  const second = await api(base, 'POST', `/api/v1/accounts/${accountID}/users/${ownerID}/sign-in-links`, accountKey, {
    next: '/files',
  });
  await page.goto(second.url);
  await page.waitFor(`!!document.querySelector('[role="dialog"]')`, 15000);
  await clickButton(page, `Switch to ${OWNER.name}`);
  // The shell may open the folder the owner last used, so the path only has to be under /files.
  await page.waitFor(`location.pathname.startsWith('/files') && !!document.querySelector('.topbar')`, 15000);
  const switched = await page.evaluate(`document.querySelector('.user-menu > button').textContent`);
  check(switched.includes(OWNER.name), `Switch signs the browser in as the owner (${switched.trim()})`);
}

// linkChecks: a share link, end to end. The owner makes an organisation link on a directory in a team space that
// allows links, through the share dialog, and copies its URL once. Signed out, the browser opens the URL: the page
// strips the secret from the address and sends the visitor to sign-in, which returns it to the link page. The member
// is then in the directory, lists it under Shared with me, and the owner removes the link with Restricted. The
// same URL is then refused, and the member loses the directory.
async function linkChecks(page, { base, accountID, accountKey }) {
  const fld = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'Link Hall' })).id;
  await api(base, 'PATCH', `/api/v1/folders/${fld}/settings`, accountKey, { allow_links: true });
  const plans = await api(base, 'POST', `/api/v1/folders/${fld}/directories`, accountKey, { path: '/Plans', parents: false });
  await rawPut(base, `/api/v1/folders/${fld}/files?path=%2FPlans%2Fbrief.txt`, accountKey, 'the brief\n');
  // An image, so the visitor's file view has a preview: a real one-pixel PNG, so the browser draws it without an error.
  const pixel = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==', 'base64');
  await rawPut(base, `/api/v1/folders/${fld}/files?path=%2FPlans%2Fplan.png`, accountKey, pixel, 'image/png');

  // The owner shares Plans through an organisation link, and copies the URL the dialog shows once.
  if (await page.evaluate(`!!document.querySelector('.topbar')`)) await signOut(page);
  else await page.goto(base + '/login');
  await signIn(page, OWNER);
  await page.waitFor(`!!document.querySelector('.teams a[href="/files/${fld}"]')`);
  await clickOn(page, `.teams a[href="/files/${fld}"]`);
  await page.waitFor(`location.pathname === '/files/${fld}'`);
  await rowAppears(page, 'Plans');
  await selectRow(page, 'Plans');
  await clickButton(page, 'Share');
  await page.waitFor(`!!document.getElementById('share-general')`);
  await clickOn(page, `input[name="share-general-access"][value="organisation"]`);
  await clickButton(page, 'Create link');
  await page.waitFor(`!!document.querySelector('.made input')`);
  const url = await page.evaluate(`document.querySelector('.made input').value`);
  check(url.startsWith(base + '/s#') && url.slice(base.length + 3).length === 43, 'the owner makes an organisation link, and the dialog shows its URL once');
  await press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  // Signed out, the URL leaves the secret out of the address and sends the visitor to sign in.
  await signOut(page);
  await page.goto(url);
  await page.waitFor(`location.pathname === '/login' && !!document.getElementById('login-email')`);
  const stripped = await page.evaluate(`location.hash === '' && location.pathname === '/login'`);
  check(stripped, 'the link page strips the secret from the address, and sends a signed-out visitor to sign in');
  await signIn(page, MEMBER);
  // The item's own address opens its folder with the item selected.
  await page.waitFor(`location.pathname.endsWith('/${plans.id}') && !location.hash`);
  check(true, 'a member opens the link, and lands on the item it names, with no secret in the address');
  const kept = await page.evaluate(`sessionStorage.getItem('selfcloud.share.secret')`);
  check(kept === null, 'the secret is gone from session storage once used');
  await navigateTo(page, '/shared');
  await page.waitFor(`location.pathname === '/shared' && !!document.querySelector('.shared a[href="/n/${plans.id}"]')`);
  check(true, 'the item the link opened is listed under Shared with me');

  // The owner removes the link with Restricted. The member loses the item at once, and the URL no longer opens it.
  await signOut(page);
  await signIn(page, OWNER);
  await page.waitFor(`!!document.querySelector('.teams a[href="/files/${fld}"]')`);
  await clickOn(page, `.teams a[href="/files/${fld}"]`);
  await page.waitFor(`location.pathname === '/files/${fld}'`);
  await rowAppears(page, 'Plans');
  await selectRow(page, 'Plans');
  await clickButton(page, 'Share');
  await page.waitFor(`!!document.querySelector('input[name="share-general-access"][value="restricted"]')`);
  await clickOn(page, `input[name="share-general-access"][value="restricted"]`);
  await page.waitFor(`document.querySelector('input[name="share-general-access"][value="restricted"]').checked`);
  await press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);
  check(true, 'the owner removes the link with Restricted');

  await signOut(page);
  await signIn(page, MEMBER);
  await navigateTo(page, '/shared');
  await page.waitFor(`location.pathname === '/shared' && !document.querySelector('.shared a[href="/n/${plans.id}"]')`);
  check(true, 'the member loses the item when the link is removed');
  await page.goto(url);
  await page.waitFor(`!!document.querySelector('.alert') && document.querySelector('.alert').textContent.includes("doesn't work")`, 15000);
  check(true, 'the removed link shows the one message a dead link gets');
  await anyoneLinkChecks(page, { base, accountID, accountKey, fld, plans });
  await dropLinkChecks(page, { base, accountID, accountKey, fld, plans });
  await publicLinksDeleteAllChecks(page, { base, accountID, accountKey, fld });
}

// anyoneLinkChecks: the owner turns on links that anyone can open, and makes one on Plans with a password. A
// signed-out visitor opens it, is asked for the password, is refused a wrong one, then sees the folder, previews
// a file, and cannot reach an item outside the link. The owner deletes the link, and the visitor gets the one message.
async function anyoneLinkChecks(page, { base, accountID, accountKey, fld, plans }) {
  const PASSWORD = 'open sesame 2026';
  const outside = (await api(base, 'POST', `/api/v1/folders/${fld}/directories`, accountKey, { path: '/Other', parents: false })).id;

  // The owner turns the account's switch on in Manage > Overview. The session reads the switch when it starts, so the
  // owner signs in again after the change.
  await signOut(page);
  await signIn(page, OWNER);
  await navigateTo(page, '/manage');
  // The switch appears once the policy has loaded; a click that finds no switch yet is tried again.
  await page.waitFor(`(() => { const el = document.getElementById('public-links-switch'); if (!el) return false; el.click(); return true; })()`);
  await page.waitFor(`document.getElementById('public-links-switch').checked`);
  await signOut(page);
  await signIn(page, OWNER);
  check(true, 'the owner turns on links that anyone can open in Manage > Overview');

  // The owner makes a link on Plans that anyone can open, with a password, and copies the URL the dialog shows once.
  await navigateTo(page, `/files/${fld}`);
  await rowAppears(page, 'Plans');
  await selectRow(page, 'Plans');
  await clickButton(page, 'Share');
  await page.waitFor(`!!document.getElementById('share-general')`);
  await page.waitFor(`!!document.querySelector('input[name="share-general-access"][value="anyone"]')`);
  await clickOn(page, `input[name="share-general-access"][value="anyone"]`);
  await page.waitFor(`!!document.getElementById('share-anyone-password')`);
  await setInput(page, 'share-anyone-password', PASSWORD);
  await clickButton(page, 'Create link');
  await page.waitFor(`!!document.querySelector('.made input')`);
  const url = await page.evaluate(`document.querySelector('.made input').value`);
  check(url.startsWith(base + '/s#') && url.slice(base.length + 3).length === 43, 'the owner makes a link that anyone can open, with a password');
  await press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  // A signed-out visitor is asked for the password; a wrong one is refused with the message; the right one opens it.
  await signOut(page);
  await page.goto(url);
  await page.waitFor(`!!document.getElementById('share-password')`);
  check(true, 'a signed-out visitor is asked for the password of a link that anyone can open');
  await setInput(page, 'share-password', 'not the password');
  await clickButton(page, 'Open');
  await page.waitFor(`!!document.querySelector('.alert.error') && document.querySelector('.alert.error').textContent.includes('not right')`);
  check(true, 'a wrong password is refused with its message');
  await setInput(page, 'share-password', PASSWORD);
  await clickButton(page, 'Open');
  await page.waitFor(`!!document.querySelector('.rows') && [...document.querySelectorAll('h1')].some((h) => h.textContent === 'Plans')`, 15000);
  const listed = await page.evaluate(`document.querySelector('.rows').textContent`);
  check(listed.includes('brief.txt') && listed.includes('plan.png'), 'the visitor sees the folder, with no sign-in');
  const kept = await page.evaluate(`location.hash.length > 1`);
  check(kept, 'the secret stays in the address, so a reload works');
  const stored = await page.evaluate(`sessionStorage.getItem('selfcloud.share.secret')`);
  check(stored === null, 'nothing about the visit is left in session storage');

  // The visitor previews the picture: the preview is a signed link, and it has a Download button.
  await page.evaluate(`[...document.querySelectorAll('.rows button')].find((b) => b.textContent === 'plan.png').click(), true`);
  await page.waitFor(`!!document.querySelector('img.public-preview')`, 15000);
  const src = await page.evaluate(`document.querySelector('img.public-preview').getAttribute('src')`);
  check(src.includes('/api/v1/links/'), 'the file opens with a preview from a signed link');
  check(await page.evaluate(`!!document.querySelector('a.btn[download]')`), 'the file has a Download button');
  // The path stops at the link's root: no item above it is named.
  const crumbs = await page.evaluate(`[...document.querySelectorAll('.crumbs button')].map((b) => b.textContent)`);
  check(JSON.stringify(crumbs) === JSON.stringify(['Plans']), `the path stops at the link's root (${crumbs.join(' / ')})`);

  // The visitor's credential cannot reach an item outside the link. The check runs on the server, from the harness.
  const secret = url.slice(url.indexOf('#') + 1);
  const opened = await api(base, 'POST', '/api/v1/share/open', '', { secret, password: PASSWORD });
  const inside = await fetch(base + `/api/v1/nodes/${plans.id}`, { headers: { Authorization: `Bearer ${opened.token}` } });
  const beside = await fetch(base + `/api/v1/nodes/${outside}`, { headers: { Authorization: `Bearer ${opened.token}` } });
  check(inside.status === 200 && beside.status === 404, `a visitor reaches the link's folder, and not the item beside it (${inside.status}, ${beside.status})`);

  // The owner deletes the link. A signed-in member who opens it gets the one dead-link message.
  await signOut(page);
  await signIn(page, OWNER);
  await navigateTo(page, `/files/${fld}`);
  await rowAppears(page, 'Plans');
  await selectRow(page, 'Plans');
  await clickButton(page, 'Share');
  await page.waitFor(`!!document.querySelector('input[name="share-general-access"][value="restricted"]')`);
  await clickOn(page, `input[name="share-general-access"][value="restricted"]`);
  await page.waitFor(`document.querySelector('input[name="share-general-access"][value="restricted"]').checked`);
  await press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);
  await signOut(page);
  await signIn(page, MEMBER);
  await page.goto(url);
  await page.waitFor(`!!document.querySelector('.alert') && document.querySelector('.alert').textContent.includes("doesn't work")`, 15000);
  check(true, 'a deleted link that anyone can open shows the one dead-link message');
}

// dropLinkChecks: the owner asks for files in Plans (Request files, with a name), a signed-out visitor sends two
// files into it, one of them named like a file already there, and sees no listing of the folder. The owner sees both
// files and turns the link off. The visitor who opens the dead link is signed in, as the anyone-link checks above
// do: a signed-out visitor to a dead link is sent to sign in, by design.
async function dropLinkChecks(page, { base, accountID, accountKey, fld, plans }) {
  await rawPut(base, `/api/v1/folders/${fld}/files?path=%2FPlans%2Freport.txt`, accountKey, 'old report\n');

  await signOut(page);
  await signIn(page, OWNER);
  await navigateTo(page, `/files/${fld}`);
  await rowAppears(page, 'Plans');
  await selectRow(page, 'Plans');
  await clickButton(page, 'Share');
  await page.waitFor(`!!document.getElementById('share-request-files')`);
  // Turning Request files on makes the link; the name request is then saved on it.
  await page.evaluate(`(() => { const box = document.querySelector('section.request label.toggle input'); box.click(); return true; })()`);
  await page.waitFor(`!!document.querySelector('section.request .made input')`, 15000);
  const url = await page.evaluate(`document.querySelector('section.request .made input').value`);
  check(url.startsWith(base + '/s#') && url.slice(base.length + 3).length === 43, 'the owner turns on Request files and gets a link');
  await page.waitFor(`(() => { const l = [...document.querySelectorAll('section.request label.toggle')].find((x) => x.textContent.includes('Ask for name')); if (!l) return false; l.querySelector('input').click(); return true; })()`);
  await clickButton(page, 'Save');
  await page.waitFor(`document.querySelector('section.request .hint[role="status"]')?.textContent.includes('Saved')`, 15000);
  await press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  // A signed-out visitor opens the link: it asks for a name and an email, and offers no listing of the folder.
  await signOut(page);
  await page.goto(url);
  await page.waitFor(`!!document.getElementById('drop-name')`, 15000);
  const nameLabel = await page.evaluate(`document.querySelector('label[for="drop-name"]').textContent`);
  check(nameLabel === 'Your name', 'a signed-out visitor sees the name field as required');
  await setInput(page, 'drop-name', 'Vera Visitor');
  await setInput(page, 'drop-email', 'vera@example.com');
  for (const [name, body] of [['report.txt', 'new report\n'], ['brief.txt', 'the brief\n']]) {
    await page.evaluate(`(() => {
      const dt = new DataTransfer();
      dt.items.add(new File([${q(body)}], ${q(name)}, { type: 'text/plain' }));
      const input = document.querySelector('.dropzone input[type="file"]');
      input.files = dt.files;
      input.dispatchEvent(new Event('change', { bubbles: true }));
      return true;
    })()`);
  }
  await page.waitFor(
    `[...document.querySelectorAll('.rows li .muted')].filter((x) => x.textContent === 'Sent').length === 2`,
    30000,
  );
  const visit = await page.evaluate(`[...document.querySelectorAll('.rows li .name')].map((x) => x.textContent)`);
  check(JSON.stringify(visit) === JSON.stringify(['report.txt', 'brief.txt']), `the visitor sees only the two files it sent (${visit.join(', ')})`);
  const listed = await page.evaluate(`!!document.querySelector('.crumbs') || [...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'Plans')`);
  check(listed === false, 'the visitor sees no listing of the folder');

  // The owner sees both files, and the one whose name was taken arrived renamed.
  await signOut(page);
  await signIn(page, OWNER);
  await navigateTo(page, `/files/${fld}`);
  await rowAppears(page, 'Plans');
  await openRow(page, 'Plans');
  await rowAppears(page, 'brief.txt');
  await rowAppears(page, 'report (1).txt');
  check(true, 'the owner sees both files; the clashing name arrived as report (1).txt');

  // The owner turns Request files off: the link stops working at once.
  await navigateTo(page, `/files/${fld}`);
  await rowAppears(page, 'Plans');
  await selectRow(page, 'Plans');
  await clickButton(page, 'Share');
  await page.waitFor(`!!document.getElementById('share-request-files')`);
  await clickOn(page, `section.request label.toggle input`);
  await page.waitFor(`!document.getElementById('drop-expires')`, 15000);
  await press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  await signOut(page);
  await signIn(page, MEMBER);
  await page.goto(url);
  await page.waitFor(`!!document.querySelector('.alert') && document.querySelector('.alert').textContent.includes("doesn't work")`, 15000);
  check(true, 'a deleted drop link shows the one dead-link message');
}

// publicLinksDeleteAllChecks: the account has an anyone link and a drop link (made here through the account key) and an
// organisation link. The owner opens Manage > Links, sees the two public links and the organisation link, and uses
// "Delete all public links", confirming the count. Both public links stop working; the organisation link stays.
async function publicLinksDeleteAllChecks(page, { base, accountID, accountKey, fld }) {
  const dir = async (path) => (await api(base, 'POST', `/api/v1/folders/${fld}/directories`, accountKey, { path, parents: false })).id;
  const [pub, inbox, shared] = [await dir('/Public'), await dir('/Inbox'), await dir('/Shared')];
  const anyone = await api(base, 'POST', `/api/v1/nodes/${pub}/share-links`, accountKey, { audience: 'anyone', role: 'viewer' });
  const drop = await api(base, 'POST', `/api/v1/nodes/${inbox}/share-links`, accountKey, { upload_only: true, title: 'Send us files' });
  const org = await api(base, 'POST', `/api/v1/nodes/${shared}/share-links`, accountKey, { audience: 'organisation', role: 'viewer' });
  const secrets = [anyone.url, drop.url].map((u) => u.slice(u.indexOf('#') + 1));

  await signOut(page);
  await signIn(page, OWNER);
  await navigateTo(page, '/manage/links');
  await page.waitFor(`location.pathname === '/manage/links' && [...document.querySelectorAll('.item-name')].some((x) => x.textContent === 'Shared')`, 15000);
  const names = await page.evaluate(`[...document.querySelectorAll('.item-name')].map((x) => x.textContent)`);
  check(['Public', 'Inbox', 'Shared'].every((n) => names.includes(n)), `Manage > Links lists the anyone link, the drop link and the organisation link (${names.join(', ')})`);

  const before = (await api(base, 'GET', `/api/v1/accounts/${accountID}/sharing-policy`, accountKey)).anyone_links;
  await clickButton(page, 'Delete all public links');
  await page.waitFor(`!!document.querySelector('[role="dialog"]') && document.querySelector('[role="dialog"]').textContent.includes(${q(`${before} link`)})`, 15000);
  check(true, `the confirmation names the count of public links (${before})`);
  await page.evaluate(`(() => { const b = [...document.querySelectorAll('[role="dialog"] button')].find((x) => x.textContent.trim() === 'Delete all public links' && !x.disabled); if (!b) return false; b.click(); return true; })()`);
  await page.waitFor(`[...document.querySelectorAll('[role="status"]')].some((x) => x.textContent.includes(${q(`${before} public link`)}))`, 15000);
  await page.waitFor(`![...document.querySelectorAll('.item-name')].some((x) => x.textContent === 'Public' || x.textContent === 'Inbox') && [...document.querySelectorAll('.item-name')].some((x) => x.textContent === 'Shared')`, 15000);
  check(true, 'Delete all public links removes the public links from the list and keeps the organisation link');

  const policy = await api(base, 'GET', `/api/v1/accounts/${accountID}/sharing-policy`, accountKey);
  check(policy.anyone_links === 0, `no link that anyone can open is left (anyone_links ${policy.anyone_links})`);
  for (const secret of secrets) {
    const res = await fetch(base + '/api/v1/share/open', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ secret }) });
    check(res.status === 404, `a deleted public link no longer opens (${res.status})`);
  }
  const kept = await api(base, 'GET', `/api/v1/nodes/${shared}/share-links`, accountKey);
  check(kept.items.some((l) => l.audience === 'organisation' && l.id === org.link.id), 'the organisation link stays');
}

// signOut signs out through the account menu, as a person would.
async function signOut(page) {
  // A page without the app shell, such as /s, has no user menu even when signed in. The app's home page shows the
  // menu to a signed-in browser and the sign-in page to a signed-out one; a signed-out browser is done there.
  if (!(await page.evaluate(`!!document.querySelector('.user-menu > button')`))) {
    await navigateTo(page, '/files');
    await page.waitFor(`!!document.querySelector('.user-menu > button') || !!document.getElementById('login-email')`, 15000);
    if (await page.evaluate(`!!document.getElementById('login-email')`)) return;
  }
  await clickOn(page, `.user-menu > button`);
  await page.waitFor(`!!document.querySelector('.menu-item:last-child')`);
  await clickOn(page, `.menu-item:last-child`);
  await page.waitFor(`location.pathname === '/login' && !!document.getElementById('login-email')`);
}

// signIn signs in on the sign-in page, and waits for the shell.
async function signIn(page, who) {
  await page.waitFor(`!!document.getElementById('login-email')`);
  await setInput(page, 'login-email', who.email);
  await setInput(page, 'login-password', who.password);
  await clickOn(page, `form button[type="submit"]`);
  await page.waitFor(`!!document.querySelector('.topbar')`);
}

// navigateTo moves inside the app, as a click on a link does, so the app's own router handles it.
async function navigateTo(page, path) {
  await page.evaluate(
    `(() => { const a = document.createElement('a'); a.href = ${q(path)}; document.body.append(a); a.click(); a.remove(); return true; })()`,
  );
}

// setCheckbox sets a checkbox by its ID and fires the change the page listens to.
async function setCheckbox(page, id, on) {
  await page.evaluate(`(() => { const el = document.getElementById(${q(id)}); if (el.checked !== ${on}) el.click(); return true; })()`);
}

// dialogSubmit presses the submit button of the open dialog's form.
async function dialogSubmit(page) {
  await clickOn(page, `[role="dialog"] form button[type="submit"]`);
}

// pickPerson types a query into the people combobox and chooses the option with the name.
async function pickPerson(page, query, name, inputId = 'share-who') {
  await setInput(page, inputId, query);
  await page.waitFor(
    `[...document.querySelectorAll('[role="listbox"] [role="option"]')].some((o) => o.textContent.includes(${q(name)}))`,
    10000,
  );
  await page.evaluate(
    `(() => { const o = [...document.querySelectorAll('[role="listbox"] [role="option"]')].find((x) => x.textContent.includes(${q(name)})); o.click(); return true; })()`,
  );
  await page.waitFor(`document.getElementById(${q(inputId)}).value === ${q(name)}`);
}

// mailSink is a minimal SMTP server that keeps the mail it is sent, so the run can take the links from it. It
// does no TLS and no login: the server dials it with smtp_tls none.
function mailSink() {
  const messages = [];
  const server = createServer((sock) => {
    let buf = '';
    let to = [];
    let data = null;
    const say = (line) => sock.write(`${line}\r\n`);
    sock.on('error', () => {});
    say('220 e2e-sink');
    sock.on('data', (chunk) => {
      buf += chunk.toString('latin1');
      let end;
      while ((end = buf.indexOf('\r\n')) !== -1) {
        const line = buf.slice(0, end);
        buf = buf.slice(end + 2);
        if (data !== null) {
          if (line !== '.') {
            data += `${line}\r\n`;
            continue;
          }
          messages.push({ to, data });
          to = [];
          data = null;
          say('250 queued');
          continue;
        }
        const verb = line.slice(0, 4).toUpperCase();
        if (verb === 'RCPT') {
          to.push(/<([^>]*)>/.exec(line)?.[1] ?? line.slice(8));
          say('250 ok');
        } else if (verb === 'DATA') {
          data = '';
          say('354 end with a dot');
        } else if (verb === 'RSET') {
          to = [];
          say('250 ok');
        } else if (verb === 'QUIT') {
          say('221 bye');
          sock.end();
        } else {
          say('250 ok');
        }
      }
    });
  });
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () =>
      resolve({ port: server.address().port, messages, close: () => server.close() }),
    );
  });
}

// mailBody returns the text of a captured message, decoded from the quoted-printable the server writes.
function mailBody(m) {
  return m.data.replace(/=\r\n/g, '').replace(/=([0-9A-F]{2})/g, (_, h) => String.fromCharCode(parseInt(h, 16)));
}

// waitForMail waits for a message to the address, and returns it.
async function waitForMail(sink, address, timeout = 15000) {
  const until = Date.now() + timeout;
  for (;;) {
    const m = sink.messages.find((x) => x.to.includes(address));
    if (m) return m;
    if (Date.now() > until) throw new Error(`no mail to ${address}`);
    await new Promise((r) => setTimeout(r, 100));
  }
}

// ownerChecks is the owner's management pages, end to end. The owner invites a person by email; the run takes the
// link from the mail the sink received and verifies the account with it. The owner then makes a group with that
// person in it, and a team space.
async function ownerChecks(page, { base, accountID, sink }) {
  await navigateTo(page, '/manage/people');
  await page.waitFor(`location.pathname === '/manage/people' && !!document.querySelector('.topbar')`);
  await clickButton(page, 'Invite person');
  await page.waitFor(`!!document.getElementById('invite-email')`);
  await setInput(page, 'invite-email', INVITEE.email);
  await setInput(page, 'invite-name', INVITEE.name);
  await dialogSubmit(page);
  await page.waitFor(`(document.querySelector('.alert.success')?.textContent ?? '') === 'The email was sent.'`);
  const mail = await waitForMail(sink, INVITEE.email);
  const token = /#token=([A-Za-z0-9_-]+)/.exec(mailBody(mail))?.[1];
  if (!token) throw new Error('the invitation mail has no link');
  await api(base, 'POST', '/api/v1/auth/verify', null, { token, password: INVITEE.password });
  check(true, 'the owner invites a person by email, and the link in the mail verifies the account');

  await navigateTo(page, '/manage/groups');
  await clickButton(page, 'New group');
  await page.waitFor(`!!document.getElementById('group-new-name')`);
  await setInput(page, 'group-new-name', 'Ops crew');
  await dialogSubmit(page);
  await page.waitFor(`[...document.querySelectorAll('.row-title')].some((e) => e.textContent.trim() === 'Ops crew')`);
  await page.evaluate(
    `(() => { const b = [...document.querySelectorAll('button')].find((x) => x.textContent.includes('Members') && x.textContent.includes(': Ops crew')); b.click(); return true; })()`,
  );
  await page.waitFor(`!!document.getElementById('group-add')`);
  await pickPerson(page, 'dana', INVITEE.name, 'group-add');
  await clickButton(page, 'Add to group');
  await page.waitFor(`(document.querySelector('.members')?.textContent ?? '').includes(${q(INVITEE.name)})`);
  check(true, 'the owner creates a group and adds the invited person to it');

  await navigateTo(page, '/manage/spaces');
  await clickButton(page, 'New team space');
  await page.waitFor(`!!document.getElementById('space-new-name')`);
  await setInput(page, 'space-new-name', 'Lab');
  await dialogSubmit(page);
  await page.waitFor(`[...document.querySelectorAll('.row-title')].some((e) => e.textContent.trim() === 'Lab')`);
  check(true, 'the owner creates a team space');
}

// p1Setup gives the member a 1 MB file in My files, through the account key, before the owner sets its limit. The
// key reaches every folder, so the member's personal folder is found by its owner.
async function p1Setup(base, accountKey, memberID) {
  const list = await api(base, 'GET', '/api/v1/folders', accountKey);
  const mine = (list.items ?? []).find((f) => f.kind === 'personal' && f.owner_user_id === memberID);
  if (!mine) throw new Error('the member has no personal folder in the listing');
  await rawPut(base, `/api/v1/folders/${mine.id}/files?path=%2Fp1-ledger.bin`, accountKey, randomBytes(1_000_000), 'application/octet-stream');
}

// p1OwnerChecks: the owner sets the member's own limit in the person dialog, to 0.01 GB (10 MB; the interface names
// limits in GB and TB only). Then Manage > Storage shows the member's total, and no file name from its My files.
async function p1OwnerChecks(page) {
  await navigateTo(page, '/manage/people');
  await page.waitFor(
    `location.pathname === '/manage/people' && [...document.querySelectorAll('.row-title')].some((e) => e.textContent.includes(${q(MEMBER.name)}))`,
  );
  await page.evaluate(
    `(() => { const b = [...document.querySelectorAll('button')].find((x) => x.textContent.includes(${q(': ' + MEMBER.name)})); b.click(); return true; })()`,
  );
  await page.waitFor(`!!document.getElementById('person-limit')`);
  await clickButton(page, 'Change limit');
  await page.waitFor(`!!document.querySelector('[role="dialog"] input[aria-label="Amount"]')`);
  await setValue(page, '[role="dialog"] input[aria-label="Amount"]', '0.01');
  await clickButton(page, 'Save limit');
  await page.waitFor(`(document.querySelector('[role="dialog"] .alert.success')?.textContent ?? '') === 'Limit saved.'`);
  await page.waitFor(`(document.getElementById('person-limit')?.textContent ?? '').includes('10 MB')`);
  check(true, 'the owner sets the member\'s limit to 10 MB in the person dialog');
  await press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  await navigateTo(page, '/manage/storage');
  await page.waitFor(
    `document.querySelector('h1')?.textContent.trim() === 'Storage' && [...document.querySelectorAll('#people-storage tbody tr')].some((r) => r.textContent.includes(${q(MEMBER.name)}))`,
    15000,
  );
  const row = await page.evaluate(
    `[...document.querySelectorAll('#people-storage tbody tr')].find((r) => r.textContent.includes(${q(MEMBER.name)})).textContent`,
  );
  check(row.includes('1 MB') && row.includes('10 MB'), `Manage > Storage shows the member's total and limit (${row.replace(/\s+/g, ' ').trim()})`);
  const content = await page.evaluate(`document.querySelector('.content').textContent`);
  check(!content.includes('p1-ledger.bin'), 'Manage > Storage shows no file name from the member\'s My files');
}

// p1MemberChecks: the member, over its limit, uploads a 9.5 MB file to My files. The server refuses the creation,
// and the interface says so. Then the member's /storage page shows the meter and the largest file it holds.
async function p1MemberChecks(page, { dir }) {
  const over = join(dir, 'p1-over.bin');
  await writeFile(over, randomBytes(9_500_000));
  await clickOn(page, `.sidenav a[href="/files"]`);
  await page.waitFor(`/^\\/files\\/fld_/.test(location.pathname) && !!document.querySelector('.toolbar input[type="file"]')`);
  await page.setFiles('.toolbar input[type="file"]:not([webkitdirectory])', [over]);
  await page.waitFor(
    `[...document.querySelectorAll('[role="alert"]')].some((e) => e.textContent.toLowerCase().includes('storage limit'))`,
    20000,
  );
  const alert = await page.evaluate(
    `[...document.querySelectorAll('[role="alert"]')].map((e) => e.textContent.trim()).find((t) => t.toLowerCase().includes('storage limit'))`,
  );
  check(!!alert, `the member's upload over its limit is refused, with the message (${alert})`);
  check(!(await page.evaluate(`!!document.querySelector(${q(rowSel('p1-over.bin'))})`)), 'the refused file is not listed in My files');

  await clickOn(page, `.sidenav a[href="/storage"]`);
  await page.waitFor(`location.pathname === '/storage' && !!document.querySelector('.content [role="meter"]')`);
  const max = await page.evaluate(`document.querySelector('.content [role="meter"]').getAttribute('aria-valuemax')`);
  check(max === '10000000', `the storage page shows the meter against the 10 MB limit (aria-valuemax ${max})`);
  await page.waitFor(
    `[...document.querySelectorAll('.storage-table th')].some((th) => th.textContent.includes('p1-ledger.bin'))`,
    15000,
  );
  check(true, 'the storage page lists the member\'s largest file');
}

// adminChecks is the installation admin's tenant pages, end to end: a new tenant whose first owner gets a link shown
// once, and an API key for it shown once. The key then works for the API, and is gone from the page once its dialog closes.
async function adminChecks(page, { base, adminKey }) {
  await page.waitFor(`location.pathname === '/admin/tenants' && !!document.querySelector('.admin-badge')`);
  check(true, 'an admin lands on the tenants list, under the Admin badge');

  await clickButton(page, 'New tenant');
  await page.waitFor(`!!document.getElementById('new-tenant-name')`);
  await setInput(page, 'new-tenant-name', TENANT.name);
  await setInput(page, 'new-tenant-owner-email', TENANT.owner.email);
  await setInput(page, 'new-tenant-owner-name', TENANT.owner.name);
  await setInput(page, 'new-tenant-limit', '5');
  await clickOn(page, `input[name="how"][value="link"]`);
  await dialogSubmit(page);
  await page.waitFor(`!!document.getElementById('tenant-link-value') || !!document.querySelector('[role="dialog"] .alert.error')`);
  const createdError = await page.evaluate(`document.querySelector('[role="dialog"] .alert.error')?.textContent ?? ''`);
  if (createdError) throw new Error(`the tenant was not created: ${createdError}`);
  await page.waitFor(`!!document.getElementById('tenant-link-value')`);
  const link = await page.evaluate(`document.getElementById('tenant-link-value').value`);
  check(link.includes('/verify#token='), 'a new tenant shows its first owner a link');
  await clickButton(page, 'I have copied it');
  await page.waitFor(`!document.getElementById('tenant-link-value')`);
  check(
    !(await page.evaluate(`document.body.textContent.includes('#token=')`)),
    'the link is not on the page once its dialog closes',
  );

  const tenantID = (await api(base, 'GET', '/api/v1/accounts', adminKey)).items.find((a) => a.name === TENANT.name)?.id;
  if (!tenantID) throw new Error('the new tenant is not listed');
  await navigateTo(page, `/admin/tenants/${tenantID}?tab=keys`);
  await page.waitFor(`location.pathname === '/admin/tenants/${tenantID}'`);
  await clickButton(page, 'New key');
  await page.waitFor(`!!document.getElementById('new-key-name')`);
  await setInput(page, 'new-key-name', 'CI');
  await dialogSubmit(page);
  await page.waitFor(`!!document.getElementById('new-key-once-value')`);
  const secret = await page.evaluate(`document.getElementById('new-key-once-value').value`);
  check(secret.length >= 20, 'the new API key is shown in its dialog');
  await clickButton(page, 'I have copied it');
  await page.waitFor(`!document.getElementById('new-key-once-value')`);
  check(
    !(await page.evaluate(`document.body.innerHTML.includes(${q(secret)})`)),
    'the key is not on the page once its dialog closes',
  );
  await api(base, 'GET', `/api/v1/accounts/${tenantID}/usage`, secret);
  check(true, 'the key the dialog showed works for the API');
}

// memberNotFound is a member opening the owner's pages: the not-found view, and no Manage link.
async function memberNotFound(page) {
  await navigateTo(page, '/manage');
  await page.waitFor(`document.querySelector('h1')?.textContent === 'Page not found'`);
  const manageLink = await page.evaluate(`!!document.querySelector('.sidenav a[href="/manage"]')`);
  check(!manageLink, 'a member who opens /manage sees the not-found view, and no Manage link');
}

// liveText reads what the open dialog announced last.
const liveText = `(document.querySelector('[role="dialog"] .live')?.textContent ?? '')`;

// sharingChecks is the sharing interface's end-to-end path. The owner shares a folder with the member as a viewer
// (no mail), the member sees it and cannot change it, the owner raises it to editor, shares with a group with an
// expiry, the member uploads, asks for access to a file it cannot reach, the owner approves, the owner hides and
// shows a space, and turns on no-download for viewers.
async function sharingChecks(page, { base, dir, accountID, accountKey, memberID, teamB, sink }) {
  // Set up through the API: a team space the member has no role on, with a directory and a file in it.
  const teamC = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'Board Room' })).id;
  await api(base, 'POST', `/api/v1/folders/${teamC}/directories`, accountKey, { path: '/Board', parents: false });
  await rawPut(base, `/api/v1/folders/${teamC}/files?path=%2FBoard%2Fnotes.txt`, accountKey, 'notes for the board\n');
  await rawPut(base, `/api/v1/folders/${teamC}/files?path=%2Fsecret.txt`, accountKey, 'the file behind the request\n');
  const group = await api(base, 'POST', `/api/v1/accounts/${accountID}/groups`, accountKey, { name: 'Field team' });
  await api(base, 'PUT', `/api/v1/accounts/${accountID}/groups/${group.id}/members/${memberID}`, accountKey);
  const top = (await api(base, 'GET', `/api/v1/folders/${teamC}/children?path=%2F`, accountKey)).items;
  const board = top.find((n) => n.name === 'Board').id;
  const secret = top.find((n) => n.name === 'secret.txt').id;
  const mate = join(dir, 'mate.txt');
  await writeFile(mate, 'from the member, into the shared folder\n');

  // The owner shares Board with the member as a viewer, without mail. Then hides Team B and shows it again.
  await signOut(page);
  await signIn(page, OWNER);
  await clickOn(page, `.teams a[href="/files/${teamC}"]`);
  await page.waitFor(`location.pathname === '/files/${teamC}'`);
  await rowAppears(page, 'Board');
  await selectRow(page, 'Board');
  await clickButton(page, 'Share');
  await page.waitFor(`!!document.getElementById('share-who')`);
  await pickPerson(page, 'mateo', MEMBER.name);
  await setCheckbox(page, 'share-notify', false);
  await dialogSubmit(page);
  await page.waitFor(`${liveText}.includes('Shared with ${MEMBER.name} as Can view')`);
  check(true, 'the owner shares a folder with the member as a viewer, without mail');
  check(
    await page.evaluate(`document.querySelector('[role="dialog"]').textContent.includes('${MEMBER.name}')`),
    'the share dialog lists the member among the people with access',
  );
  await press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  await clickOn(page, `button[aria-label="Options for Team B"]`);
  await clickMenuItem(page, 'Hide');
  await page.waitFor(`!document.querySelector('.teams a[href="/files/${teamB}"]')`);
  check(true, 'the owner hides a team space, and it leaves the list');
  await clickOn(page, `.nav-title + .teams + .check input`);
  await page.waitFor(`!!document.querySelector('.teams a[href="/files/${teamB}"]')`);
  await clickOn(page, `button[aria-label="Options for Team B"]`);
  await clickMenuItem(page, 'Show');
  // Wait until both lists have been read again: the space is listed once, and no list still marks it hidden.
  await page.waitFor(`document.querySelectorAll('.teams a[href="/files/${teamB}"]').length === 1 && !document.querySelector('.teams .count')`);
  check(true, 'the hidden space comes back when shown again');

  // The member sees the folder in Shared with me, opens it, and has no upload, rename or delete.
  await signOut(page);
  await signIn(page, MEMBER);
  await clickOn(page, `.sidenav a[href="/shared"]`);
  await page.waitFor(`location.pathname === '/shared' && document.querySelector('.shared a[href="/n/${board}"]')`);
  check(true, 'Shared with me lists the folder the owner shared');
  await clickOn(page, `.shared a[href="/n/${board}"]`);
  await page.waitFor(`location.pathname === '/files/${teamC}/${board}'`);
  await rowAppears(page, 'notes.txt');
  const viewerButtons = await buttonTexts(page);
  check(!viewerButtons.includes('Upload files') && !viewerButtons.includes('New folder'), 'a viewer of the shared folder cannot upload or make folders');
  await selectRow(page, 'notes.txt');
  const viewerSel = await buttonTexts(page);
  check(!viewerSel.includes('Rename') && !viewerSel.includes('Delete'), 'a viewer cannot rename or delete');
  check(viewerSel.includes('Download'), 'a viewer can download, since the space does not forbid it');

  // The owner raises the member to editor, shares with the group with an expiry, and sees both in the list.
  await signOut(page);
  await signIn(page, OWNER);
  await navigateTo(page, `/files/${teamC}`);
  await rowAppears(page, 'Board');
  await selectRow(page, 'Board');
  await clickButton(page, 'Share');
  await page.waitFor(`!!document.getElementById('share-who')`);
  // The people list loads after the dialog opens; the member's row is there once the list is.
  await page.waitFor(`!!document.getElementById(${q(`role-${memberID}`)})`);
  await page.evaluate(
    `(() => { const el = document.getElementById(${q(`role-${memberID}`)}); el.value = 'editor'; el.dispatchEvent(new Event('change', { bubbles: true })); return true; })()`,
  );
  await page.waitFor(`${liveText}.includes('is now Can edit')`);
  check(true, 'the owner raises the member to editor');
  await pickPerson(page, 'field', 'Field team');
  const thirty = new Date(Date.now() + 30 * 24 * 3600 * 1000).toISOString().slice(0, 10);
  await setInput(page, 'share-expires', thirty);
  await dialogSubmit(page);
  await page.waitFor(`${liveText}.includes('Shared with Field team as Can view')`);
  check(
    !(await page.evaluate(liveText)).includes('No email was sent'),
    'the reply carries no "no email" notice, since the mail went out',
  );
  check(
    (await waitForMail(sink, MEMBER.email)).to.includes(MEMBER.email),
    'the member the group reaches is mailed the share',
  );
  check(
    await page.evaluate(`document.querySelector('[role="dialog"]').textContent.includes('Ends ')`),
    'the group share shows its expiry',
  );
  await press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  // The member, now an editor, uploads into the folder.
  await signOut(page);
  await signIn(page, MEMBER);
  await navigateTo(page, `/files/${teamC}/${board}`);
  await rowAppears(page, 'notes.txt');
  await page.setFiles('.toolbar input[type="file"]:not([webkitdirectory])', [mate]);
  await rowAppears(page, 'mate.txt');
  check(true, 'an editor of the shared folder can upload into it');

  // The member asks for editor on a file it cannot reach. The form says the same whatever the answer is.
  await navigateTo(page, `/n/${secret}`);
  await page.waitFor(`!!document.getElementById('request-role')`);
  await setSelect(page, '#request-role', 'editor');
  await setInput(page, 'request-message', 'Need it for the review');
  await page.evaluate(`document.querySelector('#request-role').form.requestSubmit(), true`);
  await page.waitFor(`!!document.querySelector('.request .sent')`);
  check(
    (await page.evaluate(`document.querySelector('.request .sent').textContent`)) ===
      'If this item exists, the people who manage it have been asked.',
    'the request form gives the one confirmation',
  );

  // The owner sees the request with its count, approves it, and the member gets the file.
  await signOut(page);
  await signIn(page, OWNER);
  await page.waitFor(`/1 waiting/.test(document.querySelector('.sidenav a[href="/requests"]')?.textContent ?? '')`);
  check(true, 'the owner sees a requests entry with a count');
  await clickOn(page, `.sidenav a[href="/requests"]`);
  await page.waitFor(`location.pathname === '/requests' && !!document.querySelector('.requests .request')`);
  await page.waitFor(`document.querySelector('.requests .request').textContent.includes('secret.txt')`);
  await page.waitFor(
    `(() => { const b = [...(document.querySelector('.requests .request')?.querySelectorAll('button') ?? [])].find((x) => x.textContent.trim() === 'Approve'); if (!b) return false; b.click(); return true; })()`,
  );
  await page.waitFor(`document.querySelector('.alert.success') && document.querySelector('.alert.success').textContent.includes('Access given to ${MEMBER.name}')`);
  check(true, 'the owner approves the request');

  // The owner turns on no-download for viewers in Team B.
  await clickOn(page, `button[aria-label="Options for Team B"]`);
  await clickMenuItem(page, 'Space settings');
  await page.waitFor(`!!document.querySelector('[role="dialog"]')`);
  await page.waitFor(
    `(() => { const l = [...document.querySelectorAll('[role="dialog"] label.switch')].find((x) => x.textContent.includes("can't download")); if (!l) return false; l.querySelector('input').click(); return true; })()`,
  );
  await page.waitFor(`(document.querySelector('[role="dialog"] .live')?.textContent ?? '') === 'Saved.'`);
  check(true, 'the owner turns on no-download for viewers in Team B');
  await press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  // The member opens the file it was approved for; and as a viewer of Team B it sees no Download button.
  await signOut(page);
  await signIn(page, MEMBER);
  await navigateTo(page, `/n/${secret}`);
  await page.waitFor(`document.body.textContent.includes('secret.txt')`, 15000);
  check(true, 'the approved member opens the file it asked for');
  await clickOn(page, `.teams a[href="/files/${teamB}"]`);
  await page.waitFor(`location.pathname === '/files/${teamB}'`);
  await rowAppears(page, 'readme.txt');
  await selectRow(page, 'readme.txt');
  const noDl = await buttonTexts(page);
  check(!noDl.includes('Download'), 'a viewer of a no-download space sees no Download button');
}

// clickMenuItem opens nothing: it clicks the menu item with this text, in the open team menu.
async function clickMenuItem(page, text) {
  await page.waitFor(
    `(() => { const b = [...document.querySelectorAll('[role="menu"] [role="menuitem"]')].find((x) => x.textContent.trim() === ${q(text)}); if (!b) return false; b.click(); return true; })()`,
    10000,
  );
}

async function setInput(page, id, value) {
  await page.evaluate(`(() => {
    const el = document.getElementById(${JSON.stringify(id)});
    el.focus();
    el.value = ${JSON.stringify(value)};
    el.dispatchEvent(new Event('input', { bubbles: true }));
    return true;
  })()`);
}

// setValue types into the field the selector names, as setInput does for an ID.
async function setValue(page, selector, value) {
  await page.evaluate(`(() => {
    const el = document.querySelector(${JSON.stringify(selector)});
    el.focus();
    el.value = ${JSON.stringify(value)};
    el.dispatchEvent(new Event('input', { bubbles: true }));
    return true;
  })()`);
}

async function setSelect(page, selector, value) {
  await page.evaluate(`(() => {
    const el = document.querySelector(${JSON.stringify(selector)});
    el.value = ${JSON.stringify(value)};
    el.dispatchEvent(new Event('change', { bubbles: true }));
    return true;
  })()`);
}

main().catch((err) => {
  console.error(err.message);
  process.exit(1);
});
