// SPDX-License-Identifier: AGPL-3.0-or-later

// The windowed list's checks (decision 183): in a folder of 1,000 files the list draws only the rows near the screen, with
// spacer rows holding the place of the rest. The page scrolls to the end and back, End and Home move the focus to the first
// and last loaded rows, a shift click from the first row to the last selects all 1,000, a band dragged to an edge scrolls
// and selects, a row being renamed keeps its name through a scroll away and back, the density toggle shortens the rows
// with the last row still reachable and the choice kept through a reload (decision 194), a row at the end of the list drops
// on a folder in the tree, and a folder of 150 rows draws every row while one of 151 draws a window. The click that selects
// one row is timed at 300 and 1,000 loaded rows and printed. The folders it makes are removed at the end.
const q = (v) => JSON.stringify(v);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const rowSel = (name) => `[role="row"][aria-label="${name.replace(/["\\]/g, '\\$&')}"]`;

const MODS = { shift: 8 };

// mouse sends one mouse event; held says the left button is down.
function mouse(page, type, { x, y }, { held = false, shift = false } = {}) {
  return page.send('Input.dispatchMouseEvent', {
    type,
    x,
    y,
    button: type === 'mouseMoved' ? 'none' : 'left',
    buttons: type === 'mouseReleased' ? 0 : held || type === 'mousePressed' ? 1 : 0,
    clickCount: type === 'mouseMoved' ? 0 : 1,
    modifiers: shift ? MODS.shift : 0,
  });
}

async function clickAt(page, at, { shift = false } = {}) {
  await mouse(page, 'mouseMoved', at);
  await mouse(page, 'mousePressed', at, { shift });
  await mouse(page, 'mouseReleased', at, { shift });
}

async function glide(page, a, b, n = 8) {
  for (let i = 1; i <= n; i++) {
    await mouse(page, 'mouseMoved', { x: a.x + ((b.x - a.x) * i) / n, y: a.y + ((b.y - a.y) * i) / n }, { held: true });
    await sleep(25);
  }
}

// pointOf is the middle of the element the selector names.
const pointOf = (page, selector) =>
  page.evaluate(`(() => { const el = document.querySelector(${q(selector)}); if (!el) return null; el.scrollIntoView({ block: 'nearest' }); const r = el.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);

// treePoint is the middle of a tree row, scrolled to the centre of the sidebar's scroll area (as fu7 does), not to its edge: a row
// left at the edge is moved by the drag's own edge autoscroll while the pointer glides to it, and the pointer ends off the row.
const treePoint = (page, selector) =>
  page.evaluate(`(() => { const el = document.querySelector(${q(selector)}); if (!el) return null; el.scrollIntoView({ block: 'center' }); const r = el.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);

// drawnRows counts the rows of the list that are in the page, not the loaded rows aria-rowcount names.
const drawnRows = (page) => page.evaluate(`document.querySelectorAll('.files-list [role="row"][data-id]').length`);
const loadedRows = (page) => page.evaluate(`Number(document.querySelector('.files-list table')?.getAttribute('aria-rowcount') ?? 1) - 1`);

// listView shows the list, whichever view the saved preferences start with.
async function listView(page) {
  await page.waitFor(`!!document.querySelector('[data-testid="view-toggle"]')`, 15000);
  // data-view names the view the button switches to, so it says List while the page shows the grid.
  await page.evaluate(`(() => { const b = document.querySelector('[data-testid="view-toggle"]'); if (b.dataset.view === 'list') b.click(); return true; })()`);
  await page.waitFor(`document.querySelector('[data-testid="view-toggle"]')?.dataset.view === 'grid'`, 10000);
}

// openFolder loads a folder's page and waits for its first drawn row.
async function openFolder(page, path, first) {
  await page.goto(page.base + path);
  await page.waitFor(`location.pathname === ${q(path)}`, 15000);
  await listView(page);
  try {
    await page.waitFor(`!!document.querySelector(${q(rowSel(first))})`, 20000);
  } catch (err) {
    const seen = await page.evaluate(`(() => ({
      view: document.querySelector('[data-testid="view-toggle"]')?.dataset.view ?? null,
      list: !!document.querySelector('.files-list'),
      grid: !!document.querySelector('.files-grid'),
      tables: document.querySelectorAll('table').length,
      rows: [...document.querySelectorAll('.files-list [data-id]')].slice(0, 5).map((r) => r.getAttribute('aria-label')),
      rowcount: document.querySelector('.files-list table')?.getAttribute('aria-rowcount') ?? null,
      main: document.querySelector('main')?.textContent.slice(0, 600) ?? null,
      alert: document.querySelector('[role="alert"]')?.textContent ?? null,
      dialog: document.querySelector('[role="dialog"]')?.textContent.slice(0, 300) ?? null,
      busy: document.querySelector('[aria-busy="true"]')?.textContent.slice(0, 200) ?? null,
    }))()`);
    throw new Error(`${err.message}; ${JSON.stringify(seen)}`);
  }
}

// loadTo scrolls the page to its end until the listing has read n rows.
async function loadTo(page, n) {
  let rows = await loadedRows(page);
  for (let i = 0; i < 300 && rows < n; i++) {
    await page.evaluate(`[document.scrollingElement, ...document.querySelectorAll('*')].forEach((e) => { if (e && e.scrollHeight > e.clientHeight + 10) e.scrollTop = e.scrollHeight; }), true`);
    await sleep(300);
    rows = await loadedRows(page);
  }
  return rows;
}

// clear drops the selection through the panel's Clear button.
async function clear(page) {
  await page.evaluate(`document.querySelector('[data-testid="selection-clear"]')?.click(), true`);
  await sleep(200);
}

// selectOne clicks one drawn row and returns the milliseconds until the row shows as selected.
async function selectOne(page) {
  const ms = await page.evaluate(`(() => new Promise((resolve, reject) => {
    const rows = [...document.querySelectorAll('.files-list [role="row"][data-id]')];
    if (rows.length === 0) return reject(new Error('no drawn row'));
    const row = rows[Math.floor(rows.length / 2)];
    const started = performance.now();
    row.click();
    const tick = () => { if (row.getAttribute('aria-selected') === 'true') resolve(performance.now() - started); else requestAnimationFrame(tick); };
    tick();
  }))()`);
  await clear(page);
  return ms;
}

async function timeSelection(page, rows) {
  const samples = [];
  for (let i = 0; i < 3; i++) samples.push(await selectOne(page));
  samples.sort((a, b) => a - b);
  console.log(`PERF vr1 rows=${rows} selectMsMedian=${samples[1].toFixed(1)} selectMsAll=${samples.map((x) => x.toFixed(1)).join('/')}`);
}

export async function vr1Checks(h, { page, base, accountKey, teamA }) {
  const { check, api, press } = h;
  const suffix = Date.now().toString(36);
  const root = `vr1-${suffix}`;
  const small = `vr1-small-${suffix}`;
  // The target's name starts with a Hebrew letter so it sorts last in the tree: the listing's root collation puts Latin
  // before Hebrew, and the sort section's Hebrew folder (סדר-ארוך) before a tav. Last in the tree, it is the row the drop
  // check drags to at the bottom of a long sidebar.
  const target = `ת-vr1-target-${suffix}`;
  const ids = [];
  const file = (i) => `file-${String(i).padStart(3, '0')}.txt`;
  try {
    const made = async (path) => {
      const node = await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `/${path}`, parents: false });
      ids.push(node.id);
      return node;
    };
    const rootID = (await made(root)).id;
    const smallID = (await made(small)).id;
    await made(target);
    // About 40 extra folders beside the target, all sorting above it, so the tree is long and the target is deep in it (below the
    // fold), as it is in a full run after the earlier sections' folders. They are kept on purpose: they make the drop check honest,
    // since a short tree would leave the target already in view, where the drag's edge autoscroll cannot move it. They are removed
    // with the rest at the end.
    for (let i = 0; i < 40; i++) await made(`vr1-pad-${String(i).padStart(2, '0')}-${suffix}`);
    // A one-request write is limited to 300 a minute per key: the seed waits for the window to refill instead of
    // failing on 429, so the folder's 1,000 rows can be made in one check.
    const putFile = async (path) => {
      for (let tries = 0; ; tries++) {
        const res = await fetch(base + path, { method: 'POST', headers: { Authorization: `Bearer ${accountKey}`, 'Content-Type': 'text/plain' }, body: 'x' });
        if (res.ok) return;
        const text = await res.text();
        if (res.status !== 429 || tries >= 600) throw new Error(`POST ${path}: ${res.status} ${text}`);
        const wait = Number(res.headers.get('retry-after') ?? 1);
        await sleep((Number.isFinite(wait) && wait > 0 ? wait : 1) * 1000 + 100);
      }
    };
    const putFolder = async (folder, names) => {
      for (const name of names) await putFile(`/api/v1/folders/${teamA}/files?path=${encodeURIComponent(`/${folder}/${name}`)}`);
    };
    await putFolder(root, Array.from({ length: 1000 }, (_v, i) => file(i)));
    await putFolder(small, Array.from({ length: 150 }, (_v, i) => `small-${String(i).padStart(3, '0')}.txt`));

    await openFolder(page, `/files/${teamA}/${rootID}`, file(0));
    check((await loadedRows(page)) === 50, `a long folder first reads one page (${await loadedRows(page)} loaded rows)`);

    const at300 = await loadTo(page, 300);
    check(at300 >= 300, `scrolling reads rows a page at a time (${at300} loaded)`);
    await timeSelection(page, at300);

    const at1000 = await loadTo(page, 1000);
    check(at1000 === 1000, `scrolling to the end reads all 1,000 rows (${at1000} loaded)`);
    check((await page.evaluate(`Number(document.querySelector('.files-list table')?.getAttribute('aria-rowcount'))`)) === 1001, 'the table says it holds 1,000 rows and the column header');
    await timeSelection(page, at1000);

    await page.evaluate(`window.scrollTo(0, document.documentElement.scrollHeight), true`);
    await page.waitFor(`!!document.querySelector(${q(rowSel(file(999)))})`, 15000);
    const end = await page.evaluate(`(() => { const rows = [...document.querySelectorAll('.files-list [role="row"][data-id]')]; const last = rows[rows.length - 1]; const r = last.getBoundingClientRect(); return { drawn: rows.length, firstIndex: Number(rows[0].getAttribute('aria-rowindex')), last: last.getAttribute('aria-label'), top: r.top, bottom: r.bottom, spacers: document.querySelectorAll('.files-list tr[data-spacer]').length, scrollY: window.scrollY }; })()`);
    check(end.drawn < 200 && end.firstIndex > 300, `at the end the list draws only the rows near the screen (${end.drawn} drawn, first row ${end.firstIndex})`);
    check(end.last === file(999) && end.top >= 0 && end.bottom <= 800, `the last loaded row is drawn in the window (${JSON.stringify({ top: end.top, bottom: end.bottom })})`);
    check(end.spacers === 2, `the rows not drawn are held by two spacer rows (${end.spacers})`);
    check(end.scrollY > 1000, `the page scrolled to the end (${end.scrollY})`);

    // End and Home move the focus to the last and first loaded rows, drawing them first.
    await page.evaluate(`document.querySelector(${q(rowSel(file(999)))}).focus(), true`);
    await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'End', code: 'End', windowsVirtualKeyCode: 35, nativeVirtualKeyCode: 35 });
    await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'End', code: 'End', windowsVirtualKeyCode: 35, nativeVirtualKeyCode: 35 });
    await page.waitFor(`document.activeElement?.getAttribute('aria-label') === ${q(file(999))}`, 15000);
    const endFocus = await page.evaluate(`(() => { const row = document.activeElement; const r = row.querySelector('td')?.getBoundingClientRect() ?? row.getBoundingClientRect(); return { name: row.getAttribute('aria-label'), top: r.top, bottom: r.bottom, scrollY: window.scrollY }; })()`);
    check(endFocus.top >= 0 && endFocus.bottom <= 800, `End puts the focus on the last row, in the window (${JSON.stringify(endFocus)})`);
    await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Home', code: 'Home', windowsVirtualKeyCode: 36, nativeVirtualKeyCode: 36 });
    await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Home', code: 'Home', windowsVirtualKeyCode: 36, nativeVirtualKeyCode: 36 });
    await page.waitFor(`document.activeElement?.getAttribute('aria-label') === ${q(file(0))}`, 15000);
    const homeFocus = await page.evaluate(`(() => { const row = document.activeElement; const r = row.querySelector('td')?.getBoundingClientRect() ?? row.getBoundingClientRect(); return { name: row.getAttribute('aria-label'), top: r.top, bottom: r.bottom, scrollY: window.scrollY }; })()`);
    check(homeFocus.top >= 0 && homeFocus.bottom <= 800, `Home puts the focus on the first row, in the window (${JSON.stringify(homeFocus)})`);
    check(homeFocus.scrollY < 400, `Home brings the page back to the first row (${homeFocus.scrollY})`);

    // A shift click from the first row to the last selects every loaded row, though only the last is drawn when clicked.
    await clear(page);
    await clickAt(page, await pointOf(page, rowSel(file(0))));
    await page.waitFor(`document.querySelector(${q(rowSel(file(0)))})?.getAttribute('aria-selected') === 'true'`, 10000);
    await page.evaluate(`window.scrollTo(0, document.documentElement.scrollHeight), true`);
    await page.waitFor(`!!document.querySelector(${q(rowSel(file(999)))})`, 15000);
    await clickAt(page, await pointOf(page, rowSel(file(999))), { shift: true });
    await page.waitFor(`document.querySelector('[data-testid="selection-panel"]')?.textContent.includes('1,000 selected')`, 10000);
    check(true, 'a shift click from the first row to the last selects all 1,000 loaded rows');
    await clear(page);

    // The window leaves no empty place in the viewport to start a band, so the press is sent to the list itself; the drag is real.
    const rowH = await page.evaluate(`document.querySelector('.files-list [role="row"][data-id]')?.getBoundingClientRect().height ?? 0`);
    await page.evaluate(`window.scrollTo(0, Math.max(0, document.documentElement.scrollHeight - window.innerHeight - 2000)), true`);
    await sleep(400);
    const band = await page.evaluate(`(() => { const list = document.querySelector('.files-list'); const head = list.querySelector('.ant-table-thead th'); const lr = list.getBoundingClientRect(); const x = lr.left + lr.width / 2; const y = (head ? head.getBoundingClientRect().bottom : lr.top) + 4; list.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, cancelable: true, pointerId: 1, pointerType: 'mouse', isPrimary: true, button: 0, buttons: 1, clientX: x, clientY: y })); return { x, y, scrollY: window.scrollY }; })()`);
    const bottom = { x: band.x + 120, y: 795 };
    await glide(page, { x: band.x, y: band.y }, bottom);
    for (const limit = Date.now() + 8000; Date.now() < limit; ) {
      const moved = await page.evaluate(`window.scrollY`);
      const text = await page.evaluate(`document.querySelector('[data-testid="selection-panel"]')?.textContent ?? ''`);
      if (moved > band.scrollY + 300 && /\\d[\\d,]* selected/.test(text)) break;
      await sleep(200);
    }
    const during = await page.evaluate(`({ scrollY: window.scrollY, text: document.querySelector('[data-testid="selection-panel"]')?.textContent ?? '', drawn: document.querySelectorAll('.files-list [role="row"][data-id]').length })`);
    await mouse(page, 'mouseReleased', bottom);
    await sleep(300);
    check(during.scrollY > band.scrollY + 300, `holding the band at the bottom edge scrolls the page (${band.scrollY} to ${during.scrollY})`);
    const selected = Number((during.text.match(/([\d,]+) selected/) ?? [])[1]?.replace(/,/g, '') ?? 0);
    check(selected > during.drawn, `the band selects rows that come into the window, drawn or not (${selected} selected, ${during.drawn} drawn)`);
    await clear(page);

    // A name typed into the row being renamed is still there after the list scrolls away and back.
    await page.evaluate(`window.scrollTo(0, ${Math.round(rowH * 500)}), true`);
    await page.waitFor(`!!document.querySelector(${q(rowSel(file(500)))})`, 15000);
    await clickAt(page, await pointOf(page, rowSel(file(500))));
    await page.waitFor(`document.querySelector(${q(rowSel(file(500)))})?.getAttribute('aria-selected') === 'true'`, 5000);
    await press(page, rowSel(file(500)), 'F2');
    await page.waitFor(`!!document.querySelector('input.rename')`, 5000);
    await page.setInput('input.rename', 'renamed-500.txt');
    await page.evaluate(`window.scrollTo(0, document.documentElement.scrollHeight), true`);
    await sleep(400);
    const away = await page.evaluate(`({ value: document.querySelector('input.rename')?.value ?? null, drawn: document.querySelectorAll('.files-list [role="row"][data-id]').length })`);
    check(away.value === 'renamed-500.txt', `the renamed row keeps the typed name while the list is scrolled away (${JSON.stringify(away)})`);
    await page.evaluate(`window.scrollTo(0, ${Math.round(rowH * 500)}), true`);
    await page.waitFor(`document.querySelector('input.rename')?.value === 'renamed-500.txt'`, 10000);
    check(true, 'the typed name is still there when the list scrolls back');
    await press(page, 'input.rename', 'Escape');
    await page.waitFor(`!document.querySelector('input.rename')`, 5000);

    // Compact rows (decision 194): the toggle shortens every row, the window keeps the last row reachable by scrolling to the
    // end with no gap at the bottom, the choice survives a reload under the person's own key, and switching back restores the
    // full rows. The list is read to its end again afterwards, for the drop check below. The rename check left its row selected,
    // and the selection panel holds the toolbar's row while anything is selected, so the choice is read with nothing selected.
    await clear(page);
    const comfortableH = await page.evaluate(`document.querySelector('.files-list [role="row"][data-id]')?.getBoundingClientRect().height ?? 0`);
    await page.evaluate(`document.querySelector('[data-testid="density-toggle"]')?.click(), true`);
    await page.waitFor(`!!document.querySelector('.files-list.list--compact')`, 10000);
    await sleep(400);
    const compactH = await page.evaluate(`document.querySelector('.files-list [role="row"][data-id]')?.getBoundingClientRect().height ?? 0`);
    check(compactH > 0 && compactH < comfortableH, `compact rows are shorter than comfortable (${comfortableH} to ${compactH})`);
    await page.evaluate(`window.scrollTo(0, document.documentElement.scrollHeight), true`);
    await page.waitFor(`!!document.querySelector(${q(rowSel(file(999)))})`, 15000);
    const compactEnd = await page.evaluate(`(() => { const rows = [...document.querySelectorAll('.files-list [role="row"][data-id]')]; const last = rows[rows.length - 1]; const r = last.getBoundingClientRect(); const list = document.querySelector('.files-list').getBoundingClientRect(); const spacers = [...document.querySelectorAll('.files-list tr[data-spacer]')].map((s) => s.getBoundingClientRect().height); return { last: last.getAttribute('aria-label'), bottom: r.bottom, rowH: r.height, drawn: rows.length, topSpacer: spacers[0] ?? -1, bottomSpacer: spacers[1] ?? -1, listBottom: list.bottom }; })()`);
    check(compactEnd.last === file(999) && compactEnd.bottom <= 800, `the last row is still reachable at the end in compact (${JSON.stringify(compactEnd)})`);
    check(Math.abs(compactEnd.topSpacer + compactEnd.drawn * compactEnd.rowH + compactEnd.bottomSpacer - 1000 * compactEnd.rowH) < 1 && Math.abs(compactEnd.listBottom - compactEnd.bottom) < 4, `the compact window leaves no gap at the bottom (${JSON.stringify(compactEnd)})`);
    const densityStored = () => page.evaluate(`Object.keys(localStorage).filter((k) => k.startsWith('selfcloud.density.')).map((k) => localStorage.getItem(k)).join(',')`);
    check((await densityStored()) === 'compact', `the compact choice is stored under the person's key (${await densityStored()})`);
    await openFolder(page, `/files/${teamA}/${rootID}`, file(0));
    check((await page.evaluate(`!!document.querySelector('.files-list.list--compact') && document.querySelector('[data-testid="density-toggle"]')?.dataset.density === 'comfortable'`)) === true, 'after a reload the list is still compact');
    await page.evaluate(`document.querySelector('[data-testid="density-toggle"]')?.click(), true`);
    await page.waitFor(`!document.querySelector('.files-list.list--compact')`, 10000);
    check((await densityStored()) === 'comfortable', `switching back stores comfortable (${await densityStored()})`);
    await loadTo(page, 1000);

    // A row at the end of the list drops on a folder in the tree, after the list has been scrolled there.
    await page.evaluate(`window.scrollTo(0, document.documentElement.scrollHeight), true`);
    await page.waitFor(`!!document.querySelector(${q(rowSel(file(999)))})`, 15000);
    const treeRow = `[data-testid="folder-tree"] .tree-row[data-drop-name="${target}"]`;
    await page.waitFor(`!!document.querySelector(${q(treeRow)})`, 15000);
    // loadTo scrolls every region to its bottom, the sidebar among them, which would leave the target mid-sidebar where the drag's
    // edge autoscroll cannot move it. The sidebar is put back at its top so the long tree's last row is below the fold, as it is in a
    // full run, and the point helper has to scroll to it.
    await page.evaluate(`document.querySelector('.shell-scroll').scrollTop = 0, true`);
    await sleep(300);
    // to is measured first, so its scroll into the sidebar cannot stale what from measures; the tree row goes to the centre of the
    // sidebar's scroll area, not its edge. Both points are checked on the screen and the drop point on the row before the press.
    const to = await treePoint(page, treeRow);
    const from = await pointOf(page, `${rowSel(file(999))} .row-name`);
    const at = await page.evaluate(`(() => { const row = document.querySelector(${q(treeRow)}); const hit = document.elementFromPoint(${to.x}, ${to.y}); return { w: innerWidth, h: innerHeight, onRow: !!row && !!hit && (hit === row || row.contains(hit)) }; })()`);
    const onScreen = (p) => !!p && p.x >= 0 && p.x < at.w && p.y >= 0 && p.y < at.h;
    const points = JSON.stringify({ from, to, window: { w: at.w, h: at.h }, onRow: at.onRow });
    check(onScreen(from) && onScreen(to) && at.onRow, `the drag's points are on the screen and the tree row is under the drop point (${points})`);
    await mouse(page, 'mouseMoved', from);
    await mouse(page, 'mousePressed', from);
    await glide(page, from, to);
    // dropState is read while the pointer rests on the row, waited for up to 2 s (the tree may still be settling), not one read.
    let dropState = null;
    for (const limit = Date.now() + 2000; Date.now() < limit; ) {
      dropState = await page.evaluate(`document.querySelector(${q(treeRow)})?.dataset.dropState ?? null`);
      if (dropState !== null) break;
      await sleep(100);
    }
    await mouse(page, 'mouseReleased', to);
    await sleep(500);
    check(dropState === 'ok', `the row at the end of the list drops on the folder in the tree (${dropState}; ${points})`);
    const inTarget = await api(base, 'GET', `/api/v1/folders/${teamA}/children?path=${encodeURIComponent(`/${target}`)}&limit=50`, accountKey);
    check(inTarget.items.some((n) => n.name === file(999)), 'the file is in the folder it was dropped on');

    // A folder of 150 rows draws every row; one of 151 draws a window.
    await openFolder(page, `/files/${teamA}/${smallID}`, 'small-000.txt');
    await loadTo(page, 150);
    check((await drawnRows(page)) === 150, `a folder of 150 rows draws all of them (${await drawnRows(page)} drawn)`);
    check((await page.evaluate(`document.querySelectorAll('.files-list tr[data-spacer]').length`)) === 0, 'a folder of 150 rows has no spacer');
    await putFolder(small, ['small-150.txt']);
    await openFolder(page, `/files/${teamA}/${smallID}`, 'small-000.txt');
    await loadTo(page, 151);
    const over = await page.evaluate(`({ drawn: document.querySelectorAll('.files-list [role="row"][data-id]').length, rows: Number(document.querySelector('.files-list table')?.getAttribute('aria-rowcount')) - 1, spacers: document.querySelectorAll('.files-list tr[data-spacer]').length })`);
    check(over.rows === 151 && over.drawn < 151 && over.spacers === 2, `a folder of 151 rows draws a window (${JSON.stringify(over)})`);
  } finally {
    for (const id of ids.reverse()) await api(base, 'DELETE', `/api/v1/nodes/${id}?permanent=true`, accountKey).catch(() => undefined);
  }
}