// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice tl5 (decision 138, the slice's design not in this repository): the access requests, the notifications and the activity are tables. Checked as the signed-in
// owner: at 375 pixels neither the requests page nor the notifications page scrolls sideways, and each draws its table (a region
// with a header row, and its body either with rows or with the empty table's title); in Hebrew both are right to left; in dark mode
// both draw their table with no page error. The activity list is in an item's comments panel, which the r7 and r7b sections check.
// Run alone with SELFCLOUD_E2E_ONLY=tl5: run.mjs signs the owner in first.
import { setLanguage, sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);

// sideways reads whether the page is wider than its window, which is a sideways scroll on a phone.
const sideways = (page) => page.evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`);

// setWidth gives the page a window of this width (a phone at 375 pixels, or the desktop at 1280).
async function setWidth(page, width) {
  await page.send('Emulation.setDeviceMetricsOverride', { width, height: 800, deviceScaleFactor: 1, mobile: width < 800 });
  await sleep(300);
}

// setScheme makes the page's colour scheme dark or light, as the system's setting does.
async function setScheme(page, value) {
  await page.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value }] });
  await sleep(300);
}

// tableOf waits for the list's region with its name, and returns whether it has a header row with the given first title.
const tableOf = (name) =>
  `(() => { const r = document.querySelector('[role="region"][aria-label=${q(name)}]'); return !!r && r.querySelectorAll('thead th').length > 0; })()`;

// apiIn calls the signed-in person's own API as the page does, with the session's CSRF token, and returns the status and the JSON.
function apiIn(page, method, path, body) {
  return page.evaluate(`(async () => {
    const me = await fetch('/api/v1/me', { credentials: 'same-origin', headers: { Accept: 'application/json' } }).then((x) => x.json());
    const headers = { Accept: 'application/json', 'Content-Type': ${q(typeof body === 'string' ? 'text/plain' : 'application/json')}, 'X-CSRF-Token': me.csrf_token ?? '' };
    const r = await fetch(${q(path)}, { method: ${q(method)}, credentials: 'same-origin', headers, body: ${body === undefined ? 'undefined' : q(typeof body === 'string' ? body : JSON.stringify(body))} });
    return { status: r.status, body: r.status === 204 ? null : await r.json().catch(() => null), pf: me.personal_folder_id };
  })()`);
}

// uncaught lists the page errors raised since the count, which are the uncaught ones and a renderer crash.
const uncaught = (page, since) => page.errors.slice(since).filter((e) => e.startsWith('uncaught') || e.startsWith('the page renderer crashed'));

export async function tl5Checks(h, { page }) {
  const { check, navigateTo, signIn, signOut, OWNER, MEMBER } = h;
  let target = null;
  let requestID = null;
  try {
    // 0. The owner has an item the member cannot reach, and the member asks for access to it, so the requests page has a row.
    const name = `tl5-request-for-a-file-with-a-long-name-${Date.now().toString(36)}.txt`;
    const pf = (await apiIn(page, 'GET', '/api/v1/me')).pf;
    const put = await apiIn(page, 'POST', `/api/v1/folders/${pf}/files?path=${encodeURIComponent('/' + name)}`, 'a file the member asks for');
    check(put.status === 201, `the owner made the file the member will ask for (${put.status})`);
    const top = await apiIn(page, 'GET', `/api/v1/folders/${pf}/children?path=%2F`);
    target = top.body.items.find((n) => n.name === name).id;
    await signOut(page);
    await signIn(page, MEMBER);
    const asked = await apiIn(page, 'POST', `/api/v1/nodes/${target}/access-requests`, { role: 'editor', message: 'Please let me in' });
    check(asked.status === 202 || asked.status === 201 || asked.status === 204, `the member asked for access (${asked.status})`);
    await signOut(page);
    await signIn(page, OWNER);
    const pending = await apiIn(page, 'GET', '/api/v1/access-requests');
    requestID = pending.body.items.find((r) => r.node_id === target)?.id ?? null;
    check(requestID !== null, 'the owner has the pending request');

    // 1b. With one pending request at 375 pixels the row is drawn and the name has room: no sideways scroll, and the name cell is
    // at least 100 pixels wide.
    await setWidth(page, 375);
    await navigateTo(page, '/requests');
    await page.waitFor(`!!document.querySelector('.requests tr.request')`, 15000);
    check(!(await sideways(page)), 'the access requests with a pending request at 375 px have no sideways scroll');
    const nameWidth = await page.evaluate(`document.querySelector('.requests tr.request td')?.getBoundingClientRect().width ?? 0`);
    console.log(`tl5: the name cell of a pending request at 375 px is ${nameWidth} px wide`);
    check(nameWidth >= 100, `the name cell of a pending request at 375 px is at least 100 px wide (${nameWidth} px)`);

    // 1. At 375 pixels the requests and the notifications are tables, and neither page scrolls sideways.
    await setWidth(page, 375);
    await navigateTo(page, '/requests');
    await page.waitFor(tableOf('Access requests') + ` || document.body.textContent.includes('Nothing to decide')`, 15000);
    check(!(await sideways(page)), 'the access requests at 375 px have no sideways scroll');
    check(await page.evaluate(tableOf('Access requests')), 'the access requests are a table with a header row at 375 px');

    await navigateTo(page, '/notifications');
    await page.waitFor(tableOf('Notifications') + ` || document.body.textContent.includes('Nothing here yet')`, 15000);
    check(!(await sideways(page)), 'the notifications at 375 px have no sideways scroll');
    check(await page.evaluate(tableOf('Notifications')), 'the notifications are a table with a header row at 375 px');

    // 2. In Hebrew both pages are right to left, and the notifications keep their own title. No page error is raised meanwhile.
    const errorsBeforeHebrew = page.errors.length;
    await setLanguage(page, 'עברית');
    await page.waitFor(`document.documentElement.dir === 'rtl'`, 10000);
    check((await page.evaluate(`document.documentElement.dir`)) === 'rtl', 'in Hebrew the page is right to left');
    // The region is named by the page's title, which is in Hebrew here (the title r7b checks is התראות).
    check(await page.evaluate(tableOf('התראות')), 'in Hebrew the notifications are still a table with a header row');
    await navigateTo(page, '/requests');
    await page.waitFor(`!!document.querySelector('.requests tr.request')`, 15000);
    check((await page.evaluate(`document.documentElement.dir`)) === 'rtl', 'in Hebrew the access requests are right to left');
    check(!(await sideways(page)), 'in Hebrew the access requests with a pending request have no sideways scroll');
    const hebrewErrors = uncaught(page, errorsBeforeHebrew);
    check(hebrewErrors.length === 0, `the Hebrew pages raise no page error (${hebrewErrors.join(' | ') || 'none'})`);
    await setLanguage(page, 'English');

    // 3. In dark mode both draw their table, with no sideways scroll and no page error.
    const errorsBeforeDark = page.errors.length;
    await setScheme(page, 'dark');
    await navigateTo(page, '/notifications');
    await page.waitFor(tableOf('Notifications') + ` || document.body.textContent.includes('Nothing here yet')`, 15000);
    check(await page.evaluate(tableOf('Notifications')), 'in dark mode the notifications are a table with a header row');
    check(!(await sideways(page)), 'in dark mode at 375 px the notifications have no sideways scroll');
    await navigateTo(page, '/requests');
    await page.waitFor(`!!document.querySelector('.requests tr.request')`, 15000);
    check(!(await sideways(page)), 'in dark mode at 375 px the access requests have no sideways scroll');
    const darkErrors = uncaught(page, errorsBeforeDark);
    check(darkErrors.length === 0, `the dark pages raise no page error (${darkErrors.join(' | ') || 'none'})`);
  } finally {
    await setScheme(page, 'light');
    await setWidth(page, 1280);
    // The request is denied and the file removed for good, as the owner.
    if (requestID) await apiIn(page, 'POST', `/api/v1/access-requests/${requestID}/deny`).catch(() => undefined);
    if (target) await apiIn(page, 'DELETE', `/api/v1/nodes/${target}?permanent=true`).catch(() => undefined);
  }
}
