// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice fu7 (decision 93), the owner's notes on the dev run. At 1280 wide, and on a 390 phone where it says so:
//  1. The breadcrumbs' separators sit on the centre of the crumb text, in English light, English dark, Hebrew and at 390 wide.
//  2. The sidebar's group under a space's root has a tint; the rows' text and the current row's mark read 4.5:1 or more on it, at rest and
//     on hover, in English light, English dark and Hebrew (the e2e brand is teal).
//  3. A double click (a pointer's gesture) on a sidebar folder with subfolders opens it, in one navigation, with no word selected.
//  4. Painting: a group's read error and its capped hint are on top of the tint (elementFromPoint at their centres).
//  5. The tree's focus ring is visible, over the tint and over the bare sidebar (non-text contrast 3:1 or more).
//  6. The keyboard: ArrowRight opens a folder, Enter shows it, and the folder stays open.
//  7. On a finger, the arrow (44 px) opens a folder in the drawer, and a tap on a folder shows it and closes the drawer at once.
// FU7_PART=name,name runs only the listed parts (separators, tint, dblclick, overlay, ring, keys, phone) after the fixture: a red run
// reaches its own check that way.
import { mkdir } from 'node:fs/promises';
import { dblClick, setLanguage, setPhone, setTheme, shot, sleep, tap } from './media.mjs';

const TOL = 1;
const CONTRAST_MIN = 4.5;
const RING_MIN = 3;
const PART = process.env.FU7_PART ? new Set(process.env.FU7_PART.split(',').map((s) => s.trim()).filter(Boolean)) : null;
const want = (p) => !PART || PART.has(p);
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

// groupTints reads every group of folders under a space's root in the sidebar (or the phone drawer): its tint, and each of its rows'
// text colour with the contrast against the tint at rest. Nested groups must have no tint.
const groupTints = (page) =>
  page.evaluate(`(() => {
    ${RGB_HELPERS}
    const scope = ${SCOPE};
    if (!scope) return null;
    return [...scope.querySelectorAll('[data-tree-group="root"]')].map((g) => {
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

// hoverRead reads a row's colours while the pointer is over it: its own hover background drawn over its group's tint, and its text
// colour (the current row's mark is its text colour too). Null when the row is not on the screen.
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
async function hoverChecks(page, check, groups, label) {
  for (const g of groups) {
    for (const r of g.rows) {
      const at = await pointOf(page, r.name);
      if (!at) {
        check(false, `${label}: the row "${r.name}" is on the screen`);
        continue;
      }
      await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: at.x, y: at.y });
      await sleep(150);
      const h = await hoverRead(page, r.name);
      await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 1270, y: 790 });
      await sleep(100);
      const what = `${label}, hover, group under ${g.parent}: the row "${r.name}"${r.current ? ' (current, its mark)' : ''}`;
      check(h !== null && h.hovered, `${what}: the pointer is over the row (${JSON.stringify(h && { hovered: h.hovered })})`);
      check(h !== null && h.contrast >= CONTRAST_MIN, `${what} reads ${h ? h.contrast.toFixed(2) : 'nothing'}:1 (${h?.ink} on ${h?.under}, background ${h?.overlay}; needs ${CONTRAST_MIN})`);
    }
  }
}

// onTop says whether the element that the expression names is the topmost thing at its centre (or contains it): the painting order the
// group's tint must not cover. The element is scrolled into view first.
const onTop = (page, expr) =>
  page.evaluate(`(() => {
    const scope = ${SCOPE};
    const group = scope?.querySelector('[data-tree-group="root"]');
    const el = (${expr});
    if (!el) return null;
    el.scrollIntoView({ block: 'center' });
    const r = el.getBoundingClientRect();
    const top = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    return { hit: !!top && (top === el || el.contains(top)), top: top ? top.tagName + '.' + String(top.className).slice(0, 60) : null, text: (el.textContent ?? '').slice(0, 60), w: Math.round(r.width), h: Math.round(r.height) };
  })()`);

// answerChildren answers the reads of one folder's root (the tree's own read of My files): with a 500 (mode error), or with a list that never
// ends (mode capped, the tree shows its capped hint). The answers are the test's, not the server's. It returns the function that stops them.
async function answerChildren(page, myID, mode) {
  const onPaused = (p) => {
    const body =
      mode === 'error'
        ? { status: 500, json: { error: { code: 'internal', message: 'fu7 test' } } }
        : {
            status: 200,
            json: {
              items: Array.from({ length: 50 }, (_, i) => ({ id: `nod_fu7cap${i}`, folder_id: myID, parent_id: null, name: `fu7 capped ${i}`, kind: 'dir', size: 0, created_at: '', modified_at: '', role: 'editor', no_download: false, editors_can_share: false })),
              next_cursor: 'fu7-next',
            },
          };
    page
      .send('Fetch.fulfillRequest', {
        requestId: p.requestId,
        responseCode: body.status,
        responseHeaders: [{ name: 'Content-Type', value: 'application/json' }],
        body: Buffer.from(JSON.stringify(body.json)).toString('base64'),
      })
      .catch(() => undefined);
  };
  page.on('Fetch.requestPaused', onPaused);
  await page.send('Fetch.enable', { patterns: [{ urlPattern: `*/api/v1/folders/${myID}/children*`, requestStage: 'Request' }] });
  return async () => {
    page.off('Fetch.requestPaused', onPaused);
    await page.send('Fetch.disable');
  };
}

// paintedInk says how many pixels of a box are the ink colour of its text (within a small distance): a screenshot of the box, decoded by
// the browser. A tint drawn over the text leaves none. elementFromPoint cannot see that: the tint has pointer-events: none, so a hit
// test passes through it. Null when the box is not on the screen.
async function paintedInk(page, expr) {
  const box = await page.evaluate(`(() => { const el = ${expr}; if (!el) return null; el.scrollIntoView({ block: 'center' }); const r = el.getBoundingClientRect(); return { x: r.left + scrollX, y: r.top + scrollY, width: r.width, height: r.height, ink: getComputedStyle(el).color }; })()`);
  if (!box) return null;
  const { ink, ...clip } = box;
  const shot = await page.send('Page.captureScreenshot', { format: 'png', clip: { ...clip, scale: 1 } });
  const [r, g, b] = ink.match(/[\d.]+/g).slice(0, 3).map(Number);
  const count = await page.evaluate(`(async () => {
    const img = new Image();
    img.src = 'data:image/png;base64,' + ${JSON.stringify(shot.data)};
    await img.decode();
    const c = document.createElement('canvas');
    c.width = img.width;
    c.height = img.height;
    const ctx = c.getContext('2d');
    ctx.drawImage(img, 0, 0);
    const d = ctx.getImageData(0, 0, c.width, c.height).data;
    let near = 0;
    for (let i = 0; i < d.length; i += 4) if (Math.abs(d[i] - ${r}) + Math.abs(d[i + 1] - ${g}) + Math.abs(d[i + 2] - ${b}) <= 90) near++;
    return { near, total: c.width * c.height };
  })()`);
  return { ...count, ink };
}

// focusRing reads the focus ring of a folder's row, once focused by the keyboard: its colour, and the colour it sits on.
const focusRing = (page, name) =>
  page.evaluate(`(() => {
    ${RGB_HELPERS}
    const scope = ${SCOPE};
    const li = scope?.querySelector('[data-testid="folder-tree"] [data-drop-name="${name}"]')?.parentElement;
    if (!li) return null;
    li.focus();
    const row = li.querySelector(':scope > .tree-row');
    const cs = getComputedStyle(row);
    const group = row.closest('[data-tree-group="root"]');
    let under = group ? getComputedStyle(group, '::before').backgroundColor : null;
    for (let el = row.parentElement; !under || under === 'rgba(0, 0, 0, 0)'; el = el.parentElement) {
      if (!el) break;
      const bg = getComputedStyle(el).backgroundColor;
      if (bg !== 'rgba(0, 0, 0, 0)') under = bg;
    }
    const ring = rgb(cs.outlineColor);
    return { focusVisible: li.matches(':focus-visible'), style: cs.outlineStyle, width: cs.outlineWidth, colour: cs.outlineColor, on: under, contrast: under ? cr(ring, rgb(under)) : null, inGroup: !!group };
  })()`);

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
  // My files is the space row whose folder is myID (the first row is not always My files: a team space can sort above it).
  const mineRow = `(() => { const s = ${SCOPE}; return [...(s?.querySelectorAll('[data-testid="folder-tree"] .tree-row') ?? [])].find((r) => r.getAttribute('data-drop-folder') === '${myID}' && r.getAttribute('data-drop-dir') === '') ?? null; })()`;
  const openMine = async () => {
    // The tree loads after the page does: wait until My files' row is on the screen, then open it when it is shut.
    await page.waitFor(`[...(${SCOPE}?.querySelectorAll('[data-testid="folder-tree"] .tree-row') ?? [])].some((x) => x.getAttribute('data-drop-folder') === '${myID}' && x.getAttribute('data-drop-dir') === '')`, 20000);
    // One script finds the row, opens it when it is shut, and says what it found (a string, so a miss is visible in the log).
    const state = await page.evaluate(`(() => {
      const s = ${SCOPE};
      const rows = [...(s?.querySelectorAll('[data-testid="folder-tree"] .tree-row') ?? [])];
      const r = rows.find((x) => x.getAttribute('data-drop-folder') === '${myID}' && x.getAttribute('data-drop-dir') === '');
      if (!r) return 'no My files row among ' + rows.length + ' rows';
      const li = r.parentElement;
      const was = li?.getAttribute('aria-expanded') ?? null;
      if (was !== 'true') r.querySelector('button')?.click();
      return 'My files row found, aria-expanded was ' + was;
    })()`);
    console.log(`INFO openMine: ${state}, my id ${myID}`);
    await page.waitFor(`!!${SCOPE}?.querySelector('[data-drop-name="${P}"]')`, 15000);
  };

  // tintAt checks the groups' tint and each row's hover, in the state the page is in (a theme and a language): the folders under My files
  // (P is the current folder, so its mark is on the tinted row), and under the team space (T is current).
  const tintAt = async (label) => {
    await navigateTo(page, `/files/${myID}/${pID}`);
    await openMine();
    await page.waitFor(`!!${SCOPE}?.querySelector('[data-tree-group="root"]')`, 15000).catch(() => undefined);
    await sleep(300);
    const mine = (await groupTints(page)) ?? [];
    await hoverChecks(page, check, mine, label);
    await navigateTo(page, `/files/${teamA}/${tID}`);
    await page.waitFor(`!!${SCOPE}?.querySelector('[data-drop-name="${T}"]')`, 15000).catch(() => undefined);
    await sleep(300);
    const team = (await groupTints(page)) ?? [];
    await hoverChecks(page, check, team, label);
    const all = [...mine, ...team];
    check(mine.length >= 1 && team.length >= 1, `${label}: a tinted group is drawn under My files and under the team space's root (${all.length} groups)`);
    for (const g of all) {
      const what = `${label}, group under ${g.parent}`;
      check(g.bg !== 'rgba(0, 0, 0, 0)', `${what}: its background is a tint (${g.bg})`);
      check(g.nestedTinted === 0, `${what}: nested groups are not tinted (${g.nestedTinted} of ${g.nestedCount})`);
      check(g.rows.length > 0, `${what}: it has rows`);
      for (const r of g.rows) {
        check(r.contrast >= CONTRAST_MIN, `${what}: the row "${r.name}"${r.current ? ' (current, its mark)' : ''} reads ${r.contrast.toFixed(2)}:1 on the tint (${r.ink} on ${g.bg}; needs ${CONTRAST_MIN})`);
      }
    }
  };

  // ---- 1. The separators, at 1280 wide: English light, English dark, Hebrew light; then a 390 phone.
  if (want('separators')) {
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
  }

  // ---- 2. The tint and the hover: English light, English dark, and Hebrew (right to left).
  if (want('tint')) {
    await setLanguage(page, 'English');
    await setTheme(page, 'Light');
    await page.waitFor(`document.documentElement.dataset.theme === 'light'`, 10000);
    await tintAt('1280, English, light');
    await snap('fu7-tint-en-light.png');
    await setTheme(page, 'Dark');
    await page.waitFor(`document.documentElement.dataset.theme === 'dark'`, 10000);
    await tintAt('1280, English, dark');
    await setTheme(page, 'Light');
    await page.waitFor(`document.documentElement.dataset.theme === 'light'`, 10000);
    await setLanguage(page, 'עברית');
    await page.waitFor(`document.documentElement.dir === 'rtl'`, 10000);
    await tintAt('1280, Hebrew, light');
    await setLanguage(page, 'English');
    await page.waitFor(`document.documentElement.dir === 'ltr'`, 10000);
  }

  // ---- 3. A double click on a sidebar folder with subfolders opens it, so its subfolders show, in one navigation; no word is selected.
  if (want('dblclick')) {
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
    // Every navigation the router makes: history.pushState and replaceState (the router's own calls), counted in the page.
    await page.evaluate(`(() => { window.__navs = 0; for (const k of ['pushState', 'replaceState']) { const f = history[k]; history[k] = function (...a) { window.__navs += 1; return f.apply(this, a); }; } return true; })()`);
    // The second press of a double click is a mousedown with detail 2. The row must cancel its default (the word selection) there.
    // A headless CDP double click does not select text here (no range is made even on the old code), so the check reads the handler.
    const cancels = await page.evaluate(`(() => { const el = document.elementFromPoint(${before.x}, ${before.y}); if (!el) return 'no element'; const ev = new MouseEvent('mousedown', { bubbles: true, cancelable: true, detail: 2, button: 0, buttons: 1, clientX: ${before.x}, clientY: ${before.y} }); el.dispatchEvent(ev); return ev.defaultPrevented; })()`);
    check(cancels === true, `the second press of a double click on ${P} cancels its default, so no word is selected (defaultPrevented ${cancels})`);
    await dblClick(page, { x: before.x, y: before.y });
    // The selection is read at once: the tree re-renders as the folder opens, which can clear a word the second click picked.
    const selectedAtOnce = await page.evaluate(`window.getSelection().toString()`);
    await page.waitFor(`!!${SCOPE}?.querySelector('[data-drop-name="${C}"]')`, 15000).catch(() => undefined);
    await sleep(400);
    const after = await treeRow(page, P, C);
    const navs = await page.evaluate(`window.__navs`);
    check(after !== null && after.expanded === 'true' && after.subShown, `a double click on ${P} opens it, so its subfolder ${C} shows (expanded ${after?.expanded}, sub shown ${after?.subShown})`);
    check(after?.current === true, `after the double click ${P} is the folder shown, its row marked (current ${after?.current})`);
    check(navs === 1, `the double click navigates once (router navigations ${navs})`);
    const selected = await page.evaluate(`window.getSelection().toString()`);
    check(selectedAtOnce === '' && selected === '', `after the double click no word is selected (at once ${JSON.stringify(selectedAtOnce)}, later ${JSON.stringify(selected)})`);
    await snap('fu7-dblclick-desktop.png');
  }

  // ---- 4. The painting order: a group's read error and its capped hint are on top of the tint. Each read is
  // answered by the test (a 500, then a list that never ends), the page is reloaded, and the tree shows the error or the hint.
  if (want('overlay')) {
    const cappedAt = async (label) => {
      await navigateTo(page, `/files/${teamA}`);
      const stop = await answerChildren(page, myID, label.mode);
      try {
        await page.send('Page.reload', { ignoreCache: true });
        await page.waitFor(`!!document.querySelector('.shell-sider')`, 20000);
        // My files is opened on its own (openMine waits for P, which a failed read never shows).
        await page.waitFor(`!!${SCOPE}?.querySelector('[role="tree"] > [role="treeitem"] > .tree-row')`, 20000);
        if ((await page.evaluate(`${SCOPE}?.querySelector('[role="tree"] > [role="treeitem"]')?.getAttribute('aria-expanded') ?? null`)) !== 'true') {
          await page.evaluate(`${SCOPE}?.querySelector('[role="tree"] > [role="treeitem"] > .tree-row button')?.click(), true`);
        }
        await page.waitFor(`${SCOPE}?.querySelector('[role="tree"] > [role="treeitem"]')?.getAttribute('aria-expanded') === 'true'`, 10000).catch(() => undefined);
        // While a read is in flight the group's last row is the busy loading row (aria-busy), not the hint: skip it.
        await page.waitFor(
          label.mode === 'error'
            ? `!!${SCOPE}?.querySelector('[data-tree-group="root"] > li[role="none"] > div:not([class*="ant-skeleton"])')`
            : `!!${SCOPE}?.querySelector('[data-tree-group="root"] > li[role="none"]:last-child:not([aria-busy="true"])')`,
          20000,
        ).catch(async (err) => {
          const state = await page.evaluate(`(() => { const g = ${SCOPE}?.querySelector('[data-tree-group="root"]'); return { group: !!g, html: (g?.outerHTML ?? ${SCOPE}?.querySelector('[data-testid="folder-tree"]')?.outerHTML ?? 'no tree').slice(0, 700), path: location.pathname }; })()`);
          const reads = page.sent.filter((r) => r.url.includes('/children')).map((r) => r.url.replace(/^https?:\/\/[^/]+/, '')).slice(-4);
          console.log(`INFO ${label.name}: the wait failed (${err.message.slice(0, 120)}); state ${JSON.stringify(state)}; reads ${JSON.stringify(reads)}`);
          throw err;
        });
        await sleep(300);
        const elExpr = label.mode === 'error'
          ? `(${SCOPE}?.querySelector('[data-tree-group="root"]')?.querySelector(':scope > li[role="none"] > div:not([class*="ant-skeleton"])'))`
          : `(${SCOPE}?.querySelector('[data-tree-group="root"]')?.querySelector(':scope > li[role="none"]:last-child:not([aria-busy="true"])'))`;
        const at = await onTop(page, elExpr);
        check(at !== null && at.hit, `${label.name}: the text is on top at its centre (elementFromPoint: ${JSON.stringify(at)})`);
        const paint = await paintedInk(page, elExpr);
        check(paint !== null && paint.near >= 20, `${label.name}: the text is painted, not covered (${paint?.near} of ${paint?.total} pixels are its ink ${paint?.ink}; needs 20)`);
        if (label.mode === 'capped' && at) {
          const caption = await page.evaluate(`(() => {
            ${RGB_HELPERS}
            const group = ${SCOPE}?.querySelector('[data-tree-group="root"]');
            const li = group?.querySelector(':scope > li[role="none"]:last-child');
            if (!li) return null;
            const bg = rgb(getComputedStyle(group, '::before').backgroundColor);
            return { ink: getComputedStyle(li).color, contrast: cr(rgb(getComputedStyle(li).color), bg) };
          })()`);
          check(caption !== null && caption.contrast >= CONTRAST_MIN, `${label.name}: the capped hint reads ${caption?.contrast?.toFixed(2)}:1 on the tint (${caption?.ink}; needs ${CONTRAST_MIN})`);
        }
        if (label.mode === 'error') {
          // The browser logs each refused read; the run counts them as the test's own.
          const logged = page.errors.filter((e) => e.includes('/children') && e.includes('500'));
          page.errors = page.errors.filter((e) => !(e.includes('/children') && e.includes('500')));
          check(logged.length >= 1, `${label.name}: the browser logged the test's 500 for the read (${logged.length})`);
        }
        await snap(`fu7-overlay-${label.mode}.png`);
      } finally {
        await stop();
        // The fake list stays in the app's query cache until the page reloads (navigateTo is an in-app link), so reload here and
        // the parts after this one read the real tree.
        await page.send('Page.reload', { ignoreCache: true });
        await page.waitFor(`!!document.querySelector('.shell-sider')`, 20000);
      }
    };
    const modes = process.env.FU7_OVERLAY ? process.env.FU7_OVERLAY.split(',') : ['error', 'capped'];
    if (modes.includes('error')) await cappedAt({ mode: 'error', name: 'a group whose read failed' });
    if (modes.includes('capped')) await cappedAt({ mode: 'capped', name: 'a group whose list is capped' });
  }

  // ---- 5. The focus ring of a tree row, after a keyboard focus: visible over the tint, and over the bare sidebar.
  if (want('ring')) {
    await navigateTo(page, `/files/${teamA}`);
    await openMine();
    await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Shift', code: 'ShiftLeft', windowsVirtualKeyCode: 16 });
    await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Shift', code: 'ShiftLeft', windowsVirtualKeyCode: 16 });
    const inTint = await focusRing(page, P);
    check(inTint !== null && inTint.focusVisible, `the row ${P} takes a keyboard focus (focus-visible ${inTint?.focusVisible})`);
    check(inTint !== null && inTint.inGroup && inTint.contrast >= RING_MIN, `the focus ring of ${P}, in the group, reads ${inTint?.contrast?.toFixed(2)}:1 on the tint (${inTint?.colour} on ${inTint?.on}; needs ${RING_MIN})`);
    const teamRow = await page.evaluate(`(() => { const r = ${SCOPE}?.querySelector('[role="tree"] > [role="treeitem"] > .tree-row'); const li = r?.parentElement; if (!li) return null; li.focus(); const cs = getComputedStyle(r); let under = null; for (let el = r.parentElement; el && (!under || under === 'rgba(0, 0, 0, 0)'); el = el.parentElement) { const bg = getComputedStyle(el).backgroundColor; if (bg !== 'rgba(0, 0, 0, 0)') under = bg; } ${RGB_HELPERS} return { focusVisible: li.matches(':focus-visible'), colour: cs.outlineColor, on: under, contrast: under ? cr(rgb(cs.outlineColor), rgb(under)) : null, inGroup: !!r.closest('[data-tree-group="root"]') }; })()`);
    check(teamRow !== null && teamRow.focusVisible && !teamRow.inGroup, `a space's own row takes a keyboard focus outside the group (focus-visible ${teamRow?.focusVisible})`);
    check(teamRow !== null && teamRow.contrast !== null && teamRow.contrast >= RING_MIN, `the focus ring of a space's row reads ${teamRow?.contrast?.toFixed(2)}:1 on the bare sidebar (${teamRow?.colour} on ${teamRow?.on}; needs ${RING_MIN})`);
    await snap('fu7-focus-ring.png');
  }

  // ---- 6. The keyboard: ArrowRight opens a folder with subfolders; Enter shows it, and it stays open.
  if (want('keys')) {
    const key = async (k, vk) => {
      await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: k, code: k, windowsVirtualKeyCode: vk, text: k === 'Enter' ? '\r' : undefined });
      await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: k, code: k, windowsVirtualKeyCode: vk });
      await sleep(250);
    };
    const focusP = () => page.evaluate(`(() => { const li = ${SCOPE}?.querySelector('[data-testid="folder-tree"] [data-drop-name="${P}"]')?.parentElement; li?.focus(); return !!li; })()`);
    await navigateTo(page, `/files/${teamA}`);
    await openMine();
    if ((await treeRow(page, P, C))?.expanded === 'true') {
      await page.evaluate(`(() => { const b = [...${SCOPE}.querySelectorAll('[data-testid="folder-tree"] button')].find((x) => x.getAttribute('aria-label') === 'Collapse ${P}'); b?.click(); return true; })()`);
      await page.waitFor(`!${SCOPE}?.querySelector('[data-drop-name="${C}"]')`, 15000).catch(() => undefined);
      await sleep(300);
    }
    await focusP();
    await key('ArrowRight', 39);
    const opened = await treeRow(page, P, C);
    check(opened !== null && opened.expanded === 'true' && opened.subShown, `on the keyboard, ArrowRight opens ${P}, so ${C} shows (expanded ${opened?.expanded})`);
    await focusP();
    await key('Enter', 13);
    await page.waitFor(`location.pathname.endsWith('${pID}')`, 10000).catch(() => undefined);
    // The address changes before the tree re-renders, so the row's mark is waited for, not read at once.
    await page.waitFor(`${SCOPE}?.querySelector('[data-testid="folder-tree"] [data-drop-name="${P}"]')?.parentElement?.getAttribute('aria-current') === 'location'`, 10000).catch(() => undefined);
    const shownNow = await treeRow(page, P, C);
    check((await page.evaluate(`location.pathname`)).endsWith(pID), `on the keyboard, Enter shows ${P}`);
    check(shownNow !== null && shownNow.expanded === 'true' && shownNow.current, `after Enter, ${P} is still open and marked as the folder shown (expanded ${shownNow?.expanded}, current ${shownNow?.current})`);
  }

  // ---- 7. On a finger the arrow (44 px) is the way to open a folder in the tree; a tap on a folder shows it and closes the drawer at once.
  // A double click is a pointer gesture, so it is checked on the desktop above.
  if (want('phone')) {
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
  }

  const back = themeWas === 'dark' ? 'Dark' : 'Light';
  await setTheme(page, back);
  await page.waitFor(`document.documentElement.dataset.theme === ${JSON.stringify(back.toLowerCase())}`, 10000).catch(() => undefined);
}

// fu7Checks runs the slice's checks. The member's limit is not touched here; the fixture folders stay, as the other sections' do.
export async function fu7Checks(h, args) {
  await fu7Body(h, args);
}
