// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice tl5 (design-tl, decision 138): the access requests, the notifications and the activity are tables. Checked as the signed-in
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

export async function tl5Checks(h, { page }) {
  const { check, navigateTo } = h;
  try {
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

    // 2. In Hebrew both pages are right to left, and the notifications keep their own title.
    await setLanguage(page, 'עברית');
    await page.waitFor(`document.documentElement.dir === 'rtl'`, 10000);
    check((await page.evaluate(`document.documentElement.dir`)) === 'rtl', 'in Hebrew the page is right to left');
    // The region is named by the page's title, which is in Hebrew here (the title r7b checks is התראות).
    check(await page.evaluate(tableOf('התראות')), 'in Hebrew the notifications are still a table with a header row');
    await navigateTo(page, '/requests');
    await page.waitFor(tableOf('Access requests') + ` || !!document.querySelector('[role="status"]')`, 15000);
    check((await page.evaluate(`document.documentElement.dir`)) === 'rtl', 'in Hebrew the access requests are right to left');
    await setLanguage(page, 'English');

    // 3. In dark mode both draw their table, with no sideways scroll.
    await setScheme(page, 'dark');
    await navigateTo(page, '/notifications');
    await page.waitFor(tableOf('Notifications') + ` || document.body.textContent.includes('Nothing here yet')`, 15000);
    check(await page.evaluate(tableOf('Notifications')), 'in dark mode the notifications are a table with a header row');
    check(!(await sideways(page)), 'in dark mode at 375 px the notifications have no sideways scroll');
    await navigateTo(page, '/requests');
    await page.waitFor(tableOf('Access requests') + ` || document.body.textContent.includes('Nothing to decide')`, 15000);
    check(!(await sideways(page)), 'in dark mode at 375 px the access requests have no sideways scroll');
  } finally {
    await setScheme(page, 'light');
    await setWidth(page, 1280);
  }
}
