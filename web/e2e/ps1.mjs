// SPDX-License-Identifier: AGPL-3.0-or-later

// Page scripts (ps1, decision 219; the admin's path in the page, ps2): an installation setting that adds scripts to
// every page of the web interface. It runs inside the shared end-to-end run (run.mjs) in its own browser with its own
// profile, or alone with SELFCLOUD_E2E_ONLY=ps1. A small local server on 127.0.0.1 serves one script that marks the
// page; the installation admin sets ui.scripts to it through the settings API, and the next page served carries the
// tag right before </body>, runs the script, and names its origin in script-src. The admin then adds and removes the
// same script through the Page scripts section of the Settings page itself. The list is set back to empty afterwards,
// and the tag is gone from the next page.
import { createServer } from 'node:http';
import { freePort } from './cdp.mjs';

const q = (v) => JSON.stringify(v);

export async function ps1Checks(h, { page, base, adminKey, MEMBER, ADMIN }) {
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
      return { defer: s.defer, referrer: s.referrerPolicy, inBody: s.parentElement === document.body };
    })()`);
    check(
      tag !== null && tag.defer && tag.referrer === 'no-referrer' && tag.inBody,
      `the tag is deferred, no-referrer and in the body (${JSON.stringify(tag)})`,
    );

    const res = await fetch(base + '/files');
    const csp = res.headers.get('content-security-policy') ?? '';
    check(csp.includes(`script-src 'self' ${origin};`), `the response's policy names the script's origin (${csp})`);
    const html = await res.text();
    check(html.includes(`<script src="${src}" defer referrerpolicy="no-referrer"></script></body>`), 'the served page carries the tag right before </body>');

    // The admin's path in the page: the Page scripts section of Settings adds the same script, Save holds it, and the
    // next page runs it; Remove and Save leave the answer an empty list.
    await api(base, 'PATCH', '/api/v1/settings', adminKey, { set: { 'ui.scripts': [] } });
    await h.signOut(page);
    await h.signIn(page, ADMIN);
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    await h.navigateTo(page, '/admin/settings');
    await page.waitFor(`location.pathname === '/admin/settings' && !!document.getElementById('settings-scripts')`, 20000);
    await page.evaluate(`[...document.querySelectorAll('nav[aria-label="Sections"] button')].find((b) => b.textContent.trim() === 'Page scripts')?.click()`);
    await page.waitFor(`!!document.querySelector('[data-setting="ui.scripts"] button[aria-label="Add script"]')`, 10000);
    check(await page.evaluate(`document.querySelectorAll('[data-setting="ui.scripts"] input').length === 0`), 'the Page scripts section starts with no scripts');
    await h.clickOn(page, '[data-setting="ui.scripts"] button[aria-label="Add script"]');
    await page.waitFor(`!!document.querySelector('[data-setting="ui.scripts"] input[aria-label="Script 1"]')`, 10000);
    await page.setInput('[data-setting="ui.scripts"] input[aria-label="Script 1"]', src);
    await h.clickOn(page, '#settings-scripts .ant-btn-primary');
    await page.waitFor(`!!document.querySelector('#settings-scripts [role="status"]')`, 15000);
    const held = (await api(base, 'GET', '/api/v1/settings', adminKey)).settings.find((s) => s.key === 'ui.scripts');
    check(JSON.stringify(held.value) === q([src]) && held.source === 'settings', `the settings answer holds the script added in the page (${q(held.value)}, ${held.source})`);
    await page.send('Page.reload');
    await page.waitFor(`document.documentElement.dataset.ps1 === 'on'`, 20000);
    check(true, 'the page saved in Settings runs the script after a reload');
    await h.clickOn(page, '[data-setting="ui.scripts"] button[aria-label="Remove script 1"]');
    await page.waitFor(`!document.querySelector('[data-setting="ui.scripts"] input[aria-label="Script 1"]')`, 10000);
    await h.clickOn(page, '#settings-scripts .ant-btn-primary');
    await page.waitFor(`!!document.querySelector('#settings-scripts [role="status"]')`, 15000);
    const emptied = (await api(base, 'GET', '/api/v1/settings', adminKey)).settings.find((s) => s.key === 'ui.scripts');
    check(q(emptied.value) === '[]', `Remove and Save leave the answer an empty list (${q(emptied.value)})`);
  } finally {
    await api(base, 'PATCH', '/api/v1/settings', adminKey, { set: { 'ui.scripts': [] } }).catch(() => undefined);
    await new Promise((resolve) => srv.close(resolve));
  }

  await page.send('Page.reload');
  await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]')`, 20000);
  check(!(await page.evaluate(`[...document.scripts].some((x) => x.src === ${q(src)})`)), 'the tag is gone once the list is empty');
  check(!(await page.evaluate(`document.documentElement.dataset.ps1 === 'on'`)), 'the mark is gone once the list is empty');
}