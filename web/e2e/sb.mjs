// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice sb: every row of the sidebar's folder tree takes the menu row's metrics (decision 81). At 1280 wide, in light and dark, and
// in English and Hebrew, My files, a folder opened under it, and the Starred item have one height, the top two have the menu item's
// inline edges and corner radius, and My files' icon sits in the menu's icon column. Decision 185: every part of the expanded sidebar
// (the brand's picture, New, the tree's rows, the menu's rows and the storage block) starts at one edge, the picture, the rows and the
// block end at one edge, and the space above the picture is the side space S. On a finger (the drawer at 390 wide) every
// sidebar row is at least 44 pixels. With SELFCLOUD_E2E_SHOTS set (a folder), the pictures are saved.
import { mkdir } from 'node:fs/promises';
import { setLanguage, setPhone, setTheme, shot, sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);
const SUB = 'SB Opened';
const TOL = 0.5;
// The tree's step from one level to the next (FolderTree.tsx, paddingInlineStart: (level - 1) * 14).
const TREE_LEVEL_STEP = 14;
const same = (a, b) => Math.abs(a - b) <= TOL;
// SCOPE is the sidebar on the screen: the phone drawer when it is open, else the desktop sidebar.
const SCOPE = `(document.querySelector('.ant-drawer-open') ?? document.querySelector('.shell-sider'))`;

// rows reads the sidebar, or the phone drawer when it is open: My files, the folder opened under it, and the Starred item.
// Each sidebar row is measured too, for the finger check. Null for a row that is not on the screen.
const rows = (page) =>
  page.evaluate(`(() => {
    const scope = ${SCOPE};
    if (!scope) return null;
    const box = (el) => {
      if (!el) return null;
      const r = el.getBoundingClientRect();
      return { top: r.top, height: r.height, left: r.left, right: r.right, radius: getComputedStyle(el).borderTopLeftRadius };
    };
    const first = scope.querySelector('[data-testid="folder-tree"] [role="tree"] > [role="treeitem"] > .tree-row');
    const myFiles = box(first);
    const iconOf = (el) => { const r = el?.getBoundingClientRect(); return r ? r.left : null; };
    const subEl = scope.querySelector('[data-testid="folder-tree"] [data-drop-name="${SUB}"]');
    const sub = box(subEl);
    if (sub) sub.icon = iconOf(subEl.querySelector('a svg'));
    const menu = [...scope.querySelectorAll('.shell-menu li.ant-menu-item')];
    const starredLi = menu.find((li) => (li.getAttribute('data-menu-id') ?? '').includes('starred'));
    const starred = box(starredLi);
    if (myFiles) myFiles.icon = iconOf(first?.querySelector('a svg'));
    if (starred) starred.icon = iconOf(starredLi.querySelector('svg'));
    if (starred) starred.pad = getComputedStyle(starredLi).paddingInlineStart;
    if (starred) starred.spanLeft = iconOf(starredLi.querySelector('.ant-menu-item-icon'));
    if (myFiles) myFiles.spanLeft = iconOf(first?.querySelector('a'));
    const treeRows = [...scope.querySelectorAll('[data-testid="folder-tree"] [role="treeitem"] > .tree-row')].map((el) => ({ name: el.getAttribute('data-drop-name'), height: el.getBoundingClientRect().height }));
    const menuRows = menu.map((li) => ({ name: li.getAttribute('data-menu-id') ?? li.textContent.trim(), height: li.getBoundingClientRect().height }));
    return { myFiles, sub, starred, treeRows, menuRows, rtl: document.documentElement.dir === 'rtl' };
  })()`);

// pair checks My files, the opened folder and the Starred item against each other, in the place the sidebar is shown.
async function pair(page, check, label) {
  const m = await rows(page);
  const ready = !!(m && m.myFiles && m.starred && m.sub);
  check(ready, `${label}: My files, the opened folder and Starred are on the screen (${JSON.stringify(m && { my: !!m.myFiles, sub: !!m.sub, starred: !!m.starred })})`);
  if (!ready) return m;
  check(same(m.myFiles.height, m.starred.height), `${label}: My files and Starred are one height (${m.myFiles.height} and ${m.starred.height})`);
  check(same(m.sub.height, m.starred.height), `${label}: the opened folder under My files is the same height (${m.sub.height} and ${m.starred.height})`);
  check(same(m.sub.left, m.starred.left) && same(m.sub.right, m.starred.right), `${label}: the opened folder has the inline edges of Starred (${m.sub.left}-${m.sub.right} and ${m.starred.left}-${m.starred.right})`);
  // The opened folder sits one tree level in from My files: its icon is one level step (14 px, FolderTree's paddingInlineStart) away,
  // inward, which is to the right in English and to the left in Hebrew.
  const step = m.sub.icon - m.myFiles.icon;
  check(same(Math.abs(step), TREE_LEVEL_STEP) && (m.rtl ? step < 0 : step > 0), `${label}: the opened folder's icon is one tree level (${TREE_LEVEL_STEP} px) in from My files' icon (${step})`);
  check(same(m.myFiles.left, m.starred.left) && same(m.myFiles.right, m.starred.right), `${label}: My files has the inline edges of Starred (${m.myFiles.left}-${m.myFiles.right} and ${m.starred.left}-${m.starred.right})`);
  check(m.myFiles.radius === m.starred.radius && m.sub.radius === m.starred.radius, `${label}: the same corner radius (${m.myFiles.radius}, ${m.sub.radius} and ${m.starred.radius})`);
  console.log(`INFO ${label}: icons ${JSON.stringify({ my: m.myFiles.icon, starred: m.starred.icon, pad: m.starred.pad, spanMy: m.myFiles.spanLeft, spanStarred: m.starred.spanLeft, left: m.myFiles.left, starredLeft: m.starred.left })}`);
  check(same(m.myFiles.icon, m.starred.icon), `${label}: My files' icon is in Starred's icon column (${m.myFiles.icon} and ${m.starred.icon})`);
  return m;
}

// oneEdge reads the expanded sidebar's parts (decision 185): the brand's picture, the New button, the tree's first row, the Starred
// item and the storage block, with the sider's (or the drawer body's) box. Null when a part is not on the screen.
const oneEdge = (page) =>
  page.evaluate(`(() => {
    const scope = ${SCOPE};
    if (!scope) return null;
    const box = (el) => { if (!el) return null; const r = el.getBoundingClientRect(); return { l: r.left, r: r.right, t: r.top }; };
    const sider = document.querySelector('.shell-sider') ?? scope.querySelector('.ant-drawer-body') ?? scope;
    const menu = [...scope.querySelectorAll('.shell-menu li.ant-menu-item')];
    const card = scope.querySelector('[data-testid="storage-card"]');
    return {
      sider: box(sider),
      brand: box(scope.querySelector('[data-testid="sidebar-brand"] img')),
      new: box(scope.querySelector('[data-testid="new-button"]')),
      first: box(scope.querySelector('[data-testid="folder-tree"] [role="tree"] > [role="treeitem"] > .tree-row')),
      starred: box(menu.find((li) => (li.getAttribute('data-menu-id') ?? '').includes('starred'))),
      card: box(card),
      meter: box(card?.querySelector('[data-testid="sidebar-meter"]')),
      cardPadEnd: card ? parseFloat(getComputedStyle(card).paddingInlineEnd) : null,
      rtl: document.documentElement.dir === 'rtl',
    };
  })()`);

// edgeChecks says the parts take one edge (decision 185): every part starts where Starred starts, the picture, the rows and the block
// end where Starred ends, and the space above the picture is the space from the sider's start edge (S). The measured edges are logged.
function edgeChecks(m, check, label) {
  const ready = !!(m && m.sider && m.brand && m.new && m.first && m.starred && m.card);
  check(ready, `${label}: the brand, New, the tree, Starred and the storage block are on the screen (${JSON.stringify(m && { sider: !!m.sider, brand: !!m.brand, new: !!m.new, first: !!m.first, starred: !!m.starred, card: !!m.card })})`);
  if (!ready) return;
  const start = (b) => (m.rtl ? b.r : b.l);
  const end = (b) => (m.rtl ? b.l : b.r);
  const starts = { brand: start(m.brand), new: start(m.new), first: start(m.first), starred: start(m.starred), card: start(m.card) };
  check(Object.values(starts).every((x) => same(x, starts.starred)), `${label}: every part of the sidebar starts at one edge (${JSON.stringify(starts)})`);
  const ends = { brand: end(m.brand), first: end(m.first), starred: end(m.starred), card: end(m.card) };
  check(Object.values(ends).every((x) => same(x, ends.starred)), `${label}: the brand, the rows and the storage block end at one edge (${JSON.stringify(ends)})`);
  // Decision 185: the bar stops a menu row's inline padding (antd's token.padding) short of the block's end edge, so it does not run
  // into the block's end edge and its hover/active fill. The gap between the two end edges is that padding, in either reading direction.
  check(m.meter !== null && same(Math.abs(end(m.card) - end(m.meter)), m.cardPadEnd), `${label}: the bar ends the rows' inline padding short of the block's end edge (${JSON.stringify({ meter: m.meter, card: m.card, padEnd: m.cardPadEnd })})`);
  const topSpace = m.brand.t - m.sider.t;
  const sideSpace = Math.abs(start(m.brand) - start(m.sider));
  check(same(topSpace, sideSpace), `${label}: the space above the logo is the side space S (${topSpace} and ${sideSpace})`);
  console.log(`INFO ${label}: one edge ${JSON.stringify({ starts, ends, topSpace, sideSpace })}`);
}

// storageFit reads the storage block of the sidebar on the screen (decisions 181 and 185): its bar and its figures, its height, and whether
// the block scrolls sideways (a clipped row). Null when the block is not drawn.
const storageFit = (page) =>
  page.evaluate(`(() => {
    const scope = ${SCOPE};
    const side = scope?.querySelector('.shell-sider-body') ?? scope;
    const item = scope?.querySelector('[data-testid="storage-card"]');
    if (!side || !item) return null;
    const box = (el) => { if (!el) return null; const r = el.getBoundingClientRect(); return { left: r.left, right: r.right, width: r.width, height: r.height }; };
    const meter = item.querySelector('[data-testid="sidebar-meter"]');
    const figures = item.querySelector('.shell-storage-line');
    return { side: box(side), item: box(item), scroll: item.scrollWidth - item.clientWidth, meter: box(meter), figures: box(figures) };
  })()`);

// openMyFiles expands My files in the tree (its arrow) when it is folded, and waits for the folder under it.
async function openMyFiles(page) {
  const expanded = await page.evaluate(`${SCOPE}?.querySelector('[role="tree"] > [role="treeitem"]')?.getAttribute('aria-expanded') ?? null`);
  if (expanded !== 'true') {
    await page.evaluate(`${SCOPE}?.querySelector('[role="tree"] > [role="treeitem"] > .tree-row button')?.click(), true`);
  }
  await page.waitFor(`!!${SCOPE}?.querySelector('[data-drop-name="${SUB}"]')`, 15000);
}

async function sbBody(h, { page, base, accountKey, teamA, accountID, memberID }) {
  const { check, api, navigateTo } = h;
  const shotsDir = process.env.SELFCLOUD_E2E_SHOTS || null;
  if (shotsDir) await mkdir(shotsDir, { recursive: true });
  const snap = async (name) => {
    if (!shotsDir) return;
    await sleep(800);
    await shot(page, shotsDir, name);
  };
  const size = (w, hgt, mobile) => page.send('Emulation.setDeviceMetricsOverride', { width: w, height: hgt, deviceScaleFactor: 1, mobile });
  const themeWas = await page.evaluate(`document.documentElement.dataset.theme ?? 'light'`);
  // The member gets a limit of 10 GiB, so the sidebar draws the Storage bar (with no limit it draws the figures alone). The page
  // reads the storage at sign-in, so it is reloaded for the new limit.
  await api(base, 'PATCH', `/api/v1/accounts/${accountID}/users/${memberID}`, accountKey, { quota_bytes: 10 * 1024 ** 3 });
  await page.send('Page.reload', { ignoreCache: true });
  await page.waitFor(`!!document.querySelector('.shell-sider')`, 20000);

  // The fixture: a folder under My files, opened in the tree. The personal folder's id is in the address My files opens.
  await setPhone(page, false);
  await size(1280, 800, false);
  await navigateTo(page, `/files/${teamA}`);
  await page.waitFor(`!!document.querySelector('.shell-sider [role="tree"] > [role="treeitem"] > .tree-row a')`, 20000);
  await page.evaluate(`document.querySelector('.shell-sider [role="tree"] > [role="treeitem"] > .tree-row a').click(), true`);
  await page.waitFor(`/^\\/files\\/fld_/.test(location.pathname)`, 15000);
  const myID = await page.evaluate(`location.pathname.split('/')[2]`);
  await api(base, 'POST', `/api/v1/folders/${myID}/directories`, accountKey, { path: `/${SUB}`, parents: false }).catch(() => undefined);
  await navigateTo(page, `/files/${teamA}`);
  await page.waitFor(`!!document.querySelector('.shell-sider [role="tree"] > [role="treeitem"] > .tree-row')`, 20000);
  await openMyFiles(page);

  // ---- 1280 wide, light and dark, English; then Hebrew (right to left) in light.
  await setLanguage(page, 'English');
  await setTheme(page, 'Light');
  await page.waitFor(`document.documentElement.dataset.theme === 'light'`, 10000);
  await pair(page, check, 'English, light');
  edgeChecks(await oneEdge(page), check, 'English, light');
  await snap('sb-desktop-en-light.png');
  await setTheme(page, 'Dark');
  await page.waitFor(`document.documentElement.dataset.theme === 'dark'`, 10000);
  await pair(page, check, 'English, dark');
  await snap('sb-desktop-en-dark.png');
  await setTheme(page, 'Light');
  await page.waitFor(`document.documentElement.dataset.theme === 'light'`, 10000);
  await setLanguage(page, 'עברית');
  await page.waitFor(`document.documentElement.dir === 'rtl'`, 10000);
  await openMyFiles(page);
  await pair(page, check, 'Hebrew, light');
  edgeChecks(await oneEdge(page), check, 'Hebrew, light');
  await snap('sb-desktop-he-light.png');
  await setLanguage(page, 'English');
  await page.waitFor(`document.documentElement.dir === 'ltr'`, 10000);

  // ---- A finger: the drawer at 390 wide. Every sidebar row (the tree's and the menu's) is at least 44 pixels.
  await setPhone(page, true);
  await size(390, 800, true);
  await navigateTo(page, `/files/${teamA}`);
  await sleep(600);
  await page.evaluate(`document.querySelector('[data-testid="nav-open"]').click()`);
  await page.waitFor(`!!document.querySelector('.ant-drawer-open [data-testid="folder-tree"] [role="treeitem"]')`, 15000);
  await openMyFiles(page);
  // The drawer slides in: its rows are measured once it has stopped.
  await sleep(700);
  const phone = await pair(page, check, 'phone drawer');
  // The storage block on a finger: its bar and figures are inside the sidebar, the block is not clipped (no sideways scroll), and it
  // is a finger's target, at least 44 px tall (decision 181).
  const fit = await storageFit(page);
  check(fit !== null && fit.meter !== null && fit.figures !== null, `on a finger the storage block shows its bar and figures (${JSON.stringify(fit)})`);
  if (fit && fit.meter && fit.figures) {
    const inside = (b) => b.left >= fit.side.left - TOL && b.right <= fit.side.right + TOL;
    check(fit.scroll <= 0, `on a finger the storage block is not clipped sideways (scroll ${fit.scroll}px)`);
    check(inside(fit.meter) && inside(fit.figures), `on a finger the storage bar and figures are inside the sidebar (bar ${fit.meter.left}-${fit.meter.right}, figures ${fit.figures.left}-${fit.figures.right}, sidebar ${fit.side.left}-${fit.side.right})`);
    check(fit.item.height >= 44 - TOL, `on a finger the storage block is at least 44 px tall (${fit.item.height})`);
  }
  if (phone) {
    const short = [...phone.treeRows, ...phone.menuRows].filter((r) => r.height < 44 - TOL);
    check(phone.treeRows.length > 0 && phone.menuRows.length > 0 && short.length === 0, `on a finger every sidebar row is at least 44 px (${phone.treeRows.length} tree rows, ${phone.menuRows.length} menu items; short: ${JSON.stringify(short)})`);
  }
  await snap('sb-phone-en-drawer.png');

  // ---- Put the page back: the drawer closes, the window is the desktop's, the theme and the language are as they were.
  await page.evaluate(`document.activeElement?.blur(), true`);
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await page.waitFor(`!document.querySelector('.ant-drawer-open')`, 10000).catch(() => undefined);
  await setPhone(page, false);
  await size(1280, 800, false);
  const back = themeWas === 'dark' ? 'Dark' : 'Light';
  await setTheme(page, back);
  await page.waitFor(`document.documentElement.dataset.theme === ${q(back.toLowerCase())}`, 10000).catch(() => undefined);
}

// The member's limit is put back when the section ends, whatever happens in between: null follows the default (as d2 does).
export async function sbChecks(h, args) {
  const { api } = h;
  const { base, accountKey, accountID, memberID } = args;
  const before = await api(base, 'GET', `/api/v1/accounts/${accountID}/storage?users_limit=200`, accountKey);
  const original = before.users?.find((u) => u.id === memberID)?.quota_bytes ?? null;
  try {
    await sbBody(h, args);
  } finally {
    await api(base, 'PATCH', `/api/v1/accounts/${accountID}/users/${memberID}`, accountKey, { quota_bytes: original });
  }
}
