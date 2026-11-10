// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice tl3 (decision 122): the owner's Manage tables (the audit log, the overview's recent activity, the links and the three
// storage tables) and the drive's largest files are the one table. Checked on the signed-in owner's pages: each table has its
// headers and its region's name; the audit's When header is descending with no button; the team spaces sort by name when the
// header is pressed; at 375 pixels no page scrolls sideways and a drive row keeps only its ⋮ menu; in Hebrew the audit table is
// drawn right to left. With SELFCLOUD_E2E_SHOTS set (a folder), each table is saved there in light, dark, Hebrew and at 375
// pixels, as <table>-<view>.png. Run alone with SELFCLOUD_E2E_ONLY=tl3 (run.mjs signs the owner in first).
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { setLanguage, setPhone, setTheme, shot, sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);

// sideways reads whether the page is wider than its window, which is a sideways scroll on a phone.
const sideways = (page) => page.evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`);

// regionHeads reads the header texts of the region named name, or null when there is no such region.
const regionHeads = (name) =>
  `(() => { const r = [...document.querySelectorAll('[role="region"]')].find((x) => x.getAttribute('aria-label') === ${q(name)}); return r ? [...r.querySelectorAll('thead th')].map((h) => h.textContent.trim()) : null; })()`;

// The region's table is there once its body has drawn: a row or antd's empty message.
const drawn = (name) =>
  `!!([...document.querySelectorAll('[role="region"]')].find((x) => x.getAttribute('aria-label') === ${q(name)})?.querySelector('tbody tr, tbody .ant-empty'))`;

const SHOT_PAGES = [
  { table: 'audit', path: '/manage/audit', region: 'Audit log' },
  { table: 'overview', path: '/manage', region: 'Recent activity' },
  { table: 'links', path: '/manage/links', region: 'Links' },
  { table: 'storage', path: '/manage/storage', region: 'People, by use' },
  { table: 'largest', path: '/storage', region: 'Largest files' },
];

async function openTable(h, page, path, region) {
  await h.navigateTo(page, path);
  await page.waitFor(drawn(region), 20000);
}

export async function tl3Checks(h, { page, shots = null }) {
  const { check, navigateTo } = h;
  try {
    await setPhone(page, false);

    // 1. The audit log is one table: the Action and When headers, the When header descending with no button.
    await openTable(h, page, '/manage/audit', 'Audit log');
    const auditHeads = await page.evaluate(regionHeads('Audit log'));
    check(JSON.stringify(auditHeads) === JSON.stringify(['Action', 'When']), `the audit log is a table with the headers Action and When (${JSON.stringify(auditHeads)})`);
    const when = await page.evaluate(`(() => { const th = [...document.querySelectorAll('[role="region"] thead th')].find((x) => x.textContent.trim() === 'When'); return th ? { sort: th.getAttribute('aria-sort'), button: !!th.querySelector('button') } : null; })()`);
    check(when !== null && when.sort === 'descending' && !when.button, `the When header says descending and has no button (${JSON.stringify(when)})`);

    // 2. The overview's recent activity is the same table.
    await openTable(h, page, '/manage', 'Recent activity');
    const overviewHeads = await page.evaluate(regionHeads('Recent activity'));
    check(JSON.stringify(overviewHeads) === JSON.stringify(['Action', 'When']), `the recent activity is a table with the same headers (${JSON.stringify(overviewHeads)})`);

    // 3. Links: the existing words are the headers. A row's Delete is a named icon on a desktop.
    await openTable(h, page, '/manage/links', 'Links');
    const linkHeads = await page.evaluate(regionHeads('Links'));
    check(JSON.stringify(linkHeads.slice(0, 5)) === JSON.stringify(['Name', 'Who can open', 'Kind', 'Created', 'Status']), `the links are a table with its headers (${JSON.stringify(linkHeads)})`);
    const linkRows = await page.evaluate(`[...document.querySelectorAll('[role="region"][aria-label="Links"] tbody tr[data-id]')].length`);
    if (linkRows > 0) {
      const icons = await page.evaluate(`[...document.querySelectorAll('[role="region"][aria-label="Links"] tbody button[aria-label^="Delete link"]')].length`);
      check(icons === linkRows, `each link row has its Delete icon on a desktop (${icons} of ${linkRows})`);
    }

    // 4. Manage > Storage: the people table, and the team spaces sort by name with a header press that turns aria-sort.
    await openTable(h, page, '/manage/storage', 'People, by use');
    const peopleHeads = await page.evaluate(regionHeads('People, by use'));
    check(peopleHeads !== null && peopleHeads[0] === 'Person' && peopleHeads[1] === 'Used' && peopleHeads[2] === 'Limit', `the people are a table with the headers Person, Used and Limit (${JSON.stringify(peopleHeads)})`);
    const spacesThere = await page.evaluate(`!!document.querySelector('[role="region"][aria-label="Team spaces"] tbody tr[data-id]')`);
    if (spacesThere) {
      const sortOf = `(() => { const th = [...document.querySelectorAll('[role="region"][aria-label="Team spaces"] thead th')].find((x) => x.textContent.trim().startsWith('Team space')); return th ? th.getAttribute('aria-sort') : null; })()`;
      const before = await page.evaluate(sortOf);
      await page.evaluate(`[...document.querySelectorAll('[role="region"][aria-label="Team spaces"] thead th')].find((x) => x.textContent.trim().startsWith('Team space')).querySelector('button').click(), true`);
      await page.waitFor(`(${sortOf}) !== ${q(before)}`, 10000);
      const after = await page.evaluate(sortOf);
      check(before === 'ascending' && after === 'descending', `pressing the team space header turns its aria-sort from ascending to descending (${before} then ${after})`);
    }

    // 5. At 375 pixels no page scrolls sideways, and a drive row keeps only its ⋮ menu (its icons are in the menu).
    await setPhone(page, true);
    for (const path of ['/manage/audit', '/manage/links', '/manage/storage', '/storage']) {
      await navigateTo(page, path);
      await sleep(600);
      check(!(await sideways(page)), `${path} at 375 px has no sideways scroll`);
    }
    await navigateTo(page, '/storage');
    await page.waitFor(`!!document.querySelector('[role="region"][aria-label="Largest files"]')`, 20000);
    const phoneIcons = await page.evaluate(`document.querySelectorAll('[role="region"][aria-label="Largest files"] .row-acts').length`);
    check(phoneIcons === 0, `a drive row at 375 px keeps only its ⋮ menu (${phoneIcons} inline icon groups)`);
    await setPhone(page, false);

    // 6. In Hebrew the audit table is drawn right to left, with its headers still in place.
    await navigateTo(page, '/manage/audit');
    await setLanguage(page, 'עברית');
    await page.waitFor(`document.documentElement.dir === 'rtl' && [...document.querySelectorAll('[role="region"] thead th')].length === 2`, 15000);
    check(true, 'in Hebrew the audit table is drawn right to left with its two headers');
    await setLanguage(page, 'English');

    // 7. The pictures, if a folder is asked for: each table in light, dark, Hebrew and at 375 pixels.
    if (shots) {
      await mkdir(shots, { recursive: true });
      for (const p of SHOT_PAGES) {
        await openTable(h, page, p.path, p.region);
        await shot(page, shots, `${p.table}-light.png`);
        await setTheme(page, 'Dark');
        await sleep(400);
        await shot(page, shots, `${p.table}-dark.png`);
        await setTheme(page, 'Light');
        await setLanguage(page, 'עברית');
        await sleep(400);
        await shot(page, shots, `${p.table}-he.png`);
        await setLanguage(page, 'English');
        await setPhone(page, true);
        await sleep(400);
        await shot(page, shots, `${p.table}-375.png`);
        await setPhone(page, false);
      }
      check(true, `the tables are saved in ${shots}`);
    }
  } finally {
    // Whatever happened, the owner's page comes back to English, the system's theme, and a desktop window.
    await setPhone(page, false).catch(() => undefined);
    await setLanguage(page, 'English').catch(() => undefined);
    await setTheme(page, 'System').catch(() => undefined);
  }
}
