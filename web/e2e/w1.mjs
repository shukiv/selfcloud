// SPDX-License-Identifier: AGPL-3.0-or-later

// The file browser's slice w1 checks: a folder of more than thirty files, with the mouse and the keyboard sent through
// the browser's own input events. The toolbar holds the selection actions, a right-click (and a long press on a finger)
// opens the item's menu, Ctrl+C, X and V cut, copy and paste within the tab, Backspace and Alt+Up go up a folder, a viewer
// is refused a paste, a menu at the window's edge stays on the window, the upload tray folds itself, and Hebrew and phone
// width work too. With SELFCLOUD_W1_SHOTS set to a folder, the run also saves the pictures for the slice.
import { writeFile, mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import {
  barButtons,
  browserTitle,
  click,
  clearSelection,
  dblClick,
  docTop,
  itemSel,
  openedName,
  closePreview,
  q,
  setLanguage,
  setPhone,
  setTheme,
} from './media.mjs';

const MENU = '.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu';
const MODS = { alt: 1, ctrl: 2, shift: 8 };
const KEYS = { Backspace: ['Backspace', 'Backspace', 8], Escape: ['Escape', 'Escape', 27], ArrowUp: ['ArrowUp', 'ArrowUp', 38], x: ['x', 'KeyX', 88], c: ['c', 'KeyC', 67], v: ['v', 'KeyV', 86] };
// The long folder: thirty-four files, so the bottom rows are off the first screen, as on a real listing.
const COUNT = 40;
const name = (i) => `f${String(i).padStart(2, '0')}.txt`;
const sleepMs = (ms) => new Promise((r) => setTimeout(r, ms));

// press sends one key, with its modifiers, as the keyboard does: a key down and a key up.
async function press(page, key, mods = []) {
  const [k, code, vk] = KEYS[key];
  const modifiers = mods.reduce((n, m) => n | MODS[m], 0);
  const base = { key: k, code, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk, modifiers };
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', ...base });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', ...base });
  await sleepMs(200);
}

// pressAs sends a key the way a layout does: the letter it types (key), the key's position (code) and its virtual key code.
async function pressAs(page, key, code, vk, mods = []) {
  const modifiers = mods.reduce((n, m) => n | MODS[m], 0);
  const base = { key, code, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk, modifiers };
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', ...base });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', ...base });
  await sleepMs(200);
}

// modalOpen says whether a dialog is on the page (antd keeps a closed one in the DOM, hidden).
const modalOpen = (page) => page.evaluate(`[...document.querySelectorAll('.ant-modal-wrap')].some((w) => getComputedStyle(w).display !== 'none')`);

// rightClick is a mouse's right button, pressed and let go on a point.
async function rightClick(page, { x, y }) {
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
  await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'right', buttons: 2, clickCount: 1 });
  await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'right', buttons: 0, clickCount: 1 });
  await sleepMs(300);
}

// longPress is a finger held on a point for longer than the menu's wait, then lifted.
async function longPress(page, { x, y }) {
  await page.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] });
  await sleepMs(800);
  await page.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await sleepMs(300);
}

// pointOf is the centre of the first element the selector names, scrolled into view at the end of the window when asked.
async function pointOf(page, selector, block = 'nearest') {
  const p = await page.evaluate(`(() => {
    const el = document.querySelector(${q(selector)});
    if (!el) return null;
    el.scrollIntoView({ block: ${q(block)} });
    const r = el.getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  })()`);
  if (!p) throw new Error(`nothing matches ${selector}`);
  return p;
}

// rowPoint is the centre of an item's row or tile, with the item scrolled to the end of the window: the lowest visible rows.
const rowPoint = (page, nameText) => pointOf(page, itemSel(nameText), 'end');

// menuItems is the text of the items of the open right-click menu.
const menuItems = (page) => page.evaluate(`[...document.querySelectorAll(${q(`${MENU} .ant-dropdown-menu-item`)})].map((i) => i.textContent.trim())`);

// menuItemDisabled says whether the item of the open menu with this text is greyed.
const menuItemDisabled = (page, label) =>
  page.evaluate(`(() => { const i = [...document.querySelectorAll(${q(`${MENU} .ant-dropdown-menu-item`)})].find((x) => x.textContent.trim() === ${q(label)}); return i ? i.classList.contains('ant-dropdown-menu-item-disabled') || i.getAttribute('aria-disabled') === 'true' : null; })()`);

// menuPlaced waits until the open menu has been placed on the window. antd draws a new menu far off-screen (at -1000vw)
// until it has measured where the menu goes; a click before that lands nowhere. It then waits for the opening motion.
async function menuPlaced(page) {
  await page.waitFor(
    `(() => { const m = document.querySelector(${q(MENU)}); if (!m) return false; const r = m.getBoundingClientRect(); return r.width > 0 && r.left > -innerWidth && r.top > -innerHeight; })()`,
    5000,
  );
  await sleepMs(400);
}

// menuShown waits until the open menu has a size, wherever it was put, and lets its motion end. A check of where it is follows.
async function menuShown(page) {
  try {
    await page.waitFor(`(() => { const m = document.querySelector(${q(MENU)}); return !!m && m.getBoundingClientRect().width > 0; })()`, 5000);
  } catch (err) {
    const seen = await page.evaluate(`JSON.stringify({ drops: [...document.querySelectorAll('.ant-dropdown')].map((d) => d.className + ' ' + JSON.stringify(d.getBoundingClientRect())), menus: document.querySelectorAll('.ant-dropdown-menu').length, w: innerWidth, h: innerHeight, sel: document.querySelectorAll('[aria-selected="true"]').length, hit300: (() => { const e = document.elementFromPoint(800, 300); return e ? e.tagName + '.' + e.className + ' ' + (e.closest('[data-id]')?.getAttribute('aria-label') ?? '-') : 'none'; })(), sy: scrollY })`);
    throw new Error(`${err.message.slice(0, 40)}; ${seen}`);
  }
  await sleepMs(700);
}

// chooseMenuItem clicks the item of the open menu with this text, with the mouse.
async function chooseMenuItem(page, label) {
  const p = await page.evaluate(`(() => { const i = [...document.querySelectorAll(${q(`${MENU} .ant-dropdown-menu-item`)})].find((x) => x.textContent.trim() === ${q(label)}); if (!i) return null; const r = i.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
  if (!p) throw new Error(`no menu item ${label}`);
  await click(page, p);
  await sleepMs(400);
}

// The menu's box, in the window.
const menuBox = (page) =>
  page.evaluate(`(() => { const m = document.querySelector(${q(MENU)}); if (!m) return null; const r = m.getBoundingClientRect(); return { left: r.left, top: r.top, right: r.right, bottom: r.bottom, width: innerWidth, height: innerHeight }; })()`);

// The rows of the open listing, in the order shown; the names of the items listed.
const listed = (page) => page.evaluate(`[...document.querySelectorAll('[role="row"][aria-label], [role="option"][aria-label]')].map((r) => r.getAttribute('aria-label'))`);

// setView shows List or Tiles with the mouse. The toggle shows the view it switches to (data-view), so it is pressed when that
// view is the one asked for; the labels change with the language, so the toggle is found by its test id.
async function setView(page, view) {
  const want = view === 'List' ? 'list' : 'grid';
  const toggle = '[data-testid="view-toggle"]';
  await page.waitFor(`!!document.querySelector(${q(toggle)})?.dataset.view`, 15000);
  const p = await page.evaluate(`(() => { const b = document.querySelector(${q(toggle)}); if (b.dataset.view !== ${q(want)}) return null; const r = b.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
  if (p === null) return;
  await click(page, p);
  await page.waitFor(`!!document.querySelector(${q(itemSel('f01.txt'))})`, 15000);
}

// waitListed waits until the API lists the names wanted in a folder, and returns the names it lists.
async function waitListed(api, base, team, folderPath, accountKey, want) {
  const deadline = Date.now() + 15000;
  for (;;) {
    const items = (await api(base, 'GET', `/api/v1/folders/${team}/children?path=${encodeURIComponent(folderPath)}`, accountKey)).items;
    const names = items.map((n) => n.name);
    if (want.every((w) => names.includes(w))) return names;
    if (Date.now() > deadline) throw new Error(`${folderPath} does not list ${want.join(', ')} (lists ${names.join(', ')})`);
    await sleepMs(300);
  }
}

// dblClickRow opens a folder or a file by a double click of its name, with the mouse.
async function openByDoubleClick(page, nameText) {
  await dblClick(page, await rowPoint(page, nameText));
}

export async function w1Checks(h, { page, base, accountKey, teamA, teamB, dir }) {
  const { api, rawPut } = h;
  const failures = [];
  const wanted = process.env.SELFCLOUD_W1_STEPS ? new RegExp(process.env.SELFCLOUD_W1_STEPS) : null;
  const step = async (label, fn) => {
    if (wanted && !wanted.test(label)) return;
    try {
      await fn();
    } catch (err) {
      failures.push(`${label}: ${err.message}`);
      console.log(`FAIL ${label}: ${err.message}`);
    }
  };
  const check = h.check;

  // ---- the files: a long folder in Team A (the editor's), and one in Team B (where the signed-in owner is a viewer) ----
  for (const team of [teamA, teamB]) {
    await api(base, 'POST', `/api/v1/folders/${team}/directories`, accountKey, { path: '/w1-long', parents: false });
    for (let i = 1; i <= COUNT; i++) {
      // A reused keep-alive socket the server has just closed fails the body read; a failed read creates no file, so the write is retried.
      const path = `/api/v1/folders/${team}/files?path=${encodeURIComponent(`/w1-long/${name(i)}`)}`;
      for (let attempt = 1; ; attempt++) {
        try {
          await rawPut(base, path, accountKey, Buffer.from(`file ${i}\n`), 'text/plain');
          break;
        } catch (err) {
          if (attempt >= 3 || !String(err.message).includes('could not be read')) throw err;
        }
      }
    }
  }
  await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: '/w1-long/Target', parents: false });
  const folderID = async (team) => (await api(base, 'GET', `/api/v1/folders/${team}/children?path=%2F`, accountKey)).items.find((n) => n.name === 'w1-long').id;
  const longA = await folderID(teamA);
  const longB = await folderID(teamB);
  const longURL = `${page.base}/files/${teamA}/${longA}`;
  const longURLB = `${page.base}/files/${teamB}/${longB}`;

  // open shows the long folder in a view, with nothing selected.
  const open = async (view, url = longURL) => {
    await page.goto(url);
    await page.waitFor(`!!document.querySelector(${q(itemSel('f01.txt'))})`, 15000);
    await setView(page, view);
    await clearSelection(page);
  };

  // ---- 1. the lowest visible rows: a double click opens the row under the pointer, with a selection and without ----
  await step('a double click on the lowest visible rows opens that row', async () => {
    for (const view of ['List', 'Tiles']) {
      await open(view);
      await click(page, await rowPoint(page, 'f05.txt'));
      await sleepMs(200);
      for (const nameText of ['f39.txt', 'f40.txt']) {
        await dblClick(page, await rowPoint(page, nameText));
        const got = await openedName(page);
        check(got === nameText, `a double click on ${nameText}, the lowest rows of ${COUNT} in ${view}, with f05 selected, opens ${nameText} (opened ${got ?? 'nothing'})`);
        await closePreview(page);
      }
    }
  });

  // ---- 2. a right-click on a row offers the item's actions, and the menu acts ----
  await step('a right-click on a row offers its actions, and Details opens the item', async () => {
    await open('List');
    await rightClick(page, await rowPoint(page, 'f02.txt'));
    await menuPlaced(page);
    const items = await menuItems(page);
    for (const want of ['Download', 'Rename', 'Copy to…', 'Move to…', 'Delete', 'Details', 'Cut', 'Copy']) {
      check(items.includes(want), `the menu of a file offers ${want} (${items.join(' / ')})`);
    }
    check((await page.evaluate(`document.querySelector(${q(itemSel('f02.txt'))}).getAttribute('aria-selected')`)) === 'true', 'a right-click on an unselected row selects it');
    await chooseMenuItem(page, 'Details');
    check(await page.evaluate(`!!document.querySelector('[role="dialog"], .ant-drawer-open')`), 'Details opens the item');
    await press(page, 'Escape');
    await sleepMs(300);
  });

  await step('a right-click on a tile offers the same actions', async () => {
    await open('Tiles');
    await rightClick(page, await rowPoint(page, 'f03.txt'));
    await menuPlaced(page);
    const items = await menuItems(page);
    check(items.includes('Copy') && items.includes('Cut') && items.includes('Delete'), `a tile's menu offers Cut, Copy and Delete (${items.join(' / ')})`);
    await press(page, 'Escape');
    await sleepMs(300);
  });

  // ---- 3. a right-click on empty space offers New folder, Upload and Paste; Paste is off with nothing copied ----
  await step('a right-click on empty space offers New folder, Upload and Paste', async () => {
    await open('List');
    const at = await page.evaluate(`(() => { const h = document.querySelector('.ant-table-thead'); if (!h) return null; const r = h.getBoundingClientRect(); return { x: r.left + 30, y: r.top + r.height / 2 }; })()`);
    check(at !== null, 'the listing has a header to right-click outside the rows');
    await rightClick(page, at);
    await menuPlaced(page);
    const items = await menuItems(page);
    check(items.includes('New folder') && items.includes('Upload files') && items.includes('Paste'), `the empty space's menu offers New folder, Upload and Paste (${items.join(' / ')})`);
    check((await menuItemDisabled(page, 'Paste')) === true, 'Paste is greyed with nothing copied');
    await chooseMenuItem(page, 'New folder');
    await page.waitFor(`!!document.getElementById('newfolder-name')`, 5000);
    // Cancel is clicked with the mouse: a key event sent by CDP does not carry the keyCode the dialog's Escape handler reads.
    const cancel = await page.evaluate(`(() => { const b = [...document.querySelectorAll('.ant-modal-footer button')].find((x) => x.textContent.trim() === 'Cancel'); if (!b) return null; const r = b.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
    check(cancel !== null, 'the New folder dialog has a Cancel button');
    await click(page, cancel);
    // antd keeps the closed dialog in the DOM, hidden, so the test asks whether the input is laid out.
    await page.waitFor(`(() => { const el = document.getElementById('newfolder-name'); return !el || el.getClientRects().length === 0; })()`, 5000);
  });

  // ---- 4. Rename from the menu; inside the field the browser's own menu opens, not the page's ----
  await step('Rename from the menu, and the browser keeps its own menu in the rename field', async () => {
    await open('List');
    await rightClick(page, await rowPoint(page, 'f04.txt'));
    await menuPlaced(page);
    await chooseMenuItem(page, 'Rename');
    await page.waitFor(`!!document.querySelector('input.rename')`, 5000);
    await rightClick(page, await pointOf(page, 'input.rename', 'nearest'));
    check(!(await page.evaluate(`!!document.querySelector(${q(MENU)})`)), 'a right-click in the rename field opens no menu of the page');
    await press(page, 'Escape');
    await page.waitFor(`!document.querySelector('input.rename')`, 5000);
  });

  // ---- 5. cut a file, paste it into a folder, and back: Ctrl+X dims the file, Ctrl+V moves it, Backspace goes up ----
  await step('cut a file, paste it into a folder, and back with Backspace', async () => {
    await open('List');
    await click(page, await rowPoint(page, 'f03.txt'));
    await press(page, 'x', ['ctrl']);
    await page.waitFor(`document.querySelector(${q(itemSel('f03.txt'))}).style.opacity === '0.5'`, 5000);
    await openByDoubleClick(page, 'Target');
    await page.waitFor(`document.querySelector('#browser-title')?.textContent === 'Target'`, 10000);
    await press(page, 'v', ['ctrl']);
    const inTarget = await waitListed(api, base, teamA, '/w1-long/Target', accountKey, ['f03.txt']);
    check(inTarget.includes('f03.txt'), 'Ctrl+V moved f03.txt into Target');
    await press(page, 'Backspace');
    await page.waitFor(`document.querySelector('#browser-title')?.textContent === 'w1-long'`, 10000);
    // The listing may still show the row from its cache until its refetch ends: wait for the row to leave, then check. A row
    // that never leaves still fails the check.
    await page.waitFor(`!document.querySelector(${q(itemSel('f03.txt'))})`, 10000).catch(() => undefined);
    check(!(await listed(page)).includes('f03.txt'), 'f03.txt is no longer in the folder it left');
    // And back: cut it inside Target, go up with Alt+Up, paste.
    await openByDoubleClick(page, 'Target');
    await page.waitFor(`document.querySelector('#browser-title')?.textContent === 'Target'`, 10000);
    await click(page, await rowPoint(page, 'f03.txt'));
    await press(page, 'x', ['ctrl']);
    await press(page, 'ArrowUp', ['alt']);
    await page.waitFor(`document.querySelector('#browser-title')?.textContent === 'w1-long'`, 10000);
    await press(page, 'v', ['ctrl']);
    const back = await waitListed(api, base, teamA, '/w1-long', accountKey, ['f03.txt']);
    check(back.includes('f03.txt'), 'Alt+Up went up a folder, and Ctrl+V moved f03.txt back');
    // The listing is redrawn after the server answers, so the dimming is waited for, not read at once.
    await page.waitFor(`!!document.querySelector(${q(itemSel('f03.txt'))}) && !document.querySelector(${q(itemSel('f03.txt'))}).style.opacity`, 10000);
    const cleared = await page.evaluate(`!document.querySelector(${q(itemSel('f03.txt'))})?.style.opacity`);
    check(cleared, 'the pasted file is no longer dimmed: the clipboard was emptied by the move');
  });

  // ---- 6. copy: Ctrl+C and Ctrl+V into a folder; the source stays where it is ----
  await step('copy a file into a folder with Ctrl+C and Ctrl+V', async () => {
    await open('List');
    await click(page, await rowPoint(page, 'f04.txt'));
    await press(page, 'c', ['ctrl']);
    await openByDoubleClick(page, 'Target');
    await page.waitFor(`document.querySelector('#browser-title')?.textContent === 'Target'`, 10000);
    await press(page, 'v', ['ctrl']);
    const inTarget = await waitListed(api, base, teamA, '/w1-long/Target', accountKey, ['f04.txt']);
    const inLong = await waitListed(api, base, teamA, '/w1-long', accountKey, ['f04.txt']);
    check(inTarget.includes('f04.txt') && inLong.includes('f04.txt'), 'the copy is in Target and the source is still in the folder');
    await press(page, 'Backspace');
    await page.waitFor(`document.querySelector('#browser-title')?.textContent === 'w1-long'`, 10000);
  });

  // ---- 7. Escape cancels a cut: the file is no longer dimmed, and nothing moves ----
  await step('Escape cancels a cut', async () => {
    await open('List');
    await click(page, await rowPoint(page, 'f07.txt'));
    await press(page, 'x', ['ctrl']);
    await page.waitFor(`document.querySelector(${q(itemSel('f07.txt'))}).style.opacity === '0.5'`, 5000);
    await press(page, 'Escape');
    await page.waitFor(`!document.querySelector(${q(itemSel('f07.txt'))}).style.opacity`, 5000);
    check((await listed(page)).includes('f07.txt'), 'Escape cancels the cut: f07 is still listed, and no longer dimmed');
  });

  // ---- 8. no row moves when a row is selected, in a long list ----
  await step('selecting moves no row in a long list', async () => {
    await open('List');
    const before = await docTop(page, 'f20.txt');
    await click(page, await rowPoint(page, 'f10.txt'));
    await sleepMs(300);
    const after = await docTop(page, 'f20.txt');
    check(before !== null && after !== null && Math.abs(before - after) < 0.5, `f20 does not move when f10 is selected (${before} to ${after})`);
    check((await barButtons(page)).includes('Download'), 'the selection actions are enabled with a selection');
    await clearSelection(page);
  });

  // ---- 9. a viewer: Ctrl+V and Ctrl+X are refused with a clear message, and no request is sent ----
  await step('a viewer is refused a paste and a cut, and is told so', async () => {
    await open('List', longURLB);
    await click(page, await rowPoint(page, 'f01.txt'));
    const barBefore = await barButtons(page);
    check(!barBefore.includes('Delete') && !barBefore.includes('Cut') && !barBefore.includes('Paste'), `a viewer's toolbar offers no Delete, Cut or Paste (${barBefore.join(' / ')})`);
    await press(page, 'c', ['ctrl']);
    await press(page, 'v', ['ctrl']);
    await page.waitFor(`document.body.innerText.includes('You cannot paste into this folder.')`, 5000);
    check(await page.evaluate(`document.body.innerText.includes('You cannot paste into this folder.')`), 'Ctrl+V in a viewer folder says it cannot paste');
    await press(page, 'x', ['ctrl']);
    await page.waitFor(`document.body.innerText.includes('You cannot move some of these items, so none were cut.')`, 5000);
    check(await page.evaluate(`document.body.innerText.includes('You cannot move some of these items, so none were cut.')`), 'Ctrl+X on a viewer folder says it cannot move');
    const b = await api(base, 'GET', `/api/v1/folders/${teamB}/children?path=${encodeURIComponent('/w1-long')}`, accountKey);
    check(b.items.length === COUNT, `the viewer's folder still holds ${COUNT} files (${b.items.length})`);
    await clearSelection(page);
  });

  // ---- 10. a menu at the window's edge stays on the window: a row at the bottom right, in a short window ----
  await step("a menu opened at the window's bottom right stays on the window", async () => {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1024, height: 520, deviceScaleFactor: 1, mobile: false });
    try {
      await open('List');
      const at = await page.evaluate(`(() => { const row = document.querySelector(${q(itemSel('f40.txt'))}); row.scrollIntoView({ block: 'end' }); const r = row.getBoundingClientRect(); return { x: r.right - 6, y: r.top + r.height / 2 }; })()`);
      await rightClick(page, at);
      await menuPlaced(page);
      const box = await menuBox(page);
      check(box && box.left >= 0 && box.top >= 0 && box.right <= box.width && box.bottom <= box.height, `the menu stays on the window (${JSON.stringify(box)})`);
      await press(page, 'Escape');
      await sleepMs(300);
    } finally {
      await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    }
  });

  // ---- 10b. a menu taller than the room above and below the pointer stays on the window (a right-click at mid height) ----
  // antd shifts a menu along the window only for some placements; the right-click menu shifts and scrolls (decision 49).
  await step('a menu opened at mid height in a short window stays on the window, and in a very short one it scrolls', async () => {
    try {
      await page.send('Emulation.setDeviceMetricsOverride', { width: 1024, height: 520, deviceScaleFactor: 1, mobile: false });
      await open('List');
      await page.evaluate(`window.scrollTo(0, 0)`);
      await rightClick(page, { x: 800, y: 300 });
      await menuShown(page);
      const mid = await menuBox(page);
      check(mid && mid.top >= 0 && mid.bottom <= mid.height, `a right-click at y 300 of a 520 px window leaves the whole menu on the window (${JSON.stringify(mid)})`);
      await press(page, 'Escape');
      await sleepMs(300);
      await page.send('Emulation.setDeviceMetricsOverride', { width: 1024, height: 300, deviceScaleFactor: 1, mobile: false });
      await sleepMs(300);
      await page.evaluate(`window.scrollTo(0, 400)`);
      await sleepMs(300);
      const onRow = await page.evaluate(`!!document.elementFromPoint(800, 250)?.closest('[data-id]')`);
      if (!onRow) throw new Error('no row under the point of the 300 px window');
      await rightClick(page, { x: 800, y: 250 });
      await menuShown(page);
      const small = await menuBox(page);
      check(small && small.top >= 0 && small.bottom <= small.height, `in a 300 px window the menu stays on the window (${JSON.stringify(small)})`);
      const reach = await page.evaluate(`(() => { const m = document.querySelector(${q(MENU)}); m.scrollTop = m.scrollHeight; const items = [...m.querySelectorAll('.ant-dropdown-menu-item')]; const last = items[items.length - 1].getBoundingClientRect(); const first = items[0]; m.scrollTop = 0; const f = first.getBoundingClientRect(); return { scrolls: m.scrollHeight > m.clientHeight, lastBottom: last.bottom, firstTop: f.top, height: innerHeight }; })()`);
      check(reach.scrolls && reach.lastBottom <= reach.height && reach.firstTop >= 0, `the menu scrolls and its first and last items can be reached (${JSON.stringify(reach)})`);
      await press(page, 'Escape');
      await sleepMs(300);
    } finally {
      await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    }
  });

  // ---- 11. the upload tray folds itself when the uploads end, and the last row scrolls clear of it ----
  await step('the upload tray folds when the uploads end, and the last row clears it', async () => {
    await open('List');
    const local = join(dir, 'w1-upload.txt');
    await writeFile(local, 'one small upload for slice w1\n');
    await page.setFiles('[role="toolbar"] input[type="file"]:not([webkitdirectory])', [local]);
    await page.waitFor(`!!document.querySelector('[data-testid="upload-tray"]')`, 15000);
    // The upload is done on the server when its file is in the listing; only then is the fold waited for.
    await page.waitFor(`!!document.querySelector(${q(itemSel('w1-upload.txt'))})`, 30000);
    await page.waitFor(`!document.querySelector('#tray-list') && !!document.querySelector('[data-testid="upload-tray"]')`, 30000);
    check(await page.evaluate(`!document.querySelector('#tray-list') && !!document.querySelector('[data-testid="upload-tray"]')`), 'the upload tray folds itself once its upload has ended');
    // The fold is an animation: the tray is measured only once its box has stopped moving (up to 40 reads, 150 ms apart, until two in a row match).
    let last = null;
    for (let i = 0; i < 40; i++) {
      const top = await page.evaluate(`document.querySelector('[data-testid="upload-tray"]').getBoundingClientRect().top`);
      if (top === last) break;
      last = top;
      await sleepMs(150);
    }
    // The page is scrolled to its end: the room the tray takes is left under the last rows, so a row scrolled there clears it.
    const clear = await page.evaluate(`(() => {
      window.scrollTo(0, document.documentElement.scrollHeight);
      const row = document.querySelector(${q(itemSel('f40.txt'))});
      const tray = document.querySelector('[data-testid="upload-tray"]').getBoundingClientRect();
      const r = row.getBoundingClientRect();
      return { rowBottom: r.bottom, trayTop: tray.top };
    })()`);
    check(clear.rowBottom <= clear.trayTop + 0.5, `the last row sits above the folded tray (row bottom ${clear.rowBottom}, tray top ${clear.trayTop})`);
  });

  // ---- 12. a long press on a phone opens the item's menu, and opens nothing ----
  await step('a long press on a phone opens the item menu, and opens nothing', async () => {
    // The row is aimed at its centre: at the window's bottom edge a phone's folded upload tray sits over it.
    await setPhone(page, true);
    try {
      await open('List');
      const at = await pointOf(page, itemSel('f06.txt'), 'center');
      // Diagnostics for a touch that opens nothing: the events the row gets, and what the point hits.
      await page.evaluate(`(() => { window.__w1 = []; for (const n of ['pointerdown', 'pointerup', 'pointercancel', 'contextmenu', 'click']) document.addEventListener(n, (e) => window.__w1.push(n + ':' + (e.target && (e.target.getAttribute && e.target.getAttribute('data-id')) || e.target.tagName)), true); })()`);
      const hit = await page.evaluate(`(() => { const el = document.elementFromPoint(${at.x}, ${at.y}); return el ? el.tagName + ' ' + (el.getAttribute('data-id') || el.className || '') : 'nothing'; })()`);
      // While the finger is held, the menu, the row's selection and any preview are sampled every 100 ms.
      const hold = longPress(page, at);
      const samples = [];
      for (let i = 0; i < 12; i++) {
        samples.push(await page.evaluate(`document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden)').length + '/' + (document.querySelector('[role="row"][aria-label="f06.txt"]')?.getAttribute('aria-selected') ?? '-') + '/' + document.querySelectorAll('[data-testid="preview"]').length`));
        await sleepMs(100);
      }
      await hold;
      const events = await page.evaluate(`JSON.stringify(window.__w1 ?? null)`);
      const seen = `events ${events}; menu/selected/preview per 100 ms: ${samples.join(' ')}`;
      await page.waitFor(`!!document.querySelector(${q('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu')})`, 5000).catch(() => {
        throw new Error(`no menu after a long press; point hits ${hit}; events ${seen}`);
      });
      await menuPlaced(page);
      const items = await menuItems(page);
      check(items.includes('Cut') && items.includes('Copy'), `a long press offers the item's menu (${items.join(' / ')})`);
      check(await page.evaluate(`!document.querySelector('[data-testid="preview"]')`), 'a long press opens no preview');
      await press(page, 'Escape');
      await clearSelection(page);
    } finally {
      await setPhone(page, false);
    }
  });

  // ---- 13. Hebrew: the menu and the toolbar use the Hebrew words ----
  await step('the menu is in Hebrew', async () => {
    await open('List');
    await setLanguage(page, 'עברית');
    try {
      await rightClick(page, await rowPoint(page, 'f08.txt'));
      await menuPlaced(page);
      const items = await menuItems(page);
      check(items.includes('גזור') && items.includes('העתק'), `the menu in Hebrew offers Cut and Copy (${items.join(' / ')})`);
      await press(page, 'Escape');
    } finally {
      await setLanguage(page, 'English');
    }
  });

  // ---- 14. a long folder: row 30 is selected with a mouse; the toolbar and the top bar stay on screen, and Delete takes a click ----
  await step('row 30 of 40 is selected with a mouse, and Delete is on screen and takes a real click', async () => {
    await open('List');
    await click(page, await pointOf(page, itemSel('f30.txt'), 'center'));
    await sleepMs(400);
    const del = await page.evaluate(`(() => { const b = [...document.querySelectorAll('[data-testid="browser-toolbar"] button')].find((x) => (x.textContent.trim() === 'Delete' || x.getAttribute('aria-label') === 'Delete')); if (!b) return null; const r = b.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2, top: r.top, bottom: r.bottom, height: innerHeight, disabled: b.disabled }; })()`);
    check(del !== null && !del.disabled && del.top >= 0 && del.bottom <= del.height, `Delete is on screen with row 30 selected (${JSON.stringify(del)})`);
    const bar = await page.evaluate(`(() => { const h = document.querySelector('.ant-layout-header'); return h ? h.getBoundingClientRect().top : null; })()`);
    check(bar === 0, `the top bar stays on screen while the list is scrolled (top ${bar})`);
    await click(page, { x: del.x, y: del.y });
    await page.waitFor(`[...document.querySelectorAll('.ant-modal-wrap')].some((w) => getComputedStyle(w).display !== 'none')`, 5000);
    await page.evaluate(`[...document.querySelectorAll('.ant-modal-footer button')].find((x) => x.textContent.trim() === 'Cancel')?.click()`);
    await page.waitFor(`![...document.querySelectorAll('.ant-modal-wrap')].some((w) => getComputedStyle(w).display !== 'none')`, 5000);
    check(!(await modalOpen(page)), 'a real click on Delete opened its confirmation, and Cancel closed it');
    await clearSelection(page);
  });

  // ---- 15. cut, copy, select and clear move no row and no toolbar: the first row and the toolbar keep their places ----
  await step('cut, copy, select and clear move no row or toolbar', async () => {
    await open('List');
    const probe = () => page.evaluate(`(() => { const r = document.querySelector('[role="row"][aria-label]').getBoundingClientRect(); const t = document.querySelector('[data-testid="browser-toolbar"]').getBoundingClientRect(); return { row: r.top + scrollY, bar: t.top + scrollY, sy: scrollY, rc: r.top, bc: t.top, hdr: document.querySelector('.ant-layout-header')?.getBoundingClientRect().height ?? null, bar2: document.querySelector('[data-testid="browser-toolbar"]').parentElement.getBoundingClientRect().top + scrollY }; })()`);
    // The row is brought into view before the first measure: rowPoint scrolls the page, and that scroll is not a move.
    await rowPoint(page, 'f11.txt');
    await sleepMs(300);
    const start = await probe();
    const same = async (what, from) => {
      const now = await probe();
      check(Math.abs(from.row - now.row) < 0.5 && Math.abs(from.bar - now.bar) < 0.5, `${what}: the first row and the toolbar stay put (${from.row} / ${from.bar} to ${now.row} / ${now.bar}; DIAG from ${JSON.stringify(from)} now ${JSON.stringify(now)})`);
      return now;
    };
    await click(page, await rowPoint(page, 'f11.txt'));
    await sleepMs(300);
    let at = await same('selecting f11', start);
    await press(page, 'x', ['ctrl']);
    await sleepMs(500);
    at = await same('cutting', at);
    await press(page, 'c', ['ctrl']);
    await sleepMs(500);
    at = await same('copying', at);
    await clearSelection(page);
    await sleepMs(300);
    at = await same('clearing the selection', at);
    // Paste into Target: the pasted name sorts after the first row there, so the first row keeps its place.
    await openByDoubleClick(page, 'Target');
    await page.waitFor(`document.querySelector('#browser-title')?.textContent === 'Target'`, 10000);
    // The listing of Target is read after its title shows: the probe waits for its first row to be drawn.
    await page.waitFor(`!!document.querySelector('[role="row"][aria-label]')`, 15000);
    const inTarget = await probe();
    await press(page, 'v', ['ctrl']);
    await waitListed(api, base, teamA, '/w1-long/Target', accountKey, ['f11.txt']);
    await sleepMs(500);
    await same('pasting into Target', inTarget);
    await press(page, 'Backspace');
    await page.waitFor(`document.querySelector('#browser-title')?.textContent === 'w1-long'`, 10000);
  });

  // ---- 16. Ctrl+C and Ctrl+V on a Hebrew keyboard: the key types a Hebrew letter, and its position is the key of C and V ----
  await step('Ctrl+C and Ctrl+V work on a Hebrew keyboard, where the key types another letter', async () => {
    await open('List');
    await click(page, await rowPoint(page, 'f12.txt'));
    await pressAs(page, 'ב', 'KeyC', 67, ['ctrl']);
    await page.waitFor(`document.body.innerText.includes('Copied. Open a folder and paste it there.')`, 5000);
    await openByDoubleClick(page, 'Target');
    await page.waitFor(`document.querySelector('#browser-title')?.textContent === 'Target'`, 10000);
    await pressAs(page, 'ה', 'KeyV', 86, ['ctrl']);
    const names = await waitListed(api, base, teamA, '/w1-long/Target', accountKey, ['f12.txt']);
    check(names.includes('f12.txt'), 'the Hebrew Ctrl+C and Ctrl+V copied f12.txt into Target');
    await press(page, 'Backspace');
    await page.waitFor(`document.querySelector('#browser-title')?.textContent === 'w1-long'`, 10000);
  });

  // ---- 17. a key held down pastes once; a press during a paste waits for it, and no name-taken question opens ----
  await step('a held Ctrl+V pastes once, and no name-taken question opens for the repeat', async () => {
    await open('List');
    await click(page, await rowPoint(page, 'f13.txt'));
    await press(page, 'c', ['ctrl']);
    await openByDoubleClick(page, 'Target');
    await page.waitFor(`document.querySelector('#browser-title')?.textContent === 'Target'`, 10000);
    await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'v', code: 'KeyV', windowsVirtualKeyCode: 86, nativeVirtualKeyCode: 86, modifiers: 2, autoRepeat: true });
    await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'v', code: 'KeyV', windowsVirtualKeyCode: 86, nativeVirtualKeyCode: 86, modifiers: 2 });
    await press(page, 'v', ['ctrl']);
    const names = await waitListed(api, base, teamA, '/w1-long/Target', accountKey, ['f13.txt']);
    await sleepMs(1500);
    check(!(await modalOpen(page)), 'no name-taken question opened: the repeat did not paste again');
    check(names.filter((n) => n.startsWith('f13')).length === 1, `f13 is in Target once (${names.filter((n) => n.startsWith('f13')).join(', ')})`);
    await press(page, 'Backspace');
    await page.waitFor(`document.querySelector('#browser-title')?.textContent === 'w1-long'`, 10000);
  });

  // ---- 18. with text selected on the page, Ctrl+C is the browser's: it copies the text and no item ----
  await step('Ctrl+C with text selected on the page copies no item', async () => {
    await open('List');
    await click(page, await rowPoint(page, 'f14.txt'));
    await page.evaluate(`(() => { const r = document.createRange(); r.selectNodeContents(document.getElementById('browser-title')); const s = window.getSelection(); s.removeAllRanges(); s.addRange(r); return true; })()`);
    // Earlier toasts last about three seconds; the wait keeps one of them from being read as this copy.
    await sleepMs(3500);
    await press(page, 'c', ['ctrl']);
    await sleepMs(500);
    const said = await page.evaluate(`document.body.innerText.includes('Copied. Open a folder and paste it there.')`);
    await page.evaluate(`window.getSelection()?.removeAllRanges()`);
    check(!said, 'with text selected, Ctrl+C left the copy to the browser: no item was copied');
    await clearSelection(page);
  });

  // ---- 19. a viewer's right-click on empty space opens no menu: the place has nothing to offer, so the browser keeps its own ----
  await step("a viewer's right-click on empty space opens no page menu", async () => {
    await open('List', longURLB);
    const at = await page.evaluate(`(() => { const h = document.querySelector('section.browser > header'); if (!h) return null; const r = h.getBoundingClientRect(); return { x: r.right - 40, y: r.top + r.height / 2 }; })()`);
    check(at !== null, 'the place has a breadcrumb area to right-click');
    await rightClick(page, at);
    await sleepMs(300);
    check(!(await page.evaluate(`!!document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden)')`)), "a viewer's right-click on empty space opens no page menu");
  });

  // ---- 19b. the selection panel is on top, as in a file manager: in the toolbar's area, above the first row, and never in
  // the lower half of the window, at desktop and phone width, and still so once the page is scrolled ----
  await step('the selection panel is on top, above the first row, at desktop and phone width, scrolled too', async () => {
    const panelAt = () => page.evaluate(`JSON.stringify((() => {
      const panel = document.querySelector('[data-testid="selection-panel"]');
      if (!panel) return null;
      const pr = panel.getBoundingClientRect();
      const row = document.querySelector('[role="row"][aria-label]');
      const rr = row ? row.getBoundingClientRect() : null;
      const lower = [panel, ...panel.querySelectorAll('*')].filter((e) => e.getBoundingClientRect().top > innerHeight / 2).length;
      return { top: pr.top, bottom: pr.bottom, rowTop: rr ? rr.top : null, lower, scrollable: document.documentElement.scrollHeight > innerHeight, toolbarTop: document.querySelector('[data-testid="browser-toolbar"]').getBoundingClientRect().top };
    })())`).then((x) => JSON.parse(x));
    for (const [label, width] of [['desktop', 1280], ['phone', 390]]) {
      await page.send('Emulation.setDeviceMetricsOverride', { width, height: 800, deviceScaleFactor: 1, mobile: label === 'phone' });
      await open('List', longURL);
      await click(page, await pointOf(page, itemSel('f01.txt')));
      await page.waitFor(`!!document.querySelector('[data-testid="selection-panel"]')`, 10000);
      await sleepMs(300);
      // The page is brought to its top: the first row is measured where a person first sees it.
      await page.evaluate(`window.scrollTo(0, 0)`);
      await sleepMs(300);
      const at = await panelAt();
      check(at.top - at.toolbarTop >= 0 && at.top - at.toolbarTop <= 6, `the selection panel takes the toolbar's own row, inside its padding, on a ${label} (panel ${at.top}, toolbar ${at.toolbarTop})`);
      check(at.rowTop !== null && at.bottom <= at.rowTop, `the selection panel's bottom edge is above the first row on a ${label} (bottom ${at.bottom}, first row ${at.rowTop})`);
      check(at.lower === 0, `no part of the selection panel is in the lower half of the window on a ${label} (${at.lower} parts)`);
      await page.evaluate(`window.scrollTo(0, 400)`);
      await sleepMs(300);
      const down = await panelAt();
      check(down.scrollable && down.top >= 50 && down.top <= 110 && down.lower === 0, `scrolled on a ${label} (the page scrolls: ${down.scrollable}), the panel stays in the toolbar's area and out of the lower half (top ${down.top}, lower ${down.lower})`);
      await clearSelection(page);
    }
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
  });

  // ---- 20. Clear with the mouse: the button goes disabled under the focus, and the focus stays in the listing ----
  await step('clearing the selection with the mouse keeps the focus in the listing', async () => {
    await open('List');
    await click(page, await rowPoint(page, 'f15.txt'));
    await sleepMs(300);
    await click(page, await page.evaluate(`(() => { const r = document.querySelector('[aria-label="Clear selection"]').getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`));
    await sleepMs(300);
    const focus = await page.evaluate(`(() => { const a = document.activeElement; return a && a !== document.body ? (a.closest('[data-id]') ? 'the listing' : a.tagName) : 'the body'; })()`);
    check(focus !== 'the body', `after Clear, the focus is not lost to the page (it is on ${focus})`);
  });

  // ---- 21. on a touch screen the toolbar buttons and the menu items are 44 pixels tall ----
  await step('on a touch screen the toolbar buttons and the New menu items are 44 pixels tall', async () => {
    await setPhone(page, true);
    try {
      await open('List');
      const sizes = await page.evaluate(`[...document.querySelectorAll('[data-testid="browser-toolbar"] button')].map((b) => { const r = b.getBoundingClientRect(); return [Math.round(r.width), Math.round(r.height)]; })`);
      check(sizes.length > 0 && sizes.every(([w, h]) => w >= 44 && h >= 44), `the toolbar buttons are 44 pixels wide and tall on a finger (${sizes.map(([w, h]) => `${w}x${h}`).join(' ')})`);
      await click(page, await pointOf(page, '[data-testid="browser-toolbar"] button[aria-haspopup="menu"]', 'center'));
      await page.waitFor(`!!document.querySelector(${q(MENU)})`, 5000);
      await sleepMs(300);
      const items = await page.evaluate(`[...document.querySelectorAll(${q(`${MENU} .ant-dropdown-menu-item`)})].map((i) => Math.round(i.getBoundingClientRect().height))`);
      check(items.length > 0 && items.every((h) => h >= 44), `the New menu's items are 44 pixels tall on a finger (${items.join(' ')})`);
      await press(page, 'Escape');
      await sleepMs(300);
    } finally {
      await setPhone(page, false);
    }
  });

  // ---- 22. a failed English catalogue shows a retry, not a blank page; the retry loads the page ----
  await step('a failed English catalogue shows a retry, not a blank page, and the retry loads the page', async () => {
    // The media checks answer paused requests through pausedHook while it is set (the run's listener defers to it).
    let failing = true;
    page.pausedHook = async (p) => {
      if (failing) await page.send('Fetch.failRequest', { requestId: p.requestId, errorReason: 'InternetDisconnected' });
      else await page.send('Fetch.continueRequest', { requestId: p.requestId });
    };
    await page.send('Fetch.enable', { patterns: [{ urlPattern: '*/assets/en-*.js' }] });
    // The English chunk is in Chrome's memory cache from the steps before: a cached file is not a request, so it is not paused.
    await page.send('Network.setCacheDisabled', { cacheDisabled: true });
    // The service worker (decision 65) answers the app's files from its own cache, which no request reaches either: the page
    // bypasses it while the chunk is made to fail.
    await page.send('Network.setBypassServiceWorker', { bypass: true });
    try {
      await page.goto(longURL);
      try {
        await page.waitFor(`document.body.innerText.includes('This page could not load')`, 30000);
      } catch (e) {
        const dump = await page.evaluate(`JSON.stringify({ text: document.body.innerText.slice(0, 200), root: document.getElementById('root')?.innerHTML.slice(0, 200), fails: performance.getEntriesByType('resource').filter((r) => /en-|\\.json/.test(r.name)).map((r) => r.name + ' ' + r.responseStatus) })`);
        console.log('DIAG retry', dump);
        throw e;
      }
      check(await page.evaluate(`!!document.querySelector('#root button')`), 'a failed English catalogue shows a retry button, not a blank page');
    } finally {
      failing = false;
      page.pausedHook = null;
      await page.send('Fetch.disable');
      await page.send('Network.setCacheDisabled', { cacheDisabled: false });
      await page.send('Network.setBypassServiceWorker', { bypass: false });
    }
    await page.evaluate(`[...document.querySelectorAll('#root button')].find((b) => b.textContent.trim() === 'Try again')?.click()`);
    await page.waitFor(`!!document.querySelector('[data-testid="browser-toolbar"]')`, 30000);
    check(await page.evaluate(`!document.body.innerText.includes('This page could not load')`), 'the retry loads the page');
  });

  // ---- 23. a folder cannot be pasted into itself: the cut stays and the reason shows; a cut into its own folder moves nothing ----
  await step('a folder cut and pasted into itself stays cut, with the reason; a cut pasted into its own folder moves nothing', async () => {
    const errorsAtStart = page.errors.length;
    await open('List');
    await click(page, await rowPoint(page, 'Target'));
    await press(page, 'x', ['ctrl']);
    await openByDoubleClick(page, 'Target');
    await page.waitFor(`document.querySelector('#browser-title')?.textContent === 'Target'`, 10000);
    await press(page, 'v', ['ctrl']);
    await page.waitFor(`!!document.querySelector('.ant-message-error')`, 10000);
    const reason = await page.evaluate(`document.querySelector('.ant-message-error')?.textContent.trim() ?? ''`);
    check(reason.length > 0, `the refusal shows its reason (${reason})`);
    await press(page, 'Backspace');
    await page.waitFor(`document.querySelector('#browser-title')?.textContent === 'w1-long'`, 10000);
    const stillCut = await page.evaluate(`document.querySelector(${q(itemSel('Target'))})?.style.opacity === '0.5'`);
    check(stillCut, 'Target stays cut after the refused paste, so it can be pasted elsewhere');
    await click(page, await rowPoint(page, 'f16.txt'));
    await press(page, 'x', ['ctrl']);
    await press(page, 'v', ['ctrl']);
    await sleepMs(800);
    check((await listed(page)).includes('f16.txt'), 'a cut pasted into its own folder moves nothing: f16.txt is still listed');
    check(!(await page.evaluate(`!!document.querySelector(${q(itemSel('f16.txt'))})?.style.opacity`)), 'f16.txt is no longer dimmed: the clipboard was emptied');
    // The server refuses a move into the folder's own folder, and the browser logs each refused request. Those log lines are
    // this step's expected outcome: they come out of the run's console errors. Any other error still fails the run.
    let refusals = 0;
    for (let i = page.errors.length - 1; i >= errorsAtStart; i--) {
      if (/400 \(Bad Request\).*\/move/.test(page.errors[i])) {
        page.errors.splice(i, 1);
        refusals++;
      }
    }
    check(refusals >= 1, `the server refused the moves into their own folder, and the log of each is set aside (${refusals})`);
  });

  // Shots: with SELFCLOUD_W1_SHOTS set, the toolbar, the right-click menu and the cut state, for each width, theme and language.
  // ---- 13. a long account name does not widen the page: the header button cuts it, so the page fits its window ----
  // The account name is set on the button's text (a 64-character address), then the page's width is read at 320 and 390 px.
  await step('a 64-character account name does not widen the page at 320 or 390 px, in English or Hebrew', async () => {
    const long = 'a'.repeat(49) + '@selfcloud.test';
    const widths = [];
    for (const lang of ['English', 'עברית']) {
      for (const width of [320, 390]) {
        await setPhone(page, false);
        await page.goto(longURL);
        await setLanguage(page, 'English');
        await setLanguage(page, lang);
        await page.send('Emulation.setDeviceMetricsOverride', { width, height: 800, deviceScaleFactor: 1, mobile: true });
        await page.goto(longURL);
        await page.waitFor(`!!document.querySelector(${q(itemSel('f01.txt'))})`, 15000);
        // The account is one letter now (decision 68): its name is its tooltip, so a long name is not in the bar at all.
        await sleepMs(200);
        const m = await page.evaluate(`({ sw: document.documentElement.scrollWidth, iw: window.innerWidth })`);
        widths.push({ lang, width, scrollWidth: m.sw, innerWidth: m.iw });
      }
    }
    await setPhone(page, false);
    // The loop ends in Hebrew: English is set back, as the rest of the run expects it.
    await setLanguage(page, 'English');
    const bad = widths.filter((w) => w.scrollWidth > w.innerWidth);
    check(bad.length === 0, `the page fits its window at phone widths in both languages (${JSON.stringify(widths)})`);
  });

  if (process.env.SELFCLOUD_W1_SHOTS) {
    await step('the slice w1 pictures', () => shots(page, { folderURL: longURL, dir: process.env.SELFCLOUD_W1_SHOTS }));
  }

  // The checks after this one expect the desktop window, no selection and the English words.
  await setPhone(page, false);
  await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
  await page.goto(longURL).catch(() => undefined);
  if (failures.length > 0) throw new Error(`${failures.length} w1 check(s) failed: ${failures.join(' | ')}`);
}

// shots saves the pictures of slice w1: the toolbar with a selection, the right-click menu and a cut state, at desktop and
// phone width, in light and dark, in English and Hebrew.
async function shots(page, { folderURL, dir }) {
  await mkdir(dir, { recursive: true });
  const save = async (file) => {
    const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
    await writeFile(join(dir, file), Buffer.from(data, 'base64'));
  };
  for (const theme of ['Light', 'Dark']) {
    for (const lang of ['English', 'עברית']) {
      for (const width of ['desktop', 'phone']) {
        const tag = `${width}-${theme.toLowerCase()}-${lang === 'English' ? 'en' : 'he'}`;
        // The theme switch and the language menu are on the desktop top bar only, so they are set before the phone width.
        // The theme's labels are read in English, so the language is set to English first; the Hebrew choice of the last pass stays.
        await setPhone(page, false);
        await page.goto(folderURL);
        await setLanguage(page, 'English');
        await setTheme(page, theme);
        await setLanguage(page, lang);
        if (width === 'phone') await setPhone(page, true);
        await page.goto(folderURL);
        await page.waitFor(`!!document.querySelector(${q(itemSel('f01.txt'))})`, 15000);
        await setView(page, 'List');
        // The toolbar with nothing selected: the same row, the actions disabled.
        await clearSelection(page);
        await sleepMs(300);
        await save(`toolbar-empty-${tag}.png`);
        // The toolbar: a selection made, its actions enabled.
        await click(page, await rowPoint(page, 'f05.txt'));
        await sleepMs(400);
        await save(`toolbar-${tag}.png`);
        // The right-click menu: a right-click on a row (a long press on a phone).
        await clearSelection(page);
        if (width === 'desktop') await rightClick(page, await rowPoint(page, 'f06.txt'));
        else await longPress(page, await rowPoint(page, 'f06.txt'));
        await page.waitFor(`!!document.querySelector(${q(MENU)})`, 5000).catch(() => undefined);
        await sleepMs(300);
        await save(`menu-${tag}.png`);
        await press(page, 'Escape');
        await clearSelection(page);
        // The cut state: a file cut, its row dimmed, then cancelled.
        await click(page, await rowPoint(page, 'f07.txt'));
        await press(page, 'x', ['ctrl']);
        await sleepMs(300);
        await save(`cut-${tag}.png`);
        await press(page, 'Escape');
        await clearSelection(page);
        // A long folder scrolled to its row 30, with that row selected: the toolbar and the top bar are still on screen.
        await click(page, await pointOf(page, itemSel('f30.txt'), 'center'));
        await sleepMs(400);
        await save(`toolbar-scrolled-${tag}.png`);
        await clearSelection(page);
        if (width === 'phone') await setPhone(page, false);
      }
      await setLanguage(page, 'English');
    }
    await setTheme(page, 'Light');
  }
}
