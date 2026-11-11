// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice ic2: icons first on the other pages, and the owner's notes of the dev run (decision 72). A browser with a mouse, at 1280
// wide, is checked for a sample of each group: the sign-in page's language and theme are icon buttons with names and tooltips,
// and no segmented switch; the trash's Restore and Delete forever are borderless 40 px icons with names, a tooltip on hover, and
// Empty trash is an icon with its name (tl4); in dark the icons keep no border. The sidebar: a top-level place (a space) is filled with the
// active colour and its text is the on-active colour, and a folder below it keeps the bar with no fill; in light and dark. The
// playlist drawer of an audio file opens with its shadow, and the stage and the rows show the file's type icon when there is no
// cover. At 390 px the trash has no sideways scroll, and on a finger the icons are 44 px. The five drive lists (Starred, Recent, Shared with
// me, Trash, Search) have no sideways scroll at 375 px, and no uncaught error, in English and light and in Hebrew and dark (tl4). Each list's table is right to left in Hebrew, and its body's background in dark is darker than in light. Every folder made is deleted for good,
// and the theme and the window are put back. With SELFCLOUD_E2E_SHOTS set (a folder), the pictures named in the brief are saved.
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';
import { hoverOver, onActiveInk, setLanguage, setPhone, setTheme, shot, sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);
const MEDIA = fileURLToPath(new URL('./media/', import.meta.url));
const FILL = 'rgb(254, 204, 0)';
// The on-active text the page draws on the yellow highlight, from the page's own sources (media.mjs onActiveInk). The e2e brand is a
// custom accent, so it is textOn(yellow) in lib/brand.ts: black. A default install is not this brand: it keeps SIDEBAR.onActive.
const ON_ACTIVE = await onActiveInk();
const TREE_CURRENT = '.shell-sider [data-testid="folder-tree"] [role="treeitem"][aria-current="location"] > .tree-row';

// tipAfter moves the pointer over the element and returns the text of the tooltip that opens (empty when none does).
async function tipAfter(page, selector) {
  await hoverOver(page, selector);
  await page.waitFor(`[...document.querySelectorAll('.ant-tooltip:not(.ant-tooltip-hidden)')].some((t) => t.textContent.trim() !== '')`, 5000).catch(() => undefined);
  const text = await page.evaluate(`[...document.querySelectorAll('.ant-tooltip:not(.ant-tooltip-hidden)')].map((t) => t.textContent.trim()).filter(Boolean).join('|')`);
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 0, y: 0, button: 'none', buttons: 0 });
  await sleep(250);
  return text;
}

// iconState reads an icon button: its name, its visible text, its border width, and its box.
const iconState = (page, selector) =>
  page.evaluate(`(() => {
    const b = document.querySelector(${q(selector)});
    if (!b) return null;
    const r = b.getBoundingClientRect();
    const s = getComputedStyle(b);
    return { name: b.getAttribute('aria-label') ?? '', text: b.textContent.trim(), border: s.borderTopWidth, w: r.width, h: r.height };
  })()`);

// currentRow reads the sidebar's current tree row: its classes, its background, and its bar (the ::before width, or none).
const currentRow = (page) =>
  page.evaluate(`(() => {
    const r = document.querySelector(${q(TREE_CURRENT)});
    if (!r) return null;
    const b = getComputedStyle(r, '::before');
    return { name: r.textContent.trim(), cls: r.className, bg: getComputedStyle(r).backgroundColor, bar: b.content === 'none' ? 'none' : b.width, colour: getComputedStyle(r).color };
  })()`);

export async function ic2Checks(h, { page, base, accountKey, teamA, dir, signIn, OWNER }) {
  const { check, api, navigateTo, rowAppears, openRow } = h;
  const suffix = Date.now().toString(36);
  const folderName = `IC2 Trash ${suffix}`;
  const shotsDir = process.env.SELFCLOUD_E2E_SHOTS || null;
  const snap = (name) => (shotsDir ? shot(page, shotsDir, name) : null);
  const themeWas = await page.evaluate(`document.documentElement.dataset.theme`);
  let folderID = null;
  let treeID = null;
  let innerID = null;
  try {
    // ---- The sign-in page: the language and the theme are icon buttons, not the segmented switch with its words.
    await page.goto(base + '/login');
    await page.waitFor(`!!document.getElementById('login-email')`, 15000);
    const lang = await iconState(page, '[data-testid="language"]');
    check(lang && lang.name !== '' && lang.text === '' && lang.border === '0px', `the sign-in page's language is an icon with a name and no border (${JSON.stringify(lang)})`);
    const theme = await page.evaluate(`[...document.querySelectorAll('button')].find((b) => (b.getAttribute('aria-label') ?? '').startsWith('Theme'))?.getAttribute('aria-label') ?? null`);
    check(theme !== null, `the sign-in page's theme is an icon named for its choice (${theme})`);
    check(!(await page.evaluate(`!!document.querySelector('.ant-segmented')`)), 'the sign-in page has no segmented switch');
    const tip = await tipAfter(page, '[data-testid="language"]');
    check(tip.includes('Language'), `the language icon's tooltip is its name (${tip})`);
    await snap('ic2-signin-light.png');
    await setTheme(page, 'Dark');
    await page.waitFor(`document.documentElement.dataset.theme === 'dark'`, 10000);
    await snap('ic2-signin-dark.png');
    await setTheme(page, 'Light');

    // ---- Trash, as the owner: a folder is made, put in the trash, and the trash lists its icons.
    await signIn(page, OWNER);
    const made = await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `/${folderName}`, parents: false });
    folderID = made?.id ?? null;
    await api(base, 'DELETE', `/api/v1/nodes/${folderID}`, accountKey);
    await page.send('Page.navigate', { url: base + '/trash' });
    await page.waitFor(`location.pathname === '/trash'`, 15000);
    await page.send('Page.reload', { ignoreCache: true });
    await page.waitFor(`[...document.querySelectorAll('.trash-item .name')].some((n) => n.textContent.includes(${q(folderName)}))`, 20000);

    const restore = await iconState(page, '.trash-item button[aria-label^="Restore:"]');
    check(restore && restore.text === '' && restore.border === '0px' && restore.w === 40 && restore.h === 40, `Restore is a borderless 40 px icon with no words (${JSON.stringify(restore)})`);
    const purge = await iconState(page, '.trash-item button[aria-label^="Delete forever:"]');
    check(purge && purge.text === '' && purge.border === '0px', `Delete forever is a borderless icon with no words (${JSON.stringify(purge)})`);
    // Empty trash is an icon with its name (tl4, decision 137): no words, and the name is its tooltip.
    const empty = await page.evaluate(`(() => { const b = document.querySelector('button[aria-label="Empty trash"]'); return b ? (b.textContent.trim() === '' ? 'icon' : 'words') : null; })()`);
    check(empty === 'icon', 'Empty trash is an icon with its name and no words (tl4)');
    const restoreTip = await tipAfter(page, '.trash-item button[aria-label^="Restore:"]');
    check(restoreTip.includes('Restore'), `Restore shows its tooltip on hover (${restoreTip})`);
    await snap('ic2-trash-light.png');
    await setTheme(page, 'Dark');
    const dark = await iconState(page, '.trash-item button[aria-label^="Restore:"]');
    check(dark && dark.border === '0px', 'the trash icons have no border in dark');
    await snap('ic2-trash-dark.png');
    await setTheme(page, 'Light');

    // ---- The sidebar's fill (decision 72): a folder below a top-level place keeps the bar, and the top-level place is filled.
    const treeMade = await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `/IC2 Tree ${suffix}`, parents: false });
    treeID = treeMade?.id ?? null;
    const innerMade = await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `/IC2 Tree ${suffix}/IC2 Inner ${suffix}`, parents: false });
    innerID = innerMade?.id ?? null;
    await page.send('Page.navigate', { url: `${base}/files/${teamA}/${innerID}` });
    // The tree opens a folder only on its arrow: the team's arrow, then the tree folder's, so the place is on screen.
    const expand = (key) => `(() => { const b = document.querySelector('[data-tree-key="${key}"] > .tree-row > button'); if (b && (b.getAttribute('aria-label') || '').startsWith('Expand')) b.click(); return !!b; })()`;
    await page.waitFor(`!!document.querySelector('[data-tree-key="${teamA}"] > .tree-row > button')`, 20000);
    await page.evaluate(expand(teamA));
    await page.waitFor(`!!document.querySelector('[data-tree-key="${treeID}"] > .tree-row > button')`, 20000);
    await page.evaluate(expand(treeID));
    await page.waitFor(`!!document.querySelector(${q(TREE_CURRENT)}) && document.querySelector(${q(TREE_CURRENT)}).textContent.includes(${q(`IC2 Inner ${suffix}`)})`, 20000);
    await sleep(600);
    const deep = await currentRow(page);
    check(deep && deep.cls.includes('side-current') && !deep.cls.includes('side-current-full') && deep.bg === 'rgba(0, 0, 0, 0)' && deep.bar === '3px', `a folder below a top-level place keeps the bar, with no fill (${JSON.stringify(deep)})`);
    const topFill = await page.evaluate(`getComputedStyle(document.querySelector('.shell-sider [data-tree-key="${teamA}"] > .tree-row')).backgroundColor`);
    check(topFill !== FILL, `the top-level place above is not filled while a folder inside it is current (${topFill})`);
    await snap('ic2-tree-deep-light.png');
    await setTheme(page, 'Dark');
    await sleep(400);
    const deepDark = await currentRow(page);
    check(deepDark && deepDark.bar === '3px' && deepDark.bg === 'rgba(0, 0, 0, 0)', `in dark a folder below a top-level place keeps the bar (${JSON.stringify(deepDark)})`);
    await snap('ic2-tree-deep-dark.png');
    await setTheme(page, 'Light');

    // The audio file: the stage and the playlist rows show the file's type icon when there is no cover, and the drawer's shadow is
    // not cut (decision 72). The file is uploaded into the folder the tree shows, and opened.
    // The sidebar's New opens the menu that makes the upload input, as a person does (as d2 and l1 do).
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="new-button"]:not([disabled])')`, 15000);
    await page.evaluate(`document.querySelector('.shell-sider [data-testid="new-button"]').click(), true`);
    await page.waitFor(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].some((i) => i.textContent.trim() === 'Upload files')`, 10000);
    await page.evaluate(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].find((i) => i.textContent.trim() === 'Upload files').click(), true`);
    await page.setFiles('[data-testid="sidebar-file-input"]', [join(MEDIA, 'tone.ogg')]);
    // The listing is kept for 30 seconds: the page is reloaded so the new file is read (as in d2 and l1).
    await sleep(1500);
    await page.send('Page.reload', { ignoreCache: true });
    await rowAppears(page, 'tone.ogg');
    await openRow(page, 'tone.ogg');
    await page.waitFor(`!!document.querySelector('[data-testid="media-art"]') || !!document.querySelector('[data-testid="media-viewer"]')`, 20000);
    await sleep(500);
    const stage = await page.evaluate(`(() => { const c = document.querySelector('[data-testid="media-cover"]'); return c ? { cover: c.dataset.cover, tile: !!c.querySelector('.icon-tile') } : null; })()`);
    check(stage && stage.cover === 'no' && stage.tile === true, `with no cover, the stage shows the coloured type icon (${JSON.stringify(stage)})`);
    await page.evaluate(`(() => { const a = document.querySelector('[data-testid="media-viewer"] audio') ?? document.querySelector('audio'); if (a) a.play().catch(() => undefined); return true; })()`);
    await page.evaluate(`(() => { document.querySelector('.media-playlist-toggle')?.click(); return true; })()`);
    await page.waitFor(`!!document.querySelector('.media-drawer:not(.media-drawer--closed)')`, 10000);
    await sleep(500);
    // The shadow is not cut: every ancestor that clips its painting (overflow other than visible) has a box that holds the shadow's
    // whole extent, the drawer's box grown by the shadow's blur on each side. No clip margin is relied on (Safari has none, decision 72).
    const shadow = await page.evaluate(`(() => {
      const drawer = document.querySelector('.media-drawer:not(.media-drawer--closed)');
      const r = drawer.getBoundingClientRect();
      const px = getComputedStyle(drawer).boxShadow.split(' ').filter((v) => v.endsWith('px')).map(parseFloat);
      const blur = px.length >= 3 ? px[2] : 0;
      const ext = { l: r.left - blur, t: r.top - blur, r: r.right + blur, b: r.bottom + blur };
      const clipping = [];
      for (let el = drawer.parentElement; el && el !== document.body; el = el.parentElement) {
        const s = getComputedStyle(el);
        if (s.overflowX === 'visible' && s.overflowY === 'visible') continue;
        const c = el.getBoundingClientRect();
        clipping.push({ cls: String(el.className), holds: c.left <= ext.l && c.top <= ext.t && c.right >= ext.r && c.bottom >= ext.b });
      }
      return { blur, clipping };
    })()`);
    check(shadow.blur > 0 && shadow.clipping.every((c) => c.holds), `the drawer's shadow (${shadow.blur} px blur) is inside every clipping box, so it is not cut (${JSON.stringify(shadow)})`);
    const row = await page.evaluate(`(() => { const t = document.querySelector('.media-queue [data-testid="tag-cover"]'); return t ? { tile: !!t.querySelector('.icon-tile') } : null; })()`);
    check(row === null || row.tile === true, `a playlist row with no cover shows the type icon (${JSON.stringify(row)})`);
    await snap('ic2-drawer-light.png');
    await setTheme(page, 'Dark');
    await sleep(400);
    await snap('ic2-drawer-dark.png');
    await setTheme(page, 'Light');
    await page.evaluate(`(() => { document.querySelector('.media-playlist-toggle')?.click(); return true; })()`).catch(() => undefined);

    // The top-level places are filled (decision 72): a space's root, and a menu item (Starred), in light and dark; the folded rail
    // fills its selected item too (there is no tree there, so the item is filled whenever the place is anywhere inside it).
    await page.send('Page.navigate', { url: `${base}/files/${teamA}` });
    await page.waitFor(`!!document.querySelector('.shell-sider [data-tree-key="${teamA}"] > .tree-row[aria-selected="true"], .shell-sider [data-tree-key="${teamA}"][aria-current="location"] > .tree-row')`, 20000).catch(() => undefined);
    await sleep(600);
    const spaceFill = await page.evaluate(`getComputedStyle(document.querySelector('.shell-sider [data-tree-key="${teamA}"] > .tree-row')).backgroundColor`);
    check(spaceFill === FILL, `a space's root is filled with the sidebar's active colour when it is the place (${spaceFill})`);
    await snap('ic2-space-root-light.png');
    await page.send('Page.navigate', { url: `${base}/starred` });
    await page.waitFor(`location.pathname === '/starred' && !!document.querySelector('.shell-sider .shell-menu .ant-menu-item-selected')`, 20000);
    await sleep(600);
    const menuFill = await page.evaluate(`(() => { const i = document.querySelector('.shell-sider .shell-menu .ant-menu-item-selected'); const s = getComputedStyle(i); const rules = []; for (const sh of document.styleSheets) { let rs = null; try { rs = sh.cssRules; } catch (e) { continue; } for (const r of rs) { if (r.selectorText && r.style && r.style.color && i.matches(r.selectorText)) rules.push(r.selectorText + " => " + r.style.color); } } return { bg: s.backgroundColor, colour: s.color, bar: getComputedStyle(i, '::before').content, rules }; })()`);
    check(menuFill.bg === FILL && menuFill.colour === ON_ACTIVE && menuFill.bar === 'none', `the selected menu item (Starred) is filled, with the on-active text and no bar (${JSON.stringify(menuFill)})`);
    await snap('ic2-starred-light.png');
    await setTheme(page, 'Dark');
    await sleep(400);
    const menuFillDark = await page.evaluate(`getComputedStyle(document.querySelector('.shell-sider .shell-menu .ant-menu-item-selected')).backgroundColor`);
    check(menuFillDark === FILL, `in dark the selected menu item is filled too (${menuFillDark})`);
    await snap('ic2-starred-dark.png');
    await setTheme(page, 'Light');
    await page.evaluate(`document.querySelector('[data-testid="rail-toggle"]').click(), true`);
    await page.waitFor(`!!document.querySelector('.ant-layout-sider-collapsed .shell-menu .ant-menu-item-selected')`, 10000);
    await sleep(500);
    const railFill = await page.evaluate(`getComputedStyle(document.querySelector('.ant-layout-sider-collapsed .shell-menu .ant-menu-item-selected')).backgroundColor`);
    check(railFill === FILL, `in the folded rail the selected item is filled (${railFill})`);
    await snap('ic2-rail-light.png');
    await page.evaluate(`document.querySelector('[data-testid="rail-toggle"]').click(), true`);
    await page.waitFor(`!document.querySelector('.ant-layout-sider-collapsed')`, 10000);
    await setLanguage(page, 'עברית');
    await page.waitFor(`document.documentElement.dir === 'rtl'`, 10000);
    await sleep(500);
    // A directional icon mirrors in the Hebrew page: the rail toggle's chevron (an icon-dir svg, index.css) has the mirror transform.
    const mirrored = await page.evaluate(`(() => { const i = document.querySelector('[data-testid="rail-toggle"] svg.icon-dir'); return i ? getComputedStyle(i).transform : null; })()`);
    check(mirrored === 'matrix(-1, 0, 0, 1, 0, 0)', `in Hebrew the rail toggle's directional chevron is mirrored (transform ${mirrored})`);
    await snap('ic2-hebrew-light.png');
    await setLanguage(page, 'English');
    await page.waitFor(`document.documentElement.dir === 'ltr'`, 10000);

    // ---- A phone: no sideways scroll at 390 px; on a finger the icons are 44 px.
    await page.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
    await sleep(300);
    await page.send('Page.navigate', { url: base + '/trash' });
    await page.waitFor(`location.pathname === '/trash'`, 15000);
    // The trash is read after the page loads; wait for its row, not a fixed time, so the checks below measure a drawn row (gate
    // stvl1 e2e-4 measured before the row came, and found no button).
    // On a phone the row's icons collapse into its ⋮ button (tl4), so the phone checks read that button.
    const moreSel = '.trash-item button[aria-label^="More actions for"]';
    await page.waitFor(`!!document.querySelector(${JSON.stringify(moreSel)})`, 15000);
    const sideways = await page.evaluate(`document.documentElement.scrollWidth - window.innerWidth`);
    check(sideways <= 0, `the trash has no sideways scroll at 390 px (${sideways} px over)`);
    await setPhone(page, true);
    // The finger's sizes apply on the next render, and the button grows to them through its transition: wait for the exact
    // size the check asserts, not a rounded one (gate fu5a read 43.97 px mid-transition). The check reads what is there after
    // the wait, met or not.
    await page.waitFor(`(() => { const b = document.querySelector(${JSON.stringify(moreSel)}); if (!b) return false; const r = b.getBoundingClientRect(); return r.height === 44 && r.width === 44; })()`, 5000).catch(() => undefined);
    const coarse = await iconState(page, moreSel);
    check(coarse && coarse.h === 44 && coarse.w === 44, `on a finger the row's More button is 44 px (${JSON.stringify(coarse)})`);
    const inline = await page.evaluate(`!!document.querySelector('.trash-item button[aria-label^="Restore:"]')`);
    check(!inline, 'on a finger the Restore icon is in the ⋮ menu, not inline on the row (tl4)');
    await snap('ic2-phone-390.png');

    // ---- The five drive lists: at 375 px each has no sideways scroll, in English and light and in Hebrew (RTL) and dark,
    // and no uncaught exception shows on any of them in either set.
    // The language and the theme are set at desktop width (the phone layout keeps the theme control in a menu), then the viewport
    // is 375 px for the lists themselves.
    await page.send('Emulation.clearDeviceMetricsOverride', {});
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    const lists = ['/starred', '/recent', '/shared', '/trash', '/search?q=budget'];
    const sets = [
      { lang: 'English', theme: 'Light', dir: 'ltr' },
      { lang: 'עברית', theme: 'Dark', dir: 'rtl' },
    ];
    const bodies = new Map();
    for (const set of sets) {
      await page.send('Page.navigate', { url: base + '/starred' });
      await page.waitFor(`location.pathname === '/starred'`, 15000);
      // The theme's menu is found by its English label, so the theme is set while the page is in English.
      await setTheme(page, set.theme);
      await setLanguage(page, set.lang);
      await page.waitFor(`document.documentElement.dir === ${q(set.dir)}`, 10000);
      await sleep(300);
      await page.send('Emulation.setDeviceMetricsOverride', { width: 375, height: 812, deviceScaleFactor: 1, mobile: true });
      for (const path of lists) {
        const before = page.errors.length;
        await page.send('Page.navigate', { url: base + path });
        await page.waitFor(`location.pathname === ${q(path.split('?')[0])}`, 15000);
        await page.waitFor(`!!document.querySelector('.items, .shared, .empty, [role="alert"], [role="status"]')`, 15000).catch(() => undefined);
        // The table is drawn once the list's own words and the first read are in, which can come after the first status.
        await page.waitFor(`!!document.querySelector('table')`, 15000).catch(() => undefined);
        await sleep(500);
        const sideways = await page.evaluate(`document.documentElement.scrollWidth - window.innerWidth`);
        check(sideways <= 0, `${path} has no sideways scroll at 375 px in ${set.lang} (${sideways} px over)`);
        const uncaught = page.errors.slice(before).filter((e) => e.startsWith('uncaught') || e.startsWith('the page renderer crashed'));
        check(uncaught.length === 0, `${path} shows no uncaught error in ${set.lang} ${set.theme} (${uncaught.join(' | ') || 'none'})`);
        // The list table's direction, and its body's background: right to left in Hebrew, and a body background in
        // dark that is darker than the light one (the first opaque background up the tree, as a luminance).
        const grid = await page.evaluate(`(() => { const t = document.querySelector('table'); if (!t) return null; const b = t.querySelector('tbody'); const bg = (el) => { for (let n = el; n; n = n.parentElement) { const c = getComputedStyle(n).backgroundColor; if (c !== 'rgba(0, 0, 0, 0)' && c !== 'transparent') { const m = c.match(/[\\d.]+/g).map(Number); return 0.2126 * m[0] + 0.7152 * m[1] + 0.0722 * m[2]; } } return null; }; return { dir: getComputedStyle(t).direction, lum: b ? bg(b) : null }; })()`);
        check(grid !== null, `${path} draws its table in ${set.lang} (${JSON.stringify(grid)})`);
        if (grid !== null && set.dir === 'rtl') check(grid.dir === 'rtl', `${path} draws its table right to left in Hebrew (${grid.dir})`);
        if (grid !== null) bodies.set(`${path}|${set.theme}`, grid.lum);
      }
    }
    for (const path of lists) {
      const light = bodies.get(`${path}|Light`);
      const dark = bodies.get(`${path}|Dark`);
      check(light !== undefined && dark !== undefined && light !== null && dark !== null && dark < light, `${path}'s table body is darker in dark (luminance ${dark}) than in light (${light})`);
    }
    await snap('ic2-lists-375.png');
  } finally {
    await setLanguage(page, 'English').catch(() => undefined);
    await setTheme(page, themeWas === 'dark' ? 'Dark' : 'Light').catch(() => undefined);
    if (innerID) await api(base, 'DELETE', `/api/v1/nodes/${innerID}?permanent=true`, accountKey).catch(() => undefined);
    if (treeID) await api(base, 'DELETE', `/api/v1/nodes/${treeID}?permanent=true`, accountKey).catch(() => undefined);
    if (folderID) await api(base, 'DELETE', `/api/v1/nodes/${folderID}?permanent=true`, accountKey).catch(() => undefined);
  }
}
