// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice nt2, the New menu (decision 108): at a desktop width an editor opens the sidebar's New in a
// folder of Team A. The menu holds the groups in order (New folder | Upload files, Upload folder | Document, Spreadsheet, Presentation,
// More), each kind is a submenu of Blank and From a template, and the chord Alt+C then F makes a folder with the menu closed. Blank
// under Document makes a blank file and opens it in the editor (the fake converter's tab). The folder is removed in a finally.
import { join } from 'node:path';
import { launchBrowser, openedTab, Page, tabIds } from './cdp.mjs';
import { setLanguage, sleep } from './media.mjs';
import { hoverMenuTitle, menuRowChecks, menuShot } from './menu.mjs';

const q = (v) => JSON.stringify(v);

// key sends one key press (down and up) to the focused element, with the modifiers the chord needs (Alt is 1).
// OPEN_ITEMS is the menu items of the dropdowns that are open now, and of the submenu popups (a submenu popup is not inside a
// .ant-dropdown: it is its own .ant-dropdown-menu-submenu-popup). A closed dropdown or a hidden popup is not counted.
const OPEN_ITEMS = "[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) [role=\"menuitem\"], .ant-dropdown-menu-submenu-popup [role=\"menuitem\"]')].filter((x) => !x.closest('.ant-dropdown-hidden, .ant-dropdown-menu-submenu-hidden, .ant-menu-hidden, [hidden]'))";
// NEW_MENU is the open New menu: the open dropdown with the New folder row, and not one that is closing (its leave animation).
const NEW_MENU = "[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden)')].find((d) => d.textContent.includes('New folder') && !d.classList.contains('ant-slide-up-leave'))";
// NEW_MENU_SIZE is the New menu's width and its New folder row's height now, or null when there is no such menu.
const NEW_MENU_SIZE = `(() => { const d = ${NEW_MENU}; if (!d) return null; const row = [...d.querySelectorAll('[role="menuitem"]')].find((x) => x.textContent.trim() === 'New folder'); if (!row) return null; return { w: Math.round(d.getBoundingClientRect().width), rowH: Math.round(row.getBoundingClientRect().height) }; })()`;
// TITLE_HEIGHT is the rounded height of the open New menu's Document submenu title.
const TITLE_HEIGHT = "Math.round([...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-submenu-title')].find((x) => x.textContent.trim() === 'Document').getBoundingClientRect().height)";
// OPEN_MODAL is the modal that is displayed now (a closed one stays in the DOM, with display none).
const OPEN_MODAL = "[...document.querySelectorAll('.ant-modal-wrap')].find((w) => getComputedStyle(w).display !== 'none')";
// waitNoDropdown waits for the Escape to close every open dropdown; on failure it names the dropdowns still open.
async function waitNoDropdown(page) {
  try {
    await page.waitFor(`document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden)').length === 0`, 10000);
  } catch {
    const open = await page.evaluate(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden)')].map((d) => d.className + ' | ' + d.textContent.trim().slice(0, 60))`).catch(() => 'unreadable');
    throw new Error(`Escape did not close the menu; open: ${JSON.stringify(open)}`);
  }
}
async function key(page, { code, key: name, vk, alt = false }) {
  const modifiers = alt ? 1 : 0;
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: name, code, windowsVirtualKeyCode: vk, modifiers });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: name, code, windowsVirtualKeyCode: vk, modifiers });
  await sleep(120);
}

// openSubmenu opens a kind's submenu in the open New menu with the keyboard (ArrowRight on its title).
async function openSubmenu(page, kind, child = 'Blank') {
  const title = `[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-submenu-title')].find((x) => x.textContent.trim() === ${q(kind)})`;
  await page.waitFor(`!!${title}`, 10000);
  // The keyboard opens it, as ArrowRight does in a menu (antd): the title is focused, then the key is sent to it.
  // The menu animates in: its first frame is not ready for the keyboard, so the key is sent after the animation ends.
  await page.waitFor(`!document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden)[class*="ant-slide-up-appear"]')`, 5000);
  // A first ArrowRight can land before the title takes it (probe, 2026-11-09: the submenu was not open 10 s later, and was open
  // after the next key). So the key is sent again, up to three times, until the title reports it is expanded.
  for (let attempt = 0; attempt < 3; attempt += 1) {
    await page.evaluate(`${title}.focus()`);
    await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'ArrowRight', code: 'ArrowRight', windowsVirtualKeyCode: 39 });
    await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'ArrowRight', code: 'ArrowRight', windowsVirtualKeyCode: 39 });
    await new Promise((r) => setTimeout(r, 800));
    if (await page.evaluate(`${title}.getAttribute('aria-expanded') === 'true'`)) break;
  }
  await page.waitFor(`${OPEN_ITEMS}.some((x) => x.textContent.trim() === ${q(child)})`, 10000);
}

// clickItem clicks the open menu's entry with this text, with a user gesture (a new tab needs one).
async function clickItem(page, text) {
  const item = `${OPEN_ITEMS}.filter((x) => x.textContent.trim() === ${q(text)})`;
  await page.waitFor(`${item}.length > 0`, 10000);
  await page.send('Runtime.evaluate', { expression: `${item}[0].click()`, userGesture: true, returnByValue: true, awaitPromise: true });
}

// nt2MenuChecks runs the section. The member is an editor of Team A; the helpers are the run's.
export async function nt2MenuChecks(h, { base, accountKey, teamA, dir, browserBinary, MEMBER, EXPECTED, signIn, navigateTo, fake }) {
  const { check, api } = h;
  const suffix = Date.now().toString(36);
  const folderName = `NT2 menu ${suffix}`;
  const shotsDir = process.env.SELFCLOUD_E2E_SHOTS || null;
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
    const hints = await page.evaluate(`(() => { const row = ${OPEN_ITEMS}.find((x) => x.textContent.trim() === 'New folder'); return { keys: row?.getAttribute('aria-keyshortcuts'), hint: row?.querySelector('.menu-hint')?.getAttribute('data-hint') ?? null }; })()`);
    check(hints.keys === 'Alt+C F' && hints.hint === 'Alt+C, F', `New folder's row names its chord and shows the hint (${hints.keys}, ${hints.hint})`);
    // The rows lay out during the menu's open animation (0 px at the first frame, shorter while it runs). The wait returns the
    // New menu's size only once its New folder row is 40 px, so the size it returns is the one that was checked.
    const size = await page.waitFor(`(() => { const s = ${NEW_MENU_SIZE}; return s && s.rowH === 40 ? s : null; })()`, 5000);
    check(size.rowH === 40, `a mouse's row is 40 px (${size.rowH})`);
    // Every top-level row of the open New menu, submenu titles included (a submenu's row is its title, a child of its item).
    const heights = await page.evaluate(`[...(${NEW_MENU})?.querySelectorAll(':scope > .ant-dropdown-menu > li:not(.ant-dropdown-menu-item-divider)') ?? []].map((li) => { const row = li.matches('[role="menuitem"]') ? li : li.querySelector(':scope > [role="menuitem"]'); return (row ? row.textContent.trim() : '?') + ':' + Math.round(row ? row.getBoundingClientRect().height : -1); }).join(' | ')`);
    check(!/:(?!40\b)-?\d+/.test(heights) && !heights.includes('?'), `every row of the New menu, submenu titles too, is 40 px (${heights})`);
    check(size.w >= 320 && size.w <= 400, `the menu is 320 to 400 px wide (${size.w})`);
    // From a template, under Document, goes to the gallery at this folder, with the kind.
    await openSubmenu(page, 'Document');
    if (shotsDir) await menuShot(page, shotsDir, 'nt2-menu-document.png');
    await menuRowChecks(page, check, 'the New menu, Document submenu open', 40);
    await clickItem(page, 'From a template');
    await page.waitFor(`location.pathname === ${q(`/new/${teamA}/${folderID}`)} && location.search === '?kind=document'`, 15000);
    check(true, 'From a template goes to the gallery for this directory, with kind=document');
    await navigateTo(page, `/files/${teamA}/${folderID}`);
    await page.waitFor(`location.pathname === ${q(`/files/${teamA}/${folderID}`)} && !!document.querySelector('.shell-sider [data-testid="new-button"]:not([disabled])')`, 15000);

    // Blank under Document makes a blank Word file in the folder and opens it in the editor (decision 102: a new tab).
    await key(page, { code: 'Escape', key: 'Escape', vk: 27 });
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
    // The listing was in the background while the editor opened, and a background tab does not run the Blank menu's close
    // animation, so the menu stayed open (leave state). Bring the listing to front and wait for the close, as the chord step does.
    await page.send('Page.bringToFront');
    await waitNoDropdown(page);

    // The pointer kind sets the submenu title's height: a mouse is 40 px, a finger (a coarse pointer) 44 px.
    const reopenNew = async () => {
      await key(page, { code: 'Escape', key: 'Escape', vk: 27 });
      await waitNoDropdown(page);
      await page.evaluate(`document.querySelector('.shell-sider [data-testid="new-button"]').click()`);
      await page.waitFor(`${OPEN_ITEMS}.length >= 6`, 10000);
      await page.waitFor(`${OPEN_ITEMS}.every((x) => x.getBoundingClientRect().height > 0)`, 5000);
    };
    // A coarse pointer is emulated with touch (setEmulatedMedia's pointer feature does not change the (pointer: coarse) query).
    await page.send('Emulation.setEmulatedMedia', { features: [{ name: 'pointer', value: 'coarse' }] });
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
    await reopenNew();
    check(await page.evaluate(`matchMedia('(pointer: coarse)').matches`), 'the coarse pointer is emulated for the title check');
    // The title is scaled by the open animation (about 40.5 px at first, then 44 px): the wait returns the height once it is 44.
    const finger = await page.waitFor(`(() => { const h = ${TITLE_HEIGHT}; return h === 44 ? h : null; })()`, 5000);
    check(finger === 44, `a submenu title on a coarse pointer is 44 px (${finger})`);
    if (shotsDir) await menuShot(page, shotsDir, 'nt2-menu-coarse.png');
    await menuRowChecks(page, check, 'the New menu on a coarse pointer', 44);
    await page.send('Emulation.setEmulatedMedia', { features: [] });
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: false });
    await reopenNew();
    const mouse = await page.waitFor(`(() => { const h = ${TITLE_HEIGHT}; return h === 40 ? h : null; })()`, 5000);
    check(mouse === 40, `a submenu title with a mouse is 40 px (${mouse})`);
    await key(page, { code: 'Escape', key: 'Escape', vk: 27 });
    await waitNoDropdown(page);

    // Hebrew runs right to left: the rows are laid out from the other side, and the same middle holds. The kind's label is the
    // translated one, and so is a child's.
    await setLanguage(page, 'עברית');
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="new-button"]:not([disabled])')`, 15000);
    await page.evaluate(`document.querySelector('.shell-sider [data-testid="new-button"]').click()`);
    await page.waitFor(`${OPEN_ITEMS}.length >= 6`, 10000);
    await hoverMenuTitle(page, 'מסמך');
    await page.waitFor(`${OPEN_ITEMS}.some((x) => x.textContent.trim() === 'ריק')`, 10000);
    if (shotsDir) await menuShot(page, shotsDir, 'nt2-menu-hebrew.png');
    await menuRowChecks(page, check, 'the New menu in Hebrew, Document submenu open', 40);
    await key(page, { code: 'Escape', key: 'Escape', vk: 27 });
    await waitNoDropdown(page);
    await setLanguage(page, 'English');

    // The chord, with the menu closed: Alt+C then F opens the New folder dialog, and it opens nothing else.
    // The listing was in the background while the editor opened, so the Blank menu's close animation had not run: a closing
    // dropdown still counts as open, and the chord (rightly) waits for it. Bring the listing to front, and wait for the close.
    await page.send('Page.bringToFront');
    await page.waitFor(`!document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden)')`, 10000);
    await page.evaluate(`document.body.focus()`);
    await key(page, { code: 'KeyC', key: 'c', vk: 67, alt: true });
    await key(page, { code: 'KeyF', key: 'f', vk: 70 });
    // The open dialog only: the one modal that is displayed, with its input (a closed modal stays in the DOM).
    await page.waitFor(`!!${OPEN_MODAL}?.querySelector('input')`, 10000);
    const title = await page.evaluate(`(${OPEN_MODAL}?.querySelector('.ant-modal-title')?.textContent ?? '').trim()`);
    check(title === 'New folder', `Alt+C then F opens the New folder dialog with the menu closed (${title})`);
    check(await page.evaluate(`!document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden)')`), 'the chord opened no menu');
    // A keyboard user's Escape closes the dialog the chord opened. The name field takes the focus at once (NewFolderDialog, a zero-delay
    // focus after mount, since antd moves the focus after its open animation), so the wait for focus is short.
    const inDialog = await page.waitFor(`document.activeElement?.id === 'newfolder-name'`, 500);
    check(inDialog === true, 'focus moves to the name field of the New folder dialog');
    await key(page, { code: 'Escape', key: 'Escape', vk: 27 });
    // A closed modal stays in the DOM with display none (antd's close animation ends with it), so wait for that, not for a class.
    await page.waitFor(`[...document.querySelectorAll('.ant-modal-wrap')].every((w) => getComputedStyle(w).display === 'none')`, 10000);
    check(true, 'Escape closes the New folder dialog with focus in it');
    // The menu's own row opens the same dialog, and the focus goes to the name field there too.
    await page.evaluate(`document.querySelector('.shell-sider [data-testid="new-button"]').click()`);
    await page.waitFor(`${OPEN_ITEMS}.length >= 6`, 10000);
    await clickItem(page, 'New folder');
    const fromMenu = await page.waitFor(`document.activeElement?.id === 'newfolder-name'`, 500);
    check(fromMenu === true, 'New folder from the menu puts the focus in the name field');
    await key(page, { code: 'Escape', key: 'Escape', vk: 27 });
    await page.waitFor(`[...document.querySelectorAll('.ant-modal-wrap')].every((w) => getComputedStyle(w).display === 'none')`, 10000);
  } finally {
    page.close();
    launch.proc.kill();
    if (newID) await api(base, 'DELETE', `/api/v1/nodes/${newID}?permanent=true`, accountKey).catch(() => {});
    if (folderID) await api(base, 'DELETE', `/api/v1/nodes/${folderID}?permanent=true`, accountKey).catch(() => {});
  }
}
