// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice se3 (decision 143): the installation admin's Settings page replaces Installation. Checked as the signed-in admin:
//  1. The sidebar's Settings entry opens /admin/settings, and the old /admin/installation address lands there.
//  2. The trash retention is changed and saved: the server holds the new value, a reload shows it as "Set here", its Reset puts
//     the default back, and the server holds no value after.
//  3. A key the e2e config file writes (the piece size) is read-only and marked as set in the config file, with no Reset.
//  4. Unsaved changes ask before the sidebar leaves the page; Stay keeps the page and the change.
//  5. At 375 px the sections are tabs and nothing scrolls sideways; in Hebrew the page is right to left, the section list sits
//     at the right of the cards and nothing scrolls sideways at 375 px; and dark mode draws the page. None logs a page error.
//  6. The label column is readable at 768, 992, 1000, 1150 and 1280 px (the layout follows the content's width).
//  7. Signed out, the old address goes to sign-in and back to Settings; so does /login?next= the old address, and the address
//     with a trailing slash lands on Settings.
// Run alone with SELFCLOUD_E2E_ONLY=se3: run.mjs signs the admin in first.
import { setLanguage, setTheme, sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);
const sideways = (page) => page.evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`);

async function setWidth(page, width) {
  await page.send('Emulation.setDeviceMetricsOverride', { width, height: 800, deviceScaleFactor: 1, mobile: width < 800 });
  await sleep(300);
}

const row = (key) => `document.querySelector('[data-setting=${q(key)}]')`;
const box = (key) => `[data-setting=${q(key)}] input[role="spinbutton"]`;

// open waits for the Settings page itself: its path, and the cards drawn (a wide window), or its tabs (a phone).
async function open(page) {
  await page.waitFor(`location.pathname === '/admin/settings' && !!document.getElementById('settings-uploads')`, 20000);
}

// stored reads what the server holds for a setting.
async function stored(h, base, adminKey, key) {
  const got = await h.api(base, 'GET', '/api/v1/settings', adminKey);
  return got.settings.find((s) => s.key === key);
}

export async function se3Checks(h, { page, base, adminKey, ADMIN }) {
  const { check, navigateTo } = h;
  const errorsBefore = page.errors.length;
  try {
    // 1. The sidebar entry, and the old address.
    await setWidth(page, 1280);
    await navigateTo(page, '/admin/tenants');
    await page.waitFor(`location.pathname === '/admin/tenants'`, 15000);
    await h.clickNav(page, 'Settings');
    await open(page);
    check(await page.evaluate(`document.querySelector('h1')?.textContent === 'Settings'`), 'the sidebar entry Settings opens the Settings page');
    check(
      await page.evaluate(`[...document.querySelectorAll('[role="menuitem"]')].every((x) => x.textContent.trim() !== 'Installation')`),
      'the sidebar has no Installation entry',
    );
    // A bookmark of the old address is a page load of it.
    await page.goto(`${base}/admin/installation`);
    await open(page);
    check(true, 'the old Installation address lands on /admin/settings');
    const sections = await page.evaluate(`[...document.querySelectorAll('nav[aria-label="Sections"] button')].map((b) => b.textContent.trim())`);
    check(
      sections.join('|') === 'Uploads and storage|Sharing, trash and versions|Sign-in, search, office and administration|About',
      `the section list names the four sections (${sections.join(' | ')})`,
    );

    // 2. Change the trash retention, save, reload, reset.
    await page.waitFor(`!!${row('trash.retention')}`, 10000);
    check((await page.evaluate(`document.querySelector(${q(box('trash.retention'))}).value`)) === '30', 'the trash retention starts at the default, 30 days');
    await page.setInput(box('trash.retention'), '45');
    await page.waitFor(`!!${row('trash.retention')}.querySelector('[role="img"][aria-label="Changed"]')`, 5000);
    await h.clickOn(page, '#settings-sharing .ant-btn-primary');
    await page.waitFor(`!!document.querySelector('#settings-sharing [role="status"]')`, 15000);
    const held = await stored(h, base, adminKey, 'trash.retention');
    check(held.value === '1080h0m0s' && held.source === 'settings', `the server holds 45 days as a value set here (${held.value}, ${held.source})`);
    await page.send('Page.reload');
    await open(page);
    await page.waitFor(`!!${row('trash.retention')}`, 10000);
    const shown = await page.evaluate(`({ n: document.querySelector(${q(box('trash.retention'))}).value, tags: [...${row('trash.retention')}.querySelectorAll('.ant-tag')].map((t) => t.textContent.trim()) })`);
    check(shown.n === '45' && shown.tags.includes('Set here'), `after a reload the retention shows 45 and Set here (${shown.n}, ${shown.tags.join(', ')})`);
    await h.clickOn(page, `[data-setting="trash.retention"] button[aria-label="Back to the default (30 days)"]`);
    await page.waitFor(`document.querySelector(${q(box('trash.retention'))}).value === '30'`, 5000);
    await h.clickOn(page, '#settings-sharing .ant-btn-primary');
    await page.waitFor(`!!document.querySelector('#settings-sharing [role="status"]') && ${row('trash.retention')}.querySelector('.ant-tag')?.textContent.trim() === 'Default'`, 15000);
    const after = await stored(h, base, adminKey, 'trash.retention');
    check(after.value === '720h0m0s' && after.source === 'default', `after Reset the server holds no value, the default 30 days (${after.value}, ${after.source})`);
    check(!(await page.evaluate(`!!${row('trash.retention')}.querySelector('button[aria-label^="Back to the default"]')`)), 'a default row has no Reset');

    // 3. A key the config file writes is read-only.
    const lockedRow = await page.evaluate(`(() => {
      const r = ${row('upload.max_chunk_size')};
      return { disabled: r.querySelector('input[role="spinbutton"]').disabled, text: r.textContent, reset: !!r.querySelector('button[aria-label^="Back to the default"]') };
    })()`);
    check(lockedRow.disabled && lockedRow.text.includes("Set in the server's config file") && !lockedRow.reset, 'the piece size, which the config file writes, is read-only, marked as set in the config file, with no Reset');
    check(await page.evaluate(`${row('admin.tenant_access')}.querySelector('button[role="switch"]').disabled`), 'the sign-in-as switch, which the config file writes, is read-only');

    // 4. Unsaved changes ask before the sidebar leaves the page.
    await page.setInput(box('versions.max_count'), '50');
    await page.waitFor(`!!${row('versions.max_count')}.querySelector('[role="img"][aria-label="Changed"]')`, 5000);
    await h.clickNav(page, 'Tenants');
    await page.waitFor(`[...document.querySelectorAll('.ant-modal-title')].some((x) => x.textContent.trim() === 'Leave without saving?')`, 10000);
    check(true, 'leaving with an unsaved change asks first');
    await page.evaluate(`[...document.querySelectorAll('.ant-modal-footer button')].find((b) => b.textContent.trim() === 'Stay').click()`);
    await sleep(500);
    check(
      (await page.evaluate(`location.pathname`)) === '/admin/settings' && (await page.evaluate(`document.querySelector(${q(box('versions.max_count'))}).value`)) === '50',
      'Stay keeps the page and the unsaved change',
    );
    await page.evaluate(`document.querySelector('#settings-sharing .ant-btn:not(.ant-btn-primary)').click()`);
    await page.waitFor(`document.querySelector(${q(box('versions.max_count'))}).value === '100'`, 5000);
    check(true, 'Discard puts the section back as the server has it');

    // 4b. The label column keeps a readable width at every window width, with the sidebar docked from 992 px: either at least
    //     200 px, or the row is one stacked column.
    for (const width of [768, 992, 1000, 1150, 1280]) {
      await setWidth(page, width);
      await page.waitFor(`!!${row('upload.max_file_size')}`, 10000);
      await sleep(400);
      const col = await page.evaluate(`(() => {
        const r = ${row('upload.max_file_size')};
        return { label: r.firstElementChild.getBoundingClientRect().width, columns: getComputedStyle(r).gridTemplateColumns.split(' ').length, sideways: document.documentElement.scrollWidth > document.documentElement.clientWidth };
      })()`);
      check((col.label >= 200 || col.columns === 1) && !col.sideways, `at ${width} px the label column is ${Math.round(col.label)} px wide in ${col.columns} column(s), with no sideways scroll`);
    }
    await setWidth(page, 1280);

    // 5. A phone, Hebrew and the dark theme.
    await setWidth(page, 375);
    await page.waitFor(`!!document.querySelector('[role="tablist"]') && !document.querySelector('nav[aria-label="Sections"]')`, 10000);
    const tabs = await page.evaluate(`[...document.querySelectorAll('[role="tablist"] [role="tab"]')].map((t) => t.textContent.trim())`);
    check(tabs.length === 4 && tabs[3] === 'About', `at 375 px the sections are tabs (${tabs.join(' | ')})`);
    check(!(await sideways(page)), 'at 375 px the Settings page has no sideways scroll');
    await page.evaluate(`[...document.querySelectorAll('[role="tablist"] [role="tab"]')].find((t) => t.textContent.trim() === 'About').click()`);
    await page.waitFor(`!!document.querySelector('#settings-about') && document.querySelector('#settings-about').getBoundingClientRect().width > 0`, 10000);
    check(!(await sideways(page)), 'at 375 px the About tab has no sideways scroll');

    await setWidth(page, 1280);
    await setLanguage(page, 'עברית');
    await page.waitFor(`document.documentElement.dir === 'rtl' && document.querySelector('h1')?.textContent === 'הגדרות'`, 15000);
    await page.waitFor(`!!document.querySelector('nav') && !!document.getElementById('settings-uploads')`, 10000);
    const sides = await page.evaluate(`(() => {
      // The section list is the nav beside the cards: its next sibling holds the first card.
      const nav = [...document.querySelectorAll('nav')].find((n) => n.querySelectorAll('button').length === 4 && n.nextElementSibling?.querySelector('#settings-uploads'));
      const card = document.getElementById('settings-uploads');
      return { nav: nav.getBoundingClientRect().left, card: card.getBoundingClientRect().left };
    })()`);
    check(sides.nav > sides.card, `in Hebrew the section list is at the right of the cards (${Math.round(sides.nav)} against ${Math.round(sides.card)})`);
    await setWidth(page, 375);
    await page.waitFor(`!!document.querySelector('[role="tablist"]')`, 10000);
    check(!(await sideways(page)), 'in Hebrew at 375 px the Settings page has no sideways scroll');
    await setWidth(page, 1280);
    await setLanguage(page, 'English');
    await page.waitFor(`document.documentElement.dir === 'ltr' && document.querySelector('h1')?.textContent === 'Settings'`, 15000);

    await setTheme(page, 'Dark');
    await page.waitFor(`document.documentElement.dataset.theme === 'dark'`, 10000);
    await page.waitFor(`!!document.getElementById('settings-uploads')`, 10000);
    const dark = await page.evaluate(`getComputedStyle(document.querySelector('.ant-layout')).backgroundColor`);
    check(dark === 'rgb(7, 21, 49)', `in the dark theme the page has the dark background (${dark})`);
    await setTheme(page, 'Light');
    await page.waitFor(`document.documentElement.dataset.theme === 'light'`, 10000);
    check(page.errors.length === errorsBefore, `the Settings checks log no page error (${page.errors.slice(errorsBefore).join(' | ') || 'none'})`);

    // 6. The old address when signed out, with a trailing slash, and through the sign-in's ?next=.
    const onSettings = async (what) => {
      await page.waitFor(`location.pathname === '/admin/settings' && document.querySelector('h1')?.textContent === 'Settings' && !!document.getElementById('settings-uploads')`, 20000);
      check(true, what);
    };
    await h.signOut(page);
    await page.goto(`${base}/admin/installation`);
    await page.waitFor(`location.pathname === '/login'`, 15000);
    const next = await page.evaluate(`new URLSearchParams(location.search).get('next')`);
    check(next === '/admin/installation', `a signed-out load of the old address goes to sign-in, which returns to it (next=${next})`);
    await h.signInHere(page, ADMIN);
    await onSettings('signing in from the old address lands on Settings, not on a blank page');
    await page.goto(`${base}/admin/installation/`);
    await onSettings('the old address with a trailing slash lands on Settings');
    await h.signOut(page);
    await page.goto(`${base}/login?next=%2Fadmin%2Finstallation`);
    await h.signInHere(page, ADMIN);
    await onSettings('signing in through /login?next= the old address lands on Settings');
    check(page.errors.length === errorsBefore, `the old-address checks log no page error (${page.errors.slice(errorsBefore).join(' | ') || 'none'})`);
  } finally {
    // The window comes back whatever happened, and a retention left set is cleared.
    await setWidth(page, 1280).catch(() => undefined);
    await h.api(base, 'PATCH', '/api/v1/settings', adminKey, { reset: ['trash.retention'] }).catch(() => undefined);
  }
}
