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
  // wrapClass: the class the old element had, which owner.mjs waits for on Recent (`.empty`), kept on EmptyState's wrapper.
  // title is the table's empty title (no full stop: the owner's rule for empty titles, decisions 137 and 138) and text is its
  // description, each read apart and compared exactly. The trash has a title and no description (text null).
  { key: 'starred', path: '/starred', title: 'Nothing starred yet', text: 'Nothing is starred yet. Star a file or folder to find it here.', wrapClass: 'empty' },
  { key: 'recent', path: '/recent', title: 'Recent is empty', text: 'Nothing here yet. Files you open or change appear here.', wrapClass: 'empty' },
  { key: 'trash', path: '/trash', title: 'The trash is empty', text: null },
];
const VERSIONS_TEXT = 'There are no earlier versions.';
// The Hebrew words come from the catalogue itself: the text before the first placeholder of the Starred page's words.
const he = JSON.parse(readFileSync(new URL('../src/i18n/he.json', import.meta.url), 'utf8'));
const HE_STARRED = he['starred.empty'];
const HE_STARRED_TITLE = JSON.parse(readFileSync(new URL('../src/i18n/drive/he.json', import.meta.url), 'utf8'))['drive.empty.starred'];

const q = (v) => JSON.stringify(v);

// emptyOf reads the first antd Empty in the scope: its words, and whether it sits in a role=status region.
const emptyOf = (page, scope = 'main') =>
  page.evaluate(`(() => { const root = document.querySelector(${q(scope)}) ?? document; const e = root.querySelector('.ant-empty'); if (!e) return null; return { text: e.querySelector('.ant-empty-description')?.textContent.trim() ?? null, title: e.querySelector('.empty-title')?.textContent.trim() ?? null, desc: e.querySelector('.empty-desc')?.textContent.trim() ?? null, status: !!e.closest('[role="status"]') }; })()`);

// sideways reads whether the page is wider than its window, which is a sideways scroll on a phone.
const sideways = (page) => page.evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`);

// setWidth gives the page a window of this width (a touch phone at 375 pixels, or the desktop at 1280).
async function setWidth(page, width) {
  await page.send('Emulation.setDeviceMetricsOverride', { width, height: 800, deviceScaleFactor: 1, mobile: width < 800 });
  await sleep(300);
}

export async function em1Checks(h, { page, base, accountKey, accountID, MEMBER, shotsDir = process.env.SELFCLOUD_E2E_SHOTS || null }) {
  const { check, api, navigateTo, rowAppears, selectRow, clickButton, rawPut, signIn, signOut } = h;
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
    await page.waitFor(`document.body.innerText.includes(${q(p.title)})`, 15000);
    await sleep(300);
    await snap(`em1-${p.key}-${label}.png`);
    const wrapClass = p.wrapClass ? await page.evaluate(`!!document.querySelector('.${p.wrapClass} .ant-empty')`) : null;
    found.push({ what: `${p.key} (${label})`, title: p.title, text: p.text, status: true, empty: await emptyOf(page), sideways: sidewaysToo ? await sideways(page) : null, wrapClass });
  };

  // The empty pages are read as a new member who has nothing: made and verified through the API here, and signed in on the page.
  // The member the earlier sections use keeps whatever they left in Recent and the trash. An editor's own trashed item in a team
  // space (sd1 leaves one) is one the editor may not purge, so Empty trash cannot clear it and the page never reads empty. So
  // nothing is cleared here: the pages are read as this new member, and the signed-in member is put back at the end.
  const fresh = { email: `em1-${suffix}@example.com`, name: `Em1 Nothing Yet ${suffix}`, password: MEMBER.password };
  const created = await api(base, 'POST', `/api/v1/accounts/${accountID}/users`, accountKey, {
    email: fresh.email,
    display_name: fresh.name,
    role: 'member',
    verification: 'link',
  });
  await api(base, 'POST', '/api/v1/auth/verify', null, { token: created.verification.token, password: fresh.password });
  await setWidth(page, 1280);
  await signOut(page);
  await signIn(page, fresh);
  await navigateTo(page, '/trash');
  await page.waitFor(`document.body.innerText.includes(${q(PAGES.find((p) => p.key === 'trash').title)})`, 15000);

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
  await readPage({ key: 'starred-he', path: '/starred', title: HE_STARRED_TITLE, text: HE_STARRED }, '1280-dark-he', false);
  found.push({ what: 'starred in Hebrew (right to left)', title: HE_STARRED_TITLE, text: HE_STARRED, status: true, empty: await emptyOf(page), sideways: null, rtl: await page.evaluate(`document.documentElement.dir`) });
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

  // The empty folder's drop box (decision 85, review 1): a dashed box around the Empty, at both widths and in both themes.
  const made = await api(base, 'POST', `/api/v1/folders/${pf}/directories`, accountKey, { path: `/em1-empty-${suffix}`, parents: false });
  await navigateTo(page, `/files/${pf}/${made.id}`);
  await page.waitFor(`!!document.querySelector('[data-testid="empty-folder"]')`, 15000);
  await sleep(300);
  const readBox = () =>
    page.evaluate(`(() => { const b = document.querySelector('[data-testid="empty-folder"]'); if (!b) return null; return { cls: b.classList.contains('empty-folder-drop'), border: getComputedStyle(b).borderTopStyle, desc: b.querySelector('.ant-empty .ant-empty-description')?.textContent.trim() ?? null, sideways: document.documentElement.scrollWidth > document.documentElement.clientWidth }; })()`);
  const shotBox = async (name, width, theme) => {
    await snap(`em1-emptyfolder-${width}-${theme}.png`);
    const r = await readBox();
    found.push({ what: `empty folder drop box at ${width} px, ${theme}`, text: null, status: null, empty: r && r.desc ? { text: r.desc, status: null } : null, sideways: r ? r.sideways : null, border: r ? r.border : null, cls: r ? r.cls : null, name });
  };
  await setWidth(page, 1280);
  await setTheme(page, 'Dark');
  await shotBox('dark', 1280, 'dark');
  await setTheme(page, 'Light');
  await shotBox('light', 1280, 'light');
  await setWidth(page, 375);
  await shotBox('light', 375, 'light');
  await setWidth(page, 1280);
  await setTheme(page, 'Dark');
  await setWidth(page, 375);
  await shotBox('dark', 375, 'dark');
  await setWidth(page, 1280);
  // The member the rest of the run signs in as is put back.
  await signOut(page);
  await signIn(page, MEMBER);

  for (const f of found) {
    if (f.border !== undefined && f.border !== null) check(f.border === 'dashed', `em1: ${f.what} has its dashed border (${f.border})`);
    if (f.cls !== undefined && f.cls !== null) check(f.cls === true, `em1: ${f.what} carries the drop box class`);
    if (f.wrapClass !== undefined && f.wrapClass !== null) check(f.wrapClass === true, `em1: ${f.what} keeps the class its old element had, around antd's Empty`);
  }
  for (const f of found) {
    if (!f.empty) check(false, `em1: ${f.what} shows antd's Empty (none on the page: ${JSON.stringify(f.empty ?? null)})`);
    if (f.name) check(f.empty.text.startsWith('This folder is empty.'), `em1: ${f.what} shows the heading and the hint in antd's Empty (${f.empty.text})`);
    // A table's empty body is its title and the page's sentence as the description (decision 137), each exact; a page with no
    // description shows none.
    else if (f.title !== undefined) {
      check(f.empty.title === f.title, `em1: ${f.what} shows its empty title exactly (${f.empty.title})`);
      if (f.text === null) check(f.empty.desc === null, `em1: ${f.what} shows no description (${f.empty.desc})`);
      else check(f.empty.desc === f.text, `em1: ${f.what} shows its description exactly (${f.empty.desc})`);
    } else check(f.empty.text === f.text, `em1: ${f.what} shows antd's Empty with its words (${f.empty.text})`);
    if (f.status !== null) check(f.empty.status === true, `em1: ${f.what} keeps its status region`);
    if (f.sideways !== null) check(f.sideways === false, `em1: ${f.what} does not scroll sideways`);
    if (f.rtl !== undefined) check(f.rtl === 'rtl', `em1: ${f.what} is right to left (${f.rtl})`);
  }
}
