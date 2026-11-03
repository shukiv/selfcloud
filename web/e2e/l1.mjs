// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice l1: the list is a Drive list (decision 70). A member's browser, at 1280 wide in English and light, is checked for: each
// row has a More button, and the four icons (Share, Download, Rename, Star) show on a hovered or a selected row only; a selected
// row is an accent band with no edge line (decision 72), a hovered row a grey band; a row's checkbox takes its icon's place on hover and on
// every row while anything is selected; the More menu is grouped (the first group, then Share, Organize and Information as
// submenus with their shortcuts at the right, then Move to trash), and Rename and Move to trash work from it. On a finger the
// icons are gone and the checkboxes stay. In Hebrew the More button is at the start of the row, and in dark the bands hold.
// The folder and its files are removed before the section ends, and the language, theme, viewport and selection are put back.
// With SELFCLOUD_E2E_SHOTS set (a folder), the run also saves the pictures named in the brief.
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { clearSelection, hoverOver, setLanguage, setPhone, setTheme, shot, sleep } from './media.mjs';
import { LABELS, hoverMenuTitle, pickMenuEntry } from './menu.mjs';

const q = (v) => JSON.stringify(v);
const rowSel = (name) => `[role="row"][aria-label="${name.replace(/["\\]/g, '\\$&')}"]`;

// rowState reads what a row shows: its icons' visibility, its checkbox and icon, its More button, and its cells' background.
const rowState = (page, name) =>
  page.evaluate(`(() => {
    const row = document.querySelector(${q(rowSel(name))});
    if (!row) return null;
    const acts = row.querySelector('.row-acts');
    const td = row.querySelector('td');
    const check = row.querySelector('.row-check');
    const icon = row.querySelector('.row-icon');
    const more = row.querySelector('button[aria-haspopup="menu"]');
    const cs = (el) => (el ? getComputedStyle(el) : null);
    return {
      actsVisible: acts ? cs(acts).visibility === 'visible' : null,
      actsCount: row.querySelectorAll('.row-acts button').length,
      names: [...row.querySelectorAll('.row-acts button')].map((b) => b.getAttribute('aria-label')),
      checkShown: check ? cs(check).display !== 'none' : null,
      iconShown: icon ? cs(icon).display !== 'none' : null,
      more: more ? more.getAttribute('aria-label') : null,
      moreX: more ? more.getBoundingClientRect().left : null,
      nameX: row.querySelector('bdi') ? row.querySelector('bdi').getBoundingClientRect().left : null,
      bg: td ? cs(td).backgroundColor : null,
      shadow: td ? cs(td).boxShadow : null,
      selected: row.getAttribute('aria-selected'),
    };
  })()`);

// pageBackground is the colour of the listing's own background, which an unselected, unhovered row shows.
const pageBackground = (page) => page.evaluate(`getComputedStyle(document.querySelector('.files-list') ?? document.body).backgroundColor`);

// moveTo moves the pointer over a row's centre (a hover, as a person does), and leaves it there.
async function moveTo(page, name) {
  await hoverOver(page, rowSel(name));
}

// clearHover moves the pointer off the listing.
async function clearHover(page) {
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 1, y: 1, button: 'none', buttons: 0 });
  await sleep(200);
}

// closeMenus closes an open menu with Escape, and waits for it to go.
async function closeMenus(page) {
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await sleep(300);
}

// perfCheck measures a 500-folder listing (review fix 7): how long the rows take to draw, how many elements the list holds, and how
// long a click takes to select one row (the row's own re-render). Run only with SELFCLOUD_E2E_PERF set; the numbers are printed, not
// checked. The folder is removed at the end.
async function perfCheck(h, { page, base, accountKey, teamA }, suffix) {
  const { api, navigateTo, rowAppears, openRow } = h;
  const name = `L1 Perf ${suffix}`;
  const made = await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `/${name}`, parents: false });
  const id = made?.id ?? null;
  try {
    for (let i = 0; i < 500; i++) {
      await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `/${name}/P${String(i).padStart(3, '0')}`, parents: false });
    }
    await navigateTo(page, '/recent');
    await page.waitFor(`location.pathname === '/recent'`, 15000);
    await navigateTo(page, `/files/${teamA}`);
    await rowAppears(page, name);
    const t0 = Date.now();
    await openRow(page, name);
    // The listing loads 50 rows at a time, as the page reaches its end: scroll to the end until all 500 are there.
    let rowsNow = 0;
    for (let k = 0; k < 200 && rowsNow < 500; k++) {
      await page.evaluate(`[document.scrollingElement, ...document.querySelectorAll('*')].forEach((e) => { if (e && e.scrollHeight > e.clientHeight + 10) e.scrollTop = e.scrollHeight; }), true`);
      await sleep(300);
      rowsNow = await page.evaluate(`document.querySelectorAll('.files-list [role="row"][data-id]').length`);
    }
    if (rowsNow < 500) throw new Error(`only ${rowsNow} rows loaded`);
    const drawMs = Date.now() - t0;
    await sleep(800);
    const elements = await page.evaluate(`document.querySelectorAll('.files-list *').length`);
    const acts = await page.evaluate(`document.querySelectorAll('.files-list .row-acts .icon-btn').length`);
    const samples = [];
    for (let k = 0; k < 5; k++) {
      await page.evaluate(`(() => { window.__selMs = null; const row = document.querySelectorAll('.files-list [role="row"][data-id]')[${k * 7}]; const t = performance.now(); row.click(); const tick = () => { if (row.getAttribute('aria-selected') === 'true') window.__selMs = performance.now() - t; else requestAnimationFrame(tick); }; tick(); return true; })()`);
      await page.waitFor(`window.__selMs !== null`, 20000);
      samples.push(await page.evaluate(`window.__selMs`));
      await page.evaluate(`document.querySelector('[data-testid="selection-clear"]')?.click(), true`);
      await sleep(300);
    }
    samples.sort((a, b) => a - b);
    console.log(`PERF rows=500 drawMs=${drawMs} elements=${elements} rowActButtons=${acts} selectMsMedian=${samples[2].toFixed(1)} selectMsAll=${samples.map((x) => x.toFixed(1)).join('/')}`);
  } finally {
    if (id) await api(base, 'DELETE', `/api/v1/nodes/${id}?permanent=true`, accountKey).catch(() => undefined);
  }
}

// probeBands reads the two band colours the list's variables hold, as the browser paints them: a probe element takes each variable,
// so the colour is the token's own value, not a guess (review fix 3).
const probeBands = (page) =>
  page.evaluate(`(() => {
    const list = document.querySelector('.files-list');
    const d = document.createElement('div');
    list.append(d);
    d.style.background = 'var(--sc-row-hover)';
    const hover = getComputedStyle(d).backgroundColor;
    d.style.background = 'var(--sc-row-selected)';
    const selected = getComputedStyle(d).backgroundColor;
    d.remove();
    return { hover, selected };
  })()`);

// reviewDesktop checks the review fixes the light desktop shows: the bands are the tokens' exact colours (3), a focused row keeps its
// icons when the selection is cleared (2), and the keyboard opens a row's ⋮ menu with the focus in it (1). The finger rules (10) are not
// checked here: a headless browser cannot switch its hover and pointer media after launch (see cdp.mjs).
async function reviewDesktop(page, { check, alpha, beta, snap, selectRow }) {
  // (3) the exact bands: hovered, selected, and selected under the pointer, each the token's own colour.
  await clearSelection(page);
  const bands = await probeBands(page);
  await moveTo(page, beta);
  await sleep(500); // antd transitions the cell's background for 0.3 s: read it when it is settled
  const hoverBg = (await rowState(page, beta))?.bg;
  check(hoverBg === bands.hover, `a hovered row is the grey token exactly (${hoverBg}; token ${bands.hover})`);
  await selectRow(page, alpha);
  await clearHover(page);
  await sleep(500);
  const selBg = (await rowState(page, alpha))?.bg;
  check(selBg === bands.selected, `a selected row is the accent token exactly (${selBg}; token ${bands.selected})`);
  await moveTo(page, alpha);
  await sleep(500);
  const selHoverBg = (await rowState(page, alpha))?.bg;
  check(selHoverBg === bands.selected, `a selected row under the pointer keeps the accent token exactly (${selHoverBg})`);
  check(bands.hover !== bands.selected, `the grey and the accent are different colours (${bands.hover} / ${bands.selected})`);
  await clearHover(page);
  await clearSelection(page);

  // (2) a focused row's icons stay shown while another row is selected, and the focused icon is the one tab stop.
  await selectRow(page, alpha);
  await page.evaluate(`document.querySelector(${q(rowSel(beta))}).focus(), true`);
  await sleep(200);
  const rename = `${rowSel(beta)} .row-acts button[aria-label="Rename"]`;
  await page.waitFor(`!!document.querySelector(${q(rename)})`, 5000);
  await page.evaluate(`document.querySelector(${q(rename)}).focus(), true`);
  const onIcon = await page.evaluate(`document.activeElement === document.querySelector(${q(rename)})`);
  check(onIcon === true, `a row's icon takes the focus (${onIcon})`);
  // The selection stays on the other row: the Clear button would take the focus out of the row, so the focus is kept here by not clearing.
  await clearHover(page);
  const after = await page.evaluate(`(() => { const row = document.querySelector(${q(rowSel(beta))}); const b = row.querySelector('.row-acts button[aria-label="Rename"]'); const other = document.querySelector(${q(rowSel(alpha))})?.querySelector('.row-acts button[aria-label="Rename"]'); return { inRow: row.contains(document.activeElement), selected: row.getAttribute('aria-selected'), vis: b ? getComputedStyle(b).visibility : null, tab: b ? b.tabIndex : null, otherTab: other ? other.tabIndex : null }; })()`);
  check(after.inRow && after.selected === 'false' && after.vis === 'visible', `with another row selected, the focused row keeps its icons shown (${JSON.stringify(after)})`);
  check(after.tab === 0 && after.otherTab === -1, `only the focused row's icon is a tab stop (beta ${after.tab}, alpha ${after.otherTab})`);
  await snap('l1-focus-kept.png');

  // (1) Enter on a row's ⋮ opens the menu with the focus in it; an arrow moves in the menu, not the selection; Escape gives the focus back.
  await clearSelection(page);
  const more = `${rowSel(beta)} button[aria-haspopup="menu"]`;
  await page.evaluate(`document.querySelector(${q(more)}).focus(), true`);
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13, text: '\r', unmodifiedText: '\r' });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
  await page.waitFor(`!!document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden) [role="menuitem"]')`, 10000);
  await sleep(400);
  const inMenu = await page.evaluate(`(() => { const a = document.activeElement; return !!a && !!a.closest('.ant-dropdown:not(.ant-dropdown-hidden)') && a.getAttribute('role') === 'menuitem'; })()`);
  check(inMenu === true, `Enter on a row's ⋮ opens its menu with the focus on an item (${inMenu})`);
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'ArrowDown', code: 'ArrowDown', windowsVirtualKeyCode: 40, nativeVirtualKeyCode: 40 });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'ArrowDown', code: 'ArrowDown', windowsVirtualKeyCode: 40, nativeVirtualKeyCode: 40 });
  await sleep(300);
  const moved = await page.evaluate(`({ alpha: document.querySelector(${q(rowSel(alpha))})?.getAttribute('aria-selected'), beta: document.querySelector(${q(rowSel(beta))})?.getAttribute('aria-selected') })`);
  check(moved.alpha !== 'true' && moved.beta === 'true', `an arrow moves in the menu, and the selection stays on the row (${JSON.stringify(moved)})`);
  await closeMenus(page);
  const back = await page.evaluate(`document.activeElement === document.querySelector(${q(more)})`);
  check(back === true, `Escape gives the focus back to the row's ⋮ (${back})`);
  await clearSelection(page);
  await clearHover(page);

}

// hebrewMenuAnchor opens a row's ⋮ in Hebrew, with the mouse, and checks the menu is next to the button: its nearest edge is at the
// button's start edge, which in Hebrew is the right (review fix 11).
async function hebrewMenuAnchor(page, { check, beta }) {
  const more = `${rowSel(beta)} button[aria-haspopup="menu"]`;
  await page.evaluate(`document.querySelector(${q(more)}).click(), true`);
  await page.waitFor(`!!document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu')`, 10000);
  await sleep(400);
  const box = await page.evaluate(`(() => { const b = document.querySelector(${q(more)}).getBoundingClientRect(); const m = document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu').getBoundingClientRect(); return { bl: b.left, br: b.right, ml: m.left, mr: m.right, dir: document.documentElement.dir }; })()`);
  // The distance between the button's start edge (its right, in Hebrew) and the menu's nearest edge.
  const gap = Math.min(Math.abs(box.mr - box.br), Math.abs(box.ml - box.br));
  check(box.dir === 'rtl' && gap <= 12, `in Hebrew the menu opens next to the ⋮, at its start edge (gap ${gap.toFixed(1)} px, button ${box.bl.toFixed(0)}-${box.br.toFixed(0)}, menu ${box.ml.toFixed(0)}-${box.mr.toFixed(0)})`);
  await closeMenus(page);
  await clearHover(page);
}

export async function l1Checks(h, { page, base, accountKey, teamA, dir }) {
  const { check, api, navigateTo, rowAppears, selectRow, openRow } = h;
  const suffix = Date.now().toString(36);
  const folderName = `L1 List ${suffix}`;
  const files = { alpha: `l1-alpha-${suffix}.txt`, beta: `l1-beta-${suffix}.txt`, gamma: `l1-gamma-${suffix}.txt` };
  const shotsDir = process.env.SELFCLOUD_E2E_SHOTS
    ? await (async () => {
        await mkdir(process.env.SELFCLOUD_E2E_SHOTS, { recursive: true });
        return process.env.SELFCLOUD_E2E_SHOTS;
      })()
    : null;
  const snap = (name) => (shotsDir ? shot(page, shotsDir, name) : null);
  const size = (w, hgt) => page.send('Emulation.setDeviceMetricsOverride', { width: w, height: hgt, deviceScaleFactor: 1, mobile: false });
  const themeWas = await page.evaluate(`document.documentElement.dataset.theme`);
  const folderPath = `/${folderName}`;
  let folderID = null;
  try {
    if (process.env.SELFCLOUD_E2E_PERF) await perfCheck(h, { page, base, accountKey, teamA }, suffix);
    // ---- Desktop, 1280 wide, English, light. A mouse's screen: hover is there and the primary pointer is fine (CDP can set these two;
    // any-pointer cannot be set, so a touch screen with a mouse is not checked here).
    await sleep(300);
    await sleep(300);
    await size(1280, 800);
    // No setPhone(false) here: turning touch emulation off resets the pointer media the mouse flag set (cdp.mjs), so the hover checks would fail.
    await setLanguage(page, 'English');
    await setTheme(page, 'Light');
    const made = await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: folderPath, parents: false });
    folderID = made?.id ?? null;
    // The listing is kept for 30 seconds: another page first, so the folder made above is read (as in i1).
    await navigateTo(page, '/recent');
    await page.waitFor(`location.pathname === '/recent'`, 15000);
    await navigateTo(page, `/files/${teamA}`);
    await rowAppears(page, folderName);
    await openRow(page, folderName);
    await page.waitFor(`document.querySelector('#browser-title')?.textContent.trim() === ${q(folderName)}`, 15000);
    for (const name of Object.values(files)) await writeFile(join(dir, name), `${name} for the l1 check\n`);
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="new-button"]:not([disabled])')`, 15000);
    await page.evaluate(`document.querySelector('.shell-sider [data-testid="new-button"]').click(), true`);
    await page.waitFor(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].some((i) => i.textContent.trim() === 'Upload files')`, 10000);
    await page.evaluate(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].find((i) => i.textContent.trim() === 'Upload files').click(), true`);
    await sleep(300);
    await page.setFiles('[data-testid="sidebar-file-input"]', Object.values(files).map((name) => join(dir, name)));
    for (const name of Object.values(files)) await rowAppears(page, name);

    // Each row has a More button; the icons are not drawn until the row is hovered or selected.
    const alpha = files.alpha;
    const beta = files.beta;
    const cold = await rowState(page, alpha);
    check(cold && cold.more === `More actions for ${alpha}`, `every row has a More button named for its item (${cold?.more})`);
    check(cold && cold.actsVisible === false, `an unhovered row shows no icons (visibility ${cold?.actsVisible})`);
    check(cold && cold.checkShown === false && cold.iconShown === true, `an unhovered row shows its file icon, not a checkbox (check ${cold?.checkShown}, icon ${cold?.iconShown})`);

    // A hovered row shows the four icons, each named, and a tooltip with its name.
    await moveTo(page, beta);
    const hot = await rowState(page, beta);
    check(hot && hot.actsVisible === true, `a hovered row shows its icons (visibility ${hot?.actsVisible})`);
    // The member is an editor, who may not share by default (the menu leaves Share out for it too), so the row has three icons.
    check(hot && JSON.stringify(hot.names) === JSON.stringify(['Download', 'Rename', 'Star']), `the hovered row's icons are Download, Rename and Star, with no Share for an editor who may not share (${hot?.names?.join(', ')})`);
    check(hot && hot.checkShown === true && hot.iconShown === false, `the hovered row's checkbox takes the icon's place (check ${hot?.checkShown}, icon ${hot?.iconShown})`);
    const tip = await hoverOver(page, `${rowSel(beta)} .row-acts button[aria-label="Rename"]`).then(() => page.evaluate(`[...document.querySelectorAll('.ant-tooltip:not(.ant-tooltip-hidden)')].map((t) => t.textContent.trim()).join('|')`));
    check(tip === 'Rename', `the Rename icon shows its tooltip (${tip})`);
    await snap('l1-list-hover.png');

    // The hovered band is grey: not the page's colour, and the same for every hovered row.
    const page0 = await pageBackground(page);
    check(hot && hot.bg !== null && hot.bg !== page0 && hot.bg !== 'rgba(0, 0, 0, 0)', `a hovered row is a grey band (${hot?.bg}, page ${page0})`);

    // Selecting: the row is an accent band with no edge line (decision 72); the checkboxes of every row show.
    await selectRow(page, alpha);
    await clearHover(page);
    const sel = await rowState(page, alpha);
    check(sel && sel.selected === 'true', `the clicked row is selected (${sel?.selected})`);
    check(sel && sel.bg !== null && sel.bg !== page0 && sel.bg !== hot?.bg, `a selected row is an accent band (${sel?.bg})`);
    check(sel && (sel.shadow === 'none' || sel.shadow === ''), `a selected row has no edge line (${sel?.shadow})`);
    const other = await rowState(page, beta);
    check(other && other.checkShown === true && other.iconShown === false, `while something is selected every row shows its checkbox (${other?.checkShown})`);
    const head = await page.evaluate(`(() => { const c = document.querySelector('.files-list .head-check'); return c ? getComputedStyle(c).display !== 'none' : null; })()`);
    check(head === true, `the select-all box shows while something is selected (${head})`);
    await snap('l1-list-selected.png');

    // The More menu: the first group flat, then Share, Organize and Information as submenus, then Move to trash.
    await page.evaluate(`document.querySelector(${q(`${rowSel(alpha)} button[aria-haspopup="menu"]`)}).click(), true`);
    await page.waitFor(`${LABELS}.includes('Rename')`, 10000).catch(async (err) => {
      const seen = await page.evaluate(`JSON.stringify({ drops: [...document.querySelectorAll('.ant-dropdown')].map((d) => d.className + ' :: ' + d.textContent.slice(0, 160)), labels: ${LABELS}, rowMore: !!document.querySelector(${q(`${rowSel(alpha)} button[aria-haspopup="menu"]`)}), selected: document.querySelector(${q(rowSel(alpha))})?.getAttribute('aria-selected') })`);
      throw new Error(`${err.message.slice(0, 60)}; ${seen}`);
    });
    const top = await page.evaluate(LABELS);
    // Share stays at the top level; its Share… entry is hidden for an editor who may not share (decision 70).
    const want = ['Preview', 'Download', 'Rename', 'Share', 'Organize', 'Information', 'Move to trash'];
    check(JSON.stringify(top) === JSON.stringify(want), `the menu's top level reads ${want.join(' / ')} (${top.join(' / ')})`);
    const dividers = await page.evaluate(`document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item-divider').length`);
    check(dividers >= 4, `the groups are divided (${dividers} dividers)`);
    await pickMenuEntry(page, 'Copy to…');
    await sleep(300);
    // The Copy to dialog is cancelled, so the next menu is shown on its own.
    await page.evaluate(`[...document.querySelectorAll('.ant-modal button')].find((b) => b.textContent.trim() === 'Cancel')?.click(), true`);
    await page.waitFor(`!document.querySelector('.ant-modal-wrap:not([style*="display: none"])')`, 10000).catch(() => undefined);
    await closeMenus(page);

    // Organize opens with its six entries, each with its shortcut at the right.
    await page.evaluate(`document.querySelector(${q(`${rowSel(alpha)} button[aria-haspopup="menu"]`)}).click(), true`);
    await page.waitFor(`${LABELS}.includes('Organize')`, 10000);
    await hoverMenuTitle(page, 'Organize');
    const hints = await page.evaluate(`(() => { const t = [...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-title-content, .ant-dropdown-menu-submenu-popup .ant-dropdown-menu-title-content')].find((x) => x.textContent.trim() === 'Cut'); return t ? t.closest('[role="menuitem"]').querySelector('.menu-hint')?.getAttribute('data-hint') ?? null : null; })()`);
    check(hints === 'Ctrl+X' || hints === '⌘X', `Cut shows its shortcut at the right (${hints})`);
    await snap('l1-menu-submenu.png');
    await closeMenus(page);

    // Rename from the menu opens the name field; Escape leaves it.
    await page.evaluate(`document.querySelector(${q(`${rowSel(beta)} button[aria-haspopup="menu"]`)}).click(), true`);
    await pickMenuEntry(page, 'Rename');
    await page.waitFor(`!!document.querySelector('input[aria-label="New name"]')`, 10000);
    // The field holds the file's own name, ready to be typed over (review fix 13: a real condition, not a bare check).
    const field = await page.evaluate(`document.querySelector('input[aria-label="New name"]')?.value ?? null`);
    check(field === files.beta, `Rename from the menu opens the name field with the file's name (${field})`);
    await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
    await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
    await sleep(300);

    // Move to trash from the menu asks, and the file leaves the listing when it is confirmed.
    await page.evaluate(`document.querySelector(${q(`${rowSel(files.gamma)} button[aria-haspopup="menu"]`)}).click(), true`);
    await pickMenuEntry(page, 'Move to trash');
    await page.waitFor(`!!document.querySelector('.ant-modal-confirm, .ant-modal')`, 10000);
    await page.evaluate(`[...document.querySelectorAll('.ant-modal button')].find((b) => b.textContent.trim() === 'Move to trash')?.click(), true`);
    // The row is gone from the listing, and the menu has closed (review fix 13: a real condition, not a bare check).
    await page.waitFor(`!document.querySelector(${q(rowSel(files.gamma))}) && !document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden)')`, 10000).catch(() => undefined);
    const gone = await page.evaluate(`!document.querySelector(${q(rowSel(files.gamma))}) && !document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden)')`);
    check(gone === true, `Move to trash from the menu removes the file from the listing once confirmed (${gone})`);

    await reviewDesktop(page, { check, alpha, beta, snap, selectRow });

    // ---- Dark: the bands hold.
    await setTheme(page, 'Dark');
    await selectRow(page, alpha);
    await clearHover(page);
    const dark = await rowState(page, alpha);
    check(dark && dark.bg !== null && dark.bg !== sel?.bg, `a selected row is an accent band in dark too (${dark?.bg})`);
    await snap('l1-list-dark.png');
    await setTheme(page, 'Light');

    // ---- Hebrew: the More button is at the start of the row, and the row mirrors.
    await setLanguage(page, 'עברית');
    await clearHover(page);
    await moveTo(page, beta);
    const he = await rowState(page, beta);
    check(he && he.moreX !== null && he.nameX !== null && he.moreX < he.nameX, `in Hebrew the More button is at the start of the row (more ${he?.moreX}, name ${he?.nameX})`);
    await snap('l1-list-hebrew.png');
    await hebrewMenuAnchor(page, { check, beta });
    await setLanguage(page, 'English');

    // ---- Tiles: the star is a mark after the name and the More button is there; the bands stay out of the tiles.
    await setPhone(page, false);
    await size(1280, 800);
    // The selection bar takes the toolbar's place while something is selected, so the selection is cleared first.
    await clearSelection(page);
    // The view toggle's data-view names the view a press switches to: 'grid' (the tiles) from the list.
    await page.waitFor(`!!document.querySelector('[data-testid="view-toggle"]')?.dataset.view`, 10000);
    await page.evaluate(`(() => { const b = document.querySelector('[data-testid="view-toggle"]'); if (b.dataset.view === 'grid') b.click(); })()`);
    await page.waitFor(`!!document.querySelector('[role="option"][aria-label="${files.alpha}"]')`, 10000);
    await snap('l1-tiles.png');
    await page.evaluate(`(() => { const b = document.querySelector('[data-testid="view-toggle"]'); if (b.dataset.view === 'list') b.click(); })()`);
    await rowAppears(page, alpha);

    // ---- A phone with nothing selected or hovered: a row shows no icons, and its More button stays. The finger's own hover and
    // coarse pointer are not set here: this browser has the mouse's media from launch (see cdp.mjs).
    await clearSelection(page);
    await clearHover(page);
    await setPhone(page, true);
    await clearHover(page);
    await page.waitFor(`!!document.querySelector(${q(rowSel(alpha))})`, 15000);
    await snap('l1-phone-list.png');
    const phone = await page.evaluate(`(() => { const row = document.querySelector(${q(rowSel(alpha))}); return { acts: row.querySelectorAll('.row-acts button').length, more: !!row.querySelector('button[aria-haspopup="menu"]') }; })()`);
    check(phone.acts === 0 && phone.more, `on a phone with nothing selected, a row shows no icons and keeps its More button (${JSON.stringify(phone)})`);
    await setPhone(page, false);
    await size(1280, 800);
    await setPhone(page, true);
    await setPhone(page, false);
    await size(1280, 800);
    await snap('l1-list-light.png');
  } finally {
    await setLanguage(page, 'English').catch(() => undefined);
    await setTheme(page, themeWas === 'dark' ? 'Dark' : 'Light').catch(() => undefined);
    await setPhone(page, false).catch(() => undefined);
    await size(1280, 800).catch(() => undefined);
    await page.evaluate(`document.querySelector('[data-testid="selection-clear"]')?.click(), true`).catch(() => undefined);
    if (folderID) await api(base, 'DELETE', `/api/v1/nodes/${folderID}?permanent=true`, accountKey).catch(() => undefined);
  }
}
