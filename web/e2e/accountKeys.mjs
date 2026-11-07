// SPDX-License-Identifier: AGPL-3.0-or-later

// Round 2, H1: the account menu is reached with the keyboard alone. Tab presses move focus from the top of the page to the account
// button (no script focuses anything), Enter opens the menu, and Tab moves into it. The menu must stay open with focus in it, and
// the next arrow key must land on the first item, Change password. Every step is a real key press (Input.dispatchKeyEvent).
import { sleep } from './media.mjs';

const pressKey = async (page, key, code, vk) => {
  const text = key === 'Enter' ? '\r' : undefined;
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key, code, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk, text });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key, code, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk });
  await sleep(120);
};

const focusNow = (page) =>
  page.evaluate(`(() => { const a = document.activeElement; return a ? (a.getAttribute('role') || a.tagName) + ':' + (a.getAttribute('data-testid') || '') + ':' + a.textContent.trim() : ''; })()`);

const menuOpen = (page) =>
  page.evaluate(`!!document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden) ul[role="menu"]')`);

// accountKeyboardChecks runs the keyboard path and reports each step through check. It returns the focus after each key, for the record.
export async function accountKeyboardChecks(page, check) {
  const trail = [];
  // A reset, not a focus: the focused element is blurred so the Tab presses start from the top of the page.
  await page.evaluate(`document.activeElement && document.activeElement.blur(), true`);
  let onTrigger = false;
  for (let i = 0; i < 120 && !onTrigger; i++) {
    await pressKey(page, 'Tab', 'Tab', 9);
    onTrigger = await page.evaluate(`document.activeElement && document.activeElement.getAttribute('data-testid') === 'account-menu'`);
  }
  trail.push(`tab-to-account:${await focusNow(page)}`);
  check(onTrigger, `Tab alone reaches the account button (${trail[trail.length - 1]})`);
  if (!onTrigger) return trail;

  await pressKey(page, 'Enter', 'Enter', 13);
  await sleep(300);
  const openAfterEnter = await menuOpen(page);
  check(openAfterEnter, 'Enter on the account button opens the account menu');

  await pressKey(page, 'Tab', 'Tab', 9);
  const afterTab = await focusNow(page);
  trail.push(`tab-into-menu:${afterTab}`);
  const focusInMenu = await page.evaluate(`(() => { const m = document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden) ul[role="menu"]'); return !!m && m.contains(document.activeElement); })()`);
  check((await menuOpen(page)) && focusInMenu, `Tab from the open menu's button moves focus into the menu and keeps it open (focus: ${afterTab})`);

  // The first stop in the menu is Change password: Tab may land on it directly (the menu's item), or on the menu's list, from which the
  // first arrow key goes to the first item. Either way the first item is the first stop, and the menu stays open.
  let first = await focusNow(page);
  if (!first.startsWith('menuitem:')) {
    await pressKey(page, 'ArrowDown', 'ArrowDown', 40);
    first = await focusNow(page);
  }
  trail.push(`first-stop:${first}`);
  check(first === 'menuitem::Change password' && (await menuOpen(page)), `the first stop in the menu is Change password, with the menu open (focus: ${first})`);
  return trail;
}
