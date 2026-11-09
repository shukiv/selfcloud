// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice em1 (decision 111): an empty state is antd's Empty, with the same words as before. The member's empty pages (Starred,
// Recent, Shared, Trash) are read at 1280 and 375 pixels, in light and dark, and the Starred page in Hebrew (right to left). The
// compact case is a file's Versions panel, with one version and so no earlier one. Checked: each empty state is antd's .ant-empty
// with its words in the page's status region, no page scrolls sideways at 375 pixels, and the Hebrew page is right to left.
// Run alone with SELFCLOUD_E2E_ONLY=em1. With SELFCLOUD_E2E_SHOTS set to a folder, the pictures are saved there. The pictures are
// taken before the checks, so a failing check still leaves them.
import { mkdir } from 'node:fs/promises';
import { readFileSync } from 'node:fs';
import { setLanguage, setTheme, shot, sleep } from './media.mjs';

const PAGES = [
  { key: 'starred', path: '/starred', text: 'Nothing is starred yet. Star a file or folder to find it here.' },
  { key: 'recent', path: '/recent', text: 'Nothing here yet. Files you open or change appear here.' },
  { key: 'trash', path: '/trash', text: 'The trash is empty.' },
];
const VERSIONS_TEXT = 'There are no earlier versions.';
// The Hebrew words come from the catalogue itself: the text before the first placeholder of the Starred page's words.
const he = JSON.parse(readFileSync(new URL('../src/i18n/he.json', import.meta.url), 'utf8'));
const HE_STARRED = he['starred.empty'];

const q = (v) => JSON.stringify(v);

// emptyOf reads the first antd Empty in the scope: its words, and whether it sits in a role=status region.
const emptyOf = (page, scope = 'main') =>
  page.evaluate(`(() => { const root = document.querySelector(${q(scope)}) ?? document; const e = root.querySelector('.ant-empty'); if (!e) return null; return { text: e.querySelector('.ant-empty-description')?.textContent.trim() ?? null, status: !!e.closest('[role="status"]') }; })()`);

// sideways reads whether the page is wider than its window, which is a sideways scroll on a phone.
const sideways = (page) => page.evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`);

// setWidth gives the page a window of this width (a touch phone at 375 pixels, or the desktop at 1280).
async function setWidth(page, width) {
  await page.send('Emulation.setDeviceMetricsOverride', { width, height: 800, deviceScaleFactor: 1, mobile: width < 800 });
  await sleep(300);
}

export async function em1Checks(h, { page, base, accountKey, shotsDir = process.env.SELFCLOUD_E2E_SHOTS || null }) {
  const { check, navigateTo, rowAppears, selectRow, clickButton, clickOn, rawPut } = h;
  const suffix = Date.now().toString(36);
  const name = `em1-note-${suffix}.txt`;
  if (shotsDir) await mkdir(shotsDir, { recursive: true });
  const snap = async (file) => {
    if (shotsDir) await shot(page, shotsDir, file);
  };
  const found = [];

  // A page's empty state: its picture, its words and its status region, read after the picture is taken.
  const readPage = async (p, label, sidewaysToo) => {
    await navigateTo(page, p.path);
    await page.waitFor(`document.body.innerText.includes(${q(p.text)})`, 15000);
    await sleep(300);
    await snap(`em1-${p.key}-${label}.png`);
    found.push({ what: `${p.key} (${label})`, text: p.text, status: true, empty: await emptyOf(page), sideways: sidewaysToo ? await sideways(page) : null });
  };

  // The earlier sections of a full run leave the member's Recent and trash with items. Recent is cleared and the trash emptied
  // through their own buttons, each with its confirmation, so the pages read empty as a person sees them.
  await setWidth(page, 1280);
  const recentText = PAGES.find((p) => p.key === 'recent').text;
  await navigateTo(page, '/recent');
  await page.waitFor(`!!Array.from(document.querySelectorAll('button')).find((b) => b.textContent.trim() === 'Clear all') || document.body.innerText.includes(${q(recentText)})`, 15000);
  if (await page.evaluate(`!!Array.from(document.querySelectorAll('button')).find((b) => b.textContent.trim() === 'Clear all')`)) {
    await clickButton(page, 'Clear all');
    await page.waitFor(`!!document.querySelector('.ant-modal .ant-modal-footer button:last-child')`, 10000);
    await clickOn(page, '.ant-modal .ant-modal-footer button:last-child');
  }
  await page.waitFor(`document.body.innerText.includes(${q(recentText)})`, 15000);
  await navigateTo(page, '/trash');
  await page.waitFor(`document.readyState === 'complete'`, 10000);
  if (await page.evaluate(`!!Array.from(document.querySelectorAll('button')).find((b) => b.textContent.trim() === 'Empty trash')`)) {
    await clickButton(page, 'Empty trash');
    await page.waitFor(`!!document.querySelector('.ant-modal .ant-modal-footer button:last-child')`, 10000);
    await clickOn(page, '.ant-modal .ant-modal-footer button:last-child');
  }
  await page.waitFor(`document.body.innerText.includes(${q(PAGES.find((p) => p.key === 'trash').text)})`, 30000);

  // Desktop: light, then dark. The theme is chosen at the desktop width.
  await setTheme(page, 'Light');
  for (const p of PAGES) await readPage(p, '1280-light', false);
  await setTheme(page, 'Dark');
  for (const p of PAGES) await readPage(p, '1280-dark', false);

  // Phone: dark now (every page), then light, switched back at the desktop width.
  await setWidth(page, 375);
  for (const p of PAGES) await readPage(p, '375-dark', true);
  await setWidth(page, 1280);
  await setTheme(page, 'Light');
  await setWidth(page, 375);
  for (const p of PAGES) await readPage(p, '375-light', true);
  await setWidth(page, 1280);
  await setTheme(page, 'Dark');

  // Hebrew: the Starred page reads right to left, with its words from the Hebrew catalogue.
  await setLanguage(page, 'עברית');
  await readPage({ key: 'starred-he', path: '/starred', text: HE_STARRED }, '1280-dark-he', false);
  found.push({ what: 'starred in Hebrew (right to left)', text: HE_STARRED, status: true, empty: await emptyOf(page), sideways: null, rtl: await page.evaluate(`document.documentElement.dir`) });
  await setLanguage(page, 'English');

  // The compact case: a file with one version, whose Versions panel says there are no earlier versions.
  const pf = await page.evaluate(`fetch('/api/v1/me?storage=breakdown', { credentials: 'same-origin' }).then((r) => r.json()).then((j) => j.personal_folder_id)`);
  await rawPut(base, `/api/v1/folders/${pf}/files?path=${encodeURIComponent(`/${name}`)}`, accountKey, 'e'.repeat(300), 'text/plain');
  for (const theme of ['Light', 'Dark']) {
    await setTheme(page, theme);
    await navigateTo(page, `/files/${pf}`);
    await rowAppears(page, name);
    await selectRow(page, name);
    await clickButton(page, 'Versions');
    await page.waitFor(`!!document.querySelector('[data-testid="versions"]') && document.querySelector('[data-testid="versions"]').innerText.includes(${q(VERSIONS_TEXT)})`, 15000);
    await sleep(300);
    await snap(`em1-versions-compact-1280-${theme.toLowerCase()}.png`);
    found.push({ what: `versions panel, compact (${theme})`, text: VERSIONS_TEXT, status: null, empty: await emptyOf(page, '[data-testid="versions"]'), sideways: null });
    await page.waitFor(`(() => { const b = document.querySelector('[data-testid="versions"] .ant-drawer-close'); if (!b) return false; b.click(); return true; })()`, 10000);
    await page.waitFor(`!document.querySelector('[data-testid="versions"]')`, 10000);
  }

  for (const f of found) {
    if (!f.empty) check(false, `em1: ${f.what} shows antd's Empty (none on the page: ${JSON.stringify(f.empty ?? null)})`);
    check(f.empty.text === f.text, `em1: ${f.what} shows antd's Empty with its words (${f.empty.text})`);
    if (f.status !== null) check(f.empty.status === true, `em1: ${f.what} keeps its status region`);
    if (f.sideways !== null) check(f.sideways === false, `em1: ${f.what} does not scroll sideways`);
    if (f.rtl !== undefined) check(f.rtl === 'rtl', `em1: ${f.what} is right to left (${f.rtl})`);
  }
}
