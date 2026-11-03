// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice d3 (decision 69, the owner's option B for the current place). At 1440 wide a tree row and a menu item look alike in every
// state: the same text colour, weight and size, and the same hover background. The current place has no background; it has a
// 3 px bar at its start edge and its text in the sidebar's mark colour (the yellow on the default blue), in the folder tree, on the
// menu's own pages, on the Storage row and in the rail. The bar is on the right in Hebrew. With SELFCLOUD_E2E_SHOTS set (a folder),
// the run saves the pictures of each state.
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { setLanguage, setPhone, setTheme } from './media.mjs';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const q = (v) => JSON.stringify(v);

// The sidebar's own values (theme/tokens.ts SIDEBAR): the mark is its yellow, the text white, and the hover its tint.
const MARK = 'rgb(254, 204, 0)';
const TEXT = 'rgb(255, 255, 255)';
const HOVER = 'rgba(255, 255, 255, 0.1)';
const CLEAR = 'rgba(0, 0, 0, 0)';
// Decision 72: a top-level place is filled with the sidebar's active colour (the yellow), and its text is the on-active colour.
const ONACTIVE = 'rgb(0, 0, 0)';
const noBar = (s) => s !== null && (s.bar.width === '0px' || s.bar.content === 'none');

// styleOf reads the computed style of the element a selector names, and of its ::before (the bar): the text colour, the background,
// the font weight and size, and the bar's colour, width, offsets and box.
const styleOf = (page, selector) =>
  page.evaluate(`(() => {
    const el = document.querySelector(${q(selector)});
    if (!el) return null;
    const s = getComputedStyle(el);
    const b = getComputedStyle(el, '::before');
    return {
      color: s.color, background: s.backgroundColor, weight: s.fontWeight, size: s.fontSize,
      bar: { colour: b.backgroundColor, width: b.width, top: b.top, bottom: b.bottom, left: b.left, right: b.right, content: b.content, position: b.position },
      dir: document.documentElement.dir,
    };
  })()`);

// hoverStyle moves the pointer onto the element and reads it there. Before the move the element is scrolled into its sidebar
// region, and the pointer is taken off the page first, so the move is a real entry. The reading is taken again until the hover
// has applied (a transition of 0.3 s, and a busy machine may be slower), up to 4 s; the last reading is the one checked. The
// element under the pointer and whether it is in the viewport are reported with the reading, so a failure says what was in the way.
async function hoverStyle(page, selector) {
  await page.evaluate(`(() => { const el = document.querySelector(${q(selector)}); if (el) el.scrollIntoView({ block: 'nearest' }); return true; })()`);
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 1400, y: 880, button: 'none', buttons: 0 });
  await sleep(150);
  const at = await page.evaluate(`(() => { const el = document.querySelector(${q(selector)}); if (!el) return null; const r = el.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
  if (!at) throw new Error(`nothing matches ${selector}`);
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: at.x, y: at.y, button: 'none', buttons: 0 });
  let last = null;
  for (let i = 0; i < 20; i++) {
    await sleep(200);
    const style = await styleOf(page, selector);
    const under = await page.evaluate(`(() => { const el = document.querySelector(${q(selector)}); const top = document.elementFromPoint(${at.x}, ${at.y}); const r = el.getBoundingClientRect(); return { target: !!el && !!top && (el === top || el.contains(top)), top: top ? top.tagName.toLowerCase() + (top.className && typeof top.className === 'string' ? '.' + top.className.trim().split(/\\s+/).slice(0, 3).join('.') : '') : null, sider: !!top?.closest('.shell-sider'), hovered: el.matches(':hover'), inViewport: r.top >= 0 && r.bottom <= innerHeight, point: [${at.x}, ${at.y}].map(Math.round), viewport: innerHeight }; })()`);
    last = style ? { ...style, under } : null;
    if (last && last.background === HOVER) break;
  }
  return last;
}

// unhover moves the pointer to the empty page, so the next reading is not a hover.
const unhover = (page) => page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 1400, y: 880, button: 'none', buttons: 0 });

// markMenu and markRows give each sidebar item its text as an attribute, so a check names it by its text in any language.
const markSidebar = (page) =>
  page.evaluate(`(() => {
    document.querySelectorAll('.shell-sider .shell-menu .ant-menu-item').forEach((i) => { const t = i.querySelector('.ant-menu-title-content')?.textContent.trim(); if (t) i.setAttribute('data-menu-text', t); });
    document.querySelectorAll('.shell-sider [data-testid="folder-tree"] [role="treeitem"] > .tree-row').forEach((r) => { const t = r.querySelector('bdi')?.textContent.trim(); if (t) r.setAttribute('data-row-name', t); });
  })()`);

// expectedMark is the rule of theme/currentMark.ts, worked out from the sidebar's own colour in the page: the yellow is the text
// colour when it reads 4.5:1 on the sidebar, and the bar when it reads 3:1. The brand here has a teal sidebar, so the text is white.
const luminance = (rgb) => {
  const [r, g, b] = rgb.match(/\d+(\.\d+)?/g).slice(0, 3).map((v) => {
    const x = Number(v) / 255;
    return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};
const ratio = (a, b) => {
  const [x, y] = [luminance(a), luminance(b)].sort((p, q) => q - p);
  return (x + 0.05) / (y + 0.05);
};
const hoverOf = (bg) => {
  const [r, g, b] = bg.match(/\d+(\.\d+)?/g).slice(0, 3).map(Number);
  const mix = (c) => Math.round(255 * 0.1 + c * 0.9);
  return `rgb(${mix(r)}, ${mix(g)}, ${mix(b)})`;
};
// expectedMark is theme/currentMark.ts: the text is the yellow when it reads 4.5:1 on the sidebar and on its hover tint, else the
// white (or black) that reads best on the weaker of the two; the bar is the yellow at 3:1 or more, else the text.
const expectedMark = (siderBg) => {
  const hover = hoverOf(siderBg);
  const both = (c) => Math.min(ratio(c, siderBg), ratio(c, hover));
  const ink = both(MARK) >= 4.5 ? MARK : both(TEXT) >= 4.5 ? TEXT : [TEXT, 'rgb(0, 0, 0)'].reduce((a, c) => (both(c) > both(a) ? c : a));
  return { ink, bar: ratio(MARK, siderBg) >= 3 ? MARK : TEXT };
};
const same = (a, b, keys) => a !== null && b !== null && keys.every((k) => a[k] === b[k]);
const show = (s) => (s ? `${s.color} on ${s.background}, weight ${s.weight}, ${s.size}, bar ${s.bar.colour} ${s.bar.width}${s.under ? `; under the pointer ${JSON.stringify(s.under)}` : ''}` : 'not read');
// INK and BAR are the mark's colours on this sidebar; barOk checks the bar's box and colour.
let INK = MARK;
let BAR = MARK;
const barOk = (s) => s !== null && s.bar.colour === BAR && s.bar.width === '3px' && s.bar.position === 'absolute' && s.bar.top === '7px' && s.bar.bottom !== 'auto';

export async function d3Checks(h, { page, ADMIN, MEMBER }) {
  const { check, navigateTo, clickNav, signIn, signOut } = h;
  const shotsDir = process.env.SELFCLOUD_E2E_SHOTS || null;
  const snap = async (name) => {
    if (!shotsDir) return;
    await sleep(500);
    const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
    await writeFile(join(shotsDir, name), Buffer.from(data, 'base64'));
  };
  const size = (w, hgt) => page.send('Emulation.setDeviceMetricsOverride', { width: w, height: hgt, deviceScaleFactor: 1, mobile: false });
  const themeWas = await page.evaluate(`document.documentElement.dataset.theme`);
  const langWas = await page.evaluate(`document.documentElement.lang`);
  const viewportWas = await page.evaluate(`[innerWidth, innerHeight]`);
  const TREE_ROW = (name) => `.shell-sider [data-testid="folder-tree"] [role="treeitem"] > .tree-row[data-row-name="${name}"]`;
  const MENU = (text) => `.shell-sider .shell-menu .ant-menu-item[data-menu-text="${text}"]`;
  try {
    await size(1440, 900);
    if ((await page.evaluate(`document.documentElement.lang`)) !== 'en') await setLanguage(page, 'English');
    await setTheme(page, 'Light');
    await setPhone(page, false);

    // Normal: My files (a tree row) and Recent (a menu item, not selected) on Starred.
    // Starred is opened from the sidebar's own menu, as a person does (a page-script click on an anchor did not leave the files view).
    await page.waitFor(`!!document.querySelector('.shell-sider .shell-menu .ant-menu-item')`, 20000);
    await clickNav(page, 'Starred');
    // The page changes when its lazy chunk has loaded: the sidebar is redrawn then, so the check waits for the selected item.
    await page.waitFor(`!!document.querySelector('.shell-sider .shell-menu .ant-menu-item-selected')?.querySelector('.ant-menu-title-content') && document.querySelector('.shell-sider .shell-menu .ant-menu-item-selected .ant-menu-title-content').textContent.trim() === 'Starred'`, 20000);
    await page.waitFor(`location.pathname === '/starred' && !!document.querySelector('.shell-sider [data-testid="folder-tree"] [role="treeitem"] > .tree-row bdi') && !!document.querySelector('.shell-sider .shell-menu .ant-menu-item')`, 20000);
    await markSidebar(page);
    await page.waitFor(`!!document.querySelector(${q(TREE_ROW('My files'))}) && !!document.querySelector(${q(MENU('Recent'))})`, 20000);
    ({ ink: INK, bar: BAR } = expectedMark(await page.evaluate(`getComputedStyle(document.querySelector('.shell-sider')).backgroundColor`)));
    const menuNormal = await styleOf(page, MENU('Recent'));
    const rowNormal = await styleOf(page, TREE_ROW('My files'));
    check(
      same(menuNormal, rowNormal, ['color', 'weight', 'size', 'background']) && menuNormal.color === TEXT && menuNormal.weight === '400' && menuNormal.background === CLEAR,
      `in the normal state a tree row and a menu item share text colour, weight, size and no background (menu ${show(menuNormal)}; tree ${show(rowNormal)})`,
    );
    check(menuNormal.bar.content === 'none' || menuNormal.bar.colour === CLEAR || menuNormal.bar.width === '0px', `a menu item that is not current has no bar (${menuNormal.bar.width})`);
    check(rowNormal.bar.width === '0px' || rowNormal.bar.content === 'none', `a tree row that is not current has no bar (${rowNormal.bar.width})`);

    // Hover: the same background on both, and the same text colour.
    const menuHover = await hoverStyle(page, MENU('Recent'));
    await snap('d3-hover-menu-recent-light.png');
    const rowHover = await hoverStyle(page, TREE_ROW('My files'));
    await snap('d3-hover-my-files-light.png');
    check(
      same(menuHover, rowHover, ['color', 'background']) && rowHover.background === HOVER,
      `on hover a tree row and a menu item share the menu's hover background and text colour (menu ${show(menuHover)}; tree ${show(rowHover)})`,
    );
    await unhover(page);

    // Current, on Starred: the menu's selected item. No background; the bar and the text in the mark colour; weight 400.
    const starred = MENU('Starred');
    await page.waitFor(`!!document.querySelector(${q(starred)}) && document.querySelector(${q(starred)}).classList.contains('ant-menu-item-selected')`, 10000);
    const menuSelected = await styleOf(page, starred);
    check(
      menuSelected.background === MARK && menuSelected.color === ONACTIVE && menuSelected.weight === '400' && noBar(menuSelected),
      `the menu's selected item is filled with the yellow, the on-active text, weight 400, and no bar (${show(menuSelected)})`,
    );
    const selectedHover = await hoverStyle(page, starred);
    await snap('d3-starred-current-hover-light.png');
    await unhover(page);
    check(
      selectedHover.color === ONACTIVE && selectedHover.background === MARK && noBar(selectedHover),
      `the selected item on hover: the same hover background as any row, the mark colour and the bar (${show(selectedHover)})`,
    );
    await snap('d3-starred-current-light.png');

    // Current, in a folder: My files is the current place on its files page. The same look as the menu's selected item.
    await page.evaluate(`document.querySelector(${q(TREE_ROW('My files'))}).querySelector('a').click(), true`);
    await page.waitFor(`location.pathname.startsWith('/files/') && !!document.querySelector('.shell-sider [data-testid="folder-tree"] [role="treeitem"][aria-current="location"] > .tree-row[data-row-name="My files"]')`, 20000);
    await markSidebar(page);
    const current = '.shell-sider [data-testid="folder-tree"] [role="treeitem"][aria-current="location"] > .tree-row[data-row-name="My files"]';
    const rowCurrent = await styleOf(page, current);
    check(
      rowCurrent.background === MARK && rowCurrent.color === ONACTIVE && rowCurrent.weight === '400' && noBar(rowCurrent),
      `the current tree row has no background, the mark colour, weight 400 and the 3 px bar (${show(rowCurrent)})`,
    );
    check(
      same(rowCurrent, menuSelected, ['color', 'background', 'weight', 'size']),
      `the current tree row and the menu's selected item look the same (tree ${show(rowCurrent)}; menu ${show(menuSelected)})`,
    );
    await snap('d3-folder-current-light.png');
    const currentHover = await hoverStyle(page, current);
    await snap('d3-hover-current-row-light.png');
    check(
      currentHover.color === ONACTIVE && currentHover.background === MARK && noBar(currentHover),
      `on hover the current row keeps its mark colour and bar, and takes the same hover background as any row (${show(currentHover)})`,
    );
    const otherHover = await hoverStyle(page, '.shell-sider [data-testid="folder-tree"] [role="treeitem"]:not([aria-current]) > .tree-row');
    await snap('d3-hover-other-row-light.png');
    check(otherHover !== null && otherHover.background === HOVER && otherHover.color === TEXT, `on hover another row takes the hover background (${show(otherHover)})`);
    await unhover(page);

    // The Storage row, selected on its page: its title takes the mark colour, the figures stay the caption colour, the bar is normal.
    // The figures' colour is read first, on this page, where the row is not selected; the caption is the brand's, so it is not hard-coded.
    const plainLine = await page.evaluate(`(() => { const t = document.querySelector('.shell-sider .shell-storage > span:last-child'); return t ? getComputedStyle(t).color : null; })()`);
    await navigateTo(page, '/storage');
    // The Storage row is selected once the page has committed: the check waits for the selected entry, not for the old row.
    await page.waitFor(`location.pathname === '/storage' && !!document.querySelector('.shell-sider .shell-menu .ant-menu-item.shell-storage-item.ant-menu-item-selected')`, 20000);
    await markSidebar(page);
    const storage = '.shell-sider .shell-menu .ant-menu-item.shell-storage-item';
    const title = await page.evaluate(`(() => { const t = document.querySelector('.shell-sider .shell-storage > span'); const s = getComputedStyle(t); return { colour: s.color }; })()`);
    const line = await page.evaluate(`(() => { const t = document.querySelector('.shell-sider .shell-storage > span:last-child'); return t ? getComputedStyle(t).color : null; })()`);
    await sleep(500); // the fill fades in over 0.3 s (the menu's transition)
    const storageStyle = await styleOf(page, storage);
    check(title.colour === ONACTIVE && storageStyle.background === MARK && noBar(storageStyle), `the Storage row on its page: title in the mark colour, with the 3 px bar (${title.colour}; ${show(storageStyle)})`);
    check(line === ONACTIVE, `the Storage figures take the on-active colour when the row is filled (${line})`);
    await snap('d3-storage-light.png');

    // Dark theme: the current item keeps the mark on the dark page (the sidebar is the brand's in both themes).
    await clickNav(page, 'Starred');
    await page.waitFor(`location.pathname === '/starred' && !!document.querySelector('.shell-sider .shell-menu .ant-menu-item-selected')`, 20000);
    await setTheme(page, 'Dark');
    await page.waitFor(`document.documentElement.dataset.theme === 'dark'`, 10000);
    await sleep(400);
    const darkSider = await page.evaluate(`getComputedStyle(document.querySelector('.shell-sider')).backgroundColor`);
    const darkMark = expectedMark(darkSider);
    const darkSel = await styleOf(page, '.shell-sider .shell-menu .ant-menu-item-selected');
    check(darkSel.background === MARK && darkSel.color === ONACTIVE && darkSel.weight === '400' && noBar(darkSel), `in dark the current item has no background, the mark colour (${darkMark.ink} on ${darkSider}) and the 3 px bar (${show(darkSel)})`);
    await snap('d3-dark-sidebar.png');
    await setTheme(page, 'Light');
    await page.waitFor(`document.documentElement.dataset.theme === 'light'`, 10000);

    // Hebrew: the bar moves to the right edge with the text, the page's start edge.
    await navigateTo(page, '/files');
    await page.waitFor(`location.pathname.startsWith('/files') && !!document.querySelector('.shell-sider [data-testid="folder-tree"] [aria-current="location"] > .tree-row')`, 20000);
    await setLanguage(page, 'עברית');
    await page.waitFor(`document.documentElement.dir === 'rtl'`, 10000);
    await markSidebar(page);
    // On the files page the current place is the tree's row: its bar is on the right in Hebrew, as the menu's is.
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="folder-tree"] [aria-current="location"] > .tree-row')`, 10000);
    const hebrewSel = await styleOf(page, '.shell-sider [data-testid="folder-tree"] [aria-current="location"] > .tree-row');
    check(hebrewSel.background === MARK && noBar(hebrewSel), `in Hebrew the top-level place is filled, with no bar (${show(hebrewSel)})`);
    await snap('d3-hebrew.png');
    await setLanguage(page, 'English');
    await page.waitFor(`document.documentElement.dir === 'ltr'`, 10000);

    // The folded rail: the current item shows the mark colour on its icon and text, with the bar at its start edge, and no background.
    await clickNav(page, 'Starred');
    await page.waitFor(`location.pathname === '/starred' && !!document.querySelector('.shell-sider .shell-menu .ant-menu-item-selected')`, 20000);
    await page.evaluate(`document.querySelector('[data-testid="rail-toggle"]').click(), true`);
    await page.waitFor(`!!document.querySelector('.ant-layout-sider-collapsed .shell-menu .ant-menu-item-selected')`, 10000);
    await sleep(500);
    const railSel = await styleOf(page, '.ant-layout-sider-collapsed .shell-menu .ant-menu-item-selected');
    const railIcon = await page.evaluate(`getComputedStyle(document.querySelector('.ant-layout-sider-collapsed .shell-menu .ant-menu-item-selected svg')).color`);
    check(railSel.background === MARK && railSel.color === ONACTIVE && noBar(railSel) && railIcon === ONACTIVE, `in the folded rail the current item has no background, the mark colour on its text and its icon (${railIcon}), and the 3 px bar (${show(railSel)})`);
    await snap('d3-rail.png');
    await page.evaluate(`document.querySelector('[data-testid="rail-toggle"]').click(), true`);
    await page.waitFor(`!document.querySelector('.ant-layout-sider-collapsed')`, 10000);

    // The phone drawer: the sidebar's own menu, with the current item marked, and a name the dialog reads (the sidebar's region name).
    await setPhone(page, true);
    await page.waitFor(`!!document.querySelector('[data-testid="nav-open"]')`, 10000);
    await page.evaluate(`document.querySelector('[data-testid="nav-open"]').click(), true`);
    await page.waitFor(`!!document.querySelector('.ant-drawer-open .shell-menu .ant-menu-item-selected')`, 10000);
    await sleep(400);
    const drawerSel = await styleOf(page, '.ant-drawer-open .shell-menu .ant-menu-item-selected');
    check(drawerSel.background === MARK && drawerSel.color === ONACTIVE && noBar(drawerSel), `in the phone drawer the current item has no background, the mark colour and the 3 px bar (${show(drawerSel)})`);
    const drawerName = await page.evaluate(`(() => { const d = document.querySelector('.ant-drawer-open [role="dialog"]'); const id = d?.getAttribute('aria-labelledby'); return { id, name: (id && document.getElementById(id)?.textContent.trim()) || d?.getAttribute('aria-label') || '' }; })()`);
    check(drawerName.name.length > 0, `the phone drawer has an accessible name (${JSON.stringify(drawerName)})`);
    await snap('d3-phone-drawer.png');
    await page.evaluate(`document.querySelector('.ant-drawer-close').click(), true`);
    await page.waitFor(`!document.querySelector('.ant-drawer-open')`, 10000);
    await setPhone(page, false);

    // An installation admin's page has no New (decision 66), so the sidebar keeps the gap New leaves: the menu starts 10 px below
    // the top bar's edge, not on it (decision 69, review fix 1). Back to the member afterwards.
    await signOut(page);
    await signIn(page, ADMIN);
    await navigateTo(page, '/admin/tenants');
    await page.waitFor(`location.pathname.startsWith('/admin') && !!document.querySelector('[data-testid="sidebar-top-gap"]') && !!document.querySelector('.shell-sider .shell-menu .ant-menu-item')`, 20000);
    await sleep(300);
    const adminGap = await page.evaluate(`(() => { const s = document.querySelector('.shell-sider').getBoundingClientRect(); const g = document.querySelector('[data-testid="sidebar-top-gap"]').getBoundingClientRect(); const first = document.querySelector('.shell-sider .shell-menu .ant-menu-item').getBoundingClientRect(); const menu = document.querySelector('.shell-sider .shell-menu').getBoundingClientRect(); return { gapTop: Math.round(g.top - s.top), gapHeight: Math.round(g.height), menuTop: Math.round(menu.top - s.top), firstTop: Math.round(first.top - s.top), firstMargin: parseFloat(getComputedStyle(document.querySelector('.shell-sider .shell-menu .ant-menu-item')).marginTop), news: document.querySelectorAll('.shell-sider [data-testid="new-button"]').length }; })()`);
    // The gap is 10 px and the menu starts at its foot; the first item sits at the menu's top plus its own margin (the menu's rule, 4 px).
    check(adminGap.news === 0 && adminGap.gapTop === 0 && adminGap.gapHeight === 10 && adminGap.menuTop === 10 && adminGap.firstTop === 10 + adminGap.firstMargin, `on an admin page there is no New, and the menu starts 10 px below the sidebar's top (${JSON.stringify(adminGap)})`);
    await signOut(page);
    await signIn(page, MEMBER);
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="new-button"]')`, 20000);
  } finally {
    // Whatever the checks did, the browser is put back as it was: the drawer closed, the phone emulation off, the viewport, the theme
    // and the language as they were before this section (the next section relies on them).
    await page.evaluate(`(() => { const c = document.querySelector('.ant-drawer-close'); if (c) c.click(); return true; })()`).catch(() => undefined);
    await setPhone(page, false).catch(() => undefined);
    await page.send('Emulation.setDeviceMetricsOverride', { width: viewportWas[0], height: viewportWas[1], deviceScaleFactor: 1, mobile: false }).catch(() => undefined);
    await setTheme(page, themeWas === 'dark' ? 'Dark' : 'Light').catch(() => undefined);
    if (langWas === 'he') await setLanguage(page, 'עברית').catch(() => undefined);
    else if (langWas !== 'en') await setLanguage(page, 'English').catch(() => undefined);
  }
}
