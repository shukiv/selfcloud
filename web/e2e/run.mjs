// SPDX-License-Identifier: AGPL-3.0-or-later

// The end-to-end smoke test behind `make e2e`. It starts bin/selfcloud on a temporary data directory,
// makes an account, an owner and a member through the CLI and the API, and drives headless Chrome
// through the web interface: sign in, check the shell and the name, switch to Hebrew (and check the
// layout turns right to left and the choice is saved), switch the theme, and sign out. It fails on
// any console error, uncaught exception, policy violation, or request to another origin.
//
// Needs a Chrome or Chromium binary: set SELFCLOUD_E2E_BROWSER to its path.
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
      await api(base, 'POST', '/api/v1/auth/verify', null, {
        token: created.verification.token,
        password: u.password,
      });
    }
    check(true, 'account, owner and member created and verified through the API');

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

    // Policy: no console error, no CSP violation, no request to another origin.
    check(page.errors.length === 0, `no console errors or policy violations (${page.errors.join(' | ') || 'none'})`);
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

async function setInput(page, id, value) {
  await page.evaluate(`(() => {
    const el = document.getElementById(${JSON.stringify(id)});
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
