// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice tl1 (design-tl): the admin lists are tables. Checked on the signed-in admin's pages: the tenants list at 375 pixels
// has no sideways scroll; the Tab key reaches a sortable header from the search box, and Enter there turns its aria-sort; on
// the admins list a second admin's row has its Remove icon while the signed-in admin's own row has none. The second admin is
// made through the API for the check and removed after it, and the desktop width comes back whatever happens.
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

export async function tl1Checks(h, { page, base, adminKey }) {
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
  } finally {
    if (secondID) await h.api(base, 'DELETE', `/api/v1/admins/${secondID}`, adminKey);
    await setWidth(page, 1280);
  }
}
