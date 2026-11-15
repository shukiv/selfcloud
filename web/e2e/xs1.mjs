// SPDX-License-Identifier: AGPL-3.0-or-later

// The web interface follows the installation's outside sign-in page (xs1, decision 185). It runs inside the shared
// end-to-end run (run.mjs) in its own browser with its own profile, or alone with SELFCLOUD_E2E_ONLY=xs1, so the shared
// page's same-origin check never sees the outside addresses. A local server plays the outside page: the three settings
// name it, GET /login answers 302 to it, a signed-out visit goes there with its next, /login?password keeps the form,
// a sign-in link with next lands on that path, and a sign-out goes to the signed-out URL. The settings are reset after.
import { createServer } from 'node:http';
import { freePort } from './cdp.mjs';

const q = (v) => JSON.stringify(v);

// clickNamed presses the element of the selector whose words are text.
async function clickNamed(page, selector, text) {
  await page.waitFor(
    `(() => { const e = [...document.querySelectorAll(${q(selector)})].find((x) => x.textContent.trim() === ${q(text)}); if (!e) return false; e.click(); return true; })()`,
    10000,
  );
}

export async function xs1Checks(h, { page, base, adminKey, accountID, accountKey, memberID, teamA }) {
  const { check, api } = h;

  // The outside pages: a local server with a sign-in page that reads its next, and a signed-out page.
  const port = await freePort();
  const origin = `http://127.0.0.1:${port}`;
  const outside = `${origin}/outside?next={next}`;
  const signedOut = `${origin}/signed-out`;
  const srv = createServer((req, res) => {
    const path = new URL(req.url, origin).pathname;
    const title = path === '/outside' ? 'Outside sign-in' : 'Outside signed-out';
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    res.end(`<!doctype html><html><head><title>${title}</title></head><body><h1>${title}</h1></body></html>`);
  });
  await new Promise((resolve) => srv.listen(port, '127.0.0.1', resolve));

  const KEYS = ['users.external_sign_in_url', 'users.external_signed_out_url', 'users.password_sign_in'];
  try {
    await api(base, 'PATCH', '/api/v1/settings', adminKey, {
      set: { 'users.external_sign_in_url': outside, 'users.external_signed_out_url': signedOut, 'users.password_sign_in': false },
    });

    // GET /login answers 302 to the outside page with the next encoded; ?password keeps the page.
    const plain = await fetch(base + '/login', { redirect: 'manual' });
    check(plain.status === 302, `GET /login answers 302 while the outside page is set (status ${plain.status})`);
    check(plain.headers.get('location') === `${origin}/outside?next=%2F`, `the 302 names the outside page with next=/ (${plain.headers.get('location')})`);
    const withNext = await fetch(base + `/login?next=${encodeURIComponent(`/files/${teamA}`)}`, { redirect: 'manual' });
    check(
      withNext.status === 302 && withNext.headers.get('location') === `${origin}/outside?next=${encodeURIComponent(`/files/${teamA}`)}`,
      `the 302 carries the next path encoded (${withNext.headers.get('location')})`,
    );
    const password = await fetch(base + '/login?password', { redirect: 'manual' });
    check(password.status === 200, `GET /login?password keeps the page (status ${password.status})`);

    // A signed-out visit of a drive page: the interface itself goes to the outside page with the path as next.
    await page.goto(base + `/files/${teamA}`);
    await page.waitFor(
      `location.href.startsWith(${q(`${origin}/outside`)}) && new URLSearchParams(location.search).get('next') === ${q(`/files/${teamA}`)}`,
      20000,
    );
    check(true, 'a signed-out visit goes to the outside sign-in page with its next');

    // The installation admin's way in: the form is drawn at /login?password.
    await page.goto(base + '/login?password');
    await page.waitFor(`!!document.getElementById('login-email')`, 15000);
    check(true, '/login?password draws the sign-in form while the outside page is set');

    // A sign-in link with next still signs the member in and lands on that path.
    const made = await api(base, 'POST', `/api/v1/accounts/${accountID}/users/${memberID}/sign-in-links`, accountKey, { next: `/files/${teamA}` });
    check(made.url.startsWith(base + '/sign-in#token='), 'the account key makes a sign-in link for the member');
    await page.goto(made.url);
    await page.waitFor(`location.pathname === ${q(`/files/${teamA}`)} && !location.hash && !!document.querySelector('[data-testid="account-menu"]')`, 20000);
    check(true, 'the sign-in link lands on its next path, signed in, while the outside page is set');

    // Sign-out goes to the signed-out URL.
    await h.clickOn(page, '[data-testid="account-menu"]');
    await clickNamed(page, '.ant-dropdown-menu-item', 'Sign out');
    await page.waitFor(`location.href === ${q(signedOut)}`, 20000);
    check(true, 'sign-out goes to the signed-out URL');
  } finally {
    await api(base, 'PATCH', '/api/v1/settings', adminKey, { reset: KEYS }).catch(() => undefined);
    await new Promise((resolve) => srv.close(resolve));
  }

  const back = await fetch(base + '/login', { redirect: 'manual' });
  check(back.status === 200, `with the settings reset, GET /login serves the page again (status ${back.status})`);
}