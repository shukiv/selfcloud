// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice i1: icons first, without borders (decision 68). A member's browser, at 1280 wide in English and light, is checked for:
// the file and folder icons take their colours by type (a folder grey, a PDF, an image, audio and video red, a text file
// blue, a spreadsheet green); the selection bar is a borderless pill, its clear icon first, then the count, with the action
// icons started right after the count, then More, and each icon names itself and shows its tooltip on hover; the viewer's
// header and the player's header have borderless icons with names; the top bar is the language, theme and account icons, and
// their menus open with their words; at 390 px the bar's icons are 44 pixels, with no sideways scroll; in Hebrew the bar is
// mirrored; in dark the colours hold. The folder and its files are removed before the section ends, and the language, theme,
// viewport and view are put back. With SELFCLOUD_E2E_SHOTS set (a folder), the run also saves the pictures named in the brief.
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { accountName, hoverOver, setLanguage, setPhone, setTheme, shot, sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);
// The song is a real Ogg file, so the player keeps its controls: a file the browser cannot play shows a message in their place.
const MEDIA = fileURLToPath(new URL('./media/', import.meta.url));
// A 1 by 1 PNG: enough for the picture's icon; the thumbnail may fail, and nothing here reads it.
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==', 'base64');
const RED = 'rgb(225, 89, 80)';
const BLUE = 'rgb(55, 133, 235)';
const GREEN = 'rgb(63, 149, 90)';
const GREY = 'rgb(129, 134, 139)';
const SELECTION = '[data-testid="selection-panel"]';

// rowSel is the listing row of a name.
const rowSel = (name) => `[role="row"][aria-label="${name.replace(/["\\]/g, '\\$&')}"]`;

// tipAfter moves the pointer over the element and returns the text of the tooltip that opens (empty when none does).
async function tipAfter(page, selector) {
  await hoverOver(page, selector);
  await page.waitFor(`[...document.querySelectorAll('.ant-tooltip:not(.ant-tooltip-hidden)')].some((t) => t.textContent.trim() !== '')`, 5000).catch(() => undefined);
  const text = await page.evaluate(`[...document.querySelectorAll('.ant-tooltip:not(.ant-tooltip-hidden)')].map((t) => t.textContent.trim()).filter(Boolean).join('|')`);
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 0, y: 0, button: 'none', buttons: 0 });
  await sleep(250);
  return text;
}

// boxes reads the window's boxes of the elements a selector finds, in order, with their names and computed borders.
// A disabled icon is wrapped in a span (IconButton), so the wrapper's button is the one measured.
const boxes = (page, selector) =>
  page.evaluate(`[...document.querySelectorAll(${q(selector)})].map((wrap) => {
    const el = wrap.matches('span.icon-btn-wrap') ? wrap.querySelector('button') : wrap;
    const r = el.getBoundingClientRect();
    const s = getComputedStyle(el);
    return { slot: wrap.getAttribute('data-slot'), name: el.getAttribute('aria-label') ?? el.textContent.trim(), text: el.textContent.trim(), l: r.left, r: r.right, t: r.top, w: r.width, h: r.height, color: s.color, bw: [s.borderTopWidth, s.borderRightWidth, s.borderBottomWidth, s.borderLeftWidth], bc: s.borderTopColor, disabled: el.disabled === true || el.getAttribute('aria-disabled') === 'true' };
  })`);

// tileColour reads the background of a file's coloured square in its row, or null when the row has none.
const tileColour = (page, name) =>
  page.evaluate(`(() => {
    const row = document.querySelector(${q(rowSel(name))}) ?? document.querySelector(${q(`[role="option"][aria-label="${name}"]`)});
    const sq = row?.querySelector('.icon-tile');
    return sq ? getComputedStyle(sq).backgroundColor : null;
  })()`);
const folderColour = (page, name) =>
  page.evaluate(`(() => { const row = document.querySelector(${q(rowSel(name))}); const svg = row?.querySelector('svg.kind-folder'); return svg ? getComputedStyle(svg).color : null; })()`);

// menuItems is the text of the open menu's items, with the menu closed again.
const openMenuItems = (page) => page.evaluate(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].map((x) => x.textContent.trim())`);
async function closeMenus(page) {
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await sleep(300);
}

// The top bar's icons, and the rest of the bar, as the page draws them.
const TOP = { language: '[data-testid="language"]', theme: '[data-testid="theme"]', account: '[data-testid="account-menu"]' };

export async function i1Checks(h, { page, base, accountKey, teamA, dir }) {
  const { check, api, navigateTo, rowAppears, selectRow, openRow } = h;
  const suffix = Date.now().toString(36);
  const folderName = `I1 Icons ${suffix}`;
  const files = {
    photo: `i1-photo-${suffix}.png`,
    report: `i1-report-${suffix}.pdf`,
    song: `i1-song-${suffix}.ogg`,
    notes: `i1-notes-${suffix}.txt`,
    sheet: `i1-sheet-${suffix}.xlsx`,
  };
  const shotsDir = process.env.SELFCLOUD_E2E_SHOTS
    ? await (async () => {
        await mkdir(process.env.SELFCLOUD_E2E_SHOTS, { recursive: true });
        return process.env.SELFCLOUD_E2E_SHOTS;
      })()
    : null;
  const snap = (name) => (shotsDir ? shot(page, shotsDir, name) : null);
  const size = (w, hgt, mobile = false) => page.send('Emulation.setDeviceMetricsOverride', { width: w, height: hgt, deviceScaleFactor: 1, mobile });
  const themeWas = await page.evaluate(`document.documentElement.dataset.theme`);
  const folderPath = `/${folderName}`;
  let folderID = null;
  try {
    // ---- Desktop, 1280 wide, English, light: the files area with its icons.
    await size(1280, 800);
    await setLanguage(page, 'English');
    await setTheme(page, 'Light');
    const made = await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: folderPath, parents: false });
    folderID = made?.id ?? null;
    // The section before this one may have shown Team A a moment ago, and the app keeps a listing fresh for 30 seconds
    // (lib/query.ts): the folder made above, through the API, would not be listed. The page is loaded again, so it reads Team A.
    await navigateTo(page, `/files/${teamA}`);
    await page.waitFor(`location.pathname === ${q(`/files/${teamA}`)}`, 15000);
    await page.send('Page.reload', { ignoreCache: true });
    await rowAppears(page, folderName);
    check((await folderColour(page, folderName)) === GREY, `a folder's icon is grey (${await folderColour(page, folderName)})`);

    // The files, uploaded into the folder: five kinds, each with its own square.
    await openRow(page, folderName);
    await page.waitFor(`document.querySelector('#browser-title')?.textContent.trim() === ${q(folderName)}`, 15000);
    const local = [];
    for (const [key, name] of Object.entries(files)) {
      const body = key === 'photo' ? PNG : key === 'song' ? await readFile(join(MEDIA, 'tone.ogg')) : Buffer.from(`${key} for the i1 check\n`);
      const path = join(dir, name);
      await writeFile(path, body);
      local.push(path);
    }
    // The upload goes where the sidebar's New says (Upload files in this folder): the menu's item sets the place, then the picker
    // takes the files (as d2 does).
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="new-button"]:not([disabled])')`, 15000);
    await page.evaluate(`document.querySelector('.shell-sider [data-testid="new-button"]').click(), true`);
    await page.waitFor(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].some((i) => i.textContent.trim() === 'Upload files')`, 10000);
    await page.evaluate(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].find((i) => i.textContent.trim() === 'Upload files').click(), true`);
    await sleep(300);
    await page.setFiles('[data-testid="sidebar-file-input"]', local);
    try {
      for (const name of Object.values(files)) await rowAppears(page, name);
    } catch (err) {
      // A diagnosis for a run that stops here: what the server holds, and what the page shows.
      const kids = await api(base, 'GET', `/api/v1/folders/${teamA}/children?path=${encodeURIComponent(folderPath)}`, accountKey).catch((e) => ({ error: String(e) }));
      const shown = await page.evaluate(`({ rows: [...document.querySelectorAll('[role="row"][aria-label], [role="option"][aria-label]')].map((e) => e.getAttribute('aria-label')), text: (document.querySelector('main')?.innerText ?? '').slice(0, 300) })`);
      throw new Error(`${err.message}; server: ${JSON.stringify(kids.items?.map((n) => n.name) ?? kids)}; page: ${JSON.stringify(shown)}`);
    }

    const colours = {
      photo: await tileColour(page, files.photo),
      report: await tileColour(page, files.report),
      song: await tileColour(page, files.song),
      notes: await tileColour(page, files.notes),
      sheet: await tileColour(page, files.sheet),
    };
    check(colours.photo === RED, `an image's square is red (${colours.photo})`);
    check(colours.report === RED, `a PDF's square is red (${colours.report})`);
    check(colours.song === RED, `an audio file's square is red (${colours.song})`);
    check(colours.notes === BLUE, `a text file's square is blue (${colours.notes})`);
    check(colours.sheet === GREEN, `a spreadsheet's square is green (${colours.sheet})`);
    await snap('i1-list-light.png');

    // Tiles: the toggle's data-view names the view a click switches to, so 'grid' means the list is shown now.
    const toggle = `document.querySelector('[data-testid="view-toggle"]')`;
    const inList = `${toggle}.dataset.view === 'grid'`;
    if (!(await page.evaluate(inList))) {
      await page.evaluate(`${toggle}.click(), true`);
      await page.waitFor(inList, 10000);
    }
    await rowAppears(page, files.photo);
    await page.evaluate(`${toggle}.click(), true`);
    await page.waitFor(`!(${inList})`, 10000);
    await page.waitFor(`!!document.querySelector('[role="option"][aria-label="${files.report}"]')`, 10000);
    check((await tileColour(page, files.report)) === RED, 'the tiles keep the squares by type');
    await snap('i1-tiles.png');
    await page.evaluate(`${toggle}.click(), true`);
    await page.waitFor(inList, 10000);
    await rowAppears(page, files.photo);

    // ---- The selection bar: a borderless pill, clear, the count, the actions, More.
    await selectRow(page, files.photo);
    await page.waitFor(`!!document.querySelector(${q(SELECTION)})`, 10000);
    await sleep(300);
    const bar = (await boxes(page, `${SELECTION} > *`)).filter((b) => b.slot !== 'spacer');
    const [clear, count, ...rest] = bar;
    const actions = rest.filter((b) => b.name !== 'More' && b.text === '');
    const more = rest.find((b) => b.name === 'More');
    check(clear && clear.name === 'Clear selection', `the bar starts with the clear icon, named (${clear?.name})`);
    check(count && /1/.test(count.text), `the count follows the clear icon (${count?.text})`);
    check(actions.length >= 2 && actions.every((b) => b.bw.every((w) => w === '0px')), `the action icons have no border (${actions.map((b) => b.name).join(', ')})`);
    check(clear.bw.every((w) => w === '0px') && (!more || more.bw.every((w) => w === '0px')), 'the clear icon and More have no border');
    const after = actions[0] ? actions[0].l - count.r : null;
    check(after !== null && after >= 0 && after <= 24, `the first action starts right after the count (${after?.toFixed(1)} px from its end)`);
    const liveActions = actions.filter((b) => !b.disabled);
    check(new Set(liveActions.map((b) => b.color)).size === 1, `the actions and Trash are one neutral colour (${[...new Set(liveActions.map((b) => b.color))].join(', ')})`);
    check(!bar.some((b) => b.name === 'Delete' && b.color === 'rgb(225, 29, 72)'), 'Trash is not drawn in the error colour in the bar');
    const tipText = await tipAfter(page, `${SELECTION} button[aria-label="${actions[0].name}"]`);
    check(tipText === actions[0].name, `the first action shows its name as its tooltip (${tipText})`);
    await snap('i1-selection.png');
    check((await tileColour(page, files.report)) === RED, 'the PDF keeps its red square with a selection');

    // ---- The top bar: the language, theme and account icons, with names and tooltips; each menu opens with its words.
    const top = await boxes(page, `${TOP.language}, ${TOP.theme}, ${TOP.account}`);
    check(top.length === 3 && top.every((b) => b.bw.every((w) => w === '0px')), 'the top bar icons (language, theme, account) have no border');
    check(top[0].text === '' && top[1].text === '', 'language and theme show no words on the bar');
    check(top[2].text.length === 1 && top[2].text === (await accountName(page)).trim().charAt(0).toUpperCase(), `the account is one letter, the first of the name (${top[2].text})`);
    check((await tipAfter(page, TOP.language)) === 'Language', 'the language icon shows its tooltip, Language');
    check((await tipAfter(page, TOP.theme)) === 'Theme: Light', 'the theme icon shows its tooltip, Theme: Light (the current choice)');
    await page.evaluate(`document.querySelector(${q(TOP.language)}).click(), true`);
    await page.waitFor(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].length === 7`, 10000);
    check((await openMenuItems(page)).length === 7, 'the language menu lists the seven languages');
    await closeMenus(page);
    await page.evaluate(`document.querySelector(${q(TOP.theme)}).click(), true`);
    await page.waitFor(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].length === 3`, 10000);
    const themeItems = await openMenuItems(page);
    const themeIcons = await page.evaluate(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item svg')].length`);
    check(JSON.stringify(themeItems) === JSON.stringify(['System', 'Light', 'Dark']) && themeIcons === 3, `the theme menu has System, Light and Dark, each with an icon (${themeItems.join(', ')}; ${themeIcons} icons)`);
    await snap('i1-topbar-theme-menu.png');
    await closeMenus(page);
    await page.evaluate(`document.querySelector(${q(TOP.account)}).click(), true`);
    await page.waitFor(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].length > 0`, 10000);
    await snap('i1-account-menu.png');
    check((await openMenuItems(page)).length > 0, 'the account menu opens with its items');
    await closeMenus(page);

    // Dark: the colours hold and the bar is a dark pill.
    await setTheme(page, 'Dark');
    await sleep(300);
    check((await tileColour(page, files.report)) === RED && (await tileColour(page, files.notes)) === BLUE, 'in dark the squares keep their colours');
    await snap('i1-list-dark.png');
    await setTheme(page, 'Light');

    // ---- The viewer's header: a PDF, then the player's header: an audio file.
    await openRow(page, files.report);
    await page.waitFor(`!!document.querySelector('[data-testid="preview"] header')`, 15000);
    const viewerHead = await boxes(page, '[data-testid="preview"] header button');
    check(viewerHead.length >= 2 && viewerHead.every((b) => b.bw.every((w) => w === '0px') && b.name), `the viewer's header icons have no border and a name (${viewerHead.map((b) => b.name).join(', ')})`);
    check((await tipAfter(page, '[data-testid="preview"] header button')) !== '', 'the viewer header icon shows its tooltip');
    await snap('i1-viewer-pdf.png');
    await page.evaluate(`document.querySelector('[data-testid="preview"] header button').click(), true`);
    await page.waitFor(`!document.querySelector('[data-testid="preview"]')`, 10000);

    await openRow(page, files.song);
    await page.waitFor(`!!document.querySelector('[data-testid="preview"] header')`, 15000);
    // The viewer's own head (the playlist drawer has its own header with its bordered text buttons, which are not in this slice).
    const playHead = await boxes(page, '[data-testid="preview"] .media-viewer-head button');
    const names = playHead.map((b) => b.name);
    check(names.includes('Close') && names.includes('Playlist') && names.includes('Download'), `the player's header has Close, Playlist and Download as icons (${names.join(', ')})`);
    check(playHead.every((b) => b.bw.every((w) => w === '0px') && b.text === ''), `the player header icons have no border and no words (${JSON.stringify(playHead.map((b) => ({ n: b.name, bw: b.bw, t: b.text })))})`);
    // The controls are drawn once the player has the file: wait for them before reading them.
    await page.waitFor(`!!document.querySelector('[data-testid="media-controls"] button')`, 15000);
    const controls = await boxes(page, '[data-testid="media-controls"] button');
    check(controls.length > 0 && controls.every((b) => b.bw.every((w) => w === '0px')), `the player's control icons have no border (${controls.length})`);
    await snap('i1-player-header.png');
    await page.evaluate(`document.querySelector('[data-testid="preview"] header button').click(), true`);
    await page.waitFor(`!document.querySelector('[data-testid="preview"]')`, 10000);
    // A song that plays stays in the mini player when the viewer closes (decision 52): stop it, so the checks after this one
    // see the page without the mini player.
    await page.evaluate(`document.querySelector('[data-testid="media-mini"] button[aria-label="Stop and close"]')?.click(), true`);
    await page.waitFor(`!document.querySelector('[data-testid="media-mini"]')`, 10000);

    // ---- Phone, 390 wide, touch: 44 pixel targets, the bar fits, no sideways scroll.
    await setPhone(page, true);
    await sleep(500);
    await selectRow(page, files.photo).catch(() => undefined);
    await page.waitFor(`!!document.querySelector(${q(SELECTION)})`, 10000);
    const coarseBar = await boxes(page, `${SELECTION} > button, ${SELECTION} > span.icon-btn-wrap`);
    check(coarseBar.every((b) => b.h >= 43.5 && b.w >= 43.5), `on a finger the bar's icons are 44 pixels (${coarseBar.map((b) => Math.round(b.h)).join(', ')})`);
    const coarseTop = await boxes(page, `${TOP.language}, ${TOP.theme}, ${TOP.account}`);
    check(coarseTop.every((b) => b.h >= 43.5 && b.w >= 43.5), `on a finger the top bar's icons are 44 pixels (${coarseTop.map((b) => Math.round(b.h)).join(', ')})`);
    const phone = await page.evaluate(`({ over: document.documentElement.scrollWidth - document.documentElement.clientWidth, right: document.querySelector('[data-testid="theme"]')?.getBoundingClientRect().right ?? null, inner: innerWidth })`);
    check(phone.over <= 0, `at 390 px there is no sideways scroll (${phone.over} px over)`);
    check(phone.right !== null && phone.right <= phone.inner, `at 390 px the theme icon fits in the bar (right ${phone.right} of ${phone.inner})`);
    await snap('i1-phone-390.png');
    await setPhone(page, false);
    await size(1280, 800);
    await sleep(400);

    // ---- Hebrew: mirrored. The clear icon is on the right, the count beside it, the actions to its left.
    await setLanguage(page, 'עברית');
    await sleep(500);
    const rtl = await page.evaluate(`document.documentElement.dir === 'rtl'`);
    check(rtl, 'Hebrew is right to left');
    const heBar = (await boxes(page, `${SELECTION} > *`)).filter((b) => b.slot !== 'spacer');
    const [heClear, heCount, ...heRest] = heBar;
    const heActions = heRest.filter((b) => b.text === '' && b.name !== 'More');
    check(heClear && heCount && heClear.l > heCount.r - 1 && heActions.every((b) => b.r <= heCount.l + 1), 'in Hebrew the bar is mirrored: the clear icon on the right, the actions on the left');
    await snap('i1-hebrew.png');
    await setLanguage(page, 'English');

    // Each new rule has a check above; the section ends by putting back what it changed.
  } finally {
    await setLanguage(page, 'English').catch(() => undefined);
    await setTheme(page, themeWas === 'dark' ? 'Dark' : 'Light').catch(() => undefined);
    await setPhone(page, false).catch(() => undefined);
    await size(1280, 800).catch(() => undefined);
    await page.evaluate(`document.querySelector('[data-testid="selection-clear"]')?.click(), true`).catch(() => undefined);
    if (folderID) await api(base, 'DELETE', `/api/v1/nodes/${folderID}?permanent=true`, accountKey).catch(() => undefined);
  }
}
