// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice nt2, the New menu (design-nt2 section 3, design-nt2-phase1 W1): at a desktop width an editor opens the sidebar's New in a
// folder of Team A. The menu holds the groups in order (New folder | Upload files, Upload folder | Document, Spreadsheet, Presentation,
// More), each kind is a submenu of Blank and From a template, and the chord Alt+C then F makes a folder with the menu closed. Blank
// under Document makes a blank file and opens it in the editor (the fake converter's tab). The folder is removed in a finally.
import { join } from 'node:path';
import { launchBrowser, openedTab, Page, tabIds } from './cdp.mjs';
import { sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);

// key sends one key press (down and up) to the focused element, with the modifiers the chord needs (Alt is 1).
async function key(page, { code, key: name, vk, alt = false }) {
  const modifiers = alt ? 1 : 0;
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: name, code, windowsVirtualKeyCode: vk, modifiers });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: name, code, windowsVirtualKeyCode: vk, modifiers });
  await sleep(120);
}

// openSubmenu opens a kind's submenu in the open New menu with the keyboard (ArrowRight on its title).
async function openSubmenu(page, kind) {
  const title = `[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-submenu-title')].find((x) => x.textContent.trim() === ${q(kind)})`;
  await page.waitFor(`!!${title}`, 10000);
  // The keyboard opens it, as ArrowRight does in a menu (antd): the title is focused, then the key is sent to it.
  await page.evaluate(`${title}.focus()`);
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'ArrowRight', code: 'ArrowRight', windowsVirtualKeyCode: 39 });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'ArrowRight', code: 'ArrowRight', windowsVirtualKeyCode: 39 });
  await page.waitFor(`[...document.querySelectorAll('[role="menuitem"]')].filter((x) => !x.closest('.ant-dropdown-hidden, .ant-menu-hidden, [hidden]')).some((x) => x.textContent.trim() === 'Blank')`, 10000);
}

// clickItem clicks the open menu's entry with this text, with a user gesture (a new tab needs one).
async function clickItem(page, text) {
  const item = `[...document.querySelectorAll('[role="menuitem"]')].filter((x) => !x.closest('.ant-dropdown-hidden, .ant-menu-hidden, [hidden]')).filter((x) => x.textContent.trim() === ${q(text)})`;
  await page.waitFor(`${item}.length > 0`, 10000);
  await page.send('Runtime.evaluate', { expression: `${item}[0].click()`, userGesture: true, returnByValue: true, awaitPromise: true });
}

// nt2MenuChecks runs the section. The member is an editor of Team A; the helpers are the run's.
export async function nt2MenuChecks(h, { base, accountKey, teamA, dir, browserBinary, MEMBER, EXPECTED, signIn, navigateTo, fake }) {
  const { check, api } = h;
  const suffix = Date.now().toString(36);
  const folderName = `NT2 menu ${suffix}`;
  let folderID = null;
  let newID = null;
  const launch = await launchBrowser(browserBinary, join(dir, 'nt2menu-profile'));
  let page = new Page(launch.wsUrl);
  page.base = base;
  try {
    await page.start(EXPECTED);
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    await signIn(page, MEMBER);
    const made = await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `/${folderName}`, parents: false });
    folderID = made.id;
    await navigateTo(page, `/files/${teamA}/${folderID}`);
    await page.waitFor(`location.pathname === ${q(`/files/${teamA}/${folderID}`)} && !!document.querySelector('.shell-sider [data-testid="new-button"]:not([disabled])')`, 15000);

    // The groups, in order, and their hints: a divider between the groups, the three actions with their chord letters.
    await page.evaluate(`document.querySelector('.shell-sider [data-testid="new-button"]').click()`);
    await page.waitFor(`document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item, .ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-submenu-title').length >= 6`, 10000);
    const order = await page.evaluate(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) > .ant-dropdown-menu > li')].map((li) => (li.classList.contains('ant-dropdown-menu-item-divider') ? '|' : li.textContent.trim()))`);
    const want = ['New folder', '|', 'Upload files', 'Upload folder', '|', 'Document', 'Spreadsheet', 'Presentation', 'More'];
    check(JSON.stringify(order) === JSON.stringify(want), `the New menu's groups are in order: ${order.join(' / ')}`);
    const hints = await page.evaluate(`(() => { const row = [...document.querySelectorAll('[role="menuitem"]')].filter((x) => !x.closest('.ant-dropdown-hidden, .ant-menu-hidden, [hidden]')).find((x) => x.textContent.trim() === 'New folder'); return { keys: row?.getAttribute('aria-keyshortcuts'), hint: row?.querySelector('.menu-hint')?.getAttribute('data-hint') ?? null }; })()`);
    check(hints.keys === 'Alt+C F' && hints.hint === 'Alt+C, F', `New folder's row names its chord and shows the hint (${hints.keys}, ${hints.hint})`);
    // The rows lay out during the menu's open animation (0 px at the first frame), so wait for a laid-out row first.
    const rowSel = `[...document.querySelectorAll('[role="menuitem"]')].filter((x) => !x.closest('.ant-dropdown-hidden, .ant-menu-hidden, [hidden]'))`;
    await page.waitFor(`${rowSel}.every((x) => x.getBoundingClientRect().height > 0)`, 5000);
    const rowHeight = await page.evaluate(`${rowSel}.find((x) => x.textContent.trim() === 'New folder').getBoundingClientRect().height`);
    check(Math.round(rowHeight) === 40, `a mouse's row is 40 px (${Math.round(rowHeight)})`);
    const heights = await page.evaluate(`${rowSel}.map((x) => x.textContent.trim() + ':' + Math.round(x.getBoundingClientRect().height)).join(' | ')`);
    check(!/:(?!40\b)\d+/.test(heights), `every row of the New menu, submenu titles too, is 40 px (${heights})`);
    const width = await page.evaluate(`Math.round(document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden) > .ant-dropdown-menu').getBoundingClientRect().width)`);
    check(width >= 320 && width <= 400, `the menu is 320 to 400 px wide (${width})`);

    // From a template, under Document, goes to the gallery at this folder, with the kind.
    await openSubmenu(page, 'Document');
    await clickItem(page, 'From a template');
    await page.waitFor(`location.pathname === ${q(`/new/${teamA}/${folderID}`)} && location.search === '?kind=document'`, 15000);
    check(true, 'From a template goes to the gallery for this directory, with kind=document');
    await navigateTo(page, `/files/${teamA}/${folderID}`);
    await page.waitFor(`location.pathname === ${q(`/files/${teamA}/${folderID}`)} && !!document.querySelector('.shell-sider [data-testid="new-button"]:not([disabled])')`, 15000);

    // Blank under Document makes a blank Word file in the folder and opens it in the editor (decision 102: a new tab).
    await page.evaluate(`document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))`);
    await page.waitFor(`document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden)').length === 0`, 10000);
    await page.evaluate(`document.querySelector('.shell-sider [data-testid="new-button"]').click()`);
    const listing = page;
    const before = await tabIds(listing);
    await openSubmenu(page, 'Document');
    await clickItem(page, 'Blank');
    page = await openedTab(listing, '/edit/', before);
    await page.waitFor(`location.pathname.startsWith('/edit/') && location.pathname !== '/edit/wait'`, 15000);
    newID = (await page.evaluate(`location.pathname`)).replace('/edit/', '');
    const created = await api(base, 'GET', `/api/v1/nodes/${newID}`, accountKey);
    check(created.kind === 'file' && created.folder_id === teamA && created.name.endsWith('.docx'), `Blank under Document made a Word file in the folder (${created.name})`);
    await page.waitFor(`!!document.querySelector('iframe[name="selfcloud-office-editor"]')`, 15000);
    check(true, 'the blank file opens in the editor (the fake converter)');
    page.close();
    page = listing;
    await page.waitFor(`location.pathname === ${q(`/files/${teamA}/${folderID}`)}`, 15000);

    // The chord, with the menu closed: Alt+C then F opens the New folder dialog, and it opens nothing else.
    await page.evaluate(`document.body.focus()`);
    await key(page, { code: 'KeyC', key: 'c', vk: 67, alt: true });
    await key(page, { code: 'KeyF', key: 'f', vk: 70 });
    await page.waitFor(`!!document.querySelector('.ant-modal-wrap:not(.ant-modal-wrap-hidden) input, [role="dialog"] input')`, 10000);
    const title = await page.evaluate(`(document.querySelector('.ant-modal-title, [role="dialog"] h2, [role="dialog"] .ant-modal-title')?.textContent ?? '').trim()`);
    check(title === 'New folder', `Alt+C then F opens the New folder dialog with the menu closed (${title})`);
    check(await page.evaluate(`!document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden)')`), 'the chord opened no menu');
    await key(page, { code: 'Escape', key: 'Escape', vk: 27 });
    await page.waitFor(`!document.querySelector('.ant-modal-wrap:not(.ant-modal-wrap-hidden)')`, 10000);
  } finally {
    page.close();
    launch.proc.kill();
    if (newID) await api(base, 'DELETE', `/api/v1/nodes/${newID}?permanent=true`, accountKey).catch(() => {});
    if (folderID) await api(base, 'DELETE', `/api/v1/nodes/${folderID}?permanent=true`, accountKey).catch(() => {});
  }
}
