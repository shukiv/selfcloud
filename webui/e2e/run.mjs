// SPDX-License-Identifier: AGPL-3.0-or-later

// The browser smoke test of the React interface. It starts a server on a temporary data directory,
// makes an account with an owner and a member, and drives headless Chrome through the pages built so
// far: sign-in, the shell at desktop and phone width, the language and theme choices, the placeholder
// of a page not built yet, and a sign-in link. Every content-policy violation counts as an error.
//
// Run with make e2e-react, which builds the binary with the reactui tag and the interface first.
import { execFile, spawn } from 'node:child_process';
import { access, constants, mkdtemp, rm, writeFile } from 'node:fs/promises';
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
    await page.goto(base + '/files');
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
