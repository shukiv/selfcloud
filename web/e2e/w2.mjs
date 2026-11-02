// SPDX-License-Identifier: AGPL-3.0-or-later

// The file browser's slice w2 checks, with the mouse sent through the browser's own input events (Input.dispatchMouseEvent,
// with the buttons held and Ctrl as a modifier): an item dragged onto a folder row, a tile, a breadcrumb and the folder tree,
// moved, or copied with Ctrl; a refused place shown as refused; the selection dragged as a whole; files from the desktop that
// still upload; the rubber band on empty space, in the short list, in tiles and in a long list that scrolls under the pointer;
// the folder tree, its saved state, and its absence at phone width; and the five fixes from w1's notes. With
// SELFCLOUD_W2_SHOTS set to a folder, the run also saves the pictures for the slice.
import { writeFile, mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { barButtons, browserTitle, click, clearSelection, itemSel, q, setLanguage, setPhone, setTheme } from './media.mjs';

const sleepMs = (ms) => new Promise((r) => setTimeout(r, ms));
const MODS = { alt: 1, ctrl: 2, shift: 8 };
const KEYS = { Escape: ['Escape', 'Escape', 27], x: ['x', 'KeyX', 88], v: ['v', 'KeyV', 86], c: ['c', 'KeyC', 67], a: ['a', 'KeyA', 65] };
const LONG = 40;
const MANY = 55;
const name2 = (p, i) => `${p}${String(i).padStart(2, '0')}.txt`;

async function press(page, key, mods = []) {
  const [k, code, vk] = KEYS[key];
  const modifiers = mods.reduce((n, m) => n | MODS[m], 0);
  const base = { key: k, code, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk, modifiers };
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', ...base });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', ...base });
  await sleepMs(250);
}

// centre is the middle of the first element the selector names, scrolled into the window only if it is not in it (a scroll
// between two points of one drag would move the first).
async function centre(page, selector, block = 'nearest') {
  const p = await page.evaluate(`(() => {
    const el = document.querySelector(${q(selector)});
    if (!el) return null;
    el.scrollIntoView({ block: ${q(block)}, inline: 'nearest' });
    const r = el.getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  })()`);
  if (!p) throw new Error(`nothing matches ${selector}`);
  return p;
}

// nameAt is the centre of an item's name, which a press there is a press on the item.
const nameAt = async (page, item) => {
  await centre(page, itemSel(item));
  return page.evaluate(`(() => { const el = document.querySelector(${q(itemSel(item))}); const r = (el.querySelector('bdi') ?? el).getBoundingClientRect(); return { x: r.left + Math.min(r.width / 2, 40), y: r.top + r.height / 2 }; })()`);
};

// mouse sends one mouse event; held says the left button is down.
const mouse = (page, type, { x, y }, { held = false, ctrl = false } = {}) =>
  page.send('Input.dispatchMouseEvent', {
    type,
    x,
    y,
    button: type === 'mouseMoved' ? 'none' : 'left',
    buttons: type === 'mouseReleased' ? 0 : held || type === 'mousePressed' ? 1 : 0,
    clickCount: type === 'mouseMoved' ? 0 : 1,
    modifiers: ctrl ? MODS.ctrl : 0,
  });

// steps moves the held pointer from a to b in a few moves, as a hand does.
async function glide(page, a, b, opts, n = 6) {
  for (let i = 1; i <= n; i++) {
    await mouse(page, 'mouseMoved', { x: a.x + ((b.x - a.x) * i) / n, y: a.y + ((b.y - a.y) * i) / n }, { ...opts, held: true });
    await sleepMs(20);
  }
}

// dragTo presses on `from`, glides to `to`, runs `during` while the button is still down, then lets go. It returns what during returned.
async function dragTo(page, from, to, { ctrl = false, during } = {}) {
  await mouse(page, 'mouseMoved', from);
  await mouse(page, 'mousePressed', from, { ctrl });
  await glide(page, from, to, { ctrl });
  await sleepMs(150);
  const got = during ? await during() : undefined;
  await mouse(page, 'mouseReleased', to, { ctrl });
  await sleepMs(300);
  return got;
}

// The state of the place under a drag, as the page shows it: its attribute, and the ghost beside the pointer.
const dropState = (page, selector) => page.evaluate(`document.querySelector(${q(selector)})?.dataset.dropState ?? null`);
const ghost = (page) =>
  page.evaluate(`(() => { const g = document.querySelector('[data-testid="drag-ghost"]'); return g ? { state: g.dataset.state, mode: g.dataset.mode, text: g.textContent } : null; })()`);

// listed is the names the API lists in a folder.
async function listed(api, base, team, path, key) {
  return (await api(base, 'GET', `/api/v1/folders/${team}/children?path=${encodeURIComponent(path)}&limit=500`, key)).items.map((n) => n.name);
}

async function waitIn(api, base, team, path, key, name, present = true, ms = 15000) {
  const end = Date.now() + ms;
  for (;;) {
    const names = await listed(api, base, team, path, key);
    if (names.includes(name) === present) return names;
    if (Date.now() > end) throw new Error(`${path} ${present ? 'lacks' : 'still has'} ${name} (has ${names.join(', ')})`);
    await sleepMs(300);
  }
}

async function setView(page, view) {
  const want = view === 'List' ? 'list' : 'grid';
  const toggle = '[data-testid="view-toggle"]';
  await page.waitFor(`!!document.querySelector(${q(toggle)})?.dataset.view`, 15000);
  const p = await page.evaluate(`(() => { const b = document.querySelector(${q(toggle)}); if (b.dataset.view !== ${q(want)}) return null; const r = b.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
  if (p !== null) await click(page, p);
  await sleepMs(300);
}

// hitAt says what is under a point, for a failure message.
const hitAt = (page, { x, y }) =>
  page.evaluate(`(() => { const el = document.elementFromPoint(${x}, ${y}); if (!el) return 'nothing'; const d = el.closest('[data-drop-folder]'); return el.tagName + ' ' + (el.className || '') + ' drop=' + (d ? d.dataset.dropName + '/' + d.dataset.dropState : '-') + ' scrollY=' + scrollY; })()`);

const present = (page, item) => page.evaluate(`!!document.querySelector(${q(itemSel(item))})`);
const selectedNames = (page) =>
  page.evaluate(`[...document.querySelectorAll('[role="row"][aria-selected="true"], [role="option"][aria-selected="true"]')].map((r) => r.getAttribute('aria-label')).sort()`);

export async function w2Checks(h, { page, base, accountKey, accountID, memberID, teamA, teamB, dir }) {
  const { api, rawPut } = h;
  const failures = [];
  const wanted = process.env.SELFCLOUD_W2_STEPS ? new RegExp(process.env.SELFCLOUD_W2_STEPS) : null;
  const step = async (label, fn) => {
    if (wanted && !wanted.test(label)) return;
    try {
      await fn();
    } catch (err) {
      failures.push(`${label}: ${err.message}`);
      console.log(`FAIL ${label}: ${err.message}`);
    }
  };
  const check = h.check;
  // A dialog the page raises (the leave-the-page warning while an upload is still being closed) is answered, so it cannot block the run.
  page.on('Page.javascriptDialogOpening', () => {
    void page.send('Page.handleJavaScriptDialog', { accept: true }).catch(() => undefined);
  });
  const mk = (team, path) => api(base, 'POST', `/api/v1/folders/${team}/directories`, accountKey, { path, parents: false });
  const put = async (team, path) => {
    for (let attempt = 1; ; attempt++) {
      try {
        await rawPut(base, `/api/v1/folders/${team}/files?path=${encodeURIComponent(path)}`, accountKey, Buffer.from(`file ${path}\n`), 'text/plain');
        return;
      } catch (err) {
        if (attempt >= 3 || !String(err.message).includes('could not be read')) throw err;
      }
    }
  };

  // ---- the files ----
  await mk(teamA, '/w2');
  for (const d of ['drag', 'drag/Alpha', 'drag/Beta', 'drag/Beta/Inner', 'long', 'short', 'many']) await mk(teamA, `/w2/${d}`);
  for (let i = 1; i <= 14; i++) await put(teamA, `/w2/drag/${name2('g', i)}`);
  for (let i = 1; i <= LONG; i++) await put(teamA, `/w2/long/${name2('l', i)}`);
  for (let i = 1; i <= 6; i++) await put(teamA, `/w2/short/${name2('s', i)}`);
  for (let i = 1; i <= MANY; i++) await put(teamA, `/w2/many/${name2('m', i)}`);
  // Team B, where the signed-in member is a viewer: a folder and a file, both read only.
  await mk(teamB, '/w2');
  await mk(teamB, '/w2/Sub');
  await put(teamB, '/w2/ro.txt');
  const idOf = async (team, parent, nameWanted) => (await api(base, 'GET', `/api/v1/folders/${team}/children?path=${encodeURIComponent(parent)}`, accountKey)).items.find((n) => n.name === nameWanted).id;
  const urlOf = async (team, parent, nameWanted) => `${page.base}/files/${team}/${await idOf(team, parent, nameWanted)}`;
  const dragURL = await urlOf(teamA, '/w2', 'drag');
  const longURL = await urlOf(teamA, '/w2', 'long');
  const shortURL = await urlOf(teamA, '/w2', 'short');
  const manyURL = await urlOf(teamA, '/w2', 'many');
  const roURL = await urlOf(teamB, '/', 'w2');

  const open = async (url, view, wait) => {
    await page.goto(url);
    await page.waitFor(`!!document.querySelector(${q(itemSel(wait))})`, 20000);
    await setView(page, view);
    await clearSelection(page);
  };

  // betaKey is the tree key (the node id) of the folder called Beta in the tree.
  const betaKey = () => page.evaluate(`document.querySelector('[data-testid="folder-tree"] .tree-row[data-drop-name="Beta"]')?.parentElement?.dataset.treeKey ?? ''`);

  // ---- 1. a real mouse drag of a file onto a folder row moves it, and the folder is shown as taking it ----
  await step('a mouse drag of a file onto a folder row moves it', async () => {
    await open(dragURL, 'List', 'g01.txt');
    const from = await nameAt(page, 'g01.txt');
    const to = await centre(page, itemSel('Alpha'));
    const mid = await dragTo(page, from, to, {
      during: async () => ({ state: await dropState(page, itemSel('Alpha')), ghost: await ghost(page), body: await page.evaluate(`document.documentElement.classList.contains('sc-gesture')`) }),
    });
    check(mid.state === 'ok', `the folder under the pointer is marked as taking the drop (${mid.state})`);
    check(mid.ghost !== null && mid.ghost.mode === 'move' && mid.ghost.text.includes('Move to') && mid.ghost.text.includes('Alpha'), `the ghost says Move to Alpha (${JSON.stringify(mid.ghost)})`);
    check(mid.body === true, 'the page is in its gesture state (no text selection) during the drag');
    await waitIn(api, base, teamA, '/w2/drag/Alpha', accountKey, 'g01.txt');
    await waitIn(api, base, teamA, '/w2/drag', accountKey, 'g01.txt', false);
    check(!(await present(page, 'g01.txt')), 'the moved file left the listing');
    check((await ghost(page)) === null && (await dropState(page, itemSel('Alpha'))) === null, 'the ghost and the mark are gone after the drop');
    check(await page.evaluate(`document.body.innerText.includes('Moved')`), 'the move offers its undo, as a paste does');
    check((await browserTitle(page)) === 'drag', 'the drag did not open the folder it was dropped on');
  });

  // ---- 2. Ctrl copies: the source stays ----
  await step('a mouse drag with Ctrl held copies', async () => {
    await open(dragURL, 'List', 'g02.txt');
    const from = await nameAt(page, 'g02.txt');
    const to = await centre(page, itemSel('Alpha'));
    const mid = await dragTo(page, from, to, { ctrl: true, during: () => ghost(page) });
    check(mid && mid.mode === 'copy' && mid.text.includes('Copy to'), `the ghost says Copy to while Ctrl is held (${JSON.stringify(mid)})`);
    await waitIn(api, base, teamA, '/w2/drag/Alpha', accountKey, 'g02.txt');
    const rest = await listed(api, base, teamA, '/w2/drag', accountKey);
    check(rest.includes('g02.txt'), 'the copied file is still in its folder');
  });

  // ---- 3. refused places show as refused, and nothing moves ----
  await step('a folder dropped on itself is shown as refused, and nothing moves', async () => {
    await open(dragURL, 'List', 'Beta');
    const from = await nameAt(page, 'Beta');
    const to = await centre(page, itemSel('Beta'));
    const mid = await dragTo(page, from, to, { during: async () => ({ state: await dropState(page, itemSel('Beta')), ghost: await ghost(page), hit: await hitAt(page, to), from: await hitAt(page, from) }) });
    check(mid.state === 'refused', `the place is marked refused (${JSON.stringify(mid)})`);
    check(mid.ghost?.state === 'refused' && mid.ghost.text.includes('A folder cannot go into itself.'), `the ghost says why (${JSON.stringify(mid.ghost)})`);
    await page.waitFor(`document.body.innerText.includes('A folder cannot go into itself.')`, 5000);
    check((await listed(api, base, teamA, '/w2/drag', accountKey)).includes('Beta'), 'Beta is still where it was');
  });

  await step('a place the caller may not add to is refused, in a space where the caller is a viewer', async () => {
    await open(roURL, 'List', 'ro.txt');
    const from = await nameAt(page, 'ro.txt');
    const to = await centre(page, itemSel('Sub'));
    const mid = await dragTo(page, from, to, { during: async () => ({ state: await dropState(page, itemSel('Sub')), ghost: await ghost(page), hit: await hitAt(page, to) }) });
    check(mid.state === 'refused', `a viewer's folder is marked refused (${JSON.stringify(mid)})`);
    check(mid.ghost?.text.includes('You cannot add items to this folder.') || mid.ghost?.text.includes('You cannot move these items'), `the ghost says why (${JSON.stringify(mid.ghost)})`);
    check((await listed(api, base, teamB, '/w2/Sub', accountKey)).length === 0, 'nothing went into the viewer folder');
    check((await listed(api, base, teamB, '/w2', accountKey)).includes('ro.txt'), 'the viewer file did not move');
  });

  // ---- 4. dragging a selected item drags the whole selection ----
  await step('dragging a selected item drags the whole selection', async () => {
    await open(dragURL, 'List', 'g03.txt');
    await click(page, await nameAt(page, 'g03.txt'));
    await mouse(page, 'mouseMoved', await nameAt(page, 'g04.txt'));
    await mouse(page, 'mousePressed', await nameAt(page, 'g04.txt'), { ctrl: true });
    await mouse(page, 'mouseReleased', await nameAt(page, 'g04.txt'), { ctrl: true });
    await mouse(page, 'mouseMoved', await nameAt(page, 'g05.txt'));
    await mouse(page, 'mousePressed', await nameAt(page, 'g05.txt'), { ctrl: true });
    await mouse(page, 'mouseReleased', await nameAt(page, 'g05.txt'), { ctrl: true });
    const sel = await selectedNames(page);
    check(sel.join() === 'g03.txt,g04.txt,g05.txt', `three files are selected (${sel.join()})`);
    const from = await nameAt(page, 'g04.txt');
    const to = await centre(page, itemSel('Beta'));
    const mid = await dragTo(page, from, to, { during: () => ghost(page) });
    check(mid && mid.text.includes('3'), `the ghost counts the selection (${JSON.stringify(mid)})`);
    for (const f of ['g03.txt', 'g04.txt', 'g05.txt']) await waitIn(api, base, teamA, '/w2/drag/Beta', accountKey, f);
  });

  // ---- 5. a tile is a target too ----
  await step('a tile takes a drop', async () => {
    await open(dragURL, 'Tiles', 'g06.txt');
    const from = await nameAt(page, 'g06.txt');
    const to = await centre(page, itemSel('Alpha'));
    const mid = await dragTo(page, from, to, { during: () => dropState(page, itemSel('Alpha')) });
    check(mid === 'ok', `the folder tile is marked as taking the drop (${mid})`);
    await waitIn(api, base, teamA, '/w2/drag/Alpha', accountKey, 'g06.txt');
    await setView(page, 'List');
  });

  // ---- 6. a breadcrumb is a target ----
  await step('a breadcrumb takes a drop', async () => {
    const alphaURL = await urlOf(teamA, '/w2/drag', 'Alpha');
    await open(alphaURL, 'List', 'g06.txt');
    const links = await page.evaluate(`[...document.querySelectorAll('header nav a')].map((a) => a.textContent.trim())`);
    check(links.includes('drag'), `the breadcrumbs link the folder above (${links.join(' / ')})`);
    const from = await nameAt(page, 'g06.txt');
    const to = await centre(page, `header nav a[data-drop-name="drag"]`);
    const mid = await dragTo(page, from, to, { during: () => dropState(page, `header nav a[data-drop-name="drag"]`) });
    check(mid === 'ok', `the crumb is marked as taking the drop (${mid})`);
    await waitIn(api, base, teamA, '/w2/drag', accountKey, 'g06.txt');
    await waitIn(api, base, teamA, '/w2/drag/Alpha', accountKey, 'g06.txt', false);
  });

  // ---- 7. the folder tree: beside the listing, lazy, a target, and its state is kept ----
  await step('the folder tree is lazy, takes a drop, and keeps its state for the person', async () => {
    await open(dragURL, 'List', 'g07.txt');
    await page.waitFor(`!!document.querySelector('[data-testid="folder-tree"] [role="treeitem"]')`, 15000);
    const roots = await page.evaluate(`[...document.querySelectorAll('[data-testid="folder-tree"] [role="tree"] > [role="treeitem"] > .tree-row')].map((r) => r.textContent.trim())`);
    check(roots.length >= 2, `the tree lists the spaces (${roots.join(' / ')})`);
    // The way to the place shown is open: Team A's w2, then drag, whose folders are in the tree.
    await page.waitFor(`[...document.querySelectorAll('[data-testid="folder-tree"] .tree-row')].some((r) => r.dataset.dropName === 'Alpha')`, 15000);
    const lazy = await page.evaluate(`[...document.querySelectorAll('[data-testid="folder-tree"] .tree-row')].some((r) => r.dataset.dropName === 'Inner')`);
    check(lazy === false, 'a folder that was not opened has not been read: Inner is not in the tree');
    // Open Beta by its arrow: its folder Inner appears.
    const arrow = await page.evaluate(`(() => { const r = [...document.querySelectorAll('[data-testid="folder-tree"] .tree-row')].find((x) => x.dataset.dropName === 'Beta'); const b = r?.querySelector('button'); if (!b) return null; const bb = b.getBoundingClientRect(); return { x: bb.left + bb.width / 2, y: bb.top + bb.height / 2 }; })()`);
    check(arrow !== null, 'Beta has an arrow in the tree');
    await click(page, arrow);
    await page.waitFor(`[...document.querySelectorAll('[data-testid="folder-tree"] .tree-row')].some((r) => r.dataset.dropName === 'Inner')`, 10000);
    // Drag a file onto Inner in the tree.
    const from = await nameAt(page, 'g07.txt');
    const to = await centre(page, `[data-testid="folder-tree"] .tree-row[data-drop-name="Inner"]`);
    const mid = await dragTo(page, from, to, { during: () => dropState(page, `[data-testid="folder-tree"] .tree-row[data-drop-name="Inner"]`) });
    check(mid === 'ok', `the tree's folder is marked as taking the drop (${mid})`);
    await waitIn(api, base, teamA, '/w2/drag/Beta/Inner', accountKey, 'g07.txt');
    // A folder cannot be dropped into a folder below it: Beta onto Inner, in the tree.
    const beta = await nameAt(page, 'Beta');
    const inner = await centre(page, `[data-testid="folder-tree"] .tree-row[data-drop-name="Inner"]`);
    const refused = await dragTo(page, beta, inner, { during: async () => ({ state: await dropState(page, `[data-testid="folder-tree"] .tree-row[data-drop-name="Inner"]`), ghost: await ghost(page) }) });
    check(refused.state === 'refused', `Beta onto Inner, below it, is refused (${refused.state})`);
    check((await listed(api, base, teamA, '/w2/drag', accountKey)).includes('Beta'), 'Beta did not move into itself');
    // The state is kept: a reload keeps the folders that were opened in the sidebar's tree (slice d1 moved the tree there).
    await page.goto(dragURL);
    await page.waitFor(`!!document.querySelector(${q(itemSel('Alpha'))})`, 15000);
    await page.waitFor(`[...document.querySelectorAll('[data-testid="folder-tree"] .tree-row')].some((r) => r.dataset.dropName === 'Inner')`, 15000);
    const kept = await page.evaluate(`(() => { const item = document.querySelector('[data-testid="folder-tree"] [role="treeitem"][data-tree-key="' + ${q(await betaKey())} + '"]'); return { open: item?.getAttribute('aria-expanded'), holdsInner: !!item?.querySelector('.tree-row[data-drop-name="Inner"]') }; })()`);
    check(kept.open === 'true' && kept.holdsInner, `a reload keeps the folders that were opened: Beta is open and holds Inner (${JSON.stringify(kept)})`);
    const saved = await page.evaluate(`Object.entries(localStorage).filter(([k]) => k.startsWith('selfcloud.tree.')).map(([, v]) => v).join(' ')`);
    check(saved.includes(await betaKey()), 'the opened folder is in the state kept for the person');
  });

  // ---- 8. desktop files still upload: a drag of files from outside the page is the browser's own drag and drop ----
  await step('files dragged in from the desktop still upload', async () => {
    await open(dragURL, 'List', 'Alpha');
    const local = join(dir, 'w2-desk.txt');
    await writeFile(local, 'dropped from the desktop\n');
    const at = await centre(page, itemSel('Alpha'));
    const data = { items: [], files: [local], dragOperationsMask: 1 };
    await page.send('Input.dispatchDragEvent', { type: 'dragEnter', x: at.x, y: at.y, data });
    await page.send('Input.dispatchDragEvent', { type: 'dragOver', x: at.x, y: at.y, data });
    const overlay = await page.evaluate(`document.body.innerText.includes('Drop files here') || !!document.querySelector('[aria-hidden="true"][style*="dashed"]')`);
    // Read while the files are still over the folder, before the drop ends the drag.
    const duringGhost = await ghost(page);
    const duringDrop = await dropState(page, itemSel('Alpha'));
    await page.send('Input.dispatchDragEvent', { type: 'drop', x: at.x, y: at.y, data });
    await waitIn(api, base, teamA, '/w2/drag', accountKey, 'w2-desk.txt', true, 30000);
    // The tray folds when the upload has ended on the page too; only then does the run leave the page.
    await page.waitFor(`!document.querySelector('#tray-list') && !!document.querySelector('[data-testid="upload-tray"]')`, 30000);
    check((await listed(api, base, teamA, '/w2/drag', accountKey)).includes('w2-desk.txt'), `a file dropped from the desktop uploaded to the folder shown, not into the folder under the pointer (overlay seen: ${overlay})`);
    check(!(await listed(api, base, teamA, '/w2/drag/Alpha', accountKey)).includes('w2-desk.txt'), 'the desktop file did not land in Alpha, which was under the pointer');
    check(duringGhost === null && duringDrop === null, `no item-drag ghost and no drop mark while the desktop files were over a folder (${JSON.stringify([duringGhost, duringDrop])})`);
  });

  // ---- 9. the rubber band ----
  const bandBox = (page) => page.evaluate(`(() => { const b = document.querySelector('[data-testid="rubber-band"]'); if (!b) return null; const r = b.getBoundingClientRect(); return { w: r.width, h: r.height }; })()`);

  await step('a rubber band on the empty space below a short list selects the rows it crosses', async () => {
    await open(shortURL, 'List', 's01.txt');
    const rows = await page.evaluate(`[...document.querySelectorAll('[role="row"][aria-label]')].map((r) => { const b = r.getBoundingClientRect(); return { n: r.getAttribute('aria-label'), top: b.top, bottom: b.bottom, left: b.left, right: b.right }; })`);
    const last = rows[rows.length - 1];
    const s3 = rows.find((r) => r.n === 's03.txt');
    const start = { x: last.right - 40, y: last.bottom + 60 };
    const end = { x: last.left + 120, y: s3.top + 6 };
    const mid = await dragTo(page, start, end, { during: async () => ({ box: await bandBox(page), sel: await selectedNames(page) }) });
    const hitStart = await page.evaluate(`(() => { const el = document.elementFromPoint(${start.x}, ${start.y}); const sec = document.querySelector('section.browser').getBoundingClientRect(); return (el ? el.tagName + '.' + el.className : 'nothing') + ' inSection=' + !!el?.closest('section.browser') + ' section=' + Math.round(sec.top) + '-' + Math.round(sec.bottom) + ' win=' + innerHeight + ' scrollY=' + scrollY + ' gesture=' + document.documentElement.classList.contains('sc-gesture'); })()`);
    check(mid.box !== null && mid.box.w > 50 && mid.box.h > 50, `the band is drawn (${JSON.stringify(mid.box)}; start ${JSON.stringify(start)} hits ${hitStart})`);
    check(mid.sel.join() === 's03.txt,s04.txt,s05.txt,s06.txt', `the band selects the rows it crosses, s3 to s6 (${mid.sel.join()})`);
    check((await bandBox(page)) === null, 'the band is gone when the button is let go');
    const after = await selectedNames(page);
    check(after.join() === 's03.txt,s04.txt,s05.txt,s06.txt', `the selection stays (${after.join()})`);
    // With Ctrl a second band adds to it; without, it replaces it.
    // s1 is picked by a click; a band with Ctrl that crosses only s5 and s6 adds to it, and one without replaces it.
    const s5 = rows.find((r) => r.n === 's05.txt');
    await click(page, await nameAt(page, 's01.txt'));
    await dragTo(page, { x: last.right - 40, y: last.bottom + 60 }, { x: last.left + 120, y: s5.top + 6 }, { ctrl: true });
    const added = await selectedNames(page);
    check(added.join() === 's01.txt,s05.txt,s06.txt', `a band with Ctrl adds to the selection (${added.join()})`);
    await dragTo(page, { x: last.right - 40, y: last.bottom + 60 }, { x: last.left + 120, y: last.bottom - 4 });
    const replaced = await selectedNames(page);
    check(replaced.join() === 's06.txt', `a band without Ctrl replaces it (${replaced.join()})`);
    // A plain click on the empty space clears the selection.
    await click(page, { x: last.right - 40, y: last.bottom + 120 });
    await sleepMs(200);
    check((await selectedNames(page)).length === 0, 'a click on empty space clears the selection');
  });

  await step('a rubber band can start anywhere below the rows, down to the bottom of the window, and the page does not scroll for it', async () => {
    await open(shortURL, 'List', 's01.txt');
    const size = await page.evaluate(`({ h: innerHeight, over: document.documentElement.scrollHeight - innerHeight })`);
    check(size.over <= 0, `a short folder gives the page nothing to scroll (${size.over})`);
    // A notice above the page, or anything else that pushes the section down, adds no scrolling either: the section's height
    // comes from the room there is, not from a sum of what is expected above it. The folder tree is set aside here: its own
    // limit is a sum, so a tree that fills the window under a notice does let the page scroll while the notice shows, which is
    // harmless, and the next check holds it to the window without a notice.
    const pushed = await page.evaluate(`(() => { const main = document.querySelector('#main'); const tree = document.querySelector('[data-testid="folder-tree"]'); const was = tree.style.display; tree.style.display = 'none'; const d = document.createElement('div'); d.style.height = '32px'; d.style.flex = 'none'; main.prepend(d); const over = document.documentElement.scrollHeight - innerHeight; const sec = document.querySelector('section.browser').getBoundingClientRect().bottom; d.remove(); tree.style.display = was; return { over, bottom: Math.round(sec) }; })()`);
    check(pushed.over <= 0, `something 32 px high above the page adds no scrolling (${JSON.stringify(pushed)})`);
    check(pushed.bottom === size.h, `and the section still reaches the window's bottom (${JSON.stringify(pushed)})`);
    // A folder tree too long for the window scrolls inside itself and adds no scrolling to the page.
    const tallTree = await page.evaluate(`(() => { const t = document.querySelector('[data-testid="folder-tree"]').firstElementChild; const was = t.style.minHeight; t.style.minHeight = '2000px'; const over = document.documentElement.scrollHeight - innerHeight; t.style.minHeight = was; return over; })()`);
    check(tallTree <= 0, `a folder tree too long for the window adds no scrolling (${tallTree})`);
    const last = await page.evaluate(`(() => { const r = [...document.querySelectorAll('[role="row"][aria-label]')].pop().getBoundingClientRect(); return { right: r.right, left: r.left, top: r.top }; })()`);
    const mid = await dragTo(page, { x: last.right - 40, y: size.h - 6 }, { x: last.left + 120, y: last.top + 6 }, { during: () => bandBox(page) });
    check(mid !== null, `a press 6 px above the window's bottom starts a band (${JSON.stringify(mid)})`);
    await clearSelection(page);
  });

  await step('a press on a button, a checkbox or a row starts no band', async () => {
    await open(shortURL, 'List', 's01.txt');
    const button = await centre(page, '[data-testid="view-toggle"]');
    const mid1 = await dragTo(page, button, { x: button.x - 200, y: button.y + 300 }, { during: () => bandBox(page) });
    check(mid1 === null, 'a press on a toolbar button starts no band');
    await page.evaluate(`window.scrollTo(0, 0)`);
    const box = await page.evaluate(`(() => { const c = document.querySelectorAll('[role="row"] .ant-checkbox-input')[2]; const r = c.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
    const mid2 = await dragTo(page, box, { x: box.x + 200, y: box.y + 100 }, { during: () => bandBox(page) });
    check(mid2 === null, 'a press on a row checkbox starts no band');
    await clearSelection(page);
    const row = await nameAt(page, 's02.txt');
    const mid3 = await dragTo(page, row, { x: row.x + 150, y: row.y + 90 }, { during: async () => ({ band: await bandBox(page), ghost: await ghost(page) }) });
    check(mid3.band === null && mid3.ghost !== null, `a press on a row drags the row, and draws no band (${JSON.stringify(mid3)})`);
    // Text: a press on the row's text is a press on an item; the page has no text selection after it.
    check(await page.evaluate(`window.getSelection().toString() === ''`), 'no text was selected by the gesture');
  });

  await step('a band in tiles, and in a long list that scrolls under the pointer', async () => {
    await open(longURL, 'Tiles', 'l01.txt');
    await page.waitFor(`!!document.querySelector('[role="listbox"]')`, 10000).catch(async (err) => {
      throw new Error(`${err.message.slice(0, 50)}; view toggle ${await page.evaluate(`document.querySelector('[data-testid="view-toggle"]')?.dataset.view`)}; rows ${await page.evaluate(`document.querySelectorAll('[role=row]').length + '/' + document.querySelectorAll('[role=option]').length`)}; prefs ${await page.evaluate(`localStorage.getItem('selfcloud.files')`)}; err ${await page.evaluate(`document.body.innerText.slice(0, 200)`)}`);
    });
    const edge = await page.evaluate(`(() => { const g = document.querySelector('[role="listbox"]'); const r = g.getBoundingClientRect(); return { x: r.left + 5, y: r.top + 5, w: r.width, bottom: r.bottom }; })()`);
    const t1 = await page.evaluate(`(() => { const t = document.querySelector(${q(itemSel('l02.txt'))}).getBoundingClientRect(); return { x: t.left + t.width / 2, y: t.top + t.height / 2 }; })()`);
    const mid = await dragTo(page, { x: edge.x, y: edge.y }, t1, { during: () => selectedNames(page) });
    check(mid.includes('l01.txt') && mid.includes('l02.txt'), `a band from the tile grid's padding selects tiles (${mid.join()})`);
    await clearSelection(page);
    // Long list: press in the padding, drag to the bottom edge of the window and hold: the page scrolls and the band follows.
    await page.evaluate(`window.scrollTo(0, 0)`);
    const start = await page.evaluate(`(() => { const g = document.querySelector('[role="listbox"]').getBoundingClientRect(); return { x: g.left + 5, y: g.top + 5 }; })()`);
    const bottom = { x: start.x + 300, y: 790 };
    await mouse(page, 'mouseMoved', start);
    await mouse(page, 'mousePressed', start);
    await glide(page, start, bottom, {});
    await sleepMs(1500);
    const scrolled = await page.evaluate(`window.scrollY`);
    const sel = await selectedNames(page);
    await mouse(page, 'mouseReleased', bottom);
    // The click that ends a gesture is swallowed for a moment, so a person's next click (on Clear) comes later; the run waits as dragTo does.
    await sleepMs(300);
    check(scrolled > 100, `holding the pointer at the bottom edge scrolls the page (scrollY ${scrolled})`);
    const first = await page.evaluate(`(() => { const t = document.querySelector(${q(itemSel('l01.txt'))}); return { selected: t.getAttribute('aria-selected'), bottom: t.getBoundingClientRect().bottom }; })()`);
    check(first.selected === 'true' && first.bottom < 0, `the first tile, scrolled off the top of the window, is selected by the band (${JSON.stringify(first)}; ${sel.length} selected)`);
    await clearSelection(page);
  });

  // ---- 10. the column header is pinned under the toolbar ----
  await step('the column header stays under the toolbar in a long folder', async () => {
    await open(longURL, 'List', 'l01.txt');
    const side = await page.evaluate(`document.documentElement.scrollWidth - innerWidth`);
    check(side <= 0, `with the tree open the page does not scroll sideways at 1280 px (${side})`);
    await page.evaluate(`window.scrollTo(0, 900)`);
    await sleepMs(300);
    const pos = await page.evaluate(`(() => { const bar = document.querySelector('[data-testid="browser-toolbar"]').getBoundingClientRect(); const head = document.querySelector('.ant-table-sticky-holder thead')?.getBoundingClientRect(); return { barBottom: bar.bottom, headTop: head ? head.top : null, headBottom: head ? head.bottom : null, scroll: scrollY }; })()`);
    const clip = await page.evaluate(`(() => { const out = []; for (let e = document.querySelector('.ant-table-sticky-holder'); e; e = e.parentElement) { const o = getComputedStyle(e); if (o.overflowX !== 'visible' || o.overflowY !== 'visible') out.push(e.tagName + '.' + e.className + ' ' + o.overflowX + '/' + o.overflowY); } return out.join(' | '); })()`);
    check(pos.headTop !== null && Math.abs(pos.headTop - pos.barBottom) <= 1.5, `the column header sits right under the toolbar (toolbar bottom ${pos.barBottom}, header top ${pos.headTop}, scrolled ${pos.scroll}; clipping ancestors: ${clip})`);
    // A row scrolled to by the keyboard is not left under the header.
    const covered = await page.evaluate(`(() => { const head = document.querySelector('.ant-table-sticky-holder thead').getBoundingClientRect(); const row = document.querySelector(${q(itemSel('l30.txt'))}); row.focus(); row.scrollIntoView({ block: 'nearest' }); const r = row.getBoundingClientRect(); return { rowTop: r.top, headBottom: head.bottom }; })()`);
    check(covered.rowTop >= covered.headBottom - 1, `a row scrolled into view is not under the column header (${JSON.stringify(covered)})`);
  });

  // ---- 11. Select all says it took only the loaded rows ----
  await step('Select all says it took only the loaded rows when more pages exist', async () => {
    await open(manyURL, 'List', 'm01.txt');
    const box = await page.evaluate(`(() => { const c = document.querySelector('.ant-table-sticky-holder .ant-checkbox-input, thead .ant-checkbox-input'); const r = c.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
    check(await page.evaluate(`!!document.querySelector('.ant-btn') && [...document.querySelectorAll('button')].some((b) => /load more/i.test(b.textContent))`), 'the folder has more pages than are loaded');
    await click(page, box);
    await page.waitFor(`document.body.innerText.includes('Selected the items loaded so far: 50')`, 8000);
    const took = await selectedNames(page);
    check(took.length === 50 && took[0] === 'm01.txt' && took[49] === 'm50.txt', `Select all took the 50 loaded rows, m01 to m50 (${took.length}: ${took[0]} to ${took[took.length - 1]})`);
    check(await page.evaluate(`document.body.innerText.includes('50 selected')`), 'the panel counts 50 selected');
  });

  // ---- 12. a cut pasted where it came from says so ----
  await step('a cut pasted into the same folder says that nothing moved', async () => {
    await open(shortURL, 'List', 's02.txt');
    await click(page, await nameAt(page, 's02.txt'));
    await press(page, 'x', ['ctrl']);
    await press(page, 'v', ['ctrl']);
    await page.waitFor(`document.body.innerText.includes('These items are already in this folder, so nothing was moved.')`, 5000);
    check((await listed(api, base, teamA, '/w2/short', accountKey)).includes('s02.txt'), 'the file is where it was');
    await clearSelection(page);
  });

  // ---- 13. Copy link ----
  await step('Copy link copies this app\'s own address and makes no share link', async () => {
    await open(shortURL, 'List', 's03.txt');
    await page.send('Browser.grantPermissions', { permissions: ['clipboardReadWrite', 'clipboardSanitizedWrite'], origin: page.base }).catch(() => undefined);
    await click(page, await nameAt(page, 's03.txt'));
    const id = await idOf(teamA, '/w2/short', 's03.txt');
    const btn = await page.evaluate(`(() => { const b = [...document.querySelectorAll('[data-testid="selection-panel"] button')].find((x) => x.getAttribute('aria-label') === 'Copy link'); if (!b) return null; const r = b.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2, disabled: b.disabled }; })()`);
    check(btn !== null && !btn.disabled, 'the selection panel has an enabled Copy link icon for one item');
    await click(page, btn);
    await page.waitFor(`document.body.innerText.includes('Link copied')`, 8000);
    let text = await page.evaluate(`navigator.clipboard.readText().catch((e) => 'ERR ' + e)`);
    if (String(text).startsWith('ERR')) {
      // The browser would not let the page read the clipboard back: the rename field takes a real paste instead.
      await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'F2', code: 'F2', windowsVirtualKeyCode: 113, nativeVirtualKeyCode: 113 });
      await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'F2', code: 'F2', windowsVirtualKeyCode: 113, nativeVirtualKeyCode: 113 });
      await page.waitFor(`!!document.querySelector('input.rename')`, 5000);
      await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'v', code: 'KeyV', windowsVirtualKeyCode: 86, nativeVirtualKeyCode: 86, modifiers: MODS.ctrl, commands: ['paste'] });
      await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'v', code: 'KeyV', windowsVirtualKeyCode: 86, nativeVirtualKeyCode: 86, modifiers: MODS.ctrl });
      await sleepMs(300);
      text = await page.evaluate(`document.querySelector('input.rename')?.value ?? ''`);
      await press(page, 'Escape');
      await page.waitFor(`!document.querySelector('input.rename')`, 5000);
    }
    check(text === `${page.base}/n/${id}`, `the clipboard holds this app's address of the item (${text === `${page.base}/n/${id}` ? 'same' : `different: ${String(text).replace(id, '<id>')}`})`);
    check(!(await page.evaluate(`document.body.innerText.includes(${q(id)})`)), 'the page shows no raw ID');
    const shares = await api(base, 'GET', `/api/v1/nodes/${id}/links`, accountKey).catch((e) => ({ items: [], err: String(e.message) }));
    check((shares.items ?? []).length === 0, 'no share link was made');
    // Two items: the icon is there and off.
    await click(page, await nameAt(page, 's04.txt'));
    await mouse(page, 'mouseMoved', await nameAt(page, 's05.txt'));
    await mouse(page, 'mousePressed', await nameAt(page, 's05.txt'), { ctrl: true });
    await mouse(page, 'mouseReleased', await nameAt(page, 's05.txt'), { ctrl: true });
    check(await page.evaluate(`[...document.querySelectorAll('[data-testid="selection-panel"] button')].find((x) => x.getAttribute('aria-label') === 'Copy link')?.disabled === true`), 'Copy link is off with two items selected');
    await clearSelection(page);
  });

  // ---- 14. the panel's buttons are clear of the bar's edges ----
  await step("the selection panel's buttons are clear of the bar's top and bottom, and the row keeps its height", async () => {
    await open(shortURL, 'List', 's01.txt');
    const before = await page.evaluate(`document.querySelector('[data-testid="browser-toolbar"]').getBoundingClientRect().height`);
    await click(page, await nameAt(page, 's01.txt'));
    await page.waitFor(`!!document.querySelector('[data-testid="selection-panel"]')`, 5000);
    const m = await page.evaluate(`(() => { const p = document.querySelector('[data-testid="selection-panel"]').getBoundingClientRect(); const bs = [...document.querySelectorAll('[data-testid="selection-panel"] button')].map((b) => b.getBoundingClientRect()); return { top: p.top, bottom: p.bottom, gapTop: Math.min(...bs.map((b) => b.top)) - p.top, gapBottom: p.bottom - Math.max(...bs.map((b) => b.bottom)), bar: document.querySelector('[data-testid="browser-toolbar"]').getBoundingClientRect().height }; })()`);
    check(m.gapTop >= 3.5 && m.gapBottom >= 3.5, `every button is inset from the bar's top and bottom (${m.gapTop} / ${m.gapBottom})`);
    check(Math.abs(m.bar - before) < 0.5, `the toolbar row keeps its height (${before} to ${m.bar})`);
    await clearSelection(page);
  });

  // ---- 14b. Escape ends a drag, and the keys of a press that has not moved are the page's ----
  await step('Escape in the middle of a drag cancels it and moves nothing', async () => {
    await open(dragURL, 'List', 'g10.txt');
    const from = await nameAt(page, 'g10.txt');
    const to = await centre(page, itemSel('Alpha'));
    await mouse(page, 'mouseMoved', from);
    await mouse(page, 'mousePressed', from);
    await glide(page, from, to, {});
    await sleepMs(150);
    const under = await Promise.all([ghost(page), dropState(page, itemSel('Alpha'))]);
    check(under[0] !== null && under[1] === 'ok', `before Escape the drag shows its ghost and marks Alpha as taking it (${JSON.stringify(under)})`);
    await press(page, 'Escape');
    const after = await Promise.all([ghost(page), dropState(page, itemSel('Alpha'))]);
    check(after[0] === null && after[1] === null, `after Escape the ghost and the mark are gone, with the button still down (${JSON.stringify(after)})`);
    await mouse(page, 'mouseReleased', to);
    await sleepMs(500);
    check((await listed(api, base, teamA, '/w2/drag', accountKey)).includes('g10.txt'), 'g10.txt did not move');
    check(!(await listed(api, base, teamA, '/w2/drag/Alpha', accountKey)).includes('g10.txt'), 'g10.txt is not in Alpha');
  });

  // ---- 14c. drag and band in a right-to-left language ----
  await step('in Hebrew a drag and a band work and are drawn on the right side of the pointer', async () => {
    await open(dragURL, 'List', 'g11.txt');
    await setLanguage(page, 'עברית');
    try {
      check(await page.evaluate(`document.documentElement.dir === 'rtl'`), 'the page is right to left');
      const from = await nameAt(page, 'g11.txt');
      const to = await centre(page, itemSel('Alpha'));
      const mid = await dragTo(page, from, to, {
        during: () => page.evaluate(`(() => { const g = document.querySelector('[data-testid="drag-ghost"]'); if (!g) return null; const r = g.getBoundingClientRect(); return { text: g.textContent, left: r.left, right: r.right, top: r.top, wide: innerWidth }; })()`),
      });
      check(mid !== null && mid.text.includes('העברה'), `the ghost is in Hebrew (${mid?.text})`);
      check(mid !== null && mid.right <= to.x && mid.left >= 0, `the ghost lies on the pointer's left, the end side in right to left, and inside the window (ghost ${mid?.left} to ${mid?.right}, pointer ${to.x})`);
      await waitIn(api, base, teamA, '/w2/drag/Alpha', accountKey, 'g11.txt');
      // A band on the short folder: its drawn box spans the two points, whichever side the page reads from.
      await open(shortURL, 'List', 's01.txt');
      const rows = await page.evaluate(`[...document.querySelectorAll('[role="row"][aria-label]')].map((r) => { const b = r.getBoundingClientRect(); return { n: r.getAttribute('aria-label'), top: b.top, bottom: b.bottom, left: b.left, right: b.right }; })`);
      const last = rows[rows.length - 1];
      const s3 = rows.find((r) => r.n === 's03.txt');
      const start = { x: last.left + 60, y: last.bottom + 60 };
      const end = { x: last.right - 120, y: s3.top + 6 };
      const got = await dragTo(page, start, end, {
        during: async () => ({ sel: await selectedNames(page), box: await page.evaluate(`(() => { const b = document.querySelector('[data-testid="rubber-band"]'); if (!b) return null; const r = b.getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom }; })()`) }),
      });
      check(got.sel.join() === 's03.txt,s04.txt,s05.txt,s06.txt', `the band selects the rows it crosses in right to left (${got.sel.join()})`);
      check(
        got.box !== null && Math.abs(got.box.left - start.x) <= 2 && Math.abs(got.box.right - end.x) <= 2 && Math.abs(got.box.top - (s3.top + 6)) <= 2,
        `the band is drawn between the two points (${JSON.stringify(got.box)}; from ${JSON.stringify(start)} to ${JSON.stringify(end)})`,
      );
    } finally {
      await setLanguage(page, 'English');
    }
  });

  // ---- 14d. a person who was given only a folder below the root of a space ----
  await step('a person given only a folder below a space root gets a tree rooted at that folder, with no failure', async () => {
    const room = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'Room' })).id;
    await mk(room, '/Open');
    await mk(room, '/Open/Inside');
    await mk(room, '/Closed');
    const rootItems = (await api(base, 'GET', `/api/v1/folders/${room}/children?path=%2F`, accountKey)).items;
    const given = rootItems.find((n) => n.name === 'Open');
    await api(base, 'PUT', `/api/v1/nodes/${given.id}/permissions/${memberID}`, accountKey, { role: 'viewer', notify: false });
    // What the server really answers this person: the space's root is not found, its listing does not name the space, and the folder is theirs.
    // The probe asks for the root on purpose; the browser logs that refusal as a network error, and only that one is taken off the list.
    const mark = page.errors.length;
    const answers = await page.evaluate(`(async () => { const get = async (u) => { const r = await fetch(u, { credentials: 'same-origin' }); return { status: r.status, body: await r.json().catch(() => null) }; }; const list = await get('/api/v1/folders'); const root = await get('/api/v1/folders/${room}/children'); const dir = await get('/api/v1/nodes/${given.id}/children'); return { listed: (list.body?.items ?? []).some((f) => f.id === '${room}'), rootStatus: root.status, rootCode: root.body?.error?.code, dirStatus: dir.status }; })()`);
    await sleepMs(500);
    for (let i = page.errors.length - 1; i >= mark; i--) if (page.errors[i].includes(`/api/v1/folders/${room}/children`) && page.errors[i].includes('404')) page.errors.splice(i, 1);
    check(page.errors.length === mark, `the probe's own refusal is the only error it logged, and it is taken off the list (${page.errors.slice(mark).join(' | ') || 'none left'})`);
    check(answers.rootStatus === 404 && answers.rootCode === 'not_found' && answers.dirStatus === 200, `the server refuses the space's root with not_found and lists the given folder (${JSON.stringify(answers)})`);
    await page.goto(`${page.base}/files/${room}/${given.id}`);
    await page.waitFor(`!!document.querySelector('[data-testid="folder-tree"] .tree-row[data-drop-name="Open"]')`, 20000);
    await page.waitFor(`!!document.querySelector('[data-testid="folder-tree"] .tree-row[data-drop-name="Inside"]')`, 20000);
    // The page and the tree never ask for the root the server refuses: no such request was made since the page loaded.
    const asked = await page.evaluate(`performance.getEntriesByType('resource').map((e) => e.name).filter((n) => n.includes('/api/v1/folders/${room}/children'))`);
    check(asked.length === 0, `no read of the refused root was made by the page or the tree (${asked.join(', ') || 'none'})`);
    const tree = await page.evaluate(`(() => { const t = document.querySelector('[data-testid="folder-tree"]'); const top = t.querySelector('.tree-row[data-drop-name="Open"]'); return { names: [...t.querySelectorAll('.tree-row')].map((r) => r.dataset.dropName), alert: !!t.querySelector('[role="alert"]'), retry: [...t.querySelectorAll('button')].some((b) => /try again/i.test(b.textContent)), topDir: top.dataset.dropDir, topRole: top.dataset.dropRole, current: t.querySelector('[aria-current="location"]')?.getAttribute('data-tree-key') ?? null }; })()`);
    check(tree.names.includes('Open') && tree.names.includes('Inside') && !tree.names.includes('Room') && !tree.names.includes('Closed'), `the tree is rooted at the folder given and shows what is reachable (${tree.names.join(' / ')})`);
    check(!tree.alert && !tree.retry, 'the tree shows no failure and no Retry');
    check(tree.topDir === given.id && tree.topRole === 'viewer', `the root row is the given folder, with the person's own role (${tree.topDir}, ${tree.topRole})`);
    check(tree.current === given.id, 'the place shown is marked in the tree');
    // The tree's other open folders may still be loading, so rows move: a press at a measured point could land on another row.
    // This step is about what opening a folder shows, so the link itself is clicked.
    await page.evaluate(`document.querySelector('[data-testid="folder-tree"] .tree-row[data-drop-name="Inside"] a').click()`);
    await page.waitFor(`location.pathname.startsWith('/files/${room}/') && location.pathname !== '/files/${room}/${given.id}'`, 15000);
    check(await page.evaluate(`!document.querySelector('[data-testid="folder-tree"] [role="alert"]')`), 'opening a folder below it from the tree still shows no failure');
  });

  // ---- 15. phone: no tree, and the account button is a 44 pixel target ----
  await step('at phone width there is no tree, and the account button is 44 pixels', async () => {
    await setPhone(page, true);
    try {
      await open(shortURL, 'List', 's01.txt');
      // The tree is in the drawer on a phone (slice d1), not beside the listing: the page itself holds none.
      check(await page.evaluate(`!document.querySelector('section.browser [data-testid="folder-tree"]') && !document.querySelector('.ant-drawer-open [data-testid="folder-tree"]')`), 'a phone has no folder tree beside the listing, and the drawer is closed');
      const hgt = await page.evaluate(`document.querySelector('[data-testid="account-menu"]').getBoundingClientRect().height`);
      check(hgt >= 44, `the header account button is at least 44 px high on a finger (${hgt})`);
      const wide = await page.evaluate(`document.documentElement.scrollWidth - innerWidth`);
      check(wide <= 0, `the page does not scroll sideways at 390 px (${wide})`);
      // A finger does not draw a band or drag: a swipe on the list scrolls.
      await page.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: 200, y: 600 }] });
      await page.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: 200, y: 300 }] });
      await page.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
      check(await page.evaluate(`!document.querySelector('[data-testid="rubber-band"]') && !document.querySelector('[data-testid="drag-ghost"]')`), 'a swipe draws no band and drags nothing');
    } finally {
      await setPhone(page, false);
    }
  });

  if (process.env.SELFCLOUD_W2_SHOTS) {
    await step('the slice w2 pictures', () => shots(page, { dragURL, longURL, shortURL, dir: process.env.SELFCLOUD_W2_SHOTS }));
  }

  if (failures.length > 0) throw new Error(`w2: ${failures.length} step(s) failed:\n${failures.join('\n')}`);
}

// shots saves the pictures: the tree beside the listing, a drag under way, a band, the selection panel with Copy link; on the
// desktop in light and dark, and at 390 and 320 px; in English and Hebrew.
async function shots(page, { dragURL, longURL, shortURL, dir }) {
  await mkdir(dir, { recursive: true });
  const save = async (file) => {
    const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
    await writeFile(join(dir, file), Buffer.from(data, 'base64'));
  };
  const setWidth = async (w) => {
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: w < 600, maxTouchPoints: 1 });
    await page.send('Emulation.setDeviceMetricsOverride', { width: w, height: 800, deviceScaleFactor: 1, mobile: w < 600 });
    await sleepMs(300);
  };
  for (const theme of ['Light', 'Dark']) {
    for (const lang of ['English', 'עברית']) {
      const l = lang === 'English' ? 'en' : 'he';
      await setWidth(1280);
      await page.goto(shortURL);
      await setLanguage(page, 'English');
      await setTheme(page, theme);
      await setLanguage(page, lang);
      for (const width of [1280, 390, 320]) {
        if (theme === 'Dark' && width !== 1280) continue;
        const tag = `${width}-${theme.toLowerCase()}-${l}`;
        await setWidth(width);
        await page.goto(dragURL);
        await page.waitFor(`!!document.querySelector(${q(itemSel('Alpha'))})`, 15000);
        await setView(page, 'List');
        await clearSelection(page);
        await sleepMs(500);
        await save(`tree-${tag}.png`);
        await click(page, await nameAt(page, 'g08.txt'));
        await sleepMs(400);
        await save(`panel-${tag}.png`);
        await clearSelection(page);
        if (width === 1280) {
          // A drag in progress, over a folder that takes it, and over one that does not.
          const from = await nameAt(page, 'g09.txt');
          const to = await centre(page, itemSel('Alpha'));
          await mouse(page, 'mouseMoved', from);
          await mouse(page, 'mousePressed', from);
          await glide(page, from, to, {});
          await sleepMs(300);
          await save(`drag-ok-${tag}.png`);
          // Let go over nothing: the item stays. Then a folder dragged onto itself, shown as refused.
          await glide(page, to, { x: 640, y: 700 }, {});
          await mouse(page, 'mouseReleased', { x: 640, y: 700 });
          await sleepMs(300);
          const beta = await nameAt(page, 'Beta');
          const onBeta = await centre(page, itemSel('Beta'));
          await mouse(page, 'mouseMoved', beta);
          await mouse(page, 'mousePressed', beta);
          await glide(page, beta, { x: onBeta.x + 30, y: onBeta.y }, {});
          await sleepMs(300);
          await save(`drag-refused-${tag}.png`);
          await mouse(page, 'mouseReleased', onBeta);
          await sleepMs(300);
          await page.goto(shortURL);
          await page.waitFor(`!!document.querySelector(${q(itemSel('s01.txt'))})`, 15000);
          const rows = await page.evaluate(`[...document.querySelectorAll('[role="row"][aria-label]')].map((r) => { const b = r.getBoundingClientRect(); return { top: b.top, bottom: b.bottom, left: b.left, right: b.right }; })`);
          const last = rows[rows.length - 1];
          const start = { x: last.right - 40, y: last.bottom + 60 };
          const end = { x: last.left + 160, y: rows[1].top + 8 };
          await mouse(page, 'mouseMoved', start);
          await mouse(page, 'mousePressed', start);
          await glide(page, start, end, {});
          await sleepMs(300);
          await save(`band-${tag}.png`);
          await mouse(page, 'mouseReleased', end);
          await sleepMs(300);
          await clearSelection(page);
        }
      }
    }
  }
  await setWidth(1280);
  await setLanguage(page, 'English');
  await setTheme(page, 'Light');
  void longURL;
}
