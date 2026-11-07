// SPDX-License-Identifier: AGPL-3.0-or-later

// The account menu, by the keyboard and by the pointer's layout (decision 95, rounds 2 and 3). Every key is a real key press
// (Input.dispatchKeyEvent). Nothing focuses an element by script, except one reset: accountKeyboardChecks blurs the focused element,
// so that the Tab presses start from the top of the page, as a keyboard user's do.
//
// accountKeyboardChecks checks, in order: Tab reaches the account button; Enter opens the menu; Tab moves focus into the menu and
// the menu stays open, on Change password (the first item, the group title is not a stop); ArrowDown moves to the next item; Escape
// closes the menu and returns focus to the account button.
//
// longAddressChecks checks a long address in the header at the viewport the caller sets: the address is cut at its end, not its
// start, in both directions, and the full address is the title. The address is put into the header's text by the page (the header
// is drawn from the session's email, which the harness cannot change without a second account); the layout measured is the app's.
import { sleep } from './media.mjs';

export const pressKey = async (page, key, code, vk) => {
  const text = key === 'Enter' ? '\r' : key === 'Space' ? ' ' : undefined;
  const k = key === 'Space' ? ' ' : key;
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: k, code, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk, text });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: k, code, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk });
  await sleep(120);
};

// focusNow describes the focused element: its role (or tag), its test id, and its text.
export const focusNow = (page) =>
  page.evaluate(`(() => { const a = document.activeElement; return a ? (a.getAttribute('role') || a.tagName) + ':' + (a.getAttribute('data-testid') || '') + ':' + a.textContent.trim() : ''; })()`);

// menuOpen is true while an open dropdown's menu list is shown.
export const menuOpen = (page) =>
  page.evaluate(`!!document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden) ul[role="menu"]')`);

// tabTo presses Tab until the focused element matches the selector (at most 200 presses), from the top of the page.
export async function tabTo(page, selector) {
  // Why: Tab moves focus on from the element that has it, so a button left focused by an earlier step would make the walk start in
  // the middle of the page. blur() only drops that focus (focus goes to the body); it does not focus anything, so the first Tab is a
  // real key press that starts from the top of the page.
  await page.evaluate(`document.activeElement && document.activeElement.blur(), true`);
  for (let i = 0; i < 200; i++) {
    await pressKey(page, 'Tab', 'Tab', 9);
    if (await page.evaluate(`!!document.activeElement && document.activeElement.matches(${JSON.stringify(selector)})`)) return true;
  }
  return false;
}

// accountKeyboardChecks runs the account menu's keyboard path and reports each step through check. It returns the focus after each key.
export async function accountKeyboardChecks(page, check) {
  const trail = [];
  const onTrigger = await tabTo(page, '[data-testid="account-menu"]');
  trail.push(`tab-to-account:${await focusNow(page)}`);
  check(onTrigger, `Tab alone reaches the account button (${trail[trail.length - 1]})`);
  if (!onTrigger) return trail;

  await pressKey(page, 'Enter', 'Enter', 13);
  await sleep(300);
  check(await menuOpen(page), 'Enter on the account button opens the account menu');
  const items = await page.evaluate(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].map((x) => x.textContent.trim())`);

  // Tab goes into the menu. The first stop is Change password: the group title above the items is not a stop.
  await pressKey(page, 'Tab', 'Tab', 9);
  const first = await focusNow(page);
  trail.push(`tab-into-menu:${first}`);
  const focusInMenu = await page.evaluate(`(() => { const m = document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden) ul[role="menu"]'); return !!m && m.contains(document.activeElement); })()`);
  check((await menuOpen(page)) && focusInMenu && first === 'menuitem::Change password', `Tab from the open menu's button moves focus into the menu, on Change password, and keeps it open (focus: ${first})`);

  // ArrowDown is pressed whether or not Tab landed on an item: the next item is the one after Change password in the menu's order.
  await pressKey(page, 'ArrowDown', 'ArrowDown', 40);
  const second = await focusNow(page);
  trail.push(`arrow-down:${second}`);
  check(items.length >= 2 && second === `menuitem::${items[1]}` && (await menuOpen(page)), `ArrowDown moves to the next item, ${items[1]} (focus: ${second})`);

  // Escape closes the menu and returns focus to the account button.
  await pressKey(page, 'Escape', 'Escape', 27);
  await sleep(400);
  const closed = !(await menuOpen(page));
  const back = await page.evaluate(`!!document.activeElement && document.activeElement.getAttribute('data-testid') === 'account-menu'`);
  trail.push(`escape: closed=${closed} focus=${await focusNow(page)}`);
  check(closed && back, `Escape closes the account menu and returns focus to the account button (closed: ${closed}, focus: ${await focusNow(page)})`);
  return trail;
}

// The long address used for the measurement: 57 characters, a name, an @ and a domain.
export const LONG_ADDRESS = 'mateo.longname.for.the.cut.test@a-long-example-domain.org';

// longAddressChecks opens the account menu, puts the long address into its header, and measures the cut. The header's email box
// (`.account-menu-who`) must fit the menu, carry the full address as its title, and show the start of the address: the address box
// overflows to the end side, so the cut falls at the end of the address and the start stays on the screen. Run it at the viewport and
// in the language the caller set; `where` names them in the messages.
export async function longAddressChecks(page, check, where, snap) {
  await page.evaluate(`document.querySelector('[data-testid="account-menu"]').click(), true`);
  await page.waitFor(`!!document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden) .account-menu-who')`, 10000);
  await sleep(500);
  await page.evaluate(`(() => { const e = document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden) .account-menu-who'); e.querySelector('bdi').textContent = ${JSON.stringify(LONG_ADDRESS)}; e.title = ${JSON.stringify(LONG_ADDRESS)}; return true; })()`);
  await sleep(200);
  const m = await page.evaluate(`(() => {
    const e = document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden) .account-menu-who');
    const b = e.querySelector('bdi');
    const r = e.getBoundingClientRect();
    const br = b.getBoundingClientRect();
    const pop = e.closest('.ant-dropdown').getBoundingClientRect();
    return {
      title: e.title,
      text: b.textContent,
      overflow: getComputedStyle(e).textOverflow,
      dir: getComputedStyle(e).direction,
      spanLeft: Math.round(r.left), spanRight: Math.round(r.right), spanWidth: Math.round(r.width),
      bdiLeft: Math.round(br.left), bdiRight: Math.round(br.right), bdiWidth: Math.round(br.width),
      popLeft: Math.round(pop.left), popRight: Math.round(pop.right),
      viewport: window.innerWidth,
    };
  })()`);
  // The cut falls at the end: the address box starts where the header starts (its overflow runs to the end side), and it is wider
  // than the header. If the box overflows to the start side instead, it ends where the header ends, and the start is cut.
  const wider = m.bdiWidth > m.spanWidth;
  const startShown = Math.abs(m.bdiLeft - m.spanLeft) <= 2;
  const fits = m.spanRight <= m.popRight + 1 && m.spanLeft >= m.popLeft - 1;
  check(m.title === LONG_ADDRESS && m.text === LONG_ADDRESS, `the long address is the header's text and its title, in ${where} (${LONG_ADDRESS.length} characters)`);
  check(m.overflow === 'ellipsis' && wider && fits, `the long address is cut with an ellipsis inside the menu, in ${where} (address ${m.bdiWidth}px, header ${m.spanWidth}px, direction ${m.dir}, menu ${m.popLeft}-${m.popRight})`);
  check(startShown, `the cut falls at the end of the address, so its start is shown, in ${where} (address box starts at ${m.bdiLeft}, header at ${m.spanLeft}, direction ${m.dir})`);
  if (snap) await snap(`i1-address-${where.replace(/[^a-z0-9]+/gi, '-').toLowerCase()}.png`);
  await page.evaluate(`document.activeElement && document.activeElement.blur && document.activeElement.blur(), true`);
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await sleep(300);
  return m;
}

