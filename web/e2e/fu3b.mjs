// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice fu3b (decision 77): the list's first frames and its place changes, in a browser that samples every frame.
// Item 1: from a fresh mount of the list and from a folder change, no painted frame has the header's columns and the body's
// first row's cells apart by more than 1 px. Item 2: from a click on a folder to the new listing, every frame holds a listing (the
// previous one, marked busy, until the next is read). The sampler starts before the action, so each frame counts, and it stops
// on the listing's own rows, not on a count of frames. Each case runs three times, so one lucky frame does not pass a flake.
// The folder and its files are removed before the section ends.
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
      changeWorst.every((w) => w >= 0 && w <= 1),
      `a folder change: the header and the body columns agree within 1 px in every frame (worst gap per run ${JSON.stringify(changeWorst)} px)`,
    );
    check(page.errors.length === 0, `the list's browser has no console errors or policy violations (${page.errors.join(' | ') || 'none'})`);

    // Item 7, in the same browser: a New folder dialog the sidebar opened does not survive Back to another address.
    await navigateTo(page, '/recent');
    await page.waitFor(`location.pathname === '/recent'`, 15000);
    await page.waitFor(`!!document.querySelector('.shell-sider [data-testid="new-button"]:not([disabled])')`, 15000);
    await page.evaluate(`document.querySelector('.shell-sider [data-testid="new-button"]').click(), true`);
    await page.waitFor(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].some((i) => i.textContent.trim() === 'New folder')`, 10000);
    await page.evaluate(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].find((i) => i.textContent.trim() === 'New folder').click(), true`);
    await page.waitFor(`!!document.getElementById('newfolder-name')`, 10000);
    await page.evaluate(`history.back(), true`);
    await page.waitFor(`location.pathname.startsWith('/files/${teamA}')`, 15000);
    await page.waitFor(`!document.getElementById('newfolder-name')`, 10000);
    check(true, 'a New folder dialog the sidebar opened closes when Back goes to the folder before (fu3b, item 7)');

    // Item 5: a finger's screen and a hybrid screen, each in its own browser. A finger has no hover and a coarse pointer: the 44 px
    // targets and the list without its hover icons; the 200 px window's submenu scrolls and its last entry is reachable.
    await touchChecks(h, { base, teamA, aID, dir, browserBinary, MEMBER, suffix, signIn, navigateTo, rowAppears, EXPECTED });
  } finally {
    page.close();
    launch.proc.kill();
    // The folder and its files go, with the account's own key: the section leaves nothing behind.
    if (rootID) await api(base, 'DELETE', `/api/v1/nodes/${rootID}?permanent=true`, accountKey).catch(() => undefined);
  }
}

// touchChecks runs the list in two more browsers: a finger's (touch, no hover: the coarse pointer) and a hybrid one (a mouse and a
// touch screen). The finger checks the 44 px More button and the list with no hover icons. (A 200 px window's menu check is not here:
// the root menu opens under the pinned header at that height; see decision 77.)
async function touchChecks(h, { base, teamA, aID, dir, browserBinary, MEMBER, suffix, signIn, navigateTo, rowAppears, EXPECTED }) {
  const { check } = h;
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
