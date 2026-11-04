// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice d1: the folder tree in the sidebar, the listing beside it, and the drive-style tiles. A member's browser is measured at
// 1920 and 1280 wide (the sidebar and the listing take all the width), the tree is under My files, a folder click navigates, a
// file dropped on a tree row moves into it, the Team rows open, the phone drawer holds the tree and closes on a choice, the
// tiles are one height with a very long name and a Hebrew name, nothing overflows at 390 and 320, and the touch targets are
// 44 pixels on a finger at rest, on hover and on focus. With SELFCLOUD_E2E_SHOTS set (a folder), the run also saves the
// pictures: desktop light and dark, Hebrew desktop, the phone with the drawer open, and the phone's grid.
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { setLanguage, setPhone, setTheme } from './media.mjs';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const q = (v) => JSON.stringify(v);

const LONG = `${'an exceptionally long file name for the tile view check '.repeat(3)}final.pdf`;
const HEBREW = 'מסמך עבודה ארוך לבדיקת הקלפים בתצוגת הריבועים.txt';
const MOVED = 'd1-moved.txt';

// mouse sends one mouse event; held says the left button is down during a move (a drag).
async function mouse(page, type, x, y, held = false) {
  const down = type === 'mousePressed' || (held && type === 'mouseMoved');
  await page.send('Input.dispatchMouseEvent', { type, x, y, button: type === 'mouseMoved' ? 'none' : 'left', buttons: down ? 1 : 0, clickCount: type === 'mouseMoved' ? 0 : 1 });
}

// pressKey presses and lets go of one key, as the keyboard does.
async function pressKey(page, key, code, vk) {
  const base = { key, code, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk };
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', ...base });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', ...base });
}

// centreOf is the middle of the first element the selector names, brought into the window first. It waits for the element:
// a listing drawn again while its read comes back replaces its tiles for a moment (gate d1e, run 4).
async function centreOf(page, selector, timeout = 10000) {
  const deadline = Date.now() + timeout;
  for (;;) {
    const p = await page.evaluate(`(() => { const el = document.querySelector(${q(selector)}); if (!el) return null; el.scrollIntoView({ block: 'nearest' }); const r = el.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
    if (p) return p;
    if (Date.now() > deadline) throw new Error(`nothing matches ${selector}`);
    await sleep(100);
  }
}

// dragOnto presses on one element, glides to another, reads the drop state while the button is down, then lets go.
async function dragOnto(page, fromSel, toSel) {
  const a = await centreOf(page, fromSel);
  const b = await centreOf(page, toSel);
  await mouse(page, 'mouseMoved', a.x, a.y);
  await mouse(page, 'mousePressed', a.x, a.y);
  for (let i = 1; i <= 6; i++) {
    await mouse(page, 'mouseMoved', a.x + ((b.x - a.x) * i) / 6, a.y + ((b.y - a.y) * i) / 6, true);
    await sleep(20);
  }
  await sleep(150);
  const state = await page.evaluate(`document.querySelector(${q(toSel)})?.dataset.dropState ?? null`);
  // A drop state other than ok once in a full run (gate stfu4c, 1 of 5; d1 alone 5 of 5 ok): say what the pointer was over.
  if (state !== 'ok') {
    const seen = await page.evaluate(`(() => { const t = document.querySelector(${q(toSel)}); const r = t?.getBoundingClientRect(); const at = document.elementFromPoint(${b.x}, ${b.y}); return { target: r ? [Math.round(r.left), Math.round(r.top), Math.round(r.right), Math.round(r.bottom)] : null, point: [${Math.round(b.x)}, ${Math.round(b.y)}], at: at ? at.tagName.toLowerCase() + '.' + String(at.className).split(' ')[0] + (at.closest('[data-drop-name]') ? ' in ' + at.closest('[data-drop-name]').dataset.dropName : '') : null, dragging: !!document.querySelector('[data-drop-state]'), scrollY: Math.round(scrollY) }; })()`);
    console.log(`DIAG d1 drag: ${JSON.stringify(seen)}`);
  }
  await mouse(page, 'mouseReleased', b.x, b.y);
  await sleep(300);
  return state;
}

// measure reads the sidebar, the listing and the page's overflow at the window's width.
const measure = (page) =>
  page.evaluate(`(() => {
    const side = document.querySelector('.shell-sider')?.getBoundingClientRect();
    const section = document.querySelector('section.browser')?.getBoundingClientRect();
    const tree = document.querySelector('.shell-sider [data-testid="folder-tree"]');
    const overflow = document.documentElement.scrollWidth - document.documentElement.clientWidth;
    const wide = overflow > 0 ? [...document.querySelectorAll('body *')].filter((el) => el.getBoundingClientRect().right > document.documentElement.clientWidth + 0.5).slice(0, 3).map((el) => el.tagName.toLowerCase() + (el.getAttribute('data-testid') ? '[' + el.getAttribute('data-testid') + ']' : '') + (el.className && typeof el.className === 'string' ? '.' + el.className.split(' ')[0] : '') + ' right=' + Math.round(el.getBoundingClientRect().right)) : [];
    return { side: side ? Math.round(side.width) : null, listing: section ? Math.round(section.width) : null, treeInSidebar: !!tree, overflow, wide };
  })()`);

// targets returns the touch size of each control that a finger reaches on the tree and the tiles (the rest and the focus).
const targets = (page) =>
  page.evaluate(`(() => {
    const sel = '[data-testid="folder-tree"] .tree-row button, [role="option"] button[aria-label^="More actions for"], [role="option"] button[aria-label^="Star"], [role="option"] button[aria-label^="Select"]';
    return [...document.querySelectorAll(sel)].map((b) => { const r = b.getBoundingClientRect(); return { label: b.getAttribute('aria-label'), w: Math.round(r.width), h: Math.round(r.height) }; });
  })()`);

// sideContrast is the contrast ratio (WCAG 1.4.11, 3:1 for a control) of the "Show hidden spaces" box's border against the
// sidebar behind it, read from the computed styles. Null when the box is not on the page.
async function sideContrast(page) {
  return page.evaluate(`(() => {
    const box = document.querySelector('.shell-scroll .side-check .ant-checkbox-inner');
    if (!box) return null;
    const rgb = (s) => s.slice(s.indexOf('(') + 1, s.indexOf(')')).split(',').map((v) => Number(v.trim()));
    const lum = (c) => { const f = (v) => { const x = v / 255; return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4); }; return 0.2126 * f(c[0]) + 0.7152 * f(c[1]) + 0.0722 * f(c[2]); };
    let el = box.parentElement;
    let bg = null;
    while (el && bg === null) {
      const c = getComputedStyle(el).backgroundColor;
      if (c && c !== 'rgba(0, 0, 0, 0)' && c !== 'transparent') bg = c;
      el = el.parentElement;
    }
    if (bg === null) return null;
    const a = lum(rgb(getComputedStyle(box).borderTopColor));
    const b = lum(rgb(bg));
    return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
  })()`);
}

export async function d1Checks(h, { page, base, accountKey, teamA }) {
  const { check, api, rawPut, navigateTo } = h;
  const shotsDir = process.env.SELFCLOUD_E2E_SHOTS ? await (async () => { await mkdir(process.env.SELFCLOUD_E2E_SHOTS, { recursive: true }); return process.env.SELFCLOUD_E2E_SHOTS; })() : null;
  const snap = async (name) => {
    if (!shotsDir) return;
    await sleep(800);
    const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
    await writeFile(join(shotsDir, name), Buffer.from(data, 'base64'));
  };
  const size = (w, hgt) => page.send('Emulation.setDeviceMetricsOverride', { width: w, height: hgt, deviceScaleFactor: 1, mobile: w < 600 });

  // The fixtures: a folder with a file in it to drag, a very long name and a Hebrew name at the top of the space.
  await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: '/D1 Projects', parents: false }).catch(() => undefined);
  await rawPut(base, `/api/v1/folders/${teamA}/files?path=${encodeURIComponent('/' + MOVED)}`, accountKey, 'moved by d1\n');
  await rawPut(base, `/api/v1/folders/${teamA}/files?path=${encodeURIComponent('/' + LONG)}`, accountKey, 'long\n');
  await rawPut(base, `/api/v1/folders/${teamA}/files?path=${encodeURIComponent('/' + HEBREW)}`, accountKey, 'he\n');
  // The page's cache holds the space's listing as it was before these files: a reload reads it again.
  await page.send('Page.reload', { ignoreCache: true });
  await page.waitFor(`!!document.querySelector('.shell-sider')`, 20000);

  // ---- 1920 wide: the sidebar holds the tree and the listing takes the rest.
  await size(1920, 1000);
  await navigateTo(page, `/files/${teamA}`);
  await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="folder-tree"] [role="treeitem"]')`, 15000);
  await page.waitFor(`!!document.querySelector('section.browser [role="listbox"], section.browser [role="row"]')`, 15000);
  // The tile view is the one under test: the listing is switched to Tiles when it is in the list view (the choice is kept).
  await page.evaluate(`(() => { const b = [...document.querySelectorAll('button')].find((x) => x.getAttribute('aria-label') === 'Tiles'); if (b) b.click(); })()`);
  await page.waitFor(`!!document.querySelector('section.browser [role="listbox"]')`, 15000);
  const wide = await measure(page);
  check(wide.treeInSidebar, 'at 1920 the folder tree is in the sidebar');
  check(wide.side === 264, `at 1920 the sidebar is 264 pixels (${wide.side})`);
  check(wide.listing >= 1500, `at 1920 the listing takes the width beside the sidebar (${wide.listing} of 1656 available)`);
  check(wide.overflow <= 0, `at 1920 the page does not scroll sideways (${wide.overflow})`);
  await snap('desktop-en-light.png');
  const lightRatio = await sideContrast(page);
  check(lightRatio >= 3, `the unticked "Show hidden spaces" box has 3:1 against the sidebar in light (${lightRatio && lightRatio.toFixed(2)})`);

  // ---- The tree is under My files: My files is its first row, and the menu follows it.
  const myFiles = await page.evaluate(`(() => { const first = document.querySelector('[data-testid="folder-tree"] [role="tree"] > [role="treeitem"] .tree-row bdi'); return first?.textContent ?? null; })()`);
  check(myFiles === 'My files', `My files is the first row of the tree (${myFiles})`);
  const menuBelow = await page.evaluate(`(() => { const t = document.querySelector('[data-testid="folder-tree"]'); const m = document.querySelector('.shell-scroll .shell-menu'); return !!t && !!m && !!(t.compareDocumentPosition(m) & Node.DOCUMENT_POSITION_FOLLOWING); })()`);
  check(menuBelow, 'the menu is below the tree in the sidebar');

  // ---- A folder click navigates, and the marked row is the place. The tree starts folded (decision 69), so Team A's arrow opens it
  // first, to reach the folder in it.
  const teamAOpen = `[...document.querySelectorAll('[data-testid="folder-tree"] [role="treeitem"][aria-level="1"]')].find((x) => x.querySelector('.tree-row bdi')?.textContent === 'Team A')`;
  await page.waitFor(`!!${teamAOpen}`, 15000);
  await page.evaluate(`(() => { const t = ${teamAOpen}; if (t && t.getAttribute('aria-expanded') !== 'true') t.querySelector('button[aria-label^="Expand"]')?.click(); return true; })()`);
  await page.waitFor(`${teamAOpen}?.getAttribute('aria-expanded') === 'true'`, 15000);
  await page.waitFor(`!!document.querySelector('[data-testid="folder-tree"] [data-drop-name="D1 Projects"]')`, 15000);
  await page.evaluate(`document.querySelector('[data-testid="folder-tree"] [data-drop-name="D1 Projects"] a').click()`);
  await page.waitFor(`location.pathname.startsWith('/files/${teamA}/') && !!document.querySelector('[data-testid="folder-tree"] [aria-current="location"][data-drop-name="D1 Projects"], [data-testid="folder-tree"] [aria-current="location"]')`, 15000);
  check(true, 'a folder click in the tree opens the folder in the listing');

  // ---- Team rows open: the team's arrow reads its folders and shows them under the row.
  const teamRow = await page.evaluate(`(() => { const t = [...document.querySelectorAll('[data-testid="folder-tree"] [role="treeitem"][aria-level="1"]')].find((x) => x.querySelector('.tree-row bdi')?.textContent === ${q('Team A')}); return t ? t.getAttribute('aria-expanded') : 'missing'; })()`);
  check(teamRow !== 'missing', `the team row is in the tree (${teamRow})`);
  // Team A is open from the step above (its arrow reads its folders once); the arrow is clicked only when it is closed.
  await page.evaluate(`(() => { const t = ${teamAOpen}; if (t && t.getAttribute('aria-expanded') !== 'true') t.querySelector('button[aria-label^="Expand"]')?.click(); return true; })()`);
  await page.waitFor(`[...document.querySelectorAll('[data-testid="folder-tree"] [role="treeitem"][aria-level="1"]')].find((x) => x.querySelector('.tree-row bdi')?.textContent === 'Team A')?.getAttribute('aria-expanded') === 'true'`, 10000);
  check(true, 'a Team row opens to its folders on its arrow');

  // ---- A file dragged from the listing and dropped on a tree row moves into that folder.
  await navigateTo(page, `/files/${teamA}`);
  await page.waitFor(`!!document.querySelector('[role="option"][data-id][aria-label=${q(MOVED)}]')`, 15000);
  // The drag waits for the listing to be read: while the rows of the folder left are kept, they take no drag (fu3b review 1).
  await page.waitFor(`!document.querySelector('.files-list')?.closest('[aria-busy="true"]')`, 15000);
  const state = await dragOnto(page, `[role="option"][aria-label=${q(MOVED)}]`, `[data-testid="folder-tree"] .tree-row[data-drop-name="D1 Projects"]`);
  check(state === 'ok', `the tree row takes the drop (${state})`);
  await sleep(1500);
  const inside = await api(base, 'GET', `/api/v1/folders/${teamA}/children?path=${encodeURIComponent('/D1 Projects')}`, accountKey).catch(() => ({ items: [] }));
  check(inside.items.some((n) => n.name === MOVED), 'the dropped file is in the folder on the tree row');

  // ---- A desktop file dropped on a tree row is not taken: the browser stays on the page, and nothing uploads into the folder.
  const dropURL = await page.evaluate('location.href');
  const dropped = await page.evaluate(`(() => { const row = document.querySelector('[data-testid="folder-tree"] .tree-row[data-drop-name="D1 Projects"]'); const dt = new DataTransfer(); dt.items.add(new File(['x'], 'desk-drop.txt')); const ev = new DragEvent('drop', { bubbles: true, cancelable: true, dataTransfer: dt }); row.dispatchEvent(ev); return ev.defaultPrevented; })()`);
  await sleep(1500);
  const droppedIn = await api(base, 'GET', `/api/v1/folders/${teamA}/children?path=${encodeURIComponent('/D1 Projects')}`, accountKey).catch(() => ({ items: [] }));
  const stayedURL = await page.evaluate('location.href');
  check(dropped === true && stayedURL === dropURL && !droppedIn.items.some((n) => n.name === 'desk-drop.txt'), `a desktop file dropped on a tree row is not taken: the browser stays, nothing uploads (prevented ${dropped}, same page ${stayedURL === dropURL})`);

  // ---- Tiles: folders first in a band of chips, the files as cards of one height, long and Hebrew names included.
  const heights = await page.evaluate(`[...document.querySelectorAll('[role="listbox"] > [role="presentation"]:last-of-type [role="option"]')].map((o) => Math.round(o.getBoundingClientRect().height))`);
  check(heights.length >= 3 && new Set(heights).size === 1, `the file cards are one height (${JSON.stringify([...new Set(heights)])})`);
  // A folder's tile carries its drop attributes; a file's does not.
  const bandsOrder = await page.evaluate(`[...document.querySelectorAll('[role="listbox"] > [role="presentation"]')].map((b) => (b.querySelector('[role="option"]')?.hasAttribute('data-drop-folder') ? 'folders' : 'files'))`);
  check(bandsOrder[0] === 'folders' || bandsOrder.length === 1, `the folders band comes first (${bandsOrder.join(', ')})`);
  const cols = await page.evaluate(`(() => { const b = document.querySelector('[role="listbox"] > [role="presentation"]'); return b ? getComputedStyle(b).gridTemplateColumns.split(' ').length : 0; })()`);
  check(cols >= 4, `about five columns at 1920 wide (${cols})`);
  // Down from the first folder chip moves to the file band (or to the next folder row when the folders take more than one row).
  const folderCount = await page.evaluate(`document.querySelector('[role="listbox"] > [role="presentation"]')?.querySelectorAll('[role="option"]').length ?? 0`);
  await page.evaluate(`document.querySelector('[role="listbox"] > [role="presentation"] [role="option"]').focus()`);
  await sleep(200);
  await pressKey(page, 'ArrowDown', 'ArrowDown', 40);
  await sleep(300);
  const band = await page.evaluate(`[...document.querySelectorAll('[role="listbox"] > [role="presentation"]')].findIndex((b) => b.contains(document.activeElement))`);
  check(band === (folderCount <= cols ? 1 : 0), `Down from a folder moves to the right band (band ${band}, ${folderCount} folders in ${cols} columns)`);
  await snap('desktop-en-grid.png');

  // ---- The targets are 44 pixels on a finger, at rest, on hover and on focus.
  // The page reads the pointer when it loads, so the finger's emulation is set and the page is read again.
  // Touch emulation is what makes the page's pointer query a finger's (the window keeps its 1920 width).
  await page.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
  await page.send('Page.reload', { ignoreCache: true });
  await page.waitFor(`!!document.querySelector('section.browser [role="listbox"]') && !!document.querySelector('[data-testid="folder-tree"] [role="treeitem"]')`, 20000);
  await sleep(400);
  const rest = await targets(page);
  check(rest.length > 0 && rest.every((t) => t.w >= 44 && t.h >= 44), `touch targets are 44 pixels at rest (${rest.filter((t) => t.w < 44 || t.h < 44).length} smaller of ${rest.length}: ${JSON.stringify(rest.slice(0, 4))})`);
  // On hover: the pointer is over each control in turn, and its size is read after the 0.2 s transition.
  const hovered = [];
  for (const t of rest.slice(0, 6)) {
    const at = await page.evaluate(`(() => { const b = [...document.querySelectorAll('[data-testid="folder-tree"] .tree-row button, [role="option"] button')].find((x) => x.getAttribute('aria-label') === ${q(t.label)}); if (!b) return null; b.scrollIntoView({ block: 'nearest' }); const r = b.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
    if (!at) continue;
    await mouse(page, 'mouseMoved', at.x, at.y);
    await sleep(250);
    const now = await page.evaluate(`(() => { const b = [...document.querySelectorAll('[data-testid="folder-tree"] .tree-row button, [role="option"] button')].find((x) => x.getAttribute('aria-label') === ${q(t.label)}); const r = b.getBoundingClientRect(); return { w: Math.round(r.width), h: Math.round(r.height) }; })()`);
    hovered.push({ label: t.label, ...now });
  }
  check(hovered.length > 0 && hovered.every((t) => t.w >= 44 && t.h >= 44), `touch targets are 44 pixels on hover (${JSON.stringify(hovered.filter((t) => t.w < 44 || t.h < 44))})`);
  // On focus: each control takes the keyboard focus, and its size is read after the transition.
  const focused = await page.evaluate(`(() => { const out = []; for (const b of [...document.querySelectorAll('[data-testid="folder-tree"] .tree-row button, [role="option"] button')].slice(0, 6)) { b.focus(); const r = b.getBoundingClientRect(); out.push({ label: b.getAttribute('aria-label'), w: Math.round(r.width), h: Math.round(r.height) }); } return out; })()`);
  check(focused.length > 0 && focused.every((t) => t.w >= 44 && t.h >= 44), `touch targets are 44 pixels on focus (${JSON.stringify(focused.filter((t) => t.w < 44 || t.h < 44))})`);

  // ---- Phone: no overflow at 390 and 320, the drawer holds the tree, and a folder chosen in it closes the drawer.
  await setPhone(page, true);
  await size(390, 800);
  await navigateTo(page, `/files/${teamA}`);
  await sleep(600);
  // The pointer leaves the tiles first: a tooltip left open by a hover over a tile's menu is not part of the page's width.
  // The keyboard focus from the step above is taken off too: a focused tile's tooltip opens on focus.
  await mouse(page, 'mouseMoved', 1, 1);
  await page.evaluate(`(document.activeElement && document.activeElement.blur()), true`);
  await sleep(400);
  const phone = await measure(page);
  check(phone.overflow <= 0, `at 390 the page does not scroll sideways (${phone.overflow}; wider: ${JSON.stringify(phone.wide)})`);
  await page.evaluate(`document.querySelector('[data-testid="nav-open"]').click()`);
  await page.waitFor(`!!document.querySelector('.ant-drawer-open [data-testid="folder-tree"] [role="treeitem"]')`, 15000);
  check(true, 'at phone width the drawer holds the folder tree');
  await snap('phone-en-drawer.png');
  await page.evaluate(`document.querySelector('.ant-drawer-open [data-testid="folder-tree"] [data-drop-name="D1 Projects"] a').click()`);
  await page.waitFor(`!document.querySelector('.ant-drawer-open')`, 10000);
  check(true, 'choosing a folder in the drawer closes the drawer');
  await size(320, 700);
  await navigateTo(page, `/files/${teamA}`);
  await sleep(600);
  const narrow = await measure(page);
  check(narrow.overflow <= 0, `at 320 the page does not scroll sideways (${narrow.overflow})`);
  await size(390, 800);
  await snap('phone-en-grid.png');

  // ---- Hebrew: the page is right to left, and the tree and the tiles keep to the page.
  await setPhone(page, false);
  await size(1920, 1000);
  await sleep(300);
  await setLanguage(page, 'עברית');
  await navigateTo(page, `/files/${teamA}`);
  await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="folder-tree"] [role="treeitem"]')`, 15000);
  const he = await measure(page);
  check(he.overflow <= 0, `in Hebrew the page does not scroll sideways (${he.overflow})`);
  await snap('desktop-he-light.png');

  // ---- Dark: the same page in the dark theme, with its pictures.
  await setLanguage(page, 'English');
  await setTheme(page, 'Dark');
  await navigateTo(page, `/files/${teamA}`);
  await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="folder-tree"] [role="treeitem"]')`, 15000);
  await snap('desktop-en-dark.png');
  const darkRatio = await sideContrast(page);
  check(darkRatio >= 3, `the unticked "Show hidden spaces" box has 3:1 against the sidebar in dark (${darkRatio && darkRatio.toFixed(2)})`);
  await setTheme(page, 'Light');
  await page.send('Emulation.setEmulatedMedia', { features: [] });
  // These checks chose Tiles, and the browser keeps the choice; the checks after these look for the list's rows, so the list
  // comes back. The toggle names the view it switches to.
  await page.waitFor(`!!document.querySelector('[data-testid="view-toggle"]')?.dataset.view`, 10000);
  await page.evaluate(`(() => { const b = document.querySelector('[data-testid="view-toggle"]'); if (b.dataset.view === 'list') b.click(); })()`);
  await page.waitFor(`!!document.querySelector('section.browser [role="row"]')`, 15000);
}
