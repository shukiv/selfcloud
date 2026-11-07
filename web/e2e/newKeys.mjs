// SPDX-License-Identifier: AGPL-3.0-or-later

// The New menu, by the keyboard (decision 95, round 3). Every key is a real key press. The button is reached by Tab from the top of the
// page (tabTo), opened with Enter or Space, and the menu's items are reached with Tab and ArrowDown. The group title above a read-only
// reason is not a stop: no key may put focus on it. Escape closes the menu and returns focus to the New button.
//
// `button` selects the New button in the state being checked: the sidebar's, the folded rail's, or the phone drawer's.
import { sleep } from './media.mjs';
import { focusNow, menuOpen, pressKey, tabTo } from './accountKeys.mjs';

// focusOnTitle is true while the focused element is inside a group title (the header of a read-only New menu).
const focusOnTitle = (page) =>
  page.evaluate(`(() => { const a = document.activeElement; return !!a && a.closest('.ant-dropdown-menu-item-group-title') !== null; })()`);

export async function newMenuKeyChecks(page, check, { button, label, opener = 'Enter', readOnly = false }) {
  const trail = [];
  const reached = await tabTo(page, button);
  trail.push(`tab-to-new:${await focusNow(page)}`);
  check(reached, `Tab reaches the New button (${label}; focus: ${await focusNow(page)})`);
  if (!reached) return trail;

  await pressKey(page, opener, opener, opener === 'Space' ? 32 : 13);
  await sleep(300);
  check(await menuOpen(page), `${opener} opens the New menu (${label})`);
  const enabled = await page.evaluate(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].map((i) => ({ text: i.textContent.trim(), enabled: !i.classList.contains('ant-dropdown-menu-item-disabled') })).filter((i) => i.enabled).map((i) => i.text)`);

  if (readOnly) {
    // Every entry is disabled, so no key reaches an item. The reason is the title, and focus must never land on it.
    check(enabled.length === 0, `in the read-only folder, no entry is enabled (${label})`);
    let titleStop = false;
    for (const key of [['Tab', 9], ['ArrowDown', 40]]) {
      await pressKey(page, key[0], key[0], key[1]);
      if (await focusOnTitle(page)) titleStop = true;
      trail.push(`${key[0].toLowerCase()}:${await focusNow(page)}`);
    }
    check(!titleStop, `Tab and ArrowDown never stop on the reason's title (${label}; last focus: ${await focusNow(page)})`);
  } else {
    // Tab reaches the first enabled entry, and the title is not in the walk. The Tab result is asserted before ArrowDown is pressed,
    // so a Tab that lands elsewhere fails here and is not hidden by the arrow (round 4, L6; as accountKeys does).
    await pressKey(page, 'Tab', 'Tab', 9);
    const first = await focusNow(page);
    trail.push(`first-stop:${first}`);
    check(first === `menuitem::${enabled[0]}` && !(await focusOnTitle(page)), `Tab reaches the first entry, ${enabled[0]}, and not the title (${label}; focus: ${first})`);
    await pressKey(page, 'ArrowDown', 'ArrowDown', 40);
    const second = await focusNow(page);
    trail.push(`arrow-down:${second}`);
    check(enabled.length >= 2 && second === `menuitem::${enabled[1]}`, `ArrowDown moves to the next entry, ${enabled[1]} (${label}; focus: ${second})`);
  }

  // What the page is in when Escape is pressed: whether a drawer is open around the menu, and what has focus.
  const state = () => page.evaluate(`JSON.stringify({ menu: !!document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden) ul[role="menu"]'), drawer: !!document.querySelector('.ant-drawer-open'), focus: (() => { const a = document.activeElement; return a ? (a.getAttribute('role') || a.tagName) + ':' + (a.getAttribute('data-testid') || '') + ':' + a.textContent.trim().slice(0, 30) : ''; })() })`);
  const before = await state();
  await pressKey(page, 'Escape', 'Escape', 27);
  await sleep(400);
  const closed = !(await menuOpen(page));
  const back = await page.evaluate(`!!document.activeElement && document.activeElement.matches(${JSON.stringify(button)})`);
  trail.push(`escape: closed=${closed} back=${back}`);
  // Diagnostics only (not checked): the state before and after the Escape, and after a second Escape when the first one did nothing.
  const after = await state();
  let again = '';
  if (!closed) {
    await pressKey(page, 'Escape', 'Escape', 27);
    await sleep(400);
    again = await state();
  }
  check(closed && back, `Escape closes the New menu and returns focus to the New button (${label}; closed: ${closed}, focus: ${await focusNow(page)}; before ${before}; after ${after}; after a second Escape ${again || 'not needed'})`);
  return trail;
}
