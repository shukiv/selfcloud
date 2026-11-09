// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice tl1 (design-tl): the admin lists are tables. Checked on the signed-in admin's pages, after the admin checks of a
// full run: the tenants list at 375 pixels has no sideways scroll; a header takes the keyboard, and Enter toggles its
// aria-sort; the admins list has no Remove on the signed-in admin's own row. The desktop width is restored at the end.
// Run alone with SELFCLOUD_E2E_ONLY=tl1, once the run's admin is signed in (run.mjs does it in adminChecks).
import { sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);

// sideways reads whether the page is wider than its window, which is a sideways scroll on a phone.
const sideways = (page) => page.evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`);

// setWidth gives the page a window of this width (a phone at 375 pixels, or the desktop at 1280).
async function setWidth(page, width) {
  await page.send('Emulation.setDeviceMetricsOverride', { width, height: 800, deviceScaleFactor: 1, mobile: width < 800 });
  await sleep(300);
}

// pressEnter sends Enter to the focused element, as a keyboard does.
async function pressEnter(page) {
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13, text: '\r' });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
}

export async function tl1Checks(h, { page }) {
  const { check, navigateTo } = h;

  // 1. The tenants list at 375 pixels: a table with a name column, and no sideways scroll.
  await setWidth(page, 375);
  await navigateTo(page, '/admin/tenants');
  await page.waitFor(`[...document.querySelectorAll('[role="region"] tbody tr')].length > 0 || !!document.querySelector('[role="region"] .ant-empty')`, 15000);
  const phoneSideways = await sideways(page);
  check(!phoneSideways, 'the tenants list at 375 px has no sideways scroll');

  // 2. A sortable header takes the keyboard: the name header is focused, and Enter flips its aria-sort.
  const before = await page.evaluate(`(() => { const th = [...document.querySelectorAll('[role="region"] thead th')].find((x) => x.querySelector('button') && x.textContent.trim().startsWith(${q('Name')})); return th ? th.getAttribute('aria-sort') : null; })()`);
  await page.evaluate(`(() => { const th = [...document.querySelectorAll('[role="region"] thead th')].find((x) => x.querySelector('button') && x.textContent.trim().startsWith(${q('Name')})); th.querySelector('button').focus(); })()`);
  await pressEnter(page);
  await sleep(400);
  const after = await page.evaluate(`(() => { const th = [...document.querySelectorAll('[role="region"] thead th')].find((x) => x.querySelector('button') && x.textContent.trim().startsWith(${q('Name')})); return th ? th.getAttribute('aria-sort') : null; })()`);
  check(before !== null && after !== null && before !== after, `Enter on the Name header turns its aria-sort (${before} to ${after})`);

  // 3. The admins list: the signed-in admin's row has the You badge and no Remove icon.
  await setWidth(page, 1280);
  await navigateTo(page, '/admin/admins');
  await page.waitFor(`[...document.querySelectorAll('[role="region"] tbody tr')].some((r) => r.textContent.includes(${q('You')}))`, 15000);
  const ownRemove = await page.evaluate(
    `(() => { const rows = [...document.querySelectorAll('[role="region"] tbody tr')].filter((r) => r.querySelector('span') && [...r.querySelectorAll('span')].some((s) => s.textContent.trim() === ${q('You')})); return rows.length > 0 && rows.every((r) => ![...r.querySelectorAll('button')].some((b) => (b.getAttribute('aria-label') || '').startsWith(${q('Remove')}))); })()`,
  );
  check(ownRemove, 'the signed-in admin has the You badge and no Remove on the admins list');

  await setWidth(page, 1280);
}
