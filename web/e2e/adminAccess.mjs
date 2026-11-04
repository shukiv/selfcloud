// SPDX-License-Identifier: AGPL-3.0-or-later

// The installation admin's access to a tenant user, end to end, against a real server with admin.tenant_access on and mail
// going to a local sink. The run takes the admin through the three ways in (a reset link, a typed password, sign in as), the
// banner and its refusals, and the forced change of password. At each screen it saves a screenshot at desktop and phone
// width, in light and dark. The labels are read from the language's catalogue, so the run clicks what the person sees.
//
// Run once per language: node e2e/adminAccess.mjs en, then node e2e/adminAccess.mjs he. Screenshots go to the folder named by
// SELFCLOUD_SHOTS (created if missing), or to a temporary folder. The server is bin/selfcloud (SELFCLOUD_BIN overrides it).
// It needs Chrome or Chromium, found on PATH or named by SELFCLOUD_E2E_BROWSER, as run.mjs does.
import { execFile, spawn } from 'node:child_process';
import { access, constants, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { delimiter, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { freePort, launchBrowser, Page } from './cdp.mjs';
import { mailSink, waitForMail } from './mailsink.mjs';
import { accountName } from './media.mjs';

const execFileP = promisify(execFile);
const repo = fileURLToPath(new URL('../../', import.meta.url));
const bin = process.env.SELFCLOUD_BIN ?? join(repo, 'bin', 'selfcloud');
const LANG = process.argv[2] ?? 'en';
if (!['en', 'he'].includes(LANG)) throw new Error('the language is en or he');
const SHOTS = process.env.SELFCLOUD_SHOTS ?? join(tmpdir(), 'selfcloud-admin-shots');
const CAT = JSON.parse(await readFile(new URL(`../src/i18n/${LANG}.json`, import.meta.url), 'utf8'));

// L is the catalogue text for a key, with its {name} style values filled in.
function L(key, params = {}) {
  if (!(key in CAT)) throw new Error(`no text for ${key}`);
  return Object.entries(params).reduce((s, [k, v]) => s.replaceAll(`{${k}}`, v), CAT[key]);
}

const PASSWORD = 'correct horse battery staple';
const ADMIN = { email: 'ada@example.com', name: 'Ada Admin', password: 'ada admin passphrase 2026' };
const MATEO = { email: 'mateo@example.com', name: 'Mateo Member', password: PASSWORD };
const RITA = { email: 'rita@example.com', name: 'Rita Link', password: PASSWORD };
const DANA = { email: 'dana@example.com', name: 'Dana Typed', password: PASSWORD };
const TEMP = 'temporary words 2026';
const PERMANENT = 'a permanent passphrase 2026';
const BY_LINK = 'a passphrase set by link 2026';
const BY_EMAIL = 'a passphrase set by email 2026';
const WRONG = 'not the admin password';

const results = [];
// acceptSwitchIfAsked: a reset link opened in a browser signed in as the admin asks before it switches (decision 74).
// The member's link is the reason for the switch, so the step accepts it: the dialog's primary button, which is "Switch to
// <member>" in every language. A browser with no session goes straight on.
async function acceptSwitchIfAsked(page) {
  await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]') || !!document.querySelector('.ant-modal .ant-btn-primary')`, 15000);
  if (!(await page.evaluate(`!!document.querySelector('[data-testid="account-menu"]')`))) {
    await page.evaluate(`document.querySelector('.ant-modal .ant-btn-primary').click()`);
  }
}

function check(ok, what) {
  results.push({ ok: ok === true, what });
  console.log(`${ok === true ? 'PASS' : 'FAIL'} ${what}`);
}

const q = (v) => JSON.stringify(v);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

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
    await sleep(100);
  }
}

// api calls the API as an account key or with no key, and throws on a refusal with the answer's text (for setup only).
async function api(base, method, path, key, body) {
  const headers = {};
  if (key) headers.Authorization = `Bearer ${key}`;
  if (body) headers['Content-Type'] = 'application/json';
  const res = await fetch(base + path, { method, headers, body: body ? JSON.stringify(body) : undefined });
  const text = await res.text();
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${text}`);
  return text ? JSON.parse(text) : null;
}

// ---- the page: the same few helpers the other runs use, with the labels taken from the catalogue ----

async function open(page, path) {
  const sep = path.includes('?') ? '&' : '?';
  await page.goto(page.base + path + sep + 'lang=' + LANG);
}

async function signIn(page, who) {
  await open(page, '/login');
  await page.waitFor(`!!document.getElementById('login-email')`);
  await page.setInput('#login-email', who.email);
  await page.setInput('#login-password', who.password);
  await page.evaluate(`document.querySelector('form button[type="submit"]').click()`);
}

// clickButton clicks the enabled button in scope (a CSS selector) whose text is the label, or starts with it. The isolate marks
// of a value in Hebrew (decision 67) are read through, so the label compares as the words the catalogue holds.
async function clickButton(page, label, scope = 'body', exact = true, timeout = 10000) {
  await page.waitFor(
    `(() => { const s = document.querySelector(${q(scope)}); if (!s) return false;
      const text = (x) => x.textContent.replace(/[\u2066-\u2069]/g, '').trim();
      const b = [...s.querySelectorAll('button')].find((x) => !x.disabled && (${exact} ? text(x) === ${q(label)} : text(x).startsWith(${q(label)})));
      if (!b) return false; b.click(); return true; })()`,
    timeout,
  );
}

// clickRowButton clicks the button of the row that holds the person's name. The button's label is the start of its text: it
// carries a visually hidden suffix with the name.
async function clickRowButton(page, name, label) {
  await page.waitFor(
    `(() => { const row = [...document.querySelectorAll('li')].find((r) => r.textContent.includes(${q(name)}) && [...r.querySelectorAll('button')].some((b) => (b.getAttribute('aria-label') || b.textContent.trim()).startsWith(${q(label)})));
      if (!row) return false; [...row.querySelectorAll('button')].find((b) => (b.getAttribute('aria-label') || b.textContent.trim()).startsWith(${q(label)})).click(); return true; })()`,
    10000,
  );
}

// clickRadio chooses the option of a radio group whose label is the text.
async function clickRadio(page, label) {
  await page.waitFor(
    `(() => { const w = [...document.querySelectorAll('.ant-modal .ant-radio-wrapper')].find((x) => x.textContent.replace(/[\\u2066-\\u2069]/g, '').trim() === ${q(label)});
      if (!w) return false; w.click(); return true; })()`,
  );
}

async function waitText(page, text, timeout = 10000) {
  // The page's text reads through the isolate marks of a value in Hebrew (decision 67).
  await page.waitFor(`document.body.innerText.replace(/[\\u2066-\\u2069]/g, '').includes(${q(text)})`, timeout);
}

async function textOf(page, selector) {
  return page.evaluate(`document.querySelector(${q(selector)})?.textContent ?? ''`);
}

async function accountMenu(page) {
  return accountName(page);
}

async function signOutFromMenu(page) {
  await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]')`, 15000);
  await page.evaluate(`document.querySelector('[data-testid="account-menu"]').click()`);
  await page.waitFor(`[...document.querySelectorAll('.ant-dropdown-menu-item')].some((x) => x.textContent.trim() === ${q(L('user.signOut'))})`);
  await page.evaluate(`[...document.querySelectorAll('.ant-dropdown-menu-item')].find((x) => x.textContent.trim() === ${q(L('user.signOut'))}).click()`);
  await page.waitFor(`location.pathname === '/login' && !!document.getElementById('login-email')`, 15000);
}

// ---- screenshots: each screen at desktop and phone width, light and dark ----

const WIDTHS = [
  { name: 'desktop', width: 1280, height: 800, mobile: false },
  { name: 'phone', width: 390, height: 844, mobile: true },
];
const THEMES = ['light', 'dark'];
const shots = [];

async function shot(page, screen) {
  await mkdir(SHOTS, { recursive: true });
  for (const w of WIDTHS) {
    for (const theme of THEMES) {
      await page.send('Emulation.setDeviceMetricsOverride', { width: w.width, height: w.height, deviceScaleFactor: 1, mobile: w.mobile });
      await page.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: theme }] });
      await sleep(400);
      const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
      const file = join(SHOTS, `${screen}-${w.name}-${theme}-${LANG}.png`);
      await writeFile(file, Buffer.from(data, 'base64'));
      shots.push(file);
    }
  }
  // Back to desktop, light, for the steps that follow.
  await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
  await page.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'light' }] });
}

// ---- the server ----

async function startServer(dir, sink, port) {
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
      // Mail goes to the sink, with no TLS and no login, as the server's smtp_tls none allows on localhost.
      'mail:',
      '  smtp_host: "127.0.0.1"',
      `  smtp_port: ${sink.port}`,
      '  smtp_tls: "none"',
      '  from: "Selfcloud <drive@example.com>"',
      'ui:',
      '  brand:',
      '    name: "Admin Access Drive"',
      '    accent: "#0F766E"',
      // The three ways in to a tenant user: a reset link, a typed password and sign-in as. Off by default.
      'admin:',
      '  tenant_access: true',
      '',
    ].join('\n'),
  );
  return { base, cfg };
}

async function main() {
  const dir = await mkdtemp(join(tmpdir(), 'selfcloud-admin-'));
  const port = await freePort();
  const sink = await mailSink();
  const { base, cfg } = await startServer(dir, sink, port);

  const made = await execFileP(bin, ['admin', 'create-account', '-name', 'Acme Ltd', '-config', cfg]);
  const accountID = /account_id=(\S+)/.exec(made.stdout)?.[1];
  const accountKey = /account_key=(\S+)/.exec(made.stdout)?.[1];
  if (!accountID || !accountKey) throw new Error('the CLI did not print the account');
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
    // Three members of the tenant, each verified through the link the API gives (as an owner invites them).
    for (const who of [MATEO, RITA, DANA]) {
      const created = await api(base, 'POST', `/api/v1/accounts/${accountID}/users`, accountKey, {
        email: who.email,
        display_name: who.name,
        role: 'member',
        verification: 'link',
      });
      await api(base, 'POST', '/api/v1/auth/verify', null, { token: created.verification.token, password: who.password });
    }
    check(true, 'the admin and three members are made and verified through the API');

    const launched = await launchBrowser(await findBrowser(), join(dir, 'chrome-profile'));
    browser = launched.proc;
    page = new Page(launched.wsUrl);
    page.base = base;
    // Failed requests the run causes on purpose: the boot's check of the session (a 401 for a visitor), and a wrong admin
    // password (a 403 on sign in as).
    await page.start([
      { name: 'signed-out-boot', matches: (e) => (e.url ?? '').endsWith('/api/v1/me') },
      { name: 'wrong-admin-password', matches: (e) => (e.url ?? '').endsWith('/sign-in') },
    ]);

    await runSteps(page, { base, accountID, sink });
    passed = results.every((r) => r.ok);
  } catch (err) {
    check(false, `the run stopped: ${err.message}`);
  } finally {
    page?.close();
    browser?.kill();
    server.kill();
    sink.close();
    await sleep(200);
    // Chrome can still be writing its profile after the kill, so the removal tries again for a few seconds (as run.mjs).
    if (process.env.SELFCLOUD_E2E_KEEP !== '1') await rm(dir, { recursive: true, force: true, maxRetries: 20, retryDelay: 250 });
    if (!passed) {
      console.error(serverLog);
      if (page?.errors?.length) console.error(`browser errors: ${page.errors.join(' | ')}`);
    }
  }
  console.log(`${results.filter((r) => r.ok).length} of ${results.length} checks passed; ${shots.length} screenshots in ${SHOTS}`);
  if (!passed) process.exit(1);
}

async function runSteps(page, { base, accountID, sink }) {
  const tenantUsers = `/admin/tenants/${accountID}?tab=users`;

  // 1. The admin signs in and opens the tenant's people. Each person has reset and sign-in actions.
  await signIn(page, ADMIN);
  await page.waitFor(`location.pathname.startsWith('/admin/tenants')`, 15000);
  await open(page, tenantUsers);
  await waitText(page, MATEO.name);
  check(
    (await page.evaluate(`[...document.querySelectorAll('li')].some((r) => r.textContent.includes(${q(MATEO.name)}) && [...r.querySelectorAll('button')].some((b) => (b.getAttribute('aria-label') || b.textContent).includes(${q(L('tenant.users.reset'))})))`)) &&
      (await page.evaluate(`[...document.querySelectorAll('li')].some((r) => r.textContent.includes(${q(MATEO.name)}) && [...r.querySelectorAll('button')].some((b) => (b.getAttribute('aria-label') || b.textContent).includes(${q(L('tenant.users.signInAs'))})))`)),
    'the admin sees reset and sign-in actions on a tenant member',
  );
  await shot(page, '01-tenant-people');

  // 2. The reset dialog offers the three ways (mail is configured, and tenant access is on), and says what each does.
  await clickRowButton(page, MATEO.name, L('tenant.users.reset'));
  await page.waitFor(`!!document.querySelector('.ant-modal')`);
  await waitText(page, L('tenant.reset.emailWhat', { email: MATEO.email }));
  check(
    (await page.evaluate(`[...document.querySelectorAll('.ant-modal .ant-radio-wrapper')].length`)) === 3,
    'the reset dialog offers email, a one-time link and a typed password',
  );
  await shot(page, '02-reset-dialog-email');
  await clickRadio(page, L('tenant.reset.typed'));
  await waitText(page, L('tenant.reset.typedWhat'));
  await shot(page, '03-reset-dialog-typed');
  await clickButton(page, L('common.cancel'), '.ant-modal');

  // 3. Sign in as the member: the dialog says what happens, a wrong admin password is refused, and the right one signs in.
  await clickRowButton(page, MATEO.name, L('tenant.users.signInAs'));
  await page.waitFor(`!!document.querySelector('.ant-modal')`);
  await waitText(page, L('tenant.signInAs.what', { name: MATEO.name }));
  await shot(page, '04-sign-in-as-dialog');
  await page.setInput('#signin-as-admin', WRONG);
  await clickButton(page, L('tenant.signInAs.submit', { name: MATEO.name }), '.ant-modal');
  await waitText(page, L('error.adminPassword'));
  check((page.expectedAnswers.get('wrong-admin-password') ?? []).length === 1, 'a wrong admin password is refused once, and the dialog stays open');
  await page.setInput('#signin-as-admin', ADMIN.password);
  await clickButton(page, L('tenant.signInAs.submit', { name: MATEO.name }), '.ant-modal');
  await page.waitFor(`!!document.querySelector('[data-testid="impersonation-banner"]') && location.pathname.startsWith('/files/')`, 15000);
  const banner = await textOf(page, '[data-testid="impersonation-banner"]');
  check(banner.includes(L('impersonation.banner', { name: MATEO.name })), 'signed in as the member, the banner names the member');
  check(
    (await page.evaluate(`!!document.querySelector('[data-testid="impersonation-banner"]')?.querySelector('button')`)) &&
      (await accountMenu(page)).includes(MATEO.name),
    'the banner has the way back, and the account menu shows the member',
  );
  await waitText(page, L('nav.files'));
  const landedAt = await page.evaluate('location.pathname');
  check(landedAt.startsWith('/files/fld_'), `the member lands on My files, with My files in the sidebar (at ${landedAt})`);
  const homeHeights = await page.evaluate(`[document.documentElement.scrollHeight, window.innerHeight, document.body.scrollHeight, document.querySelector('[data-testid="impersonation-banner"]')?.getBoundingClientRect().height]`);
  check(
    homeHeights[0] <= homeHeights[1] + 1,
    `with the banner on, My files makes no page scrollbar (page ${homeHeights[0]}px, window ${homeHeights[1]}px, body ${homeHeights[2]}px, banner ${homeHeights[3]}px)`,
  );
  await shot(page, '05-banner-member-home');

  // 4. The member's change of password is refused, and the page says why.
  await open(page, '/change-password');
  await waitText(page, L('error.impersonation'));
  check(
    (await page.evaluate(`!!document.getElementById('change-current')`)) === false,
    'changing the password while signed in as another person is refused, and says why',
  );
  await shot(page, '06-refused-change-password');

  // 4b. The account menu offers no sign-out while impersonating: going back is the only way out, and the server refuses
  // a sign-out from the session too (the route table's test checks that).
  await open(page, '/');
  await waitText(page, L('nav.files'));
  await page.evaluate(`document.querySelector('[data-testid="account-menu"]').click()`);
  await page.waitFor(`document.querySelectorAll('.ant-dropdown-menu-item').length > 0`, 15000);
  const menuItems = await page.evaluate(
    `[...document.querySelectorAll('.ant-dropdown-menu-item')].map((x) => x.textContent.trim())`,
  );
  check(!menuItems.includes(L('user.signOut')), 'the account menu offers no sign-out while signed in as another person');
  await page.evaluate(`document.activeElement?.blur(); document.body.click()`);

  // 4c. Signed in as the member, the files work for the member (F8): the member makes a file in its own folder, finds it,
  // reads it, downloads it, and makes the preview link the viewer uses. The page's own fetches carry the member's session.
  await open(page, '/files');
  await waitText(page, L('nav.files'));
  check(
    await page.evaluate(`document.documentElement.scrollHeight <= window.innerHeight + 1`),
    'with the banner on, the files list makes no page scrollbar (the page is no taller than the window)',
  );
  await page.evaluate(`window.__probe = null; (async () => {
    const out = { created: false, found: false, read: false, downloaded: false, linkStatus: 0, linkReads: false };
    try {
      const me = await (await fetch('/api/v1/me', { credentials: 'same-origin' })).json();
      const csrf = { 'X-CSRF-Token': me.csrf_token };
      const put = await fetch('/api/v1/folders/' + me.personal_folder_id + '/files?path=' + encodeURIComponent('/impersonation-check.txt'), {
        method: 'POST', credentials: 'same-origin', headers: { ...csrf, 'Content-Type': 'text/plain' }, body: 'impersonation check',
      });
      out.created = put.status === 201;
      const hits = await (await fetch('/api/v1/search?q=impersonation-check', { credentials: 'same-origin' })).json();
      const id = hits.items && hits.items[0] ? hits.items[0].id : '';
      out.found = id !== '';
      if (id) {
        out.read = (await (await fetch('/api/v1/nodes/' + id + '/content', { credentials: 'same-origin' })).text()) === 'impersonation check';
        out.downloaded = (await fetch('/api/v1/nodes/' + id + '/content?disposition=attachment', { credentials: 'same-origin' })).status === 200;
        const link = await fetch('/api/v1/nodes/' + id + '/links', {
          method: 'POST', credentials: 'same-origin', headers: { ...csrf, 'Content-Type': 'application/json' },
          body: JSON.stringify({ disposition: 'inline', ttl: 300 }),
        });
        out.linkStatus = link.status;
        if (link.status === 201) {
          const url = (await link.json()).url;
          out.linkReads = (await (await fetch(url, { credentials: 'same-origin' })).text()) === 'impersonation check';
        }
      }
    } catch (err) { out.error = String(err); }
    window.__probe = out;
  })()`);
  await page.waitFor(`window.__probe !== null`, 30000);
  const probe = await page.evaluate(`window.__probe`);
  check(probe.created && probe.found, `signed in as the member, a file is made and found (${JSON.stringify(probe)})`);
  check(probe.read && probe.downloaded, 'signed in as the member, the file is read and downloaded');
  check(probe.linkStatus === 201 && probe.linkReads, 'signed in as the member, the preview link is made and opens the file');

  // 5. Going back restores the admin's session: the banner is gone and the admin is on the tenants list.
  await clickButton(page, L('impersonation.back'), '[data-testid="impersonation-banner"]');
  await page.waitFor(`location.pathname.startsWith('/admin/tenants') && !document.querySelector('[data-testid="impersonation-banner"]')`, 15000);
  check(true, 'going back restores the admin session, and the banner is gone');

  // 5b. A reset by email: the admin sends it, the mail reaches the member's address in the sink, and the member sets the
  // password from the link in it and signs in. The email reset is not behind admin.tenant_access; it reaches the user.
  await open(page, tenantUsers);
  await waitText(page, MATEO.name);
  await clickRowButton(page, MATEO.name, L('tenant.users.reset'));
  await page.waitFor(`!!document.querySelector('.ant-modal')`);
  await clickRadio(page, L('tenant.reset.email'));
  await clickButton(page, L('tenant.reset.submitEmail'), '.ant-modal');
  const mail = await waitForMail(sink, MATEO.email);
  // The mail is quoted-printable: soft line breaks go, and =3D is the equals sign of the fragment.
  const decoded = mail.data.replace(/=\r\n/g, '').replace(/=([0-9A-F]{2})/g, (_, hex) => String.fromCharCode(parseInt(hex, 16)));
  const found = /https?:\/\/\S+\/reset#token=\S+/.exec(decoded);
  check(found !== null, 'the reset email reaches the member, with a link in it');
  const emailURL = new URL(found[0].replace(/[.,;]+$/, ''));
  emailURL.searchParams.set('lang', LANG);
  await page.goto(emailURL.toString());
  await page.waitFor(`!!document.getElementById('reset-password')`, 15000);
  await page.setInput('#reset-password', BY_EMAIL);
  await page.setInput('#reset-confirm', BY_EMAIL);
  await page.evaluate(`document.querySelector('form button[type="submit"]').click()`);
  await acceptSwitchIfAsked(page);
  await page.waitFor(`location.pathname.startsWith('/files/')`, 15000);
  check((await accountMenu(page)).includes(MATEO.name), 'the emailed link sets the password, and the member is signed in with it');
  await signOutFromMenu(page);
  await signIn(page, ADMIN);
  await page.waitFor(`location.pathname.startsWith('/admin/tenants')`, 15000);

  // 6. A reset by link: the admin makes it, sees it once, and the member sets the password with it and signs in.
  await open(page, tenantUsers);
  await waitText(page, RITA.name);
  await clickRowButton(page, RITA.name, L('tenant.users.reset'));
  await page.waitFor(`!!document.querySelector('.ant-modal')`);
  await clickRadio(page, L('tenant.reset.link'));
  // A link from the admin's session needs the admin's own password (decision 41, F7).
  await page.setInput('#reset-admin', ADMIN.password);
  await clickButton(page, L('tenant.reset.submitLink'), '.ant-modal');
  await page.waitFor(`!!document.getElementById('reset-link-value')`, 15000);
  const link = await page.evaluate(`document.getElementById('reset-link-value').value`);
  check(link.includes('/reset#token='), 'the admin is shown a one-time link');
  await shot(page, '07-reset-link-shown');
  await clickButton(page, L('secret.done'), '.ant-modal');
  await page.waitFor(`!document.getElementById('reset-link-value')`);
  check(true, 'the link is gone once the admin has copied it and closed the dialog');
  // The link's token is in its fragment, so the language goes in the query before it.
  const linkURL = new URL(link);
  linkURL.searchParams.set('lang', LANG);
  await page.goto(linkURL.toString());
  await page.waitFor(`!!document.getElementById('reset-password')`, 15000);
  await page.setInput('#reset-password', BY_LINK);
  await page.setInput('#reset-confirm', BY_LINK);
  await page.evaluate(`document.querySelector('form button[type="submit"]').click()`);
  await acceptSwitchIfAsked(page);
  await page.waitFor(`location.pathname.startsWith('/files/')`, 15000);
  check((await accountMenu(page)).includes(RITA.name), 'the link sets the password, and the member is signed in with it');
  await signOutFromMenu(page);

  // 7. A typed password: the admin types it, and the member is forced to change it at the next sign-in.
  await signIn(page, ADMIN);
  await page.waitFor(`location.pathname.startsWith('/admin/tenants')`, 15000);
  await open(page, tenantUsers);
  await waitText(page, DANA.name);
  await clickRowButton(page, DANA.name, L('tenant.users.reset'));
  await page.waitFor(`!!document.querySelector('.ant-modal')`);
  await clickRadio(page, L('tenant.reset.typed'));
  await page.setInput('#reset-password', TEMP);
  await page.setInput('#reset-confirm', TEMP);
  await page.setInput('#reset-admin', ADMIN.password);
  await shot(page, '08-typed-password-dialog');
  await clickButton(page, L('tenant.reset.submitTyped'), '.ant-modal');
  await waitText(page, L('tenant.users.passwordSet'));
  check(true, 'the typed password is set, and the admin is told the user must change it');
  await signOutFromMenu(page);

  await signIn(page, { email: DANA.email, password: TEMP });
  await page.waitFor(`location.pathname !== '/login'`, 15000).catch(async () => {
    const where = await page.evaluate(`location.pathname + ' | ' + document.body.innerText.slice(0, 300)`);
    throw new Error(`the typed password did not sign the member in: ${where}`);
  });
  await page.waitFor(`location.pathname === '/change-password'`, 15000).catch(async () => {
    const where = await page.evaluate(`location.pathname + ' | ' + document.body.innerText.slice(0, 300) + ' | ' + document.getElementById('root')?.innerHTML.slice(0, 400)`);
    const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
    await writeFile(join(SHOTS, `debug-forced-${LANG}.png`), Buffer.from(data, 'base64'));
    console.error('browser errors so far:', page.errors.slice(-6).join(' | '));
    console.error('last requests:', page.sent.slice(-14).map((r) => `${r.method} ${r.url.replace(/^https?:\/\/[^/]+/, '')}`).join(' ; '));
    console.error('href:', await page.evaluate(`location.href + ' hist=' + history.length`));
    throw new Error(`the member was signed in but not sent to change the password: ${where}`);
  });
  await waitText(page, L('change.forced'));
  check(true, 'the member is sent to the change-password page at the next sign-in, and nothing else opens');
  await shot(page, '09-must-change-password');
  await open(page, '/files');
  await page.waitFor(`location.pathname === '/change-password'`, 15000);
  check(true, 'an address other than the change-password page sends the member back to it');
  await page.waitFor(`!!document.getElementById('change-current')`, 15000);
  await page.setInput('#change-current', TEMP);
  await page.setInput('#change-password', PERMANENT);
  await page.setInput('#change-confirm', PERMANENT);
  await clickButton(page, L('change.submit'));
  await page.waitFor(`location.pathname.startsWith('/files/') && !!document.querySelector('[data-testid="account-menu"]')`, 15000);
  check((await accountMenu(page)).includes(DANA.name), 'after the change, the member lands in the app');
  await shot(page, '10-after-change-home');
}

main().catch((err) => {
  console.error(err.message);
  process.exit(1);
});
