// SPDX-License-Identifier: AGPL-3.0-or-later

// The end-to-end smoke test behind `make e2e`. It starts bin/selfcloud on a temporary data directory,
// makes an account, an owner and a member through the CLI and the API, and drives headless Chrome
// through the web interface: sign in, check the shell and the name, switch to Hebrew (and check the
// layout turns right to left and the choice is saved), switch the theme, and sign out. It fails on
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

const execFileP = promisify(execFile);
const repo = fileURLToPath(new URL('../../', import.meta.url));
const bin = process.env.SELFCLOUD_BIN ?? join(repo, 'bin', 'selfcloud');

const OWNER = { email: 'olivia@example.com', name: 'Olivia Owner', password: 'correct horse battery staple' };
const MEMBER = { email: 'mateo@example.com', name: 'Mateo Member', password: 'correct horse battery staple' };
const BRAND = { name: 'E2E Drive', accent: '#0F766E' };

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

  const admin = await execFileP(bin, ['admin', 'create-key', '-name', 'e2e', '-config', cfg]);
  const adminKey = admin.stdout.trim();
  const made = await execFileP(bin, ['admin', 'create-account', '-name', 'E2E Company', '-config', cfg]);
  const accountID = /account_id=(\S+)/.exec(made.stdout)?.[1];
  const accountKey = /account_key=(\S+)/.exec(made.stdout)?.[1];
  if (!accountID || !accountKey || !adminKey) throw new Error('the CLI did not print the account');

  const server = spawn(bin, ['serve', '-config', cfg], { stdio: ['ignore', 'pipe', 'pipe'] });
  let serverLog = '';
  server.stdout.on('data', (d) => (serverLog += d));
  server.stderr.on('data', (d) => (serverLog += d));
  let browser = null;
  let page = null;
  try {
    await waitForServer(base);

    // The users: an owner and a member, each verified through the link the API returns.
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
    await page.evaluate(`document.querySelector('form button[type="submit"]').click(), true`);
    await page.waitFor(`!!document.querySelector('.topbar')`);
    const who = await page.evaluate(`document.querySelector('.user-menu > button').textContent`);
    check(who.includes(OWNER.name), `the shell shows the signed-in name (${who.trim()})`);
    const brandName = await page.evaluate(`document.querySelector('.product-name').textContent`);
    check(brandName.includes(BRAND.name), 'the header shows the configured brand name');
    const accent = await page.evaluate(`document.documentElement.style.getPropertyValue('--brand-primary')`);
    check(accent === BRAND.accent, `the accent is the configured colour (${accent})`);
    const adminLinked = await page.evaluate(
      `[...document.querySelectorAll('.sidenav a')].some((a) => a.getAttribute('href') === '/admin')`,
    );
    check(adminLinked, 'an owner sees the Admin link');

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
    await page.evaluate(`document.querySelector('input[name="theme"][value="dark"]').click(), true`);
    await page.waitFor(`document.documentElement.dataset.theme === 'dark'`);
    const bg = await page.evaluate(`getComputedStyle(document.body).backgroundColor`);
    check(bg === 'rgb(11, 17, 32)', `dark theme applies its background (${bg})`);

    // The page fits the phone width: no horizontal scroll.
    const overflow = await page.evaluate(
      `document.documentElement.scrollWidth - document.documentElement.clientWidth`,
    );
    check(overflow <= 0, `no horizontal scroll at 375px (overflow ${overflow}px)`);

    // Sign out.
    await page.evaluate(`document.querySelector('.user-menu > button').click(), true`);
    await page.waitFor(`!!document.querySelector('.menu-item:last-child')`);
    await page.evaluate(`document.querySelector('.menu-item:last-child').click(), true`);
    await page.waitFor(`location.pathname === '/login' && !!document.getElementById('login-email')`);
    check(true, 'sign out returns to the sign-in page');

    // A member sees no Admin link.
    await setInput(page, 'login-email', MEMBER.email);
    await setInput(page, 'login-password', MEMBER.password);
    await page.evaluate(`document.querySelector('form button[type="submit"]').click(), true`);
    await page.waitFor(`!!document.querySelector('.topbar')`);
    const memberAdmin = await page.evaluate(
      `[...document.querySelectorAll('.sidenav a')].some((a) => a.getAttribute('href') === '/admin')`,
    );
    check(!memberAdmin, 'a member sees no Admin link');

    // The file browser, as a member who edits Team A and only reads Team B.
    await fileBrowserChecks(page, { base, dir, teamA, teamB });

    // Policy: no console error, no CSP violation, no request to another origin.
    check(page.errors.length === 0, `no console errors or policy violations (${page.errors.join(' | ') || 'none'})`);
    check(page.expectedConflicts.length === 1, `exactly one upload met a taken name, and was asked about it (${page.expectedConflicts.length})`);
    const foreign = page.requests.filter((u) => !u.startsWith(base + '/') && u !== base);
    check(foreign.length === 0, `every request is same-origin (${foreign.length} foreign)`);
    console.log(`requests seen: ${page.requests.length}, all on ${base}`);
    console.log('e2e passed');
  } finally {
    page?.close();
    browser?.kill();
    server.kill();
    if (process.env.SELFCLOUD_E2E_KEEP !== '1') {
      await new Promise((r) => setTimeout(r, 200));
      await rm(dir, { recursive: true, force: true });
    }
    if (results.some((r) => !r.ok)) console.error(serverLog);
  }
}

// rawPut sends a body that is not JSON, as the small-file route takes it.
async function rawPut(base, path, key, body) {
  const res = await fetch(base + path, {
    method: 'POST',
    headers: { Authorization: `Bearer ${key}`, 'Content-Type': 'text/plain' },
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
  await page.evaluate(`document.querySelector('.sidenav a[href="/files"]').click(), true`);
  await page.waitFor(`/^\\/files\\/fld_/.test(location.pathname)`);
  check(true, 'My files opens the personal folder');
  await page.evaluate(`document.querySelector('.teams a[href="/files/${teamA}"]').click(), true`);
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
  await new Promise((r) => setTimeout(r, 1500));
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
  await page.evaluate(`document.querySelector('.sidenav a[href="/trash"]').click(), true`);
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
  await page.evaluate(`document.querySelector('.teams a[href="/files/${teamB}"]').click(), true`);
  await page.waitFor(`location.pathname === '/files/${teamB}'`);
  await rowAppears(page, 'readme.txt');
  await selectRow(page, 'readme.txt');
  const viewer = await buttonTexts(page);
  for (const hidden of ['New folder', 'Upload files', 'Upload folder', 'Rename', 'Delete']) {
    check(!viewer.includes(hidden), `a viewer of Team B has no ${hidden}`);
  }
  check(viewer.includes('Download'), 'a viewer of Team B can download');
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
