// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice fu7 (decision 93), the owner's notes on the dev run. Three checks, at 1280 wide and on a 390 phone:
//  1. The breadcrumbs' separators (the chevrons) sit on the centre of the crumb text: each separator's centre is within 1 px of
//     the centre of the crumb before it and after it, in English (left to right), Hebrew (right to left), light and dark.
//  2. The sidebar's group of folders under a space's root has a light tint, one block per root, and the rows' text and the current
//     row's mark read at 4.5:1 or more against it (contrast computed from the computed colours; the e2e brand is teal).
//  3. A double click on a sidebar folder that has subfolders opens it, so its subfolders show, in one navigation (one history push);
//     on a phone's drawer tree too.
import { mkdir } from 'node:fs/promises';
import { dblClick, setLanguage, setPhone, setTheme, shot, sleep, tap } from './media.mjs';

const TOL = 1;
const CONTRAST_MIN = 4.5;
const SCOPE = `(document.querySelector('.ant-drawer-open') ?? document.querySelector('.shell-sider'))`;

// RGB_HELPERS are the contrast helpers the page runs (WCAG relative luminance), as a source string.
const RGB_HELPERS = `
  const rgb = (s) => { const m = s.match(/[\\d.]+/g).map(Number); return { r: m[0], g: m[1], b: m[2] }; };
  const lin = (c) => { c /= 255; return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4; };
  const lum = (c) => 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b);
  const cr = (a, b) => { const x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
`;

// separators reads each separator's visible chevron and the crumb text on either side of it, as vertical centres.
const separators = (page) =>
  page.evaluate(`(() => {
    const bc = document.querySelector('.ant-breadcrumb');
    if (!bc) return null;
    const centre = (el) => { const r = el.getBoundingClientRect(); return { top: r.top, bottom: r.bottom, mid: (r.top + r.bottom) / 2, width: r.width }; };
    const text = (li) => (li ? (li.querySelector('bdi') ?? li.querySelector('a, span')) : null);
    return [...bc.querySelectorAll('li.ant-breadcrumb-separator')].map((sep) => {
      const icon = sep.querySelector('svg') ?? sep;
      const prev = text(sep.previousElementSibling);
      const next = text(sep.nextElementSibling);
      return {
        icon: centre(icon),
        prev: prev ? centre(prev) : null,
        next: next ? centre(next) : null,
        rtl: document.documentElement.dir === 'rtl',
      };
    });
  })()`);

// groupTints reads every group of folders under a space's root in the sidebar (or the phone drawer): its colour, and each of its
// rows' text colour with the contrast against the group, and whether any nested group is tinted (none may be).
const groupTints = (page) =>
  page.evaluate(`(() => {
    ${RGB_HELPERS}
    const scope = ${SCOPE};
    if (!scope) return null;
    return [...scope.querySelectorAll('[data-tree-group="root"]')].map((g) => {
      // The tint is the group's ::before (the block behind its rows), so its colour is read from there.
      const bg = getComputedStyle(g, '::before').backgroundColor;
      const parentRow = g.parentElement?.querySelector(':scope > .tree-row');
      const rows = [...g.querySelectorAll(':scope > li > .tree-row')].map((row) => {
        const label = row.querySelector('bdi');
        const ink = getComputedStyle(label).color;
        const current = row.parentElement?.getAttribute('aria-current') === 'location';
        return { name: row.getAttribute('data-drop-name'), ink, current, contrast: bg === 'rgba(0, 0, 0, 0)' ? 0 : cr(rgb(ink), rgb(bg)) };
      });
      const nested = [...g.querySelectorAll('ul[role="group"]')].map((u) => getComputedStyle(u, '::before').backgroundColor);
      return { parent: parentRow?.getAttribute('data-drop-name') ?? null, bg, rows, nestedTinted: nested.filter((c) => c !== 'rgba(0, 0, 0, 0)').length, nestedCount: nested.length };
    });
  })()`);

// treeRow finds a folder's row in the sidebar's tree (or the drawer's): its open state, and whether its subfolder is on the screen.
const treeRow = (page, name, sub) =>
  page.evaluate(`(() => {
    const scope = ${SCOPE};
    const row = scope?.querySelector('[data-testid="folder-tree"] [data-drop-name="${name}"]');
    if (!row) return null;
    row.scrollIntoView({ block: 'center' });
    const item = row.parentElement;
    const r = (row.querySelector('bdi') ?? row).getBoundingClientRect();
    return {
      expanded: item.getAttribute('aria-expanded'),
      current: item.getAttribute('aria-current') === 'location',
      subShown: !!scope.querySelector('[data-testid="folder-tree"] [data-drop-name="${sub}"]'),
      x: r.left + r.width / 2,
      y: r.top + r.height / 2,
    };
  })()`);


// pointOf is the centre of a folder's row in the sidebar (or the drawer), scrolled into view.
const pointOf = (page, name) =>
  page.evaluate(`(() => {
    const row = ${SCOPE}?.querySelector('[data-testid="folder-tree"] [data-drop-name="${name}"]');
    if (!row) return null;
    row.scrollIntoView({ block: 'center' });
    const r = (row.querySelector('bdi') ?? row).getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  })()`);

// hoverRead reads a row's colours while the pointer is over it: the row's own hover background drawn over its group's tint, and the
// text colour (the current row's mark is its text colour too). Null when the row is not on the screen.
const hoverRead = (page, name) =>
  page.evaluate(`(() => {
    ${RGB_HELPERS}
    const parse = (s) => { const m = s.match(/[\\d.]+/g).map(Number); return { r: m[0], g: m[1], b: m[2], a: m.length > 3 ? m[3] : 1 }; };
    const hex = (c) => '#' + [c.r, c.g, c.b].map((v) => Math.round(v).toString(16).padStart(2, '0')).join('').toUpperCase();
    const row = ${SCOPE}?.querySelector('[data-testid="folder-tree"] [data-drop-name="${name}"]');
    if (!row) return null;
    const group = row.closest('[data-tree-group="root"]');
    const tint = parse(getComputedStyle(group, '::before').backgroundColor);
    const over = parse(getComputedStyle(row).backgroundColor);
    const a = over.a;
    const shown = { r: over.r * a + tint.r * (1 - a), g: over.g * a + tint.g * (1 - a), b: over.b * a + tint.b * (1 - a) };
    const ink = rgb(getComputedStyle(row.querySelector('bdi')).color);
    return { hovered: row.matches(':hover'), overlay: getComputedStyle(row).backgroundColor, under: hex(shown), ink: getComputedStyle(row.querySelector('bdi')).color, contrast: cr(ink, shown), current: row.parentElement.getAttribute('aria-current') === 'location' };
  })()`);

// hoverChecks points the pointer at each row of the groups (a real mouse move over CDP), reads the row as hovered, and requires
// the text and the current row's mark at 4.5:1 or more on the hover, then moves the pointer away.
async function hoverChecks(page, check, groups) {
  for (const g of groups) {
    for (const r of g.rows) {
      const at = await pointOf(page, r.name);
      if (!at) {
        check(false, `hover: the row "${r.name}" is on the screen`);
        continue;
      }
      await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: at.x, y: at.y });
      await sleep(150);
      const h = await hoverRead(page, r.name);
      await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 1270, y: 790 });
      await sleep(100);
      const label = `hover, group under ${g.parent}: the row "${r.name}"${r.current ? ' (current, its mark)' : ''}`;
      check(h !== null && h.hovered, `${label}: the pointer is over the row (${JSON.stringify(h && { hovered: h.hovered })})`);
      check(h !== null && h.contrast >= CONTRAST_MIN, `${label} reads ${h ? h.contrast.toFixed(2) : 'nothing'}:1 (${h?.ink} on ${h?.under}, background ${h?.overlay}; needs ${CONTRAST_MIN})`);
    }
  }
}

async function fu7Body(h, { page, base, accountKey, teamA, accountID, memberID }) {
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
  const suffix = `${Date.now().toString(36)}`;
  const P = `fu7 Parent ${suffix}`;
  const C = `fu7 Child ${suffix}`;
  const T = `fu7 Team ${suffix}`;

  // The fixture: My files' id (the folder My files opens), a folder P under it with a subfolder C, and a folder T with a subfolder
  // under the team space.
  await setPhone(page, false);
  await size(1280, 800, false);
  await navigateTo(page, `/files/${teamA}`);
  await page.waitFor(`!!document.querySelector('.shell-sider [role="tree"] > [role="treeitem"] > .tree-row a')`, 20000);
  await page.evaluate(`document.querySelector('.shell-sider [role="tree"] > [role="treeitem"] > .tree-row a').click(), true`);
  await page.waitFor(`/^\\/files\\/fld_/.test(location.pathname)`, 15000);
  const myID = await page.evaluate(`location.pathname.split('/')[2]`);
  const pMade = await api(base, 'POST', `/api/v1/folders/${myID}/directories`, accountKey, { path: `/${P}`, parents: false });
  const pID = pMade?.id;
  const cMade = await api(base, 'POST', `/api/v1/folders/${myID}/directories`, accountKey, { path: `/${P}/${C}`, parents: false });
  const cID = cMade?.id;
  const tMade = await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `/${T}`, parents: false });
  const tID = tMade?.id;
  await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `/${T}/${C}`, parents: false });
  check(!!pID && !!cID && !!tID, `the fu7 fixture folders are made (${pID}, ${cID}, ${tID})`);
  if (!pID || !cID || !tID) return;
  // The folders were made through the API, so a tree that read My files earlier in the run (its cache is 30 s) would not show them:
  // the page is reloaded, as sb's fixture is, so the tree reads them.
  await page.send('Page.reload', { ignoreCache: true });
  await page.waitFor(`!!document.querySelector('.shell-sider')`, 20000);

  // My files' arrow opens it (the tree keeps its own open state), and the folder under it is on the screen.
  const openMine = async () => {
    const expanded = await page.evaluate(`${SCOPE}?.querySelector('[role="tree"] > [role="treeitem"]')?.getAttribute('aria-expanded') ?? null`);
    if (expanded !== 'true') await page.evaluate(`${SCOPE}?.querySelector('[role="tree"] > [role="treeitem"] > .tree-row button')?.click(), true`);
    await page.waitFor(`!!${SCOPE}?.querySelector('[data-drop-name="${P}"]')`, 15000);
  };

  // ---- 1. The separators, at 1280 wide: English light, English dark, Hebrew light; then a 390 phone.
  const separatorsAt = async (label, shotName) => {
    await navigateTo(page, `/files/${myID}/${cID}`);
    await page.waitFor(`!!document.querySelector('.ant-breadcrumb li.ant-breadcrumb-separator')`, 20000);
    await sleep(300);
    const seps = await separators(page);
    check(Array.isArray(seps) && seps.length >= 2, `${label}: the breadcrumbs of a folder two levels down show two separators or more (${seps?.length ?? 0})`);
    // A crumb on another line (the breadcrumb wraps on a narrow screen) is not a neighbour on the separator's line: it is logged, and
    // the separator must have at least one neighbour on its own line to be checked.
    for (const [i, s] of (seps ?? []).entries()) {
      const onLine = (c) => c && s.icon.top < c.bottom && c.top < s.icon.bottom;
      console.log(`INFO ${label}: separator ${i + 1} icon ${s.icon.top.toFixed(1)}-${s.icon.bottom.toFixed(1)}, before ${s.prev ? `${s.prev.top.toFixed(1)}-${s.prev.bottom.toFixed(1)}` : 'none'}, after ${s.next ? `${s.next.top.toFixed(1)}-${s.next.bottom.toFixed(1)}` : 'none'}`);
      const sides = [['before', s.prev], ['after', s.next]].filter(([, c]) => onLine(c));
      check(sides.length > 0, `${label}: separator ${i + 1} has a crumb on its own line to be centred on`);
      for (const [side, c] of sides) {
        const off = Math.abs(s.icon.mid - c.mid);
        check(off <= TOL, `${label}: separator ${i + 1} is centred on the crumb ${side} it (icon ${s.icon.mid.toFixed(2)}, crumb ${c.mid.toFixed(2)}, off ${off.toFixed(2)} px)`);
      }
    }
    await snap(shotName);
  };
  await setLanguage(page, 'English');
  await setTheme(page, 'Light');
  await page.waitFor(`document.documentElement.dataset.theme === 'light'`, 10000);
  await separatorsAt('1280, English, light', 'fu7-crumbs-en-light.png');
  await setTheme(page, 'Dark');
  await page.waitFor(`document.documentElement.dataset.theme === 'dark'`, 10000);
  await separatorsAt('1280, English, dark', 'fu7-crumbs-en-dark.png');
  await setTheme(page, 'Light');
  await page.waitFor(`document.documentElement.dataset.theme === 'light'`, 10000);
  await setLanguage(page, 'עברית');
  await page.waitFor(`document.documentElement.dir === 'rtl'`, 10000);
  await separatorsAt('1280, Hebrew, light', 'fu7-crumbs-he-light.png');
  await setLanguage(page, 'English');
  await page.waitFor(`document.documentElement.dir === 'ltr'`, 10000);
  await setPhone(page, true);
  await size(390, 800, true);
  await separatorsAt('390, English, light', 'fu7-crumbs-phone-en.png');
  await setPhone(page, false);
  await size(1280, 800, false);

  // ---- 2. The tint: under My files (P is the current folder, so its mark is on the tinted row) and under the team space (T is current).
  await navigateTo(page, `/files/${myID}/${pID}`);
  await openMine();
  // Not fatal: with no tinted group the checks below say so, one by one.
  await page.waitFor(`!!${SCOPE}?.querySelector('[data-tree-group="root"]')`, 15000).catch(() => undefined);
  await sleep(300);
  const tintsMine = await groupTints(page);
  await hoverChecks(page, check, tintsMine ?? []);
  await navigateTo(page, `/files/${teamA}/${tID}`);
  await page.waitFor(`!!${SCOPE}?.querySelector('[data-drop-name="${T}"]')`, 15000);
  await sleep(300);
  const tintsTeam = await groupTints(page);
  await hoverChecks(page, check, tintsTeam ?? []);
  const all = [...(tintsMine ?? []), ...(tintsTeam ?? [])];
  check(all.length >= 2 && (tintsMine ?? []).length >= 1 && (tintsTeam ?? []).length >= 1, `a tinted group is drawn under My files and under the team space's root (${all.length} groups)`);
  for (const g of all) {
    const label = `group under ${g.parent}`;
    check(g.bg !== 'rgba(0, 0, 0, 0)', `${label}: its background is a tint (${g.bg})`);
    check(g.nestedTinted === 0, `${label}: nested groups are not tinted (${g.nestedTinted} of ${g.nestedCount})`);
    check(g.rows.length > 0, `${label}: it has rows`);
    for (const r of g.rows) {
      check(r.contrast >= CONTRAST_MIN, `${label}: the row "${r.name}"${r.current ? ' (current, its mark)' : ''} reads ${r.contrast.toFixed(2)}:1 on the tint (${r.ink} on ${g.bg}; needs ${CONTRAST_MIN})`);
    }
  }
  await snap('fu7-tint-en-light.png');

  // ---- 3. A double click on a sidebar folder with subfolders opens it, so its subfolders show, in one navigation.
  await navigateTo(page, `/files/${teamA}`);
  await page.waitFor(`!!${SCOPE}?.querySelector('[data-drop-name="${P}"]') || !!${SCOPE}?.querySelector('[role="tree"] > [role="treeitem"] > .tree-row button')`, 20000);
  await openMine();
  // The breadcrumb step opened P (the way to C), and the open state is kept: fold P by its arrow, so the double click starts from closed.
  if ((await treeRow(page, P, C))?.expanded === 'true') {
    await page.evaluate(`(() => { const b = [...${SCOPE}.querySelectorAll('[data-testid="folder-tree"] button')].find((x) => x.getAttribute('aria-label') === 'Collapse ${P}'); b?.click(); return true; })()`);
    await page.waitFor(`!${SCOPE}?.querySelector('[data-drop-name="${C}"]')`, 15000).catch(() => undefined);
    await sleep(300);
  }
  const before = await treeRow(page, P, C);
  check(before !== null && before.expanded !== 'true' && !before.subShown, `before the double click, ${P} is closed and its subfolder is not shown (${JSON.stringify(before && { expanded: before.expanded, sub: before.subShown })})`);
  await page.evaluate(`(() => { window.__pushes = 0; const p = history.pushState; history.pushState = function (...a) { window.__pushes += 1; return p.apply(this, a); }; })()`);
  await dblClick(page, { x: before.x, y: before.y });
  await page.waitFor(`!!${SCOPE}?.querySelector('[data-drop-name="${C}"]')`, 15000).catch(() => undefined);
  await sleep(400);
  const after = await treeRow(page, P, C);
  const pushes = await page.evaluate(`window.__pushes`);
  check(after !== null && after.expanded === 'true' && after.subShown, `a double click on ${P} opens it, so its subfolder ${C} shows (expanded ${after?.expanded}, sub shown ${after?.subShown})`);
  check(after?.current === true, `after the double click ${P} is the folder shown, its row marked (current ${after?.current})`);
  check(pushes === 1, `the double click navigates once (history pushes ${pushes})`);
  await snap('fu7-dblclick-desktop.png');

  // On a finger the drawer's arrow (44 px) is the way to open a folder in the tree; a tap on a folder shows it and closes the drawer at once
  // (decision 93, round 2). A double click is a pointer gesture, so it is checked on the desktop above.
  await setPhone(page, true);
  await size(390, 800, true);
  await navigateTo(page, `/files/${teamA}`);
  await sleep(600);
  await page.evaluate(`document.querySelector('[data-testid="nav-open"]').click()`);
  await page.waitFor(`!!document.querySelector('.ant-drawer-open [data-testid="folder-tree"] [role="treeitem"]')`, 15000);
  await openMine();
  await page.evaluate(`(() => { const b = [...document.querySelectorAll('.ant-drawer-open [data-testid="folder-tree"] button')].find((x) => x.getAttribute('aria-label') === 'Collapse ${P}'); b?.click(); return true; })()`);
  await page.waitFor(`!document.querySelector('.ant-drawer-open [data-drop-name="${C}"]')`, 15000).catch(() => undefined);
  await sleep(300);
  const arrow = await page.evaluate(`(() => { const b = [...document.querySelectorAll('.ant-drawer-open [data-testid="folder-tree"] button')].find((x) => x.getAttribute('aria-label') === 'Expand ${P}'); if (!b) return null; const r = b.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2, h: r.height }; })()`);
  check(arrow !== null && arrow.h >= 44 - TOL, `on a finger, the arrow of ${P} is a 44 px target (${arrow?.h})`);
  if (arrow) {
    await tap(page, { x: arrow.x, y: arrow.y });
    await page.waitFor(`!!document.querySelector('.ant-drawer-open [data-drop-name="${C}"]')`, 15000).catch(() => undefined);
    await sleep(300);
    const opened = await treeRow(page, P, C);
    check(opened !== null && opened.subShown, `on a finger, the arrow opens ${P} in the drawer, so ${C} shows`);
  }
  const name = await pointOf(page, P);
  check(name !== null, `on a finger, ${P}'s name is on the screen in the drawer`);
  if (name) {
    await tap(page, name);
    const closed = await page.waitFor(`!document.querySelector('.ant-drawer-open')`, 1500).then(() => true, () => false);
    check(closed, `on a finger, a tap on ${P} shows it and closes the drawer at once`);
    const shown = await page.evaluate(`location.pathname`);
    check(shown.endsWith(pID), `on a finger, the tap shows ${P} (${shown})`);
  }
  await snap('fu7-dblclick-phone.png');
  await page.evaluate(`document.activeElement?.blur(), true`);
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await page.waitFor(`!document.querySelector('.ant-drawer-open')`, 10000).catch(() => undefined);
  await setPhone(page, false);
  await size(1280, 800, false);
  const back = themeWas === 'dark' ? 'Dark' : 'Light';
  await setTheme(page, back);
  await page.waitFor(`document.documentElement.dataset.theme === ${JSON.stringify(back.toLowerCase())}`, 10000).catch(() => undefined);
}

// fu7Checks runs the slice's checks. The member's limit is not touched here; the fixture folders stay, as the other sections' do.
export async function fu7Checks(h, args) {
  await fu7Body(h, args);
}
