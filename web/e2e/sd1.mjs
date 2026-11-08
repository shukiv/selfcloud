// SPDX-License-Identifier: AGPL-3.0-or-later

// The file browser's slice sd1 checks (decision 104), with the mouse sent through the browser's own input events, as w2 does: a
// folder of the sidebar's tree dragged onto another tree folder, onto a folder row of the listing and onto a breadcrumb; Ctrl copies;
// a folder onto its own child is refused, with the reason; a press that does not move on a tree name still opens the folder; a file
// dragged onto the sidebar's Trash is deleted through Delete's confirmation and Undo brings it back; a tree folder onto the Trash; Ctrl
// over the Trash is refused; the Trash is refused for an item Delete does not allow; and the sidebar scrolls while a drag holds at its
// bottom edge, with a long tree. Every target is measured again after the scroll that brings it into view has settled, right before
// the press. The Trash steps log the drag state (decision 104's diagnosis): whether the drag began, the sidebar's scrollTop, the Trash
// entry's box, and what is under the pointer. Every listener this section adds is removed when it ends.
import { click, clearSelection, itemSel, q } from './media.mjs';
import { dragTo, dropState, ghost, listed, mouse, nameAt, press, setView, sleepMs, waitIn, glide } from './w2.mjs';

const TREE_ROW = (name) => `[data-testid="folder-tree"] .tree-row[data-drop-name="${name}"]`;
const TRASH = '.shell-menu [data-drop-trash]';
const BREADCRUMB = (name) => `[data-drop-name="${name}"]:not(.tree-row)`;
const MANY = 40;

// where is the centre of a target now, measured without scrolling anything; null when nothing matches.
const where = (page, sel) =>
  page.evaluate(`(() => { const el = document.querySelector(${q(sel)}); if (!el) return null; const r = el.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);

// reveal scrolls a target into the middle of its view, lets the page settle (the sidebar follows the place for a moment after a change
// of the tree), and measures it again. A target that is not there is an error here, named, and not a missed press later.
async function reveal(page, sel) {
  const found = await page.evaluate(`(() => { const el = document.querySelector(${q(sel)}); if (!el) return false; el.scrollIntoView({ block: 'center', inline: 'nearest' }); return true; })()`);
  if (!found) throw new Error(`nothing matches ${sel}`);
  await stillScroll(page, sel);
  const p = await where(page, sel);
  if (!p) throw new Error(`nothing matches ${sel} after the scroll`);
  return p;
}

// inView says a point is on the window, where a press can land.
const inView = (page, p) => page.evaluate(`(() => { const x = ${p.x}, y = ${p.y}; return x >= 0 && y >= 0 && x <= window.innerWidth && y <= window.innerHeight; })()`);

// regionState is the sidebar's scroll region now: its scrollTop, its largest scrollTop, and its top and bottom edges.
const regionState = (page) =>
  page.evaluate(`(() => { const s = document.querySelector('.shell-scroll'); const r = s.getBoundingClientRect(); return { top: s.scrollTop, max: s.scrollHeight - s.clientHeight, edges: [Math.round(r.top), Math.round(r.bottom)] }; })()`);

// movesRegion says a point in the sidebar's bottom or top 48 px would scroll the region while the pointer stays there, which is only where
// the region can still move that way (decision 104): the bottom band while the region can scroll down, the top band while it is scrolled
// down. A point in a band the region cannot move from is stable and allowed.
const movesRegion = (page, p) =>
  page.evaluate(`(() => { const s = document.querySelector('.shell-scroll'); const r = s.getBoundingClientRect(); const max = s.scrollHeight - s.clientHeight; return (${p.y} > r.bottom - 48 && s.scrollTop < max - 1) || (${p.y} < r.top + 48 && s.scrollTop > 0); })()`);

// pair reveals the target and then the source, and measures both again once the page is still: a source that the second scroll moved
// out of view is brought back, and the pair is measured again, so no press is aimed where a row was before a scroll.
async function pair(page, fromSel, toSel) {
  // Only the tree's own rows and the Trash entry are sidebar targets (the breadcrumb's selector contains ":not(.tree-row)" and is not one).
  const sidebar = (sel) => sel.startsWith('[data-testid="folder-tree"]') || sel === TRASH;
  const tries = [];
  for (let i = 0; i < 2; i++) {
    const to = await reveal(page, toSel);
    const from = await reveal(page, fromSel);
    const toNow = await where(page, toSel);
    const fromNow = await where(page, fromSel);
    const scroll = await regionState(page);
    tries.push({ to: toNow, from: fromNow, region: scroll, toMoves: toNow ? await movesRegion(page, toNow) : null, fromMoves: fromNow ? await movesRegion(page, fromNow) : null });
    if (
      toNow &&
      fromNow &&
      (await inView(page, toNow)) &&
      (await inView(page, fromNow)) &&
      !(sidebar(toSel) && (await movesRegion(page, toNow))) &&
      !(sidebar(fromSel) && (await movesRegion(page, fromNow)))
    ) {
      // The two are measured after the last scroll: they are where the press will find them.
      return { from: fromNow, to: toNow, to0: to, from0: from };
    }
  }
  throw new Error(`${fromSel} and ${toSel} cannot both be in view, outside the sidebar's edge band, at once; measured ${JSON.stringify(tries)}`);
}

// diag logs what the Trash steps need to say why a drop did or did not happen: whether the drag began (the page marks a drag with
// sc-gesture once the pointer has moved 5 px), the sidebar's scrollTop and its edge, the Trash entry's box, and what is under the
// point. The lines start with DIAG, so they can be read in the run's output.
async function diag(page, label, point) {
  const info = await page.evaluate(`(() => {
    const s = document.querySelector('.shell-scroll');
    const t = document.querySelector(${q(TRASH)});
    const r = t ? t.getBoundingClientRect() : null;
    const at = ${point ? `document.elementFromPoint(${point.x}, ${point.y})` : 'null'};
    const drop = at ? at.closest('[data-drop-folder], [data-drop-trash], [data-tree-drag], [data-id], .shell-menu li') : null;
    const name = (el) => el.tagName.toLowerCase() + (el.getAttribute('data-drop-trash') !== null ? ' [trash]' : '') + (el.dataset && el.dataset.dropName ? ' ' + el.dataset.dropName : '') + (el.dataset && el.dataset.id ? ' id=' + el.dataset.id : '');
    return {
      label: ${q(label)},
      dragBegan: document.documentElement.classList.contains('sc-gesture'),
      scrollTop: s ? s.scrollTop : null,
      regionBottom: s ? Math.round(s.getBoundingClientRect().bottom) : null,
      trash: r ? { top: Math.round(r.top), bottom: Math.round(r.bottom), left: Math.round(r.left), right: Math.round(r.right) } : null,
      trashState: t ? t.dataset.dropState ?? null : null,
      point: ${point ? JSON.stringify(point) : 'null'},
      under: at ? name(at) : null,
      underDrop: drop ? name(drop) : null,
      location: location.pathname,
    };
  })()`);
  console.log(`DIAG ${JSON.stringify(info)}`);
  return info;
}

// dialogsNow logs every dialog and modal wrap on the page, with its count, and for each its display and visibility, aria-hidden, title,
// its first buttons, and whether its box is in the viewport. It changes nothing. Round 9's question is which dialog a confirm would use.
async function dialogsNow(page, label) {
  const info = await page.evaluate(`(() => {
    const nodes = [...document.querySelectorAll('[role="dialog"], .ant-modal-wrap')];
    const vh = window.innerHeight, vw = window.innerWidth;
    return { label: ${q(label)}, count: nodes.length, items: nodes.map((el, i) => {
      const cs = getComputedStyle(el);
      const r = el.getBoundingClientRect();
      const t = el.querySelector('.ant-modal-title');
      return {
        i, tag: el.tagName.toLowerCase(), cls: String(el.className).slice(0, 60),
        display: cs.display, visibility: cs.visibility, ariaHidden: el.getAttribute('aria-hidden'),
        title: (t ? t.textContent : '').trim().slice(0, 60),
        buttons: [...el.querySelectorAll('button')].map((b) => b.textContent.trim()).filter(Boolean).slice(0, 6),
        inView: r.width > 0 && r.height > 0 && r.bottom > 0 && r.right > 0 && r.top < vh && r.left < vw,
        box: [Math.round(r.left), Math.round(r.top), Math.round(r.width), Math.round(r.height)],
      };
    }) };
  })()`);
  console.log(`DIAG dialogs ${JSON.stringify(info)}`);
  return info;
}

// settledPoint returns the centre of the element pick finds, once it has settled. antd zooms a dialog or a notice in when it
// opens (ant-zoom-*), so a press read before the zoom ends lands where the button was mid-zoom: round 9 showed no request sent. The
// dialog and its wrap must carry no antd motion class and have no running animation, and the button's box must be the same on two
// reads 100 ms apart. It also waits for the layer above the target to clear: no other visible .ant-modal-wrap may be over it, and
// elementFromPoint at its centre must be the button or inside it (round 12: a closing Delete dialog covered the Undo notice, and a
// press there reached the dialog, not Undo). It returns null if there is no such button (the caller fails without pressing), and
// throws, naming the last read, if it is still not settled after 10 s.
async function settledPoint(page, pick, what) {
  const read = () =>
    page.evaluate(`(() => {
      const b = (${pick});
      if (!b) return null;
      let motion = false;
      for (let e = b; e && e !== document.body; e = e.parentElement) {
        if (/(^|\\s)ant-(zoom|fade|slide)-/.test(String(e.className))) motion = true;
        if (e.getAnimations && e.getAnimations().some((a) => a.playState !== 'finished')) motion = true;
      }
      const r = b.getBoundingClientRect();
      const x = r.left + r.width / 2;
      const y = r.top + r.height / 2;
      const top = document.elementFromPoint(x, y);
      const hit = top !== null && (top === b || b.contains(top));
      const above = [...document.querySelectorAll('.ant-modal-wrap')].some((w) => !w.contains(b) && getComputedStyle(w).display !== 'none');
      return { x, y, box: [r.left, r.top, r.width, r.height].map(Math.round), motion, hit, above, top: top ? top.tagName.toLowerCase() + ':' + (top.textContent || '').trim().slice(0, 30) : null };
    })()`);
  const ready = (r) => r !== null && !r.motion && r.hit && !r.above;
  const deadline = Date.now() + 10000;
  let last = null;
  for (;;) {
    const first = await read();
    last = first;
    if (ready(first)) {
      await sleepMs(100);
      const second = await read();
      last = second;
      if (ready(second) && JSON.stringify(second.box) === JSON.stringify(first.box)) return { x: second.x, y: second.y };
    }
    if (Date.now() > deadline) {
      // Never there: null, and the caller fails without pressing. There, but not settled: an error that names the last read.
      if (last === null) return null;
      throw new Error(`${what} did not settle in 10 s: last read ${JSON.stringify(last)}`);
    }
    await sleepMs(50);
  }
}

// UNDO_PICK is the visible Undo button of the notice.
const UNDO_PICK = `[...document.querySelectorAll('button')].filter((b) => b.textContent.trim() === 'Undo' && b.offsetParent !== null).pop()`;

// CONFIRM_PICK is the Move to trash button of the topmost visible dialog (antd appends a new dialog after the old ones).
const CONFIRM_PICK = `(() => { const shown = [...document.querySelectorAll('[role="dialog"]')].filter((d) => { const w = d.closest('.ant-modal-wrap'); return (!w || getComputedStyle(w).display !== 'none') && d.getBoundingClientRect().width > 0; }); const top = shown[shown.length - 1]; return top ? [...top.querySelectorAll('button')].find((x) => x.textContent.trim() === 'Move to trash') : null; })()`;

// stillScroll waits until the element's box and the sidebar's scroll are the same on two reads 100 ms apart, so a press is aimed at
// where the row stays. It fails after 5 s, if the sidebar keeps moving.
async function stillScroll(page, sel) {
  const read = () =>
    page.evaluate(`(() => { const el = document.querySelector(${q(sel)}); const s = document.querySelector('.shell-scroll'); if (!el) return null; const r = el.getBoundingClientRect(); return { top: Math.round(r.top), left: Math.round(r.left), scroll: s ? s.scrollTop : null }; })()`);
  const deadline = Date.now() + 5000;
  let last = await read();
  while (Date.now() < deadline) {
    await sleepMs(100);
    const next = await read();
    if (next !== null && last !== null && JSON.stringify(next) === JSON.stringify(last)) return;
    last = next;
  }
  throw new Error(`the sidebar did not stop moving toward ${sel} in 5 s: last read ${JSON.stringify(last)}`);
}

export async function sd1Checks(h, { page, base, accountKey, teamA, teamB }) {
  const { api, rawPut } = h;
  const check = h.check;
  const failures = [];
  const step = async (label, fn) => {
    try {
      await fn();
    } catch (err) {
      failures.push(`${label}: ${err.message}`);
      console.log(`FAIL ${label}: ${err.message}`);
    }
  };
  // A dialog the page raises is answered, so it cannot block the run. Removed when the section ends.
  const onDialog = () => {
    void page.send('Page.handleJavaScriptDialog', { accept: true }).catch(() => undefined);
  };
  page.on('Page.javascriptDialogOpening', onDialog);
  try {
    const mk = (team, path) => api(base, 'POST', `/api/v1/folders/${team}/directories`, accountKey, { path, parents: false });
    const put = (team, path) => rawPut(base, `/api/v1/folders/${team}/files?path=${encodeURIComponent(path)}`, accountKey, Buffer.from(`file ${path}\n`), 'text/plain');
    const idOf = async (team, parent, nameWanted) => (await api(base, 'GET', `/api/v1/folders/${team}/children?path=${encodeURIComponent(parent)}`, accountKey)).items.find((n) => n.name === nameWanted).id;
    const urlOf = async (team, parent, nameWanted) => `${page.base}/files/${team}/${await idOf(team, parent, nameWanted)}`;

    // ---- the files ----
    await mk(teamA, '/sd1');
    for (const d of ['Src', 'Dst', 'Other', 'Other2', 'Ctrl', 'Trashme', 'Many']) await mk(teamA, `/sd1/${d}`);
    await mk(teamA, '/sd1/Src/Carry');
    await mk(teamA, '/sd1/Dst/Deep');
    for (let i = 1; i <= MANY; i++) await mk(teamA, `/sd1/Many/F${String(i).padStart(2, '0')}`);
    for (const f of ['s01.txt', 's02.txt', 's03.txt']) await put(teamA, `/sd1/Src/${f}`);
    // Team B, where the signed-in member is a viewer: a folder with one file, which the member may not delete.
    await mk(teamB, '/sd1ro');
    await put(teamB, '/sd1ro/ro.txt');

    const sdURL = await urlOf(teamA, '/', 'sd1');
    const srcURL = await urlOf(teamA, '/sd1', 'Src');
    const dstURL = await urlOf(teamA, '/sd1', 'Dst');
    const carryURL = await urlOf(teamA, '/sd1/Src', 'Carry');
    const roURL = await urlOf(teamB, '/', 'sd1ro');

    // open shows a listing and waits for an item in it; the view is the list.
    const open = async (url, waitFor) => {
      await page.goto(url);
      await page.waitFor(`!!document.querySelector(${q(itemSel(waitFor))})`, 20000);
      await setView(page, 'List');
      await clearSelection(page);
    };
    // A folder of the tree is opened by its arrow. The place itself is marked and not opened (decision 88), so its own branch is opened too.
    const openRow = async (name) => {
      await page.waitFor(`!!document.querySelector(${q(TREE_ROW(name))})`, 15000);
      await page.evaluate(`(() => { const li = document.querySelector(${q(TREE_ROW(name))}).parentElement; if (li.getAttribute('aria-expanded') !== 'true') li.querySelector('button[aria-label^="Expand"]')?.click(); return true; })()`);
      await page.waitFor(`document.querySelector(${q(TREE_ROW(name))}).parentElement.getAttribute('aria-expanded') === 'true'`, 15000);
      await stillScroll(page, TREE_ROW(name));
    };

    // ---- 1. a tree folder dropped on another tree folder moves it, and both branches show it ----
    await step('a folder of the tree dropped on another folder of the tree moves it, and the tree shows it under its new folder', async () => {
      await open(sdURL, 'Src');
      await openRow('sd1');
      await page.waitFor(`!!document.querySelector(${q(TREE_ROW('Other'))})`, 15000);
      const { from, to } = await pair(page, TREE_ROW('Other'), TREE_ROW('Dst'));
      const mid = await dragTo(page, from, to, { during: () => dropState(page, TREE_ROW('Dst')) });
      check(mid === 'ok', `the tree folder Dst is marked as taking the drop (${mid})`);
      await waitIn(api, base, teamA, '/sd1/Dst', accountKey, 'Other');
      await waitIn(api, base, teamA, '/sd1', accountKey, 'Other', false);
      await openRow('Dst');
      const shown = await page.evaluate(`(() => { const dst = document.querySelector(${q(TREE_ROW('Dst'))}).parentElement; return { under: dst.querySelector('.tree-row[data-drop-name="Other"]') !== null, rows: document.querySelectorAll('.tree-row[data-drop-name="Other"]').length }; })()`);
      check(shown.under === true && shown.rows === 1, `the tree shows Other under Dst, once (${JSON.stringify(shown)})`);
    });

    // ---- 2. a tree folder dropped on a folder row of the listing ----
    await step('a folder of the tree dropped on a folder row of the listing moves into that folder', async () => {
      await open(srcURL, 's01.txt');
      await openRow('sd1');
      const { from, to } = await pair(page, TREE_ROW('Other2'), itemSel('Carry'));
      const mid = await dragTo(page, from, to, { during: () => dropState(page, itemSel('Carry')) });
      check(mid === 'ok', `the listing folder Carry is marked as taking the drop (${mid})`);
      await waitIn(api, base, teamA, '/sd1/Src/Carry', accountKey, 'Other2');
      await waitIn(api, base, teamA, '/sd1', accountKey, 'Other2', false);
    });

    // ---- 3. a tree folder dropped on a breadcrumb ----
    await step('a folder of the tree dropped on a breadcrumb moves into that folder', async () => {
      await open(carryURL, 'Other2');
      await openRow('sd1');
      // Other sits in Dst, which the tree shows open. The breadcrumb Src is above Carry.
      await page.waitFor(`!!document.querySelector(${q(TREE_ROW('Other'))})`, 15000);
      const { from, to } = await pair(page, TREE_ROW('Other'), BREADCRUMB('Src'));
      const mid = await dragTo(page, from, to, { during: () => dropState(page, BREADCRUMB('Src')) });
      check(mid === 'ok', `the breadcrumb Src is marked as taking the drop (${mid})`);
      await waitIn(api, base, teamA, '/sd1/Src', accountKey, 'Other');
    });

    // ---- 4. Ctrl copies a tree folder: the source stays ----
    await step('a folder of the tree dropped with Ctrl held is copied, and the source stays', async () => {
      await open(sdURL, 'Src');
      await openRow('sd1');
      const { from, to } = await pair(page, TREE_ROW('Ctrl'), TREE_ROW('Dst'));
      const mid = await dragTo(page, from, to, { ctrl: true, during: () => ghost(page) });
      check(mid !== null && mid.mode === 'copy' && mid.text.includes('Copy to'), `the ghost says Copy to while Ctrl is held (${JSON.stringify(mid)})`);
      await waitIn(api, base, teamA, '/sd1/Dst', accountKey, 'Ctrl');
      check((await listed(api, base, teamA, '/sd1', accountKey)).includes('Ctrl'), 'the source Ctrl stays in sd1');
    });

    // ---- 5. a folder onto its own child is refused, and nothing moves ----
    await step('a folder of the tree dropped on its own child in the listing is refused, with the reason', async () => {
      await open(dstURL, 'Deep');
      await openRow('sd1');
      const { from, to } = await pair(page, TREE_ROW('Dst'), itemSel('Deep'));
      const state = await dragTo(page, from, to, { during: async () => ({ state: await dropState(page, itemSel('Deep')), ghost: await ghost(page) }) });
      check(state.state === 'refused', `Dst onto its child Deep is refused (${state.state})`);
      check(state.ghost !== null && state.ghost.text.includes('A folder cannot go into itself.'), `the ghost says why (${JSON.stringify(state.ghost)})`);
      check((await listed(api, base, teamA, '/sd1', accountKey)).includes('Dst'), 'Dst did not move');
      check((await listed(api, base, teamA, '/sd1/Dst', accountKey)).includes('Deep'), 'Deep is still in Dst');
    });

    // ---- 6. a press that does not move on a tree name opens the folder, as before ----
    await step('a press that does not move on a tree folder name opens the folder in the listing', async () => {
      await open(sdURL, 'Src');
      await openRow('sd1');
      await page.waitFor(`!!document.querySelector(${q(TREE_ROW('Trashme'))})`, 15000);
      const at = await reveal(page, `${TREE_ROW('Trashme')} a`);
      await click(page, at);
      const want = await idOf(teamA, '/sd1', 'Trashme');
      await page.waitFor(`location.pathname.endsWith(${q(`/${want}`)})`, 15000);
    });

    // ---- 7. a file dropped on the sidebar's Trash is deleted through Delete; Undo brings it back ----
    await step('a file dropped on the sidebar\'s Trash goes through Delete, and Undo brings it back', async () => {
      await open(srcURL, 's01.txt');
      await openRow('sd1');
      const trash = await reveal(page, TRASH);
      const from = await nameAt(page, 's01.txt');
      await diag(page, 'step 7: before the press', from);
      const mid = await dragTo(page, from, trash, {
        during: async () => {
          await diag(page, 'step 7: held over the Trash', trash);
          return dropState(page, TRASH);
        },
      });
      await diag(page, 'step 7: after the release', null);
      check(mid === 'ok', `the Trash is marked as taking the drop (${mid})`);
      await page.waitFor(`!!document.querySelector('[role="dialog"]')`, 15000);
      const confirm = await settledPoint(page, CONFIRM_PICK, 'the Delete confirmation\'s Move to trash button');
      check(confirm !== null, 'the Delete confirmation offers Move to trash');
      if (confirm === null) throw new Error('the Delete confirmation did not open, so nothing was pressed');
      await click(page, confirm);
      await waitIn(api, base, teamA, '/sd1/Src', accountKey, 's01.txt', false);
      await page.waitFor(`[...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'Undo')`, 15000);
      // Undo is a pointer click once its notice has settled: the notice's button is still animating in when it first shows, as the Delete
      // button was in step 8. (Decision 104's round two pressed it as a DOM click for that reason; see the review's M3.)
      // Undo is a real pointer click, once the notice's button is settled and is what is under the pointer (see settledPoint: the closing
      // Delete dialog covered it in round 12, and a press there went to the dialog).
      const undo = await settledPoint(page, UNDO_PICK, 'the Undo button');
      if (undo === null) throw new Error('the Undo button did not appear, so nothing was pressed');
      await click(page, undo);
      await waitIn(api, base, teamA, '/sd1/Src', accountKey, 's01.txt');
    });

    // ---- 8. a tree folder dropped on the Trash, which is below the view: the drag holds in the bottom edge band until the region reaches
    // its end, and the Trash is reached by that edge scroll (covers the edge scroll reaching a lower row, decision 104) ----
    await step('a folder of the tree is dragged down by the edge scroll to a lower row, the Trash, and is deleted through Delete', async () => {
      await open(sdURL, 'Src');
      await openRow('sd1');
      const from = await reveal(page, TREE_ROW('Trashme'));
      if (await movesRegion(page, from)) throw new Error('Trashme is in a band that moves the region, so its press would not be stable');
      const region = () =>
        page.evaluate(`(() => { const s = document.querySelector('.shell-scroll'); const r = s.getBoundingClientRect(); return { top: s.scrollTop, max: s.scrollHeight - s.clientHeight, bottom: r.bottom, left: r.left, width: r.width }; })()`);
      const before = await region();
      await diag(page, 'step 8: before the press', from);
      const bandPoint = { x: before.left + Math.min(60, before.width / 2), y: before.bottom - 12 };
      await mouse(page, 'mouseMoved', from);
      await mouse(page, 'mousePressed', from);
      try {
        await glide(page, from, bandPoint, {});
        // Held in the band until the region is at its end and stays there for 300 ms. Nothing is released over the Trash while it passes
        // under the pointer: the release is made only after this.
        const start = Date.now();
        let since = null;
        let now = await region();
        for (;;) {
          now = await region();
          if (now.top >= now.max - 1) {
            if (since === null) since = Date.now();
            if (Date.now() - since >= 300) break;
          } else {
            since = null;
          }
          if (Date.now() - start > 15000) {
            throw new Error(`the region did not reach its end in 15 s held in the band: scrollTop ${now.top} of ${now.max}, from ${before.top}`);
          }
          await sleepMs(100);
        }
        const trash = await where(page, TRASH);
        if (!trash) throw new Error('the Trash entry is not on the page after the scroll');
        await diag(page, `step 8: region at its end (scrollTop ${now.top} of ${now.max}, from ${before.top}), held in the band`, bandPoint);
        await glide(page, bandPoint, trash, {});
        // The state is read once the Trash has one (ok or refused), so the check below reads what the page shows.
        await page.waitFor(`document.querySelector(${q(TRASH)})?.dataset.dropState != null`, 3000).catch(() => undefined);
        const state = await dropState(page, TRASH);
        await diag(page, 'step 8: over the Trash, before the release', trash);
        check(state === 'ok', `the Trash is marked as taking the drop, reached by the edge scroll (${state})`);
        await mouse(page, 'mouseReleased', trash);
      } catch (err) {
        await mouse(page, 'mouseReleased', bandPoint).catch(() => undefined);
        throw err;
      }
      await diag(page, 'step 8: after the release', null);
      await page.waitFor(`!!document.querySelector('[role="dialog"]')`, 15000);
      const trashmeID = await idOf(teamA, '/sd1', 'Trashme');
      // The press waits until the dialog has settled (settledPoint); the assertion below is unchanged.
      const confirm = await settledPoint(page, CONFIRM_PICK, 'the Delete confirmation\'s Move to trash button');
      check(confirm !== null, 'the Delete confirmation opens for the folder');
      if (confirm === null) throw new Error('the Delete confirmation did not open, so nothing was pressed');
      const sentFrom = page.sent.length;
      const responses = [];
      const onResponse = (p) => responses.push({ url: p.response.url.replace(/^https?:\/\/[^/]+/, ''), status: p.response.status });
      page.on('Network.responseReceived', onResponse);
      try {
        await click(page, confirm);
        await waitIn(api, base, teamA, '/sd1', accountKey, 'Trashme', false);
      } catch (err) {
        // Failure-time diagnosis only: the dialogs on the page, the requests sent after the press and their responses, and what the
        // server says about the folder.
        await dialogsNow(page, 'step 8: failure, after the press');
        const sent = page.sent.slice(sentFrom).map((r) => `${r.method} ${r.url.replace(/^https?:\/\/[^/]+/, '')}`);
        console.log(`DIAG step 8: requests sent after the press ${JSON.stringify(sent)}`);
        console.log(`DIAG step 8: responses ${JSON.stringify(responses)}`);
        const trash = await api(base, 'GET', `/api/v1/trash?folder=${teamA}&limit=200`, accountKey);
        const inTrash = trash.items.some((n) => n.id === trashmeID);
        const children = await listed(api, base, teamA, '/sd1', accountKey);
        console.log(`DIAG step 8: server says trashed=${inTrash}; the folder still listed under sd1=${children.includes('Trashme')}`);
        throw err;
      } finally {
        page.off('Network.responseReceived', onResponse);
      }
    });

    // ---- 9. Ctrl over the Trash is refused, and nothing is deleted ----
    await step('Ctrl over the Trash is refused: nothing is copied or deleted', async () => {
      await open(srcURL, 's02.txt');
      await openRow('sd1');
      const trash = await reveal(page, TRASH);
      const from = await nameAt(page, 's02.txt');
      const state = await dragTo(page, from, trash, {
        ctrl: true,
        during: async () => {
          await diag(page, 'step 9: held over the Trash with Ctrl', trash);
          return { state: await dropState(page, TRASH), ghost: await ghost(page) };
        },
      });
      check(state.state === 'refused', `the Trash is refused with Ctrl (${state.state})`);
      check(state.ghost !== null && state.ghost.text.includes('Nothing is copied to the Trash'), `the ghost says why (${JSON.stringify(state.ghost)})`);
      check(await page.evaluate(`!document.querySelector('[role="dialog"]')`), 'no confirmation opens');
      await waitIn(api, base, teamA, '/sd1/Src', accountKey, 's02.txt');
    });

    // ---- 10. the Trash is refused for an item Delete does not allow (a viewer's file) ----
    await step('the Trash is refused for a file the member may only read', async () => {
      await open(roURL, 'ro.txt');
      const trash = await reveal(page, TRASH);
      const from = await nameAt(page, 'ro.txt');
      const state = await dragTo(page, from, trash, {
        during: async () => {
          await diag(page, 'step 10: held over the Trash, viewer', trash);
          return dropState(page, TRASH);
        },
      });
      check(state === 'refused', `the Trash is refused for a viewer's file (${state})`);
      check(await page.evaluate(`!document.querySelector('[role="dialog"]')`), 'no confirmation opens for a viewer\'s file');
      await waitIn(api, base, teamB, '/sd1ro', accountKey, 'ro.txt');
    });

    // ---- 11. the sidebar scrolls while a drag holds at its bottom edge, with a long tree ----
    await step('the sidebar scrolls while a drag holds at its bottom edge, and Escape ends the drag without a drop', async () => {
      await open(sdURL, 'Src');
      await openRow('sd1');
      await openRow('Many');
      await page.waitFor(`!!document.querySelector(${q(TREE_ROW('F40'))})`, 15000);
      const region = await page.evaluate(`(() => { const el = document.querySelector('.shell-scroll'); return { tall: el.scrollHeight - el.clientHeight, top: el.scrollTop }; })()`);
      check(region.tall > 0, `the sidebar's tree is taller than its region (${JSON.stringify(region)})`);
      const from = await reveal(page, TREE_ROW('F01'));
      const edge = await page.evaluate(`(() => { const r = document.querySelector('.shell-scroll').getBoundingClientRect(); return { x: r.left + Math.min(60, r.width / 2), y: r.bottom - 12 }; })()`);
      await mouse(page, 'mouseMoved', from);
      await mouse(page, 'mousePressed', from);
      await glide(page, from, edge, {});
      // The fixed 1500 ms hold is the behaviour under test: the sidebar scrolls for as long as the drag holds at its edge, so the scroll is
      // read after that hold. There is no state to wait for here.
      await sleepMs(1500);
      const after = await page.evaluate(`document.querySelector('.shell-scroll').scrollTop`);
      check(after > region.top, `the sidebar scrolled down while the drag held at its bottom edge (${region.top} to ${after})`);
      await press(page, 'Escape');
      await mouse(page, 'mouseReleased', edge);
      // The drag is over when the page takes its drag mark away (sc-gesture), so the listing is read after that.
      await page.waitFor(`!document.documentElement.classList.contains('sc-gesture')`, 3000).catch(() => undefined);
      check((await listed(api, base, teamA, '/sd1/Many', accountKey)).includes('F01'), 'Escape ended the drag, and F01 did not move');
    });
  } finally {
    page.off('Page.javascriptDialogOpening', onDialog);
  }
  if (failures.length > 0) throw new Error(`sd1: ${failures.length} step(s) failed:\n${failures.join('\n')}`);
}
