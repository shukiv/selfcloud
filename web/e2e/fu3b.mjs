// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice fu3b (decision 77): the list's first frames and its place changes, in a browser that samples every frame.
// From a fresh mount of the list and from a folder change, no painted frame has the header's columns and the body's
// first row's cells apart by more than 1 px. From a click on a folder to the new listing, every frame holds a listing (the
// previous one, marked busy, until the next is read). The sampler starts before the action, so each frame counts, and it stops
// on the listing's own rows, not on a count of frames. Each case runs three times, so one lucky frame does not pass a flake.
// The folder and its files are removed before the section ends.
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { launchBrowser, Page } from './cdp.mjs';
import { sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);

// The start-up probe: a visitor who is not signed in yet asks for its own account once (the run's own expected answer, run.mjs).
const EXPECTED = [
  {
    name: 'start-up probe',
    matches: (e) => e.source === 'network' && /\/api\/v1\/me$/.test(e.url ?? '') && /status of 401\b/.test(e.text ?? ''),
  },
];

// SAMPLER records one entry per animation frame into window.__fu3b: whether the listing is drawn, whether its area is busy, its
// row count, the width of each header cell when the header is drawn, and of each cell of the first body row (0 cells when there is
// nothing to compare).
const SAMPLER = `(() => {
  const state = { frames: [], on: true };
  window.__fu3b = state;
  const widths = (sel) => [...document.querySelectorAll(sel)].map((c) => Math.round(c.getBoundingClientRect().width * 10) / 10);
  const cellWidths = (row) => (row ? [...row.children].map((c) => Math.round(c.getBoundingClientRect().width * 10) / 10) : []);
  const step = () => {
    if (!state.on) return;
    const list = document.querySelector('.files-list');
    state.frames.push({
      t: Math.round(performance.now()),
      list: !!list,
      busy: !!list?.closest('[aria-busy="true"]'),
      // The rows of a busy frame are the folder left behind: they must be inert (no pointer, no key, no focus) (fu3b).
      inert: !!list?.closest('[inert]'),
      rows: list ? list.querySelectorAll('[role="row"]').length : 0,
      // The header is compared only when it is drawn (visible): a header hidden until its columns are measured paints nothing.
      head: list && getComputedStyle(list.querySelector('.ant-table-header') ?? list).visibility === 'visible' ? widths('.files-list .ant-table-thead th') : [],
      body: list ? cellWidths(list.querySelector('tbody tr[data-id]')) : [],
    });
    requestAnimationFrame(step);
  };
  requestAnimationFrame(step);
  return true;
})()`;

// mismatch is how far the header's columns and the first body row's cells are apart, in px (the largest difference): null when
// there is no listing or no row to compare, and Infinity when the column counts differ.
function mismatch(f) {
  if (!f.list || f.head.length === 0 || f.body.length === 0) return null;
  if (f.head.length !== f.body.length) return Infinity;
  return Math.max(...f.head.map((w, i) => Math.abs(w - f.body[i])));
}

const stopAndRead = (page) => page.evaluate(`(() => { window.__fu3b.on = false; return window.__fu3b.frames; })()`);

// sequence is a compact picture of the frames, for the evidence: L when a listing is drawn (b when busy), then its row count.
const sequence = (frames) => frames.map((f) => `${f.list ? 'L' : '-'}${f.busy ? 'b' : ''}${f.rows}`).join(' ');

export async function fu3bChecks(h, { base, accountKey, teamA, dir, browserBinary, MEMBER }) {
  const { check, api, rawPut, rowAppears, openRow, signIn, navigateTo } = h;
  const suffix = Date.now().toString(36);
  const root = `/fu3b-${suffix}`;
  const subA = `fu3b-sub-a-${suffix}`;
  const subB = `fu3b-sub-b-${suffix}`;
  let rootID = null;
  const launch = await launchBrowser(browserBinary, join(dir, 'fu3b-mouse-profile'), { mouse: true });
  const page = new Page(launch.wsUrl);
  page.base = base;
  const mountWorst = [];
  const changeWorst = [];
  const blankFrames = [];
  const inertBusyFrames = [];
  try {
    await page.start(EXPECTED);
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
    await signIn(page, MEMBER);
    // A folder with two sub-folders: the first holds 40 files, so a read takes a few frames.
    rootID = (await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: root, parents: false })).id;
    const aID = (await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `${root}/${subA}`, parents: false })).id;
    await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `${root}/${subB}`, parents: false });
    for (let i = 0; i < 40; i++) {
      await rawPut(base, `/api/v1/folders/${teamA}/files?path=${encodeURIComponent(`${root}/${subA}/file-${String(i).padStart(2, '0')}-${suffix}.txt`)}`, accountKey, `${i}\n`);
    }
    await rawPut(base, `/api/v1/folders/${teamA}/files?path=${encodeURIComponent(`${root}/${subB}/only-${suffix}.txt`)}`, accountKey, 'only\n');
    const folderName = root.slice(1);

    for (let run = 1; run <= 3; run++) {
      // Item 1, a fresh mount: the list is opened from another page, so its first frames are the first frames of the list.
      await navigateTo(page, '/recent');
      await page.waitFor(`location.pathname === '/recent'`, 15000);
      // The files page is gone before the sampler starts, so the next listing is a fresh mount, not the one left on screen.
      await page.waitFor(`!document.querySelector('.files-list')`, 15000);
      await page.evaluate(SAMPLER);
      await navigateTo(page, `/files/${teamA}/${aID}`);
      await rowAppears(page, `file-39-${suffix}.txt`);
      const mountFrames = await stopAndRead(page);
      const mm = mountFrames.map(mismatch).filter((m) => m !== null);
      mountWorst.push(mm.length ? Math.max(...mm) : -1);
      const badMount = mountFrames.find((f) => mismatch(f) > 1);
      if (badMount) console.log(`DIAG run ${run} first bad mount frame ${JSON.stringify(badMount)}`);
      console.log(`DIAG run ${run} mount: ${mountFrames.length} frames, ${sequence(mountFrames)}, worst header/body gap ${mountWorst.at(-1)} px`);

      // Items 1 and 2, a folder change: a click on the other folder, and every frame from the click holds a listing.
      await navigateTo(page, `/files/${teamA}/${aID}`);
      await rowAppears(page, `file-39-${suffix}.txt`);
      await page.evaluate(SAMPLER);
      await navigateTo(page, `/files/${teamA}`);
      await rowAppears(page, folderName);
      await openRow(page, folderName);
      await rowAppears(page, subB);
      await openRow(page, subB);
      await rowAppears(page, `only-${suffix}.txt`);
      const changeFrames = await stopAndRead(page);
      const cm = changeFrames.map(mismatch).filter((m) => m !== null);
      changeWorst.push(cm.length ? Math.max(...cm) : -1);
      const badChange = changeFrames.find((f) => mismatch(f) > 1);
      if (badChange) console.log(`DIAG run ${run} first bad change frame ${JSON.stringify(badChange)}`);
      blankFrames.push(changeFrames.filter((f) => !f.list).length);
      // A frame whose rows are kept (busy) must have them inert: a busy frame with rows that are not inert is a failure.
      inertBusyFrames.push(changeFrames.filter((f) => f.busy && f.list && !f.inert).length);
      console.log(`DIAG run ${run} change: ${changeFrames.length} frames, ${sequence(changeFrames)}, worst header/body gap ${changeWorst.at(-1)} px`);
    }

    check(
      mountWorst.every((w) => w >= 0 && w <= 1),
      `a fresh list's frames: the header and the body columns agree within 1 px in every frame (worst gap per run ${JSON.stringify(mountWorst)} px)`,
    );
    check(
      blankFrames.every((b) => b === 0),
      `from a folder's click to the next listing, every frame holds a listing (frames without one per run ${JSON.stringify(blankFrames)})`,
    );
    check(
      inertBusyFrames.every((b) => b === 0),
      `while a folder's rows are kept, they are inert in every frame (busy frames with live rows per run ${JSON.stringify(inertBusyFrames)})`,
    );
    check(
      changeWorst.every((w) => w >= 0 && w <= 1),
      `a folder change: the header and the body columns agree within 1 px in every frame (worst gap per run ${JSON.stringify(changeWorst)} px)`,
    );
    check(page.errors.length === 0, `the list's browser has no console errors or policy violations (${page.errors.join(' | ') || 'none'})`);

    // Item 7, in the same browser: a New folder dialog the sidebar opened does not survive Back to another address.
    await navigateTo(page, '/recent');
    await page.waitFor(`location.pathname === '/recent'`, 15000);
    // The address bar moves before the router's change is drawn (React Router runs it as a transition), so the shell can still
    // be showing the folder. A click then opens the dialog at the folder, and the change, drawn after it, closes it. Wait for
    // the Recent page itself: the files page gone and the page's own title.
    await page.waitFor(`!document.querySelector('.files-list') && document.title.startsWith('Recent ·')`, 15000);
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="new-button"]:not([disabled])')`, 15000);
    await page.evaluate(`document.querySelector('.shell-sider [data-testid="new-button"]').click(), true`);
    await page.waitFor(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].some((i) => i.textContent.trim() === 'New folder')`, 10000);
    await page.evaluate(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].find((i) => i.textContent.trim() === 'New folder').click(), true`);
    // On a timeout, say what the page showed: the open menus, the dialogs, the messages and the console errors so far.
    await page.waitFor(`!!document.getElementById('newfolder-name')`, 10000).catch(async (err) => {
      const seen = await page.evaluate(`JSON.stringify({ path: location.pathname, menus: [...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].map((i) => i.textContent.trim()), dialogs: [...document.querySelectorAll('[role="dialog"], .ant-modal')].map((d) => (d.getAttribute('aria-label') || d.textContent || '').trim().slice(0, 80)), messages: [...document.querySelectorAll('.ant-message-notice, .ant-notification-notice, [role="alert"]')].map((m) => m.textContent.trim().slice(0, 120)), active: document.activeElement?.tagName + '#' + (document.activeElement?.id || ''), title: document.title })`).catch((e) => `unreadable: ${e.message}`);
      throw new Error(`${err.message.slice(0, 120)}; ${seen}; console: ${page.errors.slice(-5).join(' | ') || 'none'}`);
    });
    await page.evaluate(`history.back(), true`);
    await page.waitFor(`location.pathname.startsWith('/files/${teamA}')`, 15000);
    await page.waitFor(`!document.getElementById('newfolder-name')`, 10000);
    check(true, 'a New folder dialog the sidebar opened closes when Back goes to the folder before (fu3b)');

    // A finger's screen and a hybrid screen, each in its own browser. A finger has no hover and a coarse pointer: the 44 px
    // targets and the list without its hover icons; the 200 px window's submenu scrolls and its last entry is reachable.
    // The pinned header: at phone width, with the selection bar shown, the header is pinned below the toolbar; in
    // Hebrew its columns sit over the body's.
    await layoutChecks(h, page, { base, teamA, aID, navigateTo, rowAppears, suffix });

    // Decision 85: an empty folder's box, its button and a file chosen through the page's picker.
    await emptyChecks(h, page, { base, accountKey, teamA, root, suffix, dir, navigateTo, rowAppears });

    await touchChecks(h, { base, accountKey, teamA, aID, root, dir, browserBinary, MEMBER, suffix, signIn, navigateTo, rowAppears, EXPECTED });
  } finally {
    page.close();
    launch.proc.kill();
    // The folder and its files go, with the account's own key: the section leaves nothing behind.
    if (rootID) await api(base, 'DELETE', `/api/v1/nodes/${rootID}?permanent=true`, accountKey).catch(() => undefined);
  }
}

// EMPTY_READ reads an empty folder's box as the screen draws it: its heading, its instruction, its button and the button's size,
// whether the box is in the window, whether the page scrolls sideways, and the page's direction (decision 85).
const EMPTY_READ = `(() => {
  const box = document.querySelector('[data-testid="empty-folder"]');
  if (!box) return null;
  const r = box.getBoundingClientRect();
  const btn = box.querySelector('button');
  const b = btn.getBoundingClientRect();
  return {
    heading: box.querySelector('h2')?.textContent.trim() ?? null,
    instruction: box.querySelector('p')?.textContent.trim() ?? null,
    button: btn.textContent.trim(),
    buttonH: Math.round(b.height),
    buttonW: Math.round(b.width),
    inView: r.left >= 0 && r.right <= window.innerWidth,
    sideways: document.documentElement.scrollWidth > document.documentElement.clientWidth,
    dir: document.documentElement.dir,
    width: window.innerWidth,
  };
})()`;

// PICKER_HOOK counts the clicks on the page's file inputs and does not open the native picker, which a headless browser cannot
// show. The chosen files go in through DOM.setFileInputFiles afterwards, so the change event is the real one.
const PICKER_HOOK = `(() => {
  window.__fu5picks = 0;
  const original = HTMLInputElement.prototype.click;
  HTMLInputElement.prototype.click = function () {
    if (this.type === 'file') { window.__fu5picks++; return; }
    return original.call(this);
  };
  return true;
})()`;

// emptyChecks (decision 85): an empty folder I can add to shows its box with the heading, the mouse instruction and Upload files;
// Hebrew reads right to left in the same box; the button opens the page's one picker, and a file chosen through CDP lands in the
// folder, after which the box is gone. The folder is removed with the section's root.
async function emptyChecks(h, page, { base, accountKey, teamA, root, suffix, dir, navigateTo, rowAppears }) {
  const { check, api } = h;
  const emptyID = (await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `${root}/empty-${suffix}`, parents: false })).id;
  await navigateTo(page, `/files/${teamA}/${emptyID}`);
  await page.waitFor(`!!document.querySelector('[data-testid="empty-folder"]')`, 15000);
  const en = await page.evaluate(EMPTY_READ);
  check(
    en.heading === 'This folder is empty.' && en.instruction === 'Drag files here from your computer, or use Upload files.' && en.button === 'Upload files',
    `an empty folder shows its box: the heading, the mouse instruction and an Upload files button (${JSON.stringify(en)})`,
  );

  await page.send('Page.navigate', { url: `${base}/files/${teamA}/${emptyID}?lang=he` });
  await page.waitFor(`!!document.querySelector('[data-testid="empty-folder"]') && document.documentElement.dir === 'rtl'`, 15000);
  const he = await page.evaluate(EMPTY_READ);
  check(
    he.heading === 'התיקייה ריקה.' && he.button === 'העלו קבצים' && !he.sideways && he.inView,
    `in Hebrew the empty folder's box reads right to left, with its button, in the window (${JSON.stringify(he)})`,
  );

  await page.send('Page.navigate', { url: `${base}/files/${teamA}/${emptyID}?lang=en` });
  await page.waitFor(`!!document.querySelector('[data-testid="empty-folder"]') && document.documentElement.dir === 'ltr'`, 15000);
  await page.evaluate(PICKER_HOOK);
  await page.evaluate(`(() => { document.querySelector('[data-testid="empty-folder"] button').click(); return true; })()`);
  const picks = await page.evaluate(`window.__fu5picks`);
  check(picks === 1, `the empty folder's Upload files button opens the page's picker once (${picks} clicks on a file input)`);

  const name = `picked-${suffix}.txt`;
  const file = join(dir, name);
  await writeFile(file, 'picked\n');
  await page.setFiles(`[data-testid="browser-toolbar"] input[type=file]:not([webkitdirectory])`, [file]);
  await rowAppears(page, name);
  await page.waitFor(`!document.querySelector('[data-testid="empty-folder"]')`, 10000);
  check(true, 'a file chosen in the empty folder\'s picker lands in the folder, and the box is gone once it has a file');
}

// layoutChecks reads the pinned header at phone width with a selection (the selection bar is the toolbar's taller form), then the
// Hebrew list's columns against the body's. The language is put back to English after it, as the touch checks read English labels.
async function layoutChecks(h, page, { base, teamA, aID, navigateTo, rowAppears, suffix }) {
  const { check } = h;
  const last = `file-39-${suffix}.txt`;
  await page.send('Emulation.setDeviceMetricsOverride', { width: 375, height: 740, deviceScaleFactor: 1, mobile: false });
  await navigateTo(page, `/files/${teamA}/${aID}`);
  await rowAppears(page, last);
  await page.evaluate(`(() => { document.querySelector('[role="row"][aria-label=${JSON.stringify(last)}]').click(); return true; })()`);
  await page.waitFor(`!!document.querySelector('[data-testid="selection-panel"]')`, 10000);
  await page.evaluate(`window.scrollTo(0, 600), true`);
  await page.waitFor(`document.querySelector('.files-list .ant-table-thead th').getBoundingClientRect().top <= 200`, 10000);
  const pin = await page.evaluate(`(() => { const bar = document.querySelector('[role="toolbar"]').getBoundingClientRect(); const th = document.querySelector('.files-list .ant-table-thead th').getBoundingClientRect(); return { bar: Math.round(bar.bottom), head: Math.round(th.top) }; })()`);
  check(pin.head >= pin.bar, `at phone width with the selection bar shown, the list's header is pinned below the toolbar (header top ${pin.head} px, toolbar bottom ${pin.bar} px)`);

  await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
  // The language is asked for in the address: navigateTo takes a path, so the page is opened with its own navigation.
  await page.send('Page.navigate', { url: `${base}/files/${teamA}/${aID}?lang=he` });
  await rowAppears(page, last);
  const cols = await page.evaluate(`(() => { const heads = [...document.querySelectorAll('.files-list .ant-table-thead th')].map((c) => c.getBoundingClientRect()); const row = document.querySelector('.files-list tbody tr[data-id]'); const cells = row ? [...row.children].map((c) => c.getBoundingClientRect()) : []; return { dir: document.documentElement.dir, heads: heads.map((r) => [Math.round(r.left), Math.round(r.width)]), cells: cells.map((r) => [Math.round(r.left), Math.round(r.width)]) }; })()`);
  const gap = cols.heads.length === cols.cells.length && cols.heads.length > 0 ? Math.max(...cols.heads.map((hd, i) => Math.max(Math.abs(hd[0] - cols.cells[i][0]), Math.abs(hd[1] - cols.cells[i][1])))) : Infinity;
  check(cols.dir === 'rtl' && gap <= 1, `in Hebrew the list's header columns sit over the body's (dir ${cols.dir}, worst gap ${gap} px)`);
  await page.send('Page.navigate', { url: `${base}/files/${teamA}/${aID}?lang=en` });
  await rowAppears(page, last);
}

// touchChecks runs the list in two more browsers: a finger's (touch, no hover: the coarse pointer) and a hybrid one (a mouse and a
// touch screen). The finger checks the 44 px More button and the list with no hover icons. (A 200 px window's menu check is not here:
// the root menu opens under the pinned header at that height; see decision 77.)
async function touchChecks(h, { base, accountKey, teamA, aID, root, dir, browserBinary, MEMBER, suffix, signIn, navigateTo, rowAppears, EXPECTED }) {
  const { check, api } = h;
  const centre = (page, selector) =>
    page.evaluate(`(() => { const el = document.querySelector(${q(selector)}); if (!el) return null; const r = el.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
  const moreSel = `.files-list tr[data-id][aria-label="file-39-${suffix}.txt"] button[aria-haspopup="menu"]`;
  const media = (page) =>
    page.evaluate(`({ coarse: matchMedia('(pointer: coarse)').matches, fine: matchMedia('(pointer: fine)').matches, hover: matchMedia('(hover: hover)').matches, noHover: matchMedia('(hover: none)').matches })`);
  const moreSize = (page) =>
    page.evaluate(`(() => { const b = document.querySelector(${q(moreSel)}); if (!b) return null; const r = b.getBoundingClientRect(); return { w: Math.round(r.width), h: Math.round(r.height) }; })()`);

  // ---- a finger: touch only, no hover ----
  const finger = await launchBrowser(browserBinary, join(dir, 'fu3b-finger-profile'), { finger: true });
  const fp = new Page(finger.wsUrl);
  fp.base = base;
  try {
    await fp.start(EXPECTED);
    await fp.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
    await fp.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    await signIn(fp, MEMBER);
    await navigateTo(fp, `/files/${teamA}/${aID}`);
    await rowAppears(fp, `file-39-${suffix}.txt`);
    const m = await media(fp);
    check(m.coarse && m.noHover, `a finger's screen: the pointer is coarse and there is no hover (${JSON.stringify(m)})`);
    const size = await moreSize(fp);
    check(size && size.h >= 44 && size.w >= 44, `on a finger the row's More button is 44 px (${JSON.stringify(size)})`);
    const icons = await fp.evaluate(`document.querySelectorAll('.files-list .row-acts').length`);
    check(icons === 0, `on a finger the list draws no hover icons (${icons} row-acts groups)`);

    // Decision 85 on a finger at phone width (390 px): the empty folder's box says to tap Upload files, its button is 44 px, the
    // box is in the window, and the page does not scroll sideways; then Hebrew, which reads right to left in the same box.
    const emptyID = (await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `${root}/empty-finger-${suffix}`, parents: false })).id;
    await fp.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 740, deviceScaleFactor: 1, mobile: false });
    await navigateTo(fp, `/files/${teamA}/${emptyID}`);
    await fp.waitFor(`!!document.querySelector('[data-testid="empty-folder"]')`, 15000);
    const fe = await fp.evaluate(EMPTY_READ);
    check(
      fe.instruction === 'Tap Upload files to add files from this device.' && fe.buttonH >= 44 && fe.inView && !fe.sideways && fe.width === 390,
      `on a finger at 390 px the empty folder says to tap Upload files, its button is 44 px tall, the box is in the window, and there is no sideways scroll (${JSON.stringify(fe)})`,
    );
    await fp.send('Page.navigate', { url: `${base}/files/${teamA}/${emptyID}?lang=he` });
    await fp.waitFor(`!!document.querySelector('[data-testid="empty-folder"]') && document.documentElement.dir === 'rtl'`, 15000);
    const fh = await fp.evaluate(EMPTY_READ);
    check(
      fh.heading === 'התיקייה ריקה.' && fh.button === 'העלו קבצים' && fh.inView && !fh.sideways,
      `in Hebrew at 390 px on a finger the empty folder reads right to left, its box in the window and no sideways scroll (${JSON.stringify(fh)})`,
    );
  } finally {
    fp.close();
    finger.proc.kill();
  }

  // ---- a hybrid screen: a mouse and a touch screen. The primary pointer is the mouse, so the mouse layout applies. ----
  const hybrid = await launchBrowser(browserBinary, join(dir, 'fu3b-hybrid-profile'), { hybrid: true });
  const hp = new Page(hybrid.wsUrl);
  hp.base = base;
  try {
    await hp.start(EXPECTED);
    await hp.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    await signIn(hp, MEMBER);
    await navigateTo(hp, `/files/${teamA}/${aID}`);
    await rowAppears(hp, `file-39-${suffix}.txt`);
    // The primary pointer is the mouse: (pointer: fine) and (hover: hover) match, and a touch pointer is available too
    // (any-pointer: coarse). (pointer: coarse) reads the primary pointer only, so it does not match: the app's coarse test
    // (useCoarsePointer) is false here, and the mouse layout applies (40 px More button, hover icons).
    const m = await hp.evaluate(`({ fine: matchMedia('(pointer: fine)').matches, primaryCoarse: matchMedia('(pointer: coarse)').matches, anyCoarse: matchMedia('(any-pointer: coarse)').matches, hover: matchMedia('(hover: hover)').matches })`);
    check(m.fine && m.hover && m.anyCoarse && !m.primaryCoarse, `a hybrid screen: the primary pointer is fine with hover, and a touch pointer is available (${JSON.stringify(m)})`);
    const size = await moreSize(hp);
    check(size && size.h === 40 && size.w === 40, `on a hybrid screen the mouse layout applies: the row's More button is 40 px (${JSON.stringify(size)})`);
  } finally {
    hp.close();
    hybrid.proc.kill();
  }
}
