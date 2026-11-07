// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice d2: the sidebar's New button and the storage block (decision 66). A member's browser is measured at 1440 wide: the New
// icon and its word line up with the rows' icons and names (within a pixel), in English and mirrored in Hebrew, and every row has
// one font size. New folder from Starred makes the folder in My files, and Upload files from inside a folder puts the file there;
// a folder the member cannot add to shows its entries disabled, with the reason. The storage line matches the Storage page, and
// the bar takes its three colours at the member's limit (set through the API, and put back before the section ends). The
// collapsed rail has a round New with a tooltip, the phone drawer has New at its top, and the toolbar has its New only on a phone.
// The current place (decision 69, option B) has no background: its text reads at 4.5:1 or more on the sidebar, and its bar at 3:1 or
// more (the sidebar's rule, theme/currentMark.ts); the Storage bar keeps its track, edge and fill.
// With SELFCLOUD_E2E_SHOTS set (a folder), the run also saves the pictures.
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { onActiveInk, setLanguage, setPhone, setTheme } from './media.mjs';
import { menuHeaderText } from './menu.mjs';
import { newMenuKeyChecks } from './newKeys.mjs';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const q = (v) => JSON.stringify(v);
const MADE = 'D2 Made';
const RAIL_KEY = 'selfcloud.navRail';
const SHARE_LEVELS = { ok: 0.5, warning: 0.85 };
// The bar's fill, as the computed style reads it (the accent is the sidebar's yellow; the others are the warning and error).
const FILL = { ok: 'rgb(254, 204, 0)', warning: 'rgb(255, 169, 64)', danger: 'rgb(255, 107, 107)' };

// rowGeometry reads the four rows the columns are measured on, and the font of each row's name. Each reading is a box: its icon
// and its name, as the screen draws them.
const rowGeometry = (page) =>
  page.evaluate(`(() => {
    const box = (el) => { if (!el) return null; const r = el.getBoundingClientRect(); return { l: r.left, r: r.right }; };
    const fs = (el) => (el ? getComputedStyle(el).fontSize : null);
    const menu = (text) => [...document.querySelectorAll('.shell-menu .ant-menu-item')].find((i) => i.querySelector('.ant-menu-title-content')?.textContent.trim() === text);
    const tree = (text) => [...document.querySelectorAll('[data-testid="folder-tree"] .tree-row')].find((row) => row.querySelector('a bdi')?.textContent.trim() === text);
    const newB = document.querySelector('.shell-sider [data-testid="new-button"]');
    const starred = menu('Starred');
    // My files is the tree's first row; its label is translated in Hebrew, so the row is found by place, not by text.
    const mine = tree('My files') ?? document.querySelector('[data-testid="folder-tree"] .tree-row');
    const team = tree('Team A');
    const storage = document.querySelector('.shell-menu .shell-storage-item');
    return {
      new: { icon: box(newB?.querySelector('svg')), name: box(newB?.querySelector('span')), fs: fs(newB?.querySelector('span')) },
      starred: { icon: box(starred?.querySelector('svg')), name: box(starred?.querySelector('.ant-menu-title-content')), fs: fs(starred?.querySelector('.ant-menu-title-content')) },
      mine: { icon: box(mine?.querySelector('a svg')), name: box(mine?.querySelector('a bdi')), fs: fs(mine?.querySelector('a bdi')) },
      team: { icon: box(team?.querySelector('a svg')), name: box(team?.querySelector('a bdi')), fs: fs(team?.querySelector('a bdi')) },
      storage: { icon: box(storage?.querySelector('svg')), name: box(storage?.querySelector('.shell-storage > span')), fs: fs(storage?.querySelector('.shell-storage > span')) },
      newBox: box(newB),
      // decision 69: New is as wide as its text plus a 24 px end padding, so the word's right edge is 24 px short of the button's.
      newWordEnd: box(newB?.querySelector('span'))?.r ?? null,
    };
  })()`);

// columns compares the New button and the other rows: an icon column and a name column, read from the left edge (or the right
// edge when the page is mirrored). It returns the offsets, so the check can say how far apart they are.
// The reference row is Starred, whose label is translated in Hebrew: there the Team A row (a space's own name) stands for it.
function columns(g, rtl) {
  const at = (box) => (box ? (rtl ? box.r : box.l) : null);
  const base = rtl && !g.starred.icon ? g.team : g.starred;
  const ref = { icon: at(base.icon), name: at(base.name) };
  const rows = { new: g.new, mine: g.mine, team: g.team, storage: g.storage };
  const out = {};
  for (const [key, row] of Object.entries(rows)) {
    out[key] = { icon: at(row.icon) === null ? null : Math.round((at(row.icon) - ref.icon) * 10) / 10, name: at(row.name) === null ? null : Math.round((at(row.name) - ref.name) * 10) / 10 };
  }
  return out;
}

// quotaSet puts one limit on the member, through the API, as the account's key (null follows the default, 0 is no limit).
const quotaSet = (api, base, accountID, accountKey, memberID, bytes) =>
  api(base, 'PATCH', `/api/v1/accounts/${accountID}/users/${memberID}`, accountKey, { quota_bytes: bytes });

// heldOf reads what the member holds, from the page's own session (GET /api/v1/me): files plus reserved bytes.
const heldOf = (page) =>
  page.evaluate(`fetch('/api/v1/me', { credentials: 'same-origin' }).then((r) => r.json()).then((me) => me.storage.used_bytes + me.storage.reserved_bytes)`);

// newMenu opens the sidebar's New and waits for its menu to show the item text, if one is given. It returns the menu's items.
async function newMenu(page, waitFor = null) {
  // The button is disabled while the place is read: the menu opens only once it is not. A menu still open from before is shut
  // first, since a click on the button then would close it again.
  await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="new-button"]:not([disabled])')`, 15000);
  await page.waitFor(`![...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden)')].length`, 10000).catch(async () => { await page.evaluate(`document.activeElement?.blur?.()`); });
  const open = `[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].length > 0`;
  for (let attempt = 0; attempt < 2; attempt++) {
    await page.evaluate(`document.querySelector('.shell-sider [data-testid="new-button"]').click(), true`);
    if (await page.waitFor(open, 4000).then(() => true, () => false)) break;
  }
  await page.waitFor(open, 10000);
  if (waitFor) await page.waitFor(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].some((i) => i.textContent.trim() === ${q(waitFor)})`, 10000);
  return page.evaluate(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].map((i) => ({ text: i.textContent.trim(), disabled: i.classList.contains('ant-dropdown-menu-item-disabled') }))`);
}

// clickItem chooses the open menu's item with this text.
async function clickItem(page, text) {
  await page.waitFor(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].some((i) => i.textContent.trim() === ${q(text)} && !i.classList.contains('ant-dropdown-menu-item-disabled'))`, 10000);
  // A real press, not a script click: the browser counts it as the person's gesture, which the picker needs to open (decision 66).
  // The menu animates in: its item is measured once it has stopped.
  await sleep(400);
  const at = await page.evaluate(`(() => { const i = [...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].find((x) => x.textContent.trim() === ${q(text)}); const r = i.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: at.x, y: at.y, button: 'none', buttons: 0 });
  await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: at.x, y: at.y, button: 'left', buttons: 1, clickCount: 1 });
  await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: at.x, y: at.y, button: 'left', buttons: 0, clickCount: 1 });
}

// storageLine reads the sidebar's bar and its figures: the level the bar is at, its fill, and the line under it.
// settledLine waits until the figures are read, and returns them. The upload check starts from a read line, not from a title.
async function settledLine(page) {
  for (let k = 0; k < 30; k++) {
    const cur = await storageLine(page);
    if (cur.line !== null) return cur;
    await sleep(500);
  }
  throw new Error('the sidebar never read the storage figures');
}

// The line is null until the figures are read: before that the block holds its title alone.
const storageLine = (page) =>
  page.evaluate(`(() => {
    const meter = document.querySelector('.shell-sider [data-testid="sidebar-meter"]');
    const item = document.querySelector('.shell-menu .shell-storage-item');
    const spans = item ? item.querySelectorAll('.shell-storage > span') : [];
    const line = meter ? meter.nextElementSibling : spans.length > 1 ? spans[spans.length - 1] : null;
    return {
      figures: line ? getComputedStyle(line).color : null,
      level: meter?.dataset.level ?? null,
      fill: meter?.firstElementChild ? getComputedStyle(meter.firstElementChild).backgroundColor : null,
      title: item?.querySelector('.shell-storage > span')?.textContent.trim() ?? null,
      line: line?.textContent.trim() ?? null,
    };
  })()`);

// shotClip saves one part of the page, given as a box in the window's pixels.
async function shotClip(page, dir, name, box) {
  if (!dir) return;
  const { data } = await page.send('Page.captureScreenshot', { format: 'png', clip: { x: box.x, y: box.y, width: box.w, height: box.h, scale: 1 } });
  await writeFile(join(dir, name), Buffer.from(data, 'base64'));
}

// boxOf is the window's box of the element the selector names, widened by pad pixels.
const boxOf = (page, selector, pad = 8) =>
  page.evaluate(`(() => { const el = document.querySelector(${q(selector)}); if (!el) return null; const r = el.getBoundingClientRect(); return { x: Math.max(0, r.left - ${pad}), y: Math.max(0, r.top - ${pad}), w: r.width + ${pad * 2}, h: r.height + ${pad * 2} }; })()`);

// measureSize reads a size as the screen draws it (decision 67): the x of its first digit and the x of its unit. With exact,
// the size is the whole text of one leaf element (a cell, a figure); otherwise it is the first size in the root's text. The
// isolate marks are read through, so a size wrapped for a right-to-left sentence reads as the same size.
function measureSize(selector, exact) {
  const SIZE = /\d[\d.,]*(\s)([^\s\d\u2066-\u2069]+)/;
  const plain = (s) => s.replace(/[\u2066-\u2069]/g, '').trim();
  const root = document.querySelector(selector);
  if (!root) return null;
  const leaves = [root, ...root.querySelectorAll('*')].filter((e) => e.children.length === 0);
  const holder = exact ? leaves.find((e) => /^\d[\d.,]*\s[^\s\d]+$/.test(plain(e.textContent))) : root;
  if (!holder) return null;
  const walker = document.createTreeWalker(holder, NodeFilter.SHOW_TEXT);
  const parts = [];
  let at = 0;
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    parts.push({ n, at });
    at += n.data.length;
  }
  const m = SIZE.exec(parts.map((p) => p.n.data).join(''));
  if (!m) return null;
  const x = (i) => {
    const p = parts.findLast((q2) => q2.at <= i);
    const r = document.createRange();
    r.setStart(p.n, i - p.at);
    r.setEnd(p.n, i - p.at + 1);
    return r.getBoundingClientRect().left;
  };
  return { size: plain(m[0]), digit: x(m.index), unit: x(m.index + m[0].length - m[2].length) };
}
const sizeAt = (page, selector, exact) => page.evaluate(`(${measureSize.toString()})(${q(selector)}, ${exact})`);
const LINE = '.shell-menu .shell-storage-item .shell-storage > span:last-child';
// The selected tree row and the selected Storage row (the current place), and the bar inside the Storage row.
const TREE_ROW = '[data-testid="folder-tree"] [role="treeitem"][aria-current="location"] > .tree-row';
const SEL_ROW = '.shell-menu .shell-storage-item.ant-menu-item-selected';
const TREE_MARK = TREE_ROW;
// noBarIn says the element has no bar: its ::before draws nothing (decision 72: a filled top-level place has no bar).
const noBarIn = (page, sel) =>
  page.evaluate(`(() => { const el = document.querySelector(${JSON.stringify(sel)}); if (!el) return null; const s = getComputedStyle(el, '::before'); return s.content === 'none' || s.width === '0px'; })()`);
const METER = '[data-testid="sidebar-meter"]';

// contrastOf reads a colour the way getComputedStyle gives it and the WCAG ratio of that colour on the colour it is drawn on.
// bgs lists the layers the text sits on, top first; the last one (the bottom) must be opaque, and each layer above it is blended
// over the one below, so an alpha colour (the track, the figures) is read as it looks. Returns null when a layer is missing.
const contrastOf = (page, sel, prop, bgs) =>
  page.evaluate(`(() => {
    const parse = (s) => { const m = /rgba?\\(([^)]+)\\)/.exec(s); if (!m) return null; const p = m[1].split(',').map((x) => parseFloat(x)); return { rgb: p.slice(0, 3), a: p.length > 3 ? p[3] : 1 }; };
    const over = (top, under) => top.rgb.map((c, i) => top.a * c + (1 - top.a) * under.rgb[i]);
    const lum = (c) => { const [r, g, b] = c.map((v) => { const x = v / 255; return x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4; }); return 0.2126 * r + 0.7152 * g + 0.0722 * b; };
    const el = document.querySelector(${q(sel)});
    const layers = ${q(bgs)}.map((b) => document.querySelector(b));
    if (!el || layers.some((x) => !x)) return null;
    let under = parse(getComputedStyle(layers[layers.length - 1]).backgroundColor);
    if (!under || under.a < 1) return null;
    let bg = under.rgb;
    for (let i = layers.length - 2; i >= 0; i--) {
      const c = parse(getComputedStyle(layers[i]).backgroundColor);
      if (!c) return null;
      bg = over(c, { rgb: bg, a: 1 });
    }
    const fg = parse(getComputedStyle(el)[${q(prop)}]);
    if (!fg) return null;
    const rgb = over(fg, { rgb: bg, a: 1 });
    const [hi, lo] = [lum(rgb), lum(bg)].sort((x, y) => y - x);
    return { ratio: (hi + 0.05) / (lo + 0.05), text: getComputedStyle(el)[${q(prop)}], bg: bg.map(Math.round) };
  })()`);

// SIDER is the sidebar: the layer the current place's text and bar sit on (decision 69, option B).
const SIDER = '.shell-sider';
// barOf reads the current place's bar (its ::before) against the sidebar, as the ratio of its colour on the sidebar.
const barOf = (page, sel) =>
  page.evaluate(`(() => {
    const parse = (s) => { const m = /rgba?\\(([^)]+)\\)/.exec(s); return m ? m[1].split(',').map((x) => parseFloat(x)) : null; };
    const lum = (c) => { const [r, g, b] = c.map((v) => { const x = v / 255; return x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4; }); return 0.2126 * r + 0.7152 * g + 0.0722 * b; };
    const el = document.querySelector(${q(sel)});
    const bg = el && parse(getComputedStyle(document.querySelector(${q(SIDER)})).backgroundColor);
    const bar = el && parse(getComputedStyle(el, '::before').backgroundColor);
    if (!bg || !bar) return null;
    const [hi, lo] = [lum(bar.slice(0, 3)), lum(bg.slice(0, 3))].sort((x, y) => y - x);
    return { ratio: (hi + 0.05) / (lo + 0.05), text: getComputedStyle(el, '::before').backgroundColor, bg: bg.slice(0, 3).map(Math.round) };
  })()`);
// onMark reads the Storage row's title and figures on the sidebar, and the bar's track, edge and fill, as the Storage block draws them.
const onMark = (page) =>
  Promise.all([
    contrastOf(page, `${SEL_ROW} .shell-storage > span:first-child`, 'color', [SEL_ROW]),
    contrastOf(page, `${SEL_ROW} .shell-storage > span:last-child`, 'color', [SEL_ROW]),
    contrastOf(page, METER, 'backgroundColor', [SIDER]),
    contrastOf(page, `${METER} > span`, 'backgroundColor', [METER, SIDER]),
    contrastOf(page, METER, 'boxShadow', [SIDER]),
  ]).then(([title, line, track, fill, edge]) => ({ title, line, track, fill, edge }));
// The tree's current row and the selected Storage row are drawn a moment after the page changes (the tree follows the folder the
// listing shows), and their colours move over 0.3 s (the menu's and the links' transitions), so the colours are read once the
// row is current and the transitions are done.
const waitTreeCurrent = async (page, key) => {
  await page.waitFor(`!!document.querySelector('[data-tree-key="${key}"][aria-current="location"]')`, 15000);
  await sleep(700);
};
// waitSelected waits for the Storage row to be the selected item (filled with the active colour, decision 72).
const waitSelected = async (page) => {
  await page.waitFor(`(() => { const li = document.querySelector('${SEL_ROW}'); return !!li && getComputedStyle(li).backgroundColor === 'rgb(254, 204, 0)'; })()`, 15000);
  await sleep(700);
};
const showRatio = (r) => (r && typeof r.ratio === 'number' ? `${r.ratio.toFixed(2)}:1, ${r.text} on rgb(${r.bg.join(', ')})` : r ? `not a number (${JSON.stringify(r)})` : 'not read');

export async function d2Checks(h, { page, base, accountKey, teamA, teamB, dir, accountID, memberID }) {
  const { check, api, navigateTo, clickNav, press, rowAppears } = h;
  // The folder the upload check puts files in: its own, so this section runs alone (d1 makes the same folder; the second create is refused and ignored).
  await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: '/D1 Projects', parents: false }).catch(() => undefined);
  const shotsDir = process.env.SELFCLOUD_E2E_SHOTS
    ? await (async () => {
        await mkdir(process.env.SELFCLOUD_E2E_SHOTS, { recursive: true });
        return process.env.SELFCLOUD_E2E_SHOTS;
      })()
    : null;
  const snap = async (name) => {
    if (!shotsDir) return;
    await sleep(800);
    const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
    await writeFile(join(shotsDir, name), Buffer.from(data, 'base64'));
  };
  const size = (w, hgt) => page.send('Emulation.setDeviceMetricsOverride', { width: w, height: hgt, deviceScaleFactor: 1, mobile: w < 600 });
  const reload = async () => {
    await page.send('Page.reload', { ignoreCache: true });
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="new-button"]:not([disabled])')`, 20000);
  };

  // The member's limit is put back before the section ends, whatever happens in between: null follows the default.
  const before = await api(base, 'GET', `/api/v1/accounts/${accountID}/storage?users_limit=200`, accountKey);
  const original = before.users?.find((u) => u.id === memberID)?.quota_bytes ?? null;
  // Each run names what it makes with its own suffix, so a re-run on the same account does not collide. The finally removes it
  // all, and puts back what the checks change: the rail's saved choice, the language, the theme, the viewport and the picker.
  const suffix = Date.now().toString(36);
  const MADE_NAME = `${MADE} ${suffix}`;
  const uploadName = `d2-upload-${suffix}.txt`;
  const folderUploadName = `d2-folder-${suffix}.txt`;
  const railWas = await page.evaluate(`window.localStorage.getItem('${RAIL_KEY}')`);
  const langWas = await page.evaluate(`document.documentElement.lang`);
  const themeWas = await page.evaluate(`document.documentElement.dataset.theme`);
  const localFiles = [];
  let myFolder = null;
  let pickerPatched = false;
  try {
    // ---- Desktop, 1440 wide, English, light: New is in the sidebar, and the toolbar has none.
    await size(1440, 900);
    await navigateTo(page, `/files/${teamA}`);
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="new-button"]:not([disabled])') && !!document.querySelector('[data-testid="folder-tree"] .tree-row')`, 20000);
    // The toolbar's New is the plus icon under 480 px (decision 68), so its name (aria-label) is read too.
    const toolbarNew = await page.evaluate(`[...document.querySelectorAll('[data-testid="browser-toolbar"] button')].some((b) => b.textContent.trim() === 'New' || b.getAttribute('aria-label') === 'New')`);
    check(!toolbarNew, 'at 1440 the toolbar has no New: the sidebar holds it');

    // Upload files from inside a folder: D1 Projects is the folder shown, and the picker the menu opens gives the file to that
    // folder. The picker is opened inside the menu's click, which the browser needs (decision 66); the patch records it.
    await navigateTo(page, `/files/${teamA}`);
    // The tree starts folded (decision 69): Team A's arrow opens it to reach the folder.
    const teamARow = `[...document.querySelectorAll('[data-testid="folder-tree"] [role="treeitem"][aria-level="1"]')].find((x) => x.querySelector('.tree-row bdi')?.textContent === 'Team A')`;
    await page.waitFor(`!!${teamARow}`, 15000);
    await page.evaluate(`(() => { const t = ${teamARow}; if (t && t.getAttribute('aria-expanded') !== 'true') t.querySelector('button[aria-label^="Expand"]')?.click(); return true; })()`);
    await page.waitFor(`!!document.querySelector('[data-drop-name="D1 Projects"] a')`, 15000);
    await page.evaluate(`document.querySelector('[data-drop-name="D1 Projects"] a').click(), true`);
    await page.waitFor(`location.pathname.startsWith('/files/${teamA}/') && document.querySelector('#browser-title')?.textContent.trim() === 'D1 Projects'`, 15000);
    await page.evaluate(`(() => { window.__d2Click = HTMLInputElement.prototype.click; HTMLInputElement.prototype.click = function () { window.__pickerActive = navigator.userActivation.isActive; return window.__d2Click.call(this); }; })()`);
    pickerPatched = true;
    await newMenu(page, 'Upload files');
    await clickItem(page, 'Upload files');
    const pickerInGesture = await page.evaluate(`window.__pickerActive === true`);
    check(pickerInGesture, 'the file picker opens inside the click of the menu item (the browser holds the user activation)');
    const folderFile = join(dir, folderUploadName);
    localFiles.push(folderFile);
    await writeFile(folderFile, 'd2, inside a folder\n');
    await page.setFiles('[data-testid="sidebar-file-input"]', [folderFile]);
    await rowAppears(page, folderUploadName);
    const inFolder = await api(base, 'GET', `/api/v1/folders/${teamA}/children?path=${encodeURIComponent('/D1 Projects')}`, accountKey);
    check(inFolder.items.some((n) => n.name === folderUploadName), `upload files from inside a folder puts the file there: D1 Projects is shown and holds it (${folderUploadName})`);

    // The storage line: the member's own files, in My files. The team folder above does not move the member's meter.
    await page.evaluate(`[...document.querySelectorAll('[data-testid="folder-tree"] .tree-row')].find((r) => r.textContent.trim() === 'My files')?.querySelector('a')?.click()`);
    await page.waitFor(`!!document.querySelector('[role="row"][aria-label]') && document.querySelector('#browser-title')?.textContent === 'My files'`, 15000);
    myFolder = await page.evaluate(`location.pathname.split('/')[2] ?? null`);
    check(typeof myFolder === 'string' && myFolder.startsWith('fld_'), 'My files is the folder the run uploads to and cleans up');
    // The line is read before the upload: a line that is not read yet is null, so the change below is a real one.
    const before1 = await settledLine(page);
    // Two megabytes, so the figures the sidebar shows change whatever the member held before.
    const upload = join(dir, uploadName);
    localFiles.push(upload);
    await writeFile(upload, Buffer.alloc(2 * 1024 * 1024, 'd'));
    await newMenu(page, 'Upload files');
    await clickItem(page, 'Upload files');
    await page.setFiles('[data-testid="sidebar-file-input"]', [upload]);
    await rowAppears(page, uploadName);
    check(true, 'Upload files from the sidebar puts the file in the folder shown (My files)');
    // The sidebar reads the figures again when the upload batch ends (Shell), and the files page reloads its listing at the same
    // time, which reads them too (FilesPage). The line must change from the one read before the upload.
    for (let k = 0; k < 30; k++) { await sleep(500); const cur = await storageLine(page); if (cur.line !== before1.line) break; }
    const after1 = await storageLine(page);
    check(after1.line !== null && after1.line !== before1.line, `the storage line is read again after the upload batch (${before1.line} -> ${after1.line})`);

    // The member's limit is set to 61%, from what the member holds now, and the page is read again so the meter shows it.
    const held = await heldOf(page);
    check(held > 0, `the member holds something to measure (${held} bytes)`);
    await quotaSet(api, base, accountID, accountKey, memberID, Math.floor(held / 0.61));
    await reload();
    await navigateTo(page, `/files/${teamA}`);
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="sidebar-meter"]')`, 15000);
    const at61 = await storageLine(page);
    check(at61.title === 'Storage (61% full)', `the row says the share held (${at61.title})`);
    check(at61.level === 'ok' && at61.fill === FILL.ok, `under 80% the bar is the accent (${at61.level}, ${at61.fill})`);
    // The current folder has no background (decision 69, option B): its name, its arrow and its bar read on the sidebar.
    await waitTreeCurrent(page, teamA);
    const treeName = await contrastOf(page, `${TREE_ROW} a bdi`, 'color', [TREE_ROW]);
    const treeArrow = await contrastOf(page, `${TREE_ROW} > button`, 'color', [TREE_ROW]);
    const treeBarNone = await noBarIn(page, TREE_MARK);
    check(treeName !== null && treeName.ratio >= 4.5, `the selected top-level place's name reads on its fill at 4.5:1 or more (${showRatio(treeName)})`);
    check(treeArrow !== null && treeArrow.ratio >= 4.5, `the selected top-level place's arrow reads on its fill at 4.5:1 or more (${showRatio(treeArrow)})`);
    check(treeBarNone === true, `the selected top-level place has no bar: its fill marks it (decision 72)`);
    await shotClip(page, shotsDir, 'c1-tree-selected-light.png', await boxOf(page, '.ant-layout-sider', 0));
    await snap('d2-desktop-en-light.png');
    const menuItems = await newMenu(page, 'New folder');
    check(['New folder', 'Upload files', 'Upload folder'].every((t) => menuItems.some((i) => i.text === t && !i.disabled)), `the New menu holds the three entries, all on (${JSON.stringify(menuItems)})`);
    await snap('d2-desktop-en-light-menu.png');
    await press(page, 'body', 'Escape');
    await page.waitFor(`![...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden)')].length`, 10000);

    // The sidebar's figures are the Storage page's figures.
    await navigateTo(page, '/storage');
    await page.waitFor(`location.pathname === '/storage'`, 15000);
    // The Storage page is mock-up A (decision 79): its meter's bar says the figures in its text alternative, "X of Y used: parts".
    const meterAlt = `document.querySelector('.storage-card .storage-bar[role="img"]')?.getAttribute('aria-label') ?? ''`;
    await page.waitFor(`(${meterAlt}).includes(' used: ')`, 15000);
    const pageLine = await page.evaluate(`(${meterAlt}).split(': ')[0] || null`);
    check(pageLine === at61.line, `the sidebar line matches the Storage page's figures (${at61.line} / ${pageLine})`);
    // On this page the Storage row is the current place: its title and figures read on the sidebar, and the bar keeps its track and fill.
    await waitSelected(page);
    const yEn = await onMark(page);
    const storageBarNone = await noBarIn(page, SEL_ROW);
    check(yEn.title !== null && yEn.title.ratio >= 4.5, `on the selected Storage row the title reads on its fill at 4.5:1 or more (${showRatio(yEn.title)})`);
    check(yEn.line !== null && yEn.line.ratio >= 4.5, `on the selected Storage row the figures read on its fill at 4.5:1 or more (${showRatio(yEn.line)})`);
    // Decision 69 (option B): the figures keep the caption colour when the row is selected, the same as when it is not (read above, on
    // the files page, where the row is not selected). The caption is the brand's: blue by default, and the text colour on a brand
    // whose sidebar the caption does not read on.
    const onActive = await onActiveInk();
    check(yEn.line !== null && yEn.line.text === onActive,`on the selected Storage row the figures take the on-active colour (${yEn.line?.text})`);
    check(storageBarNone === true, `on the selected Storage row there is no bar: its fill marks it (decision 72)`);
    // The track is not checked against the sidebar: a yellow fill keeps 3:1 against a track dark enough to read on the blue, and a track
    // light enough to reach 3:1 against the blue leaves the yellow under 1.2:1 (computed for the default blue and the teal, review
    // fix 3). The edge (the caption ring, below) is the bar's outline against the sidebar.
    check(yEn.fill !== null && yEn.fill.ratio >= 3, `on the selected Storage row the bar's fill is 3:1 or more against its track (${showRatio(yEn.fill)})`);
    check(yEn.edge !== null && yEn.edge.ratio >= 3, `on the selected Storage row the bar's edge is 3:1 or more against the sidebar (${showRatio(yEn.edge)})`);
    await shotClip(page, shotsDir, 'c1-storage-en-light.png', await boxOf(page, '.ant-layout-sider', 0));
    await navigateTo(page, `/files/${teamA}`);
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="sidebar-meter"]')`, 15000);

    // ---- The three colours of the bar, at 85% and at 100%, and no limit at all (set through the API).
    const held2 = await heldOf(page);
    await quotaSet(api, base, accountID, accountKey, memberID, Math.floor(held2 / SHARE_LEVELS.warning));
    await reload();
    await navigateTo(page, `/files/${teamA}`);
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="sidebar-meter"]')`, 15000);
    const at85 = await storageLine(page);
    check(at85.level === 'warning' && at85.fill === FILL.warning, `from 80% the bar is the warning colour (${at85.level}, ${at85.fill})`);
    await shotClip(page, shotsDir, 'd2-meter-85.png', await boxOf(page, '.shell-menu .shell-storage-item'));

    await quotaSet(api, base, accountID, accountKey, memberID, held2);
    await reload();
    await navigateTo(page, `/files/${teamA}`);
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="sidebar-meter"]')`, 15000);
    const at100 = await storageLine(page);
    check(at100.level === 'danger' && at100.fill === FILL.danger, `at 100% the bar is the error colour (${at100.level}, ${at100.fill})`);
    check(at100.title === 'Storage (100% full)', `at 100% the row says so (${at100.title})`);
    await shotClip(page, shotsDir, 'd2-meter-100.png', await boxOf(page, '.shell-menu .shell-storage-item'));

    await quotaSet(api, base, accountID, accountKey, memberID, 0);
    await reload();
    await navigateTo(page, `/files/${teamA}`);
    const none = await settledLine(page);
    check(none.title === 'Storage' && none.level === null && none.fill === null, `with no limit the row says Storage, and there is no bar (${none.title}, ${none.level})`);
    check(none.line !== null && !none.line.includes(' of ') && none.line.endsWith(' used'), `with no limit the line says what is used (${none.line})`);
    await shotClip(page, shotsDir, 'd2-meter-no-limit.png', await boxOf(page, '.shell-menu .shell-storage-item'));
    // The limit is back as it was for the rest of the run.
    await quotaSet(api, base, accountID, accountKey, memberID, original);
    await reload();
    await navigateTo(page, `/files/${teamA}`);

    // ---- The alignment: the New button and every row share one icon column and one name column, and one font size.
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="folder-tree"] .tree-row') && !!document.querySelector('.shell-menu .ant-menu-item')`, 15000);
    const geo = await rowGeometry(page);
    const off = columns(geo, false);
    const near = (v) => v !== null && Math.abs(v) <= 1;
    check(near(off.new.icon) && near(off.new.name), `New's plus and word line up with Starred's icon and name, within a pixel (${JSON.stringify(off.new)})`);
    check(near(off.mine.icon) && near(off.mine.name), `My files' icon and name line up with Starred's, within a pixel (${JSON.stringify(off.mine)})`);
    check(near(off.team.icon) && near(off.team.name), `Team A's icon and name line up with Starred's, within a pixel (${JSON.stringify(off.team)})`);
    check(near(off.storage.icon) && near(off.storage.name), `the Storage block's icon and name line up with Starred's, within a pixel (${JSON.stringify(off.storage)})`);
    const fonts = [geo.new.fs, geo.starred.fs, geo.mine.fs, geo.team.fs, geo.storage.fs];
    check(fonts.every((f) => f === '14px'), `every row has one font size, 14 px (${fonts.join(', ')})`);
    await shotClip(page, shotsDir, 'd2-columns.png', { x: 0, y: 40, w: 264, h: 220 });
    console.log(`alignment (px, English, from Starred): ${JSON.stringify({ new: geo.new, starred: geo.starred, mine: geo.mine, team: geo.team, storage: geo.storage })}`);
    // New fits its text (decision 69): it starts at the sidebar's content edge, 12 px in, and ends 24 px after its word, not at the edge.
    const newWidth = geo.newBox ? geo.newBox.r - geo.newBox.l : 0;
    check(geo.newBox !== null && near(geo.newBox.l - 12) && geo.newBox.r < 252 - 60 && geo.newWordEnd !== null && Math.abs(geo.newBox.r - geo.newWordEnd - 24) <= 1.5, `the New button fits its text: 12 px in from the start edge, 24 px after its word, and not the sidebar's width (${JSON.stringify(geo.newBox)}, word ends ${geo.newWordEnd}, width ${newWidth})`);
    // A size reads digits then unit, left to right, in English (decision 67): the sidebar line's first size.
    const lineEn = await sizeAt(page, LINE, false);
    check(lineEn !== null && lineEn.digit < lineEn.unit, `in English the storage line draws its digits, then the unit (${JSON.stringify(lineEn)})`);

    // Hebrew: the page mirrors, and the columns mirror with it.
    await setLanguage(page, 'עברית');
    await page.waitFor(`document.documentElement.dir === 'rtl' && !!document.querySelector('.shell-sider [data-testid="folder-tree"] .tree-row')`, 15000);
    const geoHe = await rowGeometry(page);
    const offHe = columns(geoHe, true);
    check(near(offHe.new.icon) && near(offHe.new.name), `in Hebrew New's plus and word line up with the rows, mirrored, within a pixel (${JSON.stringify(offHe.new)})`);
    check(near(offHe.mine.icon) && near(offHe.mine.name), `in Hebrew My files' icon and name line up with Starred's (${JSON.stringify(offHe.mine)})`);
    check(near(offHe.storage.icon) && near(offHe.storage.name), `in Hebrew the Storage block lines up with Starred (${JSON.stringify(offHe.storage)})`);
    // A size keeps its order inside the Hebrew sentence (decision 67): digits, then the unit, left to right. Read on the sidebar's
    // line, on a file's size cell in the listing, and on the Storage page's figure.
    const lineHe = await sizeAt(page, LINE, false);
    check(lineHe !== null && lineHe.digit < lineHe.unit, `in Hebrew the storage line draws its digits, then the unit, left to right (${JSON.stringify(lineHe)})`);
    await snap('d2-desktop-he-light.png');
    await shotClip(page, shotsDir, 'b1-he-storage-line.png', await boxOf(page, '.shell-menu .shell-storage-item'));
    await navigateTo(page, `/files/${myFolder}`);
    await page.waitFor(`!!document.querySelector('[role="row"][aria-label="${uploadName}"]')`, 15000);
    const cellHe = await sizeAt(page, `[role="row"][aria-label="${uploadName}"]`, true);
    check(cellHe !== null && cellHe.digit < cellHe.unit, `in Hebrew a file's size cell draws its digits, then the unit, left to right (${JSON.stringify(cellHe)})`);
    await shotClip(page, shotsDir, 'b1-he-listing-sizes.png', await boxOf(page, `[role="row"][aria-label="${uploadName}"]`));
    await navigateTo(page, '/storage');
    // The page's figure is the meter's large amount (mock-up A, decision 79): the first number in its card.
    const FIGURE = '.storage-card .num';
    await page.waitFor(`location.pathname === '/storage' && !!document.querySelector(${q(FIGURE)})`, 15000);
    const pageHe = await sizeAt(page, FIGURE, true);
    check(pageHe !== null && pageHe.digit < pageHe.unit, `in Hebrew the Storage page's figure draws its digits, then the unit, left to right (${JSON.stringify(pageHe)})`);
    await shotClip(page, shotsDir, 'b1-he-storage-page.png', await boxOf(page, FIGURE));
    // Arabic reads right to left, so its digits come first at the right: the digits sit right of the unit.
    await setLanguage(page, 'العربية');
    await page.waitFor(`document.documentElement.dir === 'rtl' && document.documentElement.lang === 'ar'`, 15000);
    const lineAr = await sizeAt(page, LINE, false);
    check(lineAr !== null && lineAr.digit > lineAr.unit, `in Arabic the storage line keeps its right-to-left order, digits first (${JSON.stringify(lineAr)})`);
    await shotClip(page, shotsDir, 'b1-ar-storage-line.png', await boxOf(page, '.shell-menu .shell-storage-item'));
    // The Storage page in Arabic: the selected row's title and figures read on the sidebar there too.
    await navigateTo(page, '/storage');
    await page.waitFor(`location.pathname === '/storage' && !!document.querySelector('${SEL_ROW} .shell-storage > span:last-child')`, 15000);
    await waitSelected(page);
    const yAr = await onMark(page);
    check(yAr.title !== null && yAr.title.ratio >= 4.5 && yAr.line !== null && yAr.line.ratio >= 4.5, `in Arabic the selected Storage row reads on the sidebar at 4.5:1 or more (title ${showRatio(yAr.title)}; figures ${showRatio(yAr.line)})`);
    await shotClip(page, shotsDir, 'c1-storage-ar-light.png', await boxOf(page, '.ant-layout-sider', 0));
    await setLanguage(page, 'English');
    await page.waitFor(`document.documentElement.dir === 'ltr'`, 15000);

    // Dark: the same sidebar in the dark theme.
    await setTheme(page, 'Dark');
    await navigateTo(page, `/files/${teamA}`);
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="new-button"]')`, 15000);
    await snap('d2-desktop-en-dark.png');
    await waitTreeCurrent(page, teamA);
    const treeDark = await contrastOf(page, `${TREE_ROW} a bdi`, 'color', [TREE_ROW]);
    check(treeDark !== null && treeDark.ratio >= 4.5, `in dark, the selected top-level place's name reads on its fill at 4.5:1 or more (${showRatio(treeDark)})`);
    await shotClip(page, shotsDir, 'c1-tree-selected-dark.png', await boxOf(page, '.ant-layout-sider', 0));
    await setTheme(page, 'Light');

    // ---- New folder from Starred makes the folder in My files; the message opens it.
    await clickNav(page, 'Starred');
    await page.waitFor(`location.pathname === '/starred'`, 15000);
    await newMenu(page, 'New folder');
    await clickItem(page, 'New folder');
    await page.waitFor(`!!document.getElementById('newfolder-name')`, 15000);
    await h.setInput(page, 'newfolder-name', MADE_NAME);
    await page.evaluate(`[...document.querySelectorAll('.ant-modal-footer button')].find((b) => b.textContent.trim() === ${q('Create folder')}).click(), true`);
    await page.waitFor(`[...document.querySelectorAll('.ant-message-notice')].some((m) => m.textContent.includes(${q(`Created ${MADE_NAME}.`)}))`, 15000);
    check(true, 'New folder from Starred says the folder is made, with a way to open it');
    await page.waitFor(`[...document.querySelectorAll('.ant-message-notice button')].some((b) => b.textContent.trim() !== '')`, 10000);
    await page.evaluate(`[...document.querySelectorAll('.ant-message-notice button')].find((b) => b.textContent.trim() !== '').click(), true`);
    await page.waitFor(`document.querySelector('#browser-title')?.textContent.trim() === ${q(MADE_NAME)}`, 15000);
    check(true, 'the message opens the folder made from Starred');
    await clickNav(page, 'My files');
    await page.waitFor(`/^\\/files\\/fld_/.test(location.pathname)`, 15000);
    await rowAppears(page, MADE_NAME);
    check(true, 'the folder made from Starred is in My files');

    // ---- A folder the member cannot add to: New is disabled there, with the reason, and nothing goes to My files.
    await navigateTo(page, `/files/${teamB}`);
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="new-button"]')`, 15000);
    // Decision 95: the reason is the menu's header (what the menu is named by), not an item; the entries stay disabled.
    const roItems = await newMenu(page, 'New folder');
    const roReason = await menuHeaderText(page);
    check(roReason === 'You cannot add to this folder.' && roItems.length > 0 && roItems.every((i) => i.disabled), `in a folder the member cannot add to, the reason is the menu's header and the entries are disabled (header ${JSON.stringify(roReason)}, ${JSON.stringify(roItems)})`);
    await press(page, 'body', 'Escape');
    await page.waitFor(`![...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden)')].length`, 10000);

    // Round 3, M1: the same New menu by real key presses, in the read-only folder: the reason is the title, and no key stops on it.
    await newMenuKeyChecks(page, check, { button: '.shell-sider [data-testid="new-button"]', label: 'desktop, Team B, read-only, Enter', readOnly: true });
    // Decision 95, in Hebrew: the reason is the menu's header in the Hebrew words of he.json, and the entries stay disabled.
    // The language is put back to English in a finally, so a failure in here cannot leave the rest of the section in Hebrew (L6).
    try {
      await setLanguage(page, 'עברית');
      await page.waitFor(`document.documentElement.dir === 'rtl'`, 15000);
      await navigateTo(page, `/files/${teamB}`);
      await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="new-button"]')`, 15000);
      const roItemsHe = await newMenu(page);
      await page.waitFor(`!!document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden) ul[role="menu"][aria-labelledby]')`, 10000);
      const roReasonHe = await menuHeaderText(page);
      const heStrings = JSON.parse(await readFile(new URL('../src/i18n/he.json', import.meta.url), 'utf8'));
      check(roReasonHe === heStrings['files.new.readOnly'] && roItemsHe.length > 0 && roItemsHe.every((i) => i.disabled), `in Hebrew, a folder the member cannot add to names its menu with the reason, and the entries are disabled (header ${JSON.stringify(roReasonHe)})`);
      await press(page, 'body', 'Escape');
      await page.waitFor(`![...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden)')].length`, 10000);
    } finally {
      await press(page, 'body', 'Escape').catch(() => undefined);
      await setLanguage(page, 'English');
      await page.waitFor(`document.documentElement.lang === 'en'`, 15000);
    }

    // ---- Collapsed: a round New with its name in a tooltip, and the same menu. The place is Team A, which the member can add to.
    await navigateTo(page, `/files/${teamA}`);
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="new-button"]:not([disabled])')`, 15000);
    // Round 3, M1: the writable folder, by real key presses, Enter opens the sidebar's New.
    await newMenuKeyChecks(page, check, { button: '.shell-sider [data-testid="new-button"]', label: 'desktop, Team A, writable, Enter' });
    await clickOn(page, '[data-testid="rail-toggle"]');
    await page.waitFor(`!!document.querySelector('.ant-layout-sider-collapsed [data-testid="new-button"]')`, 15000);
    const round = await page.evaluate(`(() => { const b = document.querySelector('.ant-layout-sider-collapsed [data-testid="new-button"]'); const r = b.getBoundingClientRect(); return { label: b.getAttribute('aria-label'), w: Math.round(r.width), h: Math.round(r.height) }; })()`);
    check(round.label === 'New' && round.w === 44 && round.h === 44, `folded, the New button is a round 44 pixel button named New (${JSON.stringify(round)})`);
    // The pointer comes from away from the button, so the enter is a real one: the Tooltip opens on it.
    await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 700, y: 500, button: 'none', buttons: 0 });
    await sleep(200);
    await hover(page, '.ant-layout-sider-collapsed [data-testid="new-button"]');
    const tipShown = await page.waitFor(`[...document.querySelectorAll('.ant-tooltip')].some((t) => t.textContent.trim() === 'New')`, 10000).then(() => true, () => false);
    check(tipShown, 'folded, New has its name in a tooltip');
    await shotClip(page, shotsDir, 'd2-collapsed.png', await boxOf(page, '.ant-layout-sider', 0));
    const folded = await newMenu(page, 'New folder');
    check(folded.some((i) => i.text === 'New folder' && !i.disabled), 'folded, the New menu opens with its entries');
    await press(page, 'body', 'Escape');
    await page.waitFor(`![...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden)')].length`, 10000);
    // Round 3, M1: the folded rail's round New, by real key presses, opened with Space.
    await newMenuKeyChecks(page, check, { button: '.ant-layout-sider-collapsed [data-testid="new-button"]', label: 'folded, Team A, writable, Space', opener: 'Space' });
    await clickOn(page, '[data-testid="rail-toggle"]');
    await page.waitFor(`!document.querySelector('.ant-layout-sider-collapsed')`, 15000);

    // ---- Phone: the drawer holds New at its top, and the toolbar keeps its New. The phone emulation is a coarse pointer, so
    // the Storage row is a finger's target: at least 44 px tall.
    await setPhone(page, true);
    await size(390, 800);
    await navigateTo(page, `/files/${teamA}`);
    await page.waitFor(`!!document.querySelector('[data-testid="nav-open"]')`, 15000);
    const phoneToolbar = await page.evaluate(`[...document.querySelectorAll('[data-testid="browser-toolbar"] button')].some((b) => b.textContent.trim() === 'New' || b.getAttribute('aria-label') === 'New')`);
    check(phoneToolbar, 'at 390 the toolbar keeps its New');
    await snap('d2-phone-toolbar.png');
    await page.evaluate(`document.querySelector('[data-testid="nav-open"]').click(), true`);
    await page.waitFor(`!!document.querySelector('.ant-drawer-open [data-testid="new-button"]')`, 15000);
    const drawerTop = await page.evaluate(`(() => { const n = document.querySelector('.ant-drawer-open [data-testid="new-button"]').getBoundingClientRect().top; const t = document.querySelector('.ant-drawer-open [data-testid="folder-tree"]')?.getBoundingClientRect().top ?? Infinity; return { n, t }; })()`);
    check(drawerTop.n < drawerTop.t, `the drawer shows New at its top, above the tree (${JSON.stringify(drawerTop)})`);
    // Round 3, M1: the drawer's New at 390 px, by real key presses (the drawer keeps the focus inside it while it is open).
    await newMenuKeyChecks(page, check, { button: '.ant-drawer-open [data-testid="new-button"]', label: '390 px, drawer, Team A, writable, Enter' });
    await page.waitFor(`!!document.querySelector('.ant-drawer-open .shell-storage-item')`, 15000);
    // The drawn height is 56 px with the figures, so the height alone would pass without the rule (which matters before the figures
    // are read, when the row is its title alone). The row's declared minimum is what the rule sets, so both are checked.
    const storageRow = await page.evaluate(`(() => { const el = document.querySelector('.ant-drawer-open .shell-storage-item'); return { coarse: window.matchMedia('(pointer: coarse)').matches, h: el.getBoundingClientRect().height, min: parseFloat(getComputedStyle(el).minBlockSize) }; })()`);
    check(storageRow.coarse && storageRow.h >= 44 && storageRow.min >= 44, `on a coarse pointer the Storage row is at least 44 px tall, and its minimum says so (${JSON.stringify(storageRow)})`);
    await snap('d2-phone-drawer.png');
    await page.evaluate(`document.querySelector('.ant-drawer-close').click(), true`);
    await page.waitFor(`!document.querySelector('.ant-drawer-open')`, 10000);
    await setPhone(page, false);
    await size(1440, 900);
    await navigateTo(page, `/files/${teamA}`);
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="new-button"]')`, 15000);
  } finally {
    // Whatever the checks did, the run leaves what it found: the member's limit, the uploads and the folder made here, the
    // rail, the language, the theme, the viewport and the picker. Each step is tried on its own, so one failure does not stop the rest.
    const failures = [];
    const step = async (what, fn) => {
      try {
        await fn();
      } catch (err) {
        failures.push(`${what}: ${err.message}`);
      }
    };
    await step('the member limit', () => quotaSet(api, base, accountID, accountKey, memberID, original));
    await step('the files and folder made here', async () => {
      const removeWhere = async (path, names) => {
        const kids = (await api(base, 'GET', path, accountKey)).items ?? [];
        for (const n of kids) if (names.includes(n.name)) await api(base, 'DELETE', `/api/v1/nodes/${n.id}?permanent=true`, accountKey);
        const left = ((await api(base, 'GET', path, accountKey)).items ?? []).filter((n) => names.includes(n.name));
        if (left.length > 0) throw new Error(`${left.length} left under ${path}`);
      };
      if (myFolder) await removeWhere(`/api/v1/folders/${myFolder}/children?path=%2F`, [uploadName, MADE_NAME]);
      await removeWhere(`/api/v1/folders/${teamA}/children?path=${encodeURIComponent('/D1 Projects')}`, [folderUploadName]);
    });
    // Read again after the removal: what the server still holds under the run's names is a failure, whatever the steps above did.
    await step('nothing of the run is still on the server', async () => {
      const left = [];
      if (myFolder) {
        const kids = (await api(base, 'GET', `/api/v1/folders/${myFolder}/children?path=%2F`, accountKey)).items ?? [];
        left.push(...kids.filter((n) => n.name === uploadName || n.name === MADE_NAME).map((n) => n.name));
      }
      const d1 = (await api(base, 'GET', `/api/v1/folders/${teamA}/children?path=${encodeURIComponent('/D1 Projects')}`, accountKey)).items ?? [];
      left.push(...d1.filter((n) => n.name === folderUploadName).map((n) => n.name));
      if (left.length > 0) throw new Error(`still there: ${left.join(', ')}`);
    });
    await step('the local upload files', async () => {
      for (const f of localFiles) await rm(f, { force: true });
    });
    await step('the picker', async () => {
      if (!pickerPatched) return;
      await page.evaluate(`(() => { if (window.__d2Click) { HTMLInputElement.prototype.click = window.__d2Click; delete window.__d2Click; } delete window.__pickerActive; })()`);
      if (!(await page.evaluate(`HTMLInputElement.prototype.click.toString().includes('[native code]')`))) throw new Error('the click is still patched');
    });
    await step('the viewport', () => setPhone(page, false));
    await step('the language', async () => {
      const now = await page.evaluate(`document.documentElement.lang`);
      if (now !== langWas) await setLanguage(page, langWas === 'he' ? 'עברית' : 'English');
    });
    await step('the theme', async () => {
      const now = await page.evaluate(`document.documentElement.dataset.theme`);
      if (now !== themeWas) await setTheme(page, themeWas === 'dark' ? 'Dark' : 'Light');
    });
    await step('the rail', async () => {
      if (await page.evaluate(`!!document.querySelector('.ant-layout-sider-collapsed')`)) await clickOn(page, '[data-testid="rail-toggle"]');
      await page.evaluate(railWas === null ? `window.localStorage.removeItem('${RAIL_KEY}')` : `window.localStorage.setItem('${RAIL_KEY}', ${q(railWas)})`);
    });
    check(failures.length === 0, `the run leaves nothing behind (${failures.join('; ') || 'nothing failed'})`);
  }
}

// hover moves the pointer over the element the selector names, so a tooltip opens.
async function hover(page, selector) {
  const at = await page.evaluate(`(() => { const el = document.querySelector(${q(selector)}); if (!el) return null; const r = el.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
  if (!at) throw new Error(`nothing matches ${selector}`);
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: at.x, y: at.y, button: 'none', buttons: 0 });
  await sleep(300);
}

// clickOn clicks the first element the selector names, once it is on the page.
async function clickOn(page, selector) {
  await page.waitFor(`!!document.querySelector(${q(selector)})`, 10000);
  await page.evaluate(`document.querySelector(${q(selector)}).click(), true`);
}
