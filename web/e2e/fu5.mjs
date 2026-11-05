// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice fu5, the second item (decision 85): the list's rows do not move when the pointer hovers them. The icons of a row appear
// on hover, selection or focus with a mouse (decision 70), and that must not change any row's top or height, any cell's edge, or
// the header's columns. A mouse's browser measures a folder of three files, in light and dark and in English and Hebrew: with no
// row hovered, while each row is hovered, and in the frames after the pointer moves from row to row. Every frame must match the
// rest state to 0.05 px. The boxes are read in every frame by a sampler, so a frame that moved is found, not only the end state.
import { setLanguage, setTheme } from './media.mjs';

const q = (v) => JSON.stringify(v);

// GEOM is the geometry of the list: the header cells' boxes, and each row's box with its cells' boxes (left, top, width, height).
const GEOM = `function geom() {
  const list = document.querySelector('.files-list');
  if (!list) return null;
  const r = (el) => { const b = el.getBoundingClientRect(); return [b.left, b.top, b.width, b.height].map((v) => Math.round(v * 100) / 100); };
  const head = [...list.querySelectorAll('.ant-table-thead th')].map(r);
  const rows = {};
  // Each row's cells, and every element inside them (the icon, the checkbox, the name), with the row's own border widths: a
  // line that thickens or moves on hover shows here.
  for (const tr of list.querySelectorAll('tbody tr[data-id]')) {
    const inside = [...tr.children[0].querySelectorAll('*')].map((el) => ({ k: el.tagName.toLowerCase() + '.' + String(typeof el.className === 'string' ? el.className : '').split(' ').filter(Boolean).slice(0, 2).join('.'), b: r(el) }));
    const cs = getComputedStyle(tr);
    rows[tr.getAttribute('aria-label')] = { row: r(tr), cells: [...tr.children].map(r), inside, lines: [cs.borderTopWidth, cs.borderBottomWidth, cs.paddingTop, cs.paddingBottom] };
  }
  return { head, rows };
}`;

// SAMPLER records the geometry and the number of rows whose icons are drawn, in every frame, into window.__fu5.
const SAMPLER = `(() => {
  ${GEOM};
  const state = { on: true, frames: [] };
  window.__fu5 = state;
  const shown = () => [...document.querySelectorAll('.files-list tbody tr[data-id]')].filter((tr) => { const a = tr.querySelector('.row-acts'); return !!a && getComputedStyle(a).visibility === 'visible'; }).map((tr) => tr.getAttribute('aria-label'));
  const step = () => {
    if (!state.on) return;
    state.frames.push({ g: geom(), shown: shown() });
    requestAnimationFrame(step);
  };
  requestAnimationFrame(step);
  return true;
})()`;

// READ is the geometry now, and the rows whose icons are drawn now.
const READ = `(() => { ${GEOM}; const acts = [...document.querySelectorAll('.files-list tbody tr[data-id]')].filter((tr) => { const a = tr.querySelector('.row-acts'); return !!a && getComputedStyle(a).visibility === 'visible'; }).map((tr) => tr.getAttribute('aria-label')); return { g: geom(), shown: acts }; })()`;

// rowCentre is the centre of a row's box in the window's pixels.
const rowCentre = (page, name) =>
  page.evaluate(`(() => { const tr = document.querySelector('.files-list tbody tr[data-id][aria-label=${q(name)}]'); if (!tr) return null; const b = tr.getBoundingClientRect(); return { x: b.left + b.width / 2, y: b.top + b.height / 2 }; })()`);

// moveTo moves the mouse to a point of the window (a hover, with no press).
async function moveTo(page, at) {
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: at.x, y: at.y, button: 'none', buttons: 0 });
}

// diffs lists how a frame's geometry differs from the rest state, one line per box, beyond the tolerance.
function diffs(rest, g) {
  const out = [];
  if (!g) return ['no list'];
  const cmp = (label, a, b) => {
    if (!a || !b || a.length !== b.length) return out.push(`${label}: shape`);
    a.forEach((v, i) => {
      const d = Math.abs(v - b[i]);
      if (d > 0.05) out.push(`${label}[${i}] ${b[i]} -> ${v} (${d.toFixed(2)} px)`);
    });
  };
  cmp('header', rest.head.flat(), g.head.flat());
  for (const name of Object.keys(rest.rows)) {
    const rr = rest.rows[name];
    const gr = g.rows[name];
    if (!gr) {
      out.push(`row ${name} missing`);
      continue;
    }
    cmp(`${name} row`, rr.row, gr.row);
    cmp(`${name} cells`, rr.cells.flat(), gr.cells.flat());
    // The icons drawn on hover are new elements at the row's end: the elements the two states share are compared in order.
    // An element with no box at rest (display none) is one hover draws; it is not a move, so it is left out.
    const shared = Math.min(rr.inside.length, gr.inside.length);
    for (let i = 0; i < shared; i++) {
      const a = rr.inside[i];
      const b = gr.inside[i];
      // The icon and the checkbox swap places on hover (one is display none at rest): an element with no box in either state is
      // the swap, and is left out. The row's end (the icons) is not in these lists.
      if ((a.b[2] === 0 && a.b[3] === 0) || (b.b[2] === 0 && b.b[3] === 0)) continue;
      cmp(`${name} inside[${i}] ${a.k}`, a.b, b.b);
    }
    if (JSON.stringify(rr.lines) !== JSON.stringify(gr.lines)) out.push(`${name} lines ${rr.lines} -> ${gr.lines}`);
  }
  return out;
}

// rowsOf lists the rows' names in the order of the listing.
const rowsOf = (rest) => Object.keys(rest.rows);

// measureCombo runs the rest state, then each row hovered in turn, with the sampler on: each row's hover is read in every frame from
// the move until three frames after the icons are drawn. It returns the diffs found, and the frames read.
async function measureCombo(h, page, label, names) {
  const { check } = h;
  // The pointer goes to the empty space beside the list, so no row is hovered for the rest state.
  const away = await page.evaluate(`(() => { const s = document.querySelector('section.browser'); const b = s.getBoundingClientRect(); return { x: b.right - 4, y: b.bottom - 4 }; })()`);
  await moveTo(page, away);
  await page.waitFor(`[...document.querySelectorAll('.files-list .row-acts')].every((e) => getComputedStyle(e).visibility !== 'visible')`, 10000);
  const restRead = await page.evaluate(READ);
  const rest = restRead.g;
  // DIAG: the boxes that hold the name on the first row, at rest and on hover (top and height), so the moving one is named.
  const chain = `(() => { const tr = document.querySelector('.files-list tbody tr[data-id]'); const td = tr.children[0]; const outer = td.firstElementChild; const slot = outer.firstElementChild; const box = (el) => { if (!el) return 'none'; const b = el.getBoundingClientRect(); return [b.top, b.height].map((v) => Math.round(v * 100) / 100).join('/') + (el.getAttribute('style') ? ' [' + el.getAttribute('style').slice(0, 90) + ']' : ''); }; return { row: box(tr), td: box(td), outer: box(outer), slot: box(slot), icon: box(slot.querySelector('.row-icon')), check: box(slot.querySelector('.row-check')), name: box(outer.children[1]), bdi: box(outer.querySelector('bdi')) }; })()`;
  const chainRest = await page.evaluate(chain);
  console.log(`DIAG fu5 ${label} chain at rest ${JSON.stringify(chainRest)}`);
  await moveTo(page, await rowCentre(page, names[0]));
  await page.waitFor(`!!document.querySelector('.files-list tbody tr[data-id] .row-check') && getComputedStyle(document.querySelector('.files-list tbody tr[data-id] .row-check')).display !== 'none'`, 10000).catch(() => undefined);
  console.log(`DIAG fu5 ${label} chain on hover ${JSON.stringify(await page.evaluate(chain))}`);
  await moveTo(page, away);
  // DIAG: what the first inner elements of the first row are, and their parents' classes (the names of the moving boxes).
  console.log(`DIAG fu5 ${label} elements: ${await page.evaluate(`(() => { const tr = document.querySelector('.files-list tbody tr[data-id]'); return [...tr.querySelectorAll('td *')].slice(0, 4).map((el) => el.tagName + ' class="' + String(el.className).slice(0, 60) + '" in ' + el.parentElement.tagName + ' class="' + String(el.parentElement.className).slice(0, 60) + '" text="' + (el.textContent || '').trim().slice(0, 30) + '"').join(' | '); })()`)}`);
  const found = [];
  const frameCounts = [];
  for (const name of names) {
    await page.evaluate(SAMPLER);
    const at = await rowCentre(page, name);
    await moveTo(page, at);
    // Condition wait: the icons of this row are drawn, then three more frames are sampled (a few frames after the move).
    await page.waitFor(
      `(() => { const s = window.__fu5; const tr = document.querySelector('.files-list tbody tr[data-id][aria-label=${q(name)}]'); const a = tr && tr.querySelector('.row-acts'); const shown = !!a && getComputedStyle(a).visibility === 'visible'; if (!shown) return false; if (s.shownAt === undefined) s.shownAt = s.frames.length; return s.frames.length >= s.shownAt + 3; })()`,
      10000,
    ).catch(() => undefined);
    const frames = await page.evaluate(`(() => { window.__fu5.on = false; return window.__fu5.frames; })()`);
    frameCounts.push(frames.length);
    console.log(`DIAG fu5 ${label} hover ${name}: icons drawn in ${frames.filter((f) => f.shown.includes(name)).length} of ${frames.length} frames; rows drawn in the last frame ${JSON.stringify(frames.at(-1)?.shown)}`);
    frames.forEach((f, i) => {
      const d = diffs(rest, f.g);
      if (d.length) found.push({ hovered: name, frame: i, of: frames.length, diffs: d.slice(0, 4), shown: f.shown });
    });
  }
  // Moving between rows: the pointer goes from the first row to the last in one move, and back, with every frame read.
  await page.evaluate(SAMPLER);
  for (const name of [names[0], names[names.length - 1], names[0]]) {
    await moveTo(page, await rowCentre(page, name));
    await sleepFrames(page, 3);
  }
  const moved = await page.evaluate(`(() => { window.__fu5.on = false; return window.__fu5.frames; })()`);
  moved.forEach((f, i) => {
    const d = diffs(rest, f.g);
    if (d.length) found.push({ hovered: 'moving between rows', frame: i, of: moved.length, diffs: d.slice(0, 4), shown: f.shown });
  });
  console.log(`DIAG fu5 ${label}: rows ${rowsOf(rest).length}, icons drawn at rest ${restRead.shown.length}, frames per hover ${frameCounts.join('/')}, moved frames ${moved.length}, frames with a diff ${found.length}`);
  for (const f of found.slice(0, 6)) console.log(`DIAG fu5 ${label} diff ${JSON.stringify(f)}`);
  return { label, found };
}

// sleepFrames waits until the page has drawn n more frames, by a condition (no fixed time).
async function sleepFrames(page, n) {
  await page.evaluate(`(() => { window.__fu5.waitN = (window.__fu5.frames.length) + ${n}; return true; })()`);
  await page.waitFor(`window.__fu5.frames.length >= window.__fu5.waitN`, 10000);
}

export async function fu5Checks(h, { page, base, accountKey, teamA }) {
  const { check, api, rawPut, navigateTo, rowAppears } = h;
  const suffix = Date.now().toString(36);
  const folderName = `fu5 hover ${suffix}`;
  const names = [`fu5-alpha-${suffix}.txt`, `fu5-beta-${suffix}.txt`, `fu5-gamma-${suffix}.txt`];
  const made = await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `/${folderName}`, parents: false });
  const results = [];
  try {
    for (const name of names) await rawPut(base, `/api/v1/folders/${teamA}/files?path=${encodeURIComponent(`/${folderName}/${name}`)}`, accountKey, `${name}\n`);
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    for (const [theme, lang] of [
      ['Light', 'English'],
      ['Dark', 'English'],
      ['Light', 'עברית'],
      ['Dark', 'עברית'],
    ]) {
      await navigateTo(page, `/files/${teamA}/${made.id}`);
      await page.waitFor(`document.querySelectorAll('.files-list tbody tr[data-id]').length === ${names.length}`, 15000);
      await setTheme(page, theme);
      await setLanguage(page, lang);
      await rowAppears(page, names[0]);
      await page.waitFor(`document.querySelectorAll('.files-list tbody tr[data-id]').length === ${names.length}`, 15000);
      // Each combination is measured and reported before any check is made, so one failing combination does not hide the others.
      results.push(await measureCombo(h, page, `${theme} ${lang === 'English' ? 'English' : 'Hebrew'}`, names));
      await setLanguage(page, 'English');
    }
  } finally {
    if (made?.id) await api(base, 'DELETE', `/api/v1/nodes/${made.id}?permanent=true`, accountKey).catch(() => undefined);
  }
  for (const r of results) {
    check(r.found.length === 0, `${r.label}: no row, cell or header column moves on hover, in any frame (${r.found.length} frames with a difference, ${names.length} rows)`);
  }
}
