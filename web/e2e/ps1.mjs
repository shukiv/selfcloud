// SPDX-License-Identifier: AGPL-3.0-or-later

// Page scripts (ps1, decision 216): an installation setting that adds scripts to every page of the web interface.
// It runs inside the shared end-to-end run (run.mjs) in its own browser with its own profile, or alone with
// SELFCLOUD_E2E_ONLY=ps1. A small local server on 127.0.0.1 serves one script that marks the page; the installation
// admin sets ui.scripts to it through the settings API, and the next page served carries the tag right before
// </body>, runs the script, and names its origin in script-src. The list is set back to empty afterwards, and the
// tag is gone from the next page.
import { createServer } from 'node:http';
import { freePort } from './cdp.mjs';

const q = (v) => JSON.stringify(v);

export async function ps1Checks(h, { page, base, adminKey, MEMBER }) {
  const { check, api } = h;
  const port = await freePort();
  const origin = `http://127.0.0.1:${port}`;
  const src = `${origin}/ps1.js`;
  const srv = createServer((req, res) => {
    if (new URL(req.url, origin).pathname === '/ps1.js') {
      res.setHeader('Content-Type', 'text/javascript; charset=utf-8');
      res.end('document.documentElement.dataset.ps1 = "on";');
      return;
    }
    res.statusCode = 404;
    res.end();
  });
  await new Promise((resolve) => srv.listen(port, '127.0.0.1', resolve));
  try {
    await h.signIn(page, MEMBER);
    await page.goto(base + '/files');
    await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]')`, 20000);
    check(!(await page.evaluate(`document.documentElement.dataset.ps1 === 'on'`)), 'the page carries no mark before the setting');

    await api(base, 'PATCH', '/api/v1/settings', adminKey, { set: { 'ui.scripts': [src] } });
    await page.send('Page.reload');
    await page.waitFor(`document.documentElement.dataset.ps1 === 'on'`, 20000);
    check(true, 'the next page runs the listed script, with no restart');

    const tag = await page.evaluate(`(() => {
      const s = [...document.scripts].find((x) => x.src === ${q(src)});
      if (!s) return null;
      return { defer: s.defer, referrer: s.referrerPolicy, inBody: s.parentElement === document.body, last: s.nextElementSibling === null };
    })()`);
    check(
      tag !== null && tag.defer && tag.referrer === 'no-referrer' && tag.inBody && tag.last,
      `the tag is deferred, no-referrer and the last element of the body (${JSON.stringify(tag)})`,
    );

    const res = await fetch(base + '/files');
    const csp = res.headers.get('content-security-policy') ?? '';
    check(csp.includes(`script-src 'self' ${origin};`), `the response's policy names the script's origin (${csp})`);
    const html = await res.text();
    check(html.includes(`<script src="${src}" defer referrerpolicy="no-referrer"></script></body>`), 'the served page carries the tag right before </body>');
  } finally {
    await api(base, 'PATCH', '/api/v1/settings', adminKey, { set: { 'ui.scripts': [] } }).catch(() => undefined);
    await new Promise((resolve) => srv.close(resolve));
  }

  await page.send('Page.reload');
  await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]')`, 20000);
  check(!(await page.evaluate(`[...document.scripts].some((x) => x.src === ${q(src)})`)), 'the tag is gone once the list is empty');
  check(!(await page.evaluate(`document.documentElement.dataset.ps1 === 'on'`)), 'the mark is gone once the list is empty');
}