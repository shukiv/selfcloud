// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice tl1 (design-tl): the admin lists are tables. Checked on the signed-in admin's pages: the tenants list at 375 pixels
// has no sideways scroll; the Tab key reaches a sortable header from the search box, and Enter there turns its aria-sort; on
// the admins list a second admin's row has its Remove icon while the signed-in admin's own row has none; on a tenant's
// people, a busy row keeps the focus on its pressed icon, and Remove asks first; an empty keys table keeps its headers and
// its New key. The second admin, the pending person and the empty tenant are made through the API for the checks and
// removed after them, and the desktop width comes back whatever happens.
// Run alone with SELFCLOUD_E2E_ONLY=tl1: run.mjs signs the admin in first.
import { sleep } from './media.mjs';
import { pressKey } from './accountKeys.mjs';

const q = (v) => JSON.stringify(v);

// sideways reads whether the page is wider than its window, which is a sideways scroll on a phone.
const sideways = (page) => page.evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`);

// setWidth gives the page a window of this width (a phone at 375 pixels, or the desktop at 1280).
async function setWidth(page, width) {
  await page.send('Emulation.setDeviceMetricsOverride', { width, height: 800, deviceScaleFactor: 1, mobile: width < 800 });
  await sleep(300);
}

// The Name header's cell, found by its label and its sort button.
const NAME_TH = `[...document.querySelectorAll('[role="region"] thead th')].find((x) => x.querySelector('button') && x.textContent.trim().startsWith(${q('Name')}))`;

// rowsOf reads the admins list's rows: each row's name cell text, whether it has the You badge, and its buttons' names.
const ROWS = `[...document.querySelectorAll('[role="region"] tbody tr[data-id]')].map((r) => ({ text: r.textContent, you: [...r.querySelectorAll('.ant-tag')].some((s) => s.textContent.trim() === ${q('You')}), buttons: [...r.querySelectorAll('button')].map((b) => b.getAttribute('aria-label') || '') }))`;

// The row of a tenant's person, by its accessible name, and one of its buttons by its name.
const personRow = (name) => `document.querySelector('[role="region"] tbody tr[aria-label=${q(name)}]')`;
const rowButton = (name, label) => `${personRow(name)}?.querySelector('button[aria-label=${q(label)}]')`;

// focusNow names the focused element: its tag and its accessible name, or its tag alone.
const focusNow = (page) =>
  page.evaluate(`(() => { const a = document.activeElement; return a ? a.tagName + (a.getAttribute('aria-label') ? ':' + a.getAttribute('aria-label') : '') : 'none'; })()`);

// holdRequests pauses the browser's requests whose address matches pattern until the returned release is called. paused
// resolves at the first one. The listener and the interception are removed by release.
async function holdRequests(page, pattern) {
  const held = [];
  let first;
  const paused = new Promise((resolve) => (first = resolve));
  const onPaused = (p) => {
    held.push(p.requestId);
    first();
  };
  page.on('Fetch.requestPaused', onPaused);
  await page.send('Fetch.enable', { patterns: [{ urlPattern: pattern, requestStage: 'Request' }] });
  return {
    paused,
    release: async () => {
      page.off('Fetch.requestPaused', onPaused);
      for (const id of held) await page.send('Fetch.continueRequest', { requestId: id }).catch(() => undefined);
      await page.send('Fetch.disable');
    },
  };
}

// tenantPeopleChecks (the re-check's items 2 and 3) runs on a pending person of the run's account, made through the API.
// While the person's resend is on its way (the browser holds the request), the row is busy: the Resend icon pressed with
// the keyboard keeps the focus, disabled, and keeps it when the answer comes. Then the ⋮ menu's Remove asks first, and its
// footer's danger button removes the person. A person left behind by a failure is removed through the API.
async function tenantPeopleChecks(h, { page, base, accountID, accountKey }) {
  const { check, navigateTo } = h;
  const pat = { email: 'pat.pending@example.com', display_name: 'Pat Pending', role: 'member', verification: 'email' };
  const resend = `Resend invitation: ${pat.display_name}`;
  let patID = null;
  let hold = null;
  try {
    patID = (await h.api(base, 'POST', `/api/v1/accounts/${accountID}/users`, accountKey, pat)).id;
    await navigateTo(page, `/admin/tenants/${accountID}?tab=users`);
    await page.waitFor(`!!${rowButton(pat.display_name, resend)}`, 15000);

    // 4. The busy row in a real browser: Enter on the Resend icon, the request held, the focus read while it is held.
    hold = await holdRequests(page, `*/api/v1/accounts/${accountID}/users/${patID}/verification*`);
    await page.evaluate(`${rowButton(pat.display_name, resend)}.focus()`);
    await pressKey(page, 'Enter', 'Enter', 13);
    await hold.paused;
    await page.waitFor(`(() => { const b = ${rowButton(pat.display_name, resend)}; return !!b && (b.disabled || b.getAttribute('aria-disabled') === 'true'); })()`, 5000);
    const busyFocus = await focusNow(page);
    check(busyFocus === `BUTTON:${resend}`, `while the row is busy, the pressed Resend icon keeps the focus, disabled (${busyFocus})`);
    await hold.release();
    hold = null;
    await page.waitFor(`document.body.textContent.includes(${q('The email was sent.')}) || document.body.textContent.includes(${q('The email could not be sent')})`, 15000);
    await page.waitFor(`(() => { const b = ${rowButton(pat.display_name, resend)}; return !!b && !b.disabled && b.getAttribute('aria-disabled') !== 'true'; })()`, 5000);
    const afterFocus = await focusNow(page);
    check(afterFocus === `BUTTON:${resend}`, `when the answer comes, the Resend icon still has the focus (${afterFocus})`);

    // 5. Remove from account: the ⋮ entry is a danger one, the dialog asks, and its footer's danger button removes.
    await h.clickOn(page, `[role="region"] tbody tr[aria-label=${q(pat.display_name)}] button[aria-label=${q(`More actions for ${pat.display_name}`)}]`);
    await page.waitFor(
      `(() => { const e = [...document.querySelectorAll('.ant-dropdown-menu-item')].find((x) => x.textContent.trim() === ${q('Remove…')}); if (!e) return false; window.__tl1Danger = e.classList.contains('ant-dropdown-menu-item-danger'); e.click(); return true; })()`,
    );
    check(await page.evaluate(`window.__tl1Danger === true`), 'the ⋮ menu has Remove, a danger entry');
    await page.waitFor(`[...document.querySelectorAll('.ant-modal-title')].some((x) => x.textContent.trim() === ${q(`Remove ${pat.display_name}?`)})`, 10000);
    const footerDanger = await page.evaluate(`(() => { const b = document.querySelector('.ant-modal-footer .ant-btn-dangerous'); return b ? b.textContent.trim() : null; })()`);
    check(footerDanger === 'Remove', `the confirmation's footer has the danger button Remove (${footerDanger})`);
    await h.clickOn(page, '.ant-modal-footer .ant-btn-dangerous');
    await page.waitFor(`document.body.textContent.includes(${q(`${pat.display_name} was removed.`)}) && !${personRow(pat.display_name)}`, 15000);
    let gone = false;
    try {
      await h.api(base, 'GET', `/api/v1/accounts/${accountID}/users/${patID}`, accountKey);
    } catch (err) {
      gone = String(err.message).includes(': 404 ');
    }
    check(gone, 'the removed person is gone from the account, and from its list');
    if (gone) patID = null;
  } finally {
    if (hold) await hold.release();
    if (patID) await h.api(base, 'DELETE', `/api/v1/accounts/${accountID}/users/${patID}`, accountKey).catch(() => undefined);
  }
}

// emptyTableChecks (the owner's rule of 2026-11-09, decision 114): a new tenant, made through the API, has no API key. Its
// keys table keeps its header row, its body holds antd's Empty with the title and the New key action, and the action opens
// the new key form. The tenant is deleted through the API after.
async function emptyTableChecks(h, { page, base, adminKey }) {
  const { check, navigateTo } = h;
  let tenantID = null;
  try {
    tenantID = (await h.api(base, 'POST', '/api/v1/accounts', adminKey, { name: 'Empty Tables Ltd' })).id;
    await navigateTo(page, `/admin/tenants/${tenantID}?tab=keys`);
    const region = `[...document.querySelectorAll('[role="region"]')].find((r) => r.getAttribute('aria-label') === ${q('API keys')})`;
    await page.waitFor(`!!${region}?.querySelector('tbody .ant-empty .empty-title')`, 15000);
    const seen = await page.evaluate(`(() => {
      const r = ${region};
      return {
        headers: [...r.querySelectorAll('thead th')].map((th) => th.textContent.trim()).filter(Boolean),
        title: r.querySelector('tbody .empty-title')?.textContent ?? null,
        action: r.querySelector('tbody .ant-empty button')?.textContent.trim() ?? null,
      };
    })()`);
    check(
      seen.headers.join(', ') === 'Name, Created, Last used, Expires, Status',
      `an empty keys table keeps its header row (${seen.headers.join(', ')})`,
    );
    check(seen.title === 'No API keys yet.' && seen.action === 'New key', `its body has the Empty, with its title and New key (${seen.title}; ${seen.action})`);
    await page.evaluate(`${region}.querySelector('tbody .ant-empty button').click()`);
    await page.waitFor(`!!document.getElementById('new-key-name')`, 10000);
    check(true, 'the empty table\'s New key opens the new key form');
    await pressKey(page, 'Escape', 'Escape', 27);
    await page.waitFor(`!document.getElementById('new-key-name')`, 10000);
  } finally {
    if (tenantID) await h.api(base, 'DELETE', `/api/v1/accounts/${tenantID}`, adminKey).catch(() => undefined);
  }
}

export async function tl1Checks(h, { page, base, adminKey, accountID, accountKey }) {
  const { check, navigateTo } = h;
  const second = { email: 'tess.second@example.com', display_name: 'Tess Second', verification: 'email' };
  let secondID = null;
  try {
    // 1. The tenants list at 375 pixels: a table with a name column, and no sideways scroll.
    await setWidth(page, 375);
    await navigateTo(page, '/admin/tenants');
    await page.waitFor(`[...document.querySelectorAll('[role="region"] tbody tr')].length > 0 || !!document.querySelector('[role="region"] .ant-empty')`, 15000);
    check(!(await sideways(page)), 'the tenants list at 375 px has no sideways scroll');

    // 2. The keyboard reaches the Name header with Tab from the search box (the box is focused by script, the rest is keys),
    //    and Enter on it turns its aria-sort.
    const before = await page.evaluate(`(() => { const th = ${NAME_TH}; return th ? th.getAttribute('aria-sort') : null; })()`);
    await page.evaluate(`document.getElementById('tenant-search').focus()`);
    let reached = false;
    for (let i = 0; i < 12 && !reached; i++) {
      await pressKey(page, 'Tab', 'Tab', 9);
      reached = await page.evaluate(`(() => { const th = ${NAME_TH}; return !!th && th.querySelector('button') === document.activeElement; })()`);
    }
    check(reached, 'Tab from the search box reaches the Name header');
    await pressKey(page, 'Enter', 'Enter', 13);
    await sleep(400);
    const after = await page.evaluate(`(() => { const th = ${NAME_TH}; return th ? th.getAttribute('aria-sort') : null; })()`);
    check(reached && before !== null && after !== null && before !== after, `Enter on the Name header turns its aria-sort (${before} to ${after})`);

    // 3. The admins list: a second admin's row has a Remove icon (the positive control), and the signed-in admin's row, with
    //    the You badge, has none.
    secondID = (await h.api(base, 'POST', '/api/v1/admins', adminKey, second)).id;
    await setWidth(page, 1280);
    await navigateTo(page, '/admin/admins');
    await page.waitFor(`(() => { const rows = ${ROWS}; return rows.some((r) => r.you) && rows.some((r) => r.text.includes(${q(second.display_name)})); })()`, 15000);
    const rows = await page.evaluate(ROWS);
    const own = rows.filter((r) => r.you);
    const other = rows.find((r) => r.text.includes(second.display_name));
    check(!!other && other.buttons.includes(`Remove: ${second.display_name}`), `a second admin's row has its Remove icon (${other ? other.buttons.join(', ') : 'no row'})`);
    check(own.length === 1 && !own[0].buttons.some((b) => b.startsWith('Remove')), 'the signed-in admin has the You badge and no Remove on the admins list');

    await tenantPeopleChecks(h, { page, base, accountID, accountKey });
    await emptyTableChecks(h, { page, base, adminKey });
  } finally {
    if (secondID) await h.api(base, 'DELETE', `/api/v1/admins/${secondID}`, adminKey);
    await setWidth(page, 1280);
  }
}
