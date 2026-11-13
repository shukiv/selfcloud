// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice ks1 (decision 169): the shell's global keys and the keyboard shortcuts list, in its own browser with a mouse.
// At a desktop width "/" focuses the top bar's search box and types nothing; Ctrl+/ opens the list, Escape closes it and
// gives the focus back to the control it was on, and Ctrl+/ again toggles it closed. The account menu's entry names its key
// and opens the same list. In Hebrew the list is right to left and titled in Hebrew, while its key caps keep their Latin
// names and their Latin order.
import { join } from 'node:path';
import { launchBrowser, Page } from './cdp.mjs';
import { sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);
const SEARCH = 'header input[type="search"]';
const ACCOUNT = '[data-testid="account-menu"]';
// OPEN_MODAL is the modal displayed now (a closed one stays in the DOM, with display none); NO_MODAL is the wait for them all closed.
const OPEN_MODAL = "[...document.querySelectorAll('.ant-modal-wrap')].find((w) => getComputedStyle(w).display !== 'none')";
const NO_MODAL = "[...document.querySelectorAll('.ant-modal-wrap')].every((w) => getComputedStyle(w).display === 'none')";
// MENU_SHORTCUTS is the account menu's shortcuts entry, named by the key it answers (aria-keyshortcuts).
const MENU_SHORTCUTS = '.ant-dropdown:not(.ant-dropdown-hidden) [role="menuitem"][aria-keyshortcuts]';
// OPEN_LANG is the language menu's item with this name, in the open dropdown.
const openLang = (name) => `[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].find((x) => x.textContent.trim() === ${q(name)})`;

// key sends one key press (down and up) to the focused element, with the modifiers the chord needs (Ctrl is 2).
async function key(page, { code, key: name, vk, ctrl = false }) {
  const modifiers = ctrl ? 2 : 0;
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: name, code, windowsVirtualKeyCode: vk, modifiers });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: name, code, windowsVirtualKeyCode: vk, modifiers });
  await sleep(150);
}
const slash = (page, ctrl = false) => key(page, { code: 'Slash', key: '/', vk: 191, ctrl });
const escape = (page) => key(page, { code: 'Escape', key: 'Escape', vk: 27 });

// openList presses Ctrl+/ with the focus out of any field, and waits for the list to draw its rows (its strings load with it).
// A menu that is closing (its leave animation) is still an open overlay to the chord, so it waits for the menu to hide first.
async function openList(page) {
  await page.waitFor(`document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden)').length === 0`, 5000);
  await page.evaluate(`document.body.focus()`);
  await slash(page, true);
  await page.waitFor(`!!${OPEN_MODAL}?.querySelector('.shortcuts-list')`, 10000);
}
const listTitle = (page) => page.evaluate(`(${OPEN_MODAL}?.querySelector('.ant-modal-title')?.textContent ?? '').trim()`);

// closeList closes the list with Escape. A real key press goes to the focused element, and Escape is read by the dialog's
// wrapper, so the focus is put in the dialog first (on its close control, as a keyboard user's Tab would reach it).
async function closeList(page) {
  await page.evaluate(`${OPEN_MODAL}?.querySelector('.ant-modal-close')?.focus()`);
  await escape(page);
  await page.waitFor(NO_MODAL, 10000);
}

// ks1Checks runs the section. The member is an editor of Team A; the helpers are the run's.
export async function ks1Checks(h, { base, dir, teamA, browserBinary, MEMBER, EXPECTED, signIn, navigateTo }) {
  const { check } = h;
  const launch = await launchBrowser(browserBinary, join(dir, 'ks1-profile'), { mouse: true });
  const page = new Page(launch.wsUrl);
  page.base = base;
  try {
    await page.start(EXPECTED);
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    await signIn(page, MEMBER);
    await navigateTo(page, `/files/${teamA}`);
    await page.waitFor(`!!document.querySelector(${q(SEARCH)})`, 15000);

    // "/" focuses the search box and types no character into it.
    await page.evaluate(`document.body.focus()`);
    await slash(page);
    check((await page.waitFor(`document.activeElement === document.querySelector(${q(SEARCH)})`, 5000)) === true, '"/" focuses the top bar\'s search box');
    check((await page.evaluate(`document.querySelector(${q(SEARCH)}).value`)) === '', '"/" types nothing into the search box');

    // Ctrl+/ opens the list, titled, with its six groups and a Latin Ctrl cap. The focus is on the account control first,
    // so the return of the focus on close can be checked against it.
    await page.evaluate(`document.querySelector(${q(ACCOUNT)}).focus()`);
    await slash(page, true);
    await page.waitFor(`!!${OPEN_MODAL}?.querySelector('.shortcuts-list')`, 10000);
    check((await listTitle(page)) === 'Keyboard shortcuts', `Ctrl+/ opens the keyboard shortcuts list (${await listTitle(page)})`);
    const groups = await page.evaluate(`${OPEN_MODAL}.querySelectorAll('.shortcuts-group').length`);
    check(groups === 6, `the list has its six groups (${groups})`);
    check(await page.evaluate(`[...${OPEN_MODAL}.querySelectorAll('.shortcuts-kbd')].some((k) => k.textContent === 'Ctrl')`), 'the list names Ctrl as a key cap');

    // Escape closes the list and gives the focus back to the control it was on before the chord.
    await closeList(page);
    check(true, 'Escape closes the shortcuts list');
    check(await page.evaluate(`document.activeElement === document.querySelector(${q(ACCOUNT)})`), 'Escape returns the focus to where it was before the list opened');

    // Ctrl+/ a second time toggles the list closed (the open list is the overlay the chord is in, so it still closes it).
    await openList(page);
    await slash(page, true);
    await page.waitFor(NO_MODAL, 10000);
    check(true, 'Ctrl+/ a second time closes the list');

    // The account menu's entry names its key (Control+/ to a screen reader, Ctrl+/ as the hint) and opens the same list.
    await page.evaluate(`document.querySelector(${q(ACCOUNT)}).click()`);
    await page.waitFor(`!!document.querySelector(${q(MENU_SHORTCUTS)})`, 10000);
    const hint = await page.evaluate(`(() => { const m = document.querySelector(${q(MENU_SHORTCUTS)}); return { keys: m.getAttribute('aria-keyshortcuts'), hint: m.querySelector('.menu-hint')?.getAttribute('data-hint') ?? null }; })()`);
    check(hint.keys === 'Control+/' && hint.hint === 'Ctrl+/', `the account menu's entry names its key (${hint.keys}, ${hint.hint})`);
    await page.evaluate(`document.querySelector(${q(MENU_SHORTCUTS)}).click()`);
    await page.waitFor(`!!${OPEN_MODAL}?.querySelector('.shortcuts-list')`, 10000);
    check((await listTitle(page)) === 'Keyboard shortcuts', 'the account menu opens the shortcuts list');
    await closeList(page);

    // Hebrew: the list is right to left and titled in Hebrew, while its key caps keep their Latin names and order.
    await page.evaluate(`document.querySelector('[data-testid="language"]').click()`);
    await page.waitFor(`!!${openLang('עברית')}`, 10000);
    await page.evaluate(`${openLang('עברית')}.click()`);
    await page.waitFor(`document.documentElement.dir === 'rtl'`, 10000);
    await openList(page);
    check((await listTitle(page)) === 'קיצורי מקלדת', `in Hebrew the list is titled in Hebrew (${await listTitle(page)})`);
    const listDir = await page.evaluate(`getComputedStyle(${OPEN_MODAL}.querySelector('.shortcuts-list')).direction`);
    check(listDir === 'rtl', `in Hebrew the list is right to left (${listDir})`);
    const chordDir = await page.evaluate(`getComputedStyle(${OPEN_MODAL}.querySelector('.shortcuts-chord')).direction`);
    check(chordDir === 'ltr', `the key caps keep their Latin order on the right-to-left page (${chordDir})`);
    check(await page.evaluate(`[...${OPEN_MODAL}.querySelectorAll('.shortcuts-kbd')].some((k) => k.textContent === 'Ctrl')`), 'in Hebrew the key caps keep their Latin names (Ctrl)');
    await closeList(page);

    check(page.errors.length === 0, `the shortcuts browser has no console errors or policy violations (${page.errors.join(' | ') || 'none'})`);
  } finally {
    page.close();
    launch.proc.kill();
  }
}
