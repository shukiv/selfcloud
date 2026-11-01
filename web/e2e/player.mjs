// SPDX-License-Identifier: AGPL-3.0-or-later

// The player and its playlist (slice x1), in real Chrome with real input: the mouse for the bar, the panel and the drag,
// the keyboard for the keys, and the browser's own playback. Each folder here is made by the run, with small files.
// A playlist of more than 50 files is made, so the playlist must read a second page of the folder when it reaches the
// end of the first. Link requests are counted per file played; the count is the run's own (page.sent).
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const media = fileURLToPath(new URL('./media/', import.meta.url));
const q = (v) => JSON.stringify(v);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// The file that is not media, though its name says it is (the same bytes media.mjs uses for its broken file).
const BROKEN = Buffer.concat([
  Buffer.from([0, 0, 0, 16, 0x66, 0x74, 0x79, 0x70, 0x69, 0x73, 0x6f, 0x6d, 0, 0, 2, 0]),
  Buffer.from(Array.from({ length: 4096 }, (_, i) => (i * 131 + 7) & 255)),
]);

const MEDIA = '[data-testid="media-screen"] video, [data-testid="media-screen"] audio';

// dblClick sends a mouse double click the way Chrome does: two presses with the click count at two.
async function dblClick(page, { x, y }) {
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
  for (const clickCount of [1, 2]) {
    await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', buttons: 1, clickCount });
    await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', buttons: 0, clickCount });
    await sleep(100);
  }
}

// click is one mouse click at a point.
async function click(page, { x, y }) {
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
  await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', buttons: 1, clickCount: 1 });
  await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', buttons: 0, clickCount: 1 });
}

// key sends one key press as the keyboard does: down, then up. The key is the DOM key name; code and the virtual key code
// are what Chrome expects for it.
async function key(page, k, code, vk, modifiers = 0) {
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: k, code, windowsVirtualKeyCode: vk, modifiers });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: k, code, windowsVirtualKeyCode: vk, modifiers });
  await sleep(150);
}

// centre is the centre of an element in the viewport, scrolled into view first, or null when it is not there.
async function centre(page, selector) {
  return page.evaluate(`(() => {
    const el = document.querySelector(${q(selector)});
    if (!el) return null;
    el.scrollIntoView({ block: 'center' });
    const r = el.getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + r.height / 2, left: r.left, width: r.width };
  })()`);
}

// itemPoint is the centre of an item's row in the folder listing, as the browser lists it.
async function itemPoint(page, name) {
  const p = await page.evaluate(`(() => {
    const row = document.querySelector(${q(`[role="row"][aria-label=${JSON.stringify(name)}]`)});
    if (!row) return null;
    row.scrollIntoView({ block: 'center' });
    const r = (row.querySelector('bdi') ?? row).getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  })()`);
  if (!p) throw new Error(`no item named ${name}`);
  return p;
}

// playItemUnscrolled plays a file by a double click on its row, without scrolling the listing to the row. The row must be on
// the listing's first page, so the listing keeps the rest of the folder unread.
async function playItemUnscrolled(page, name) {
  const found = await page.evaluate(`(() => {
    const row = document.querySelector(${q(`[role="row"][aria-label=${JSON.stringify(name)}]`)});
    if (!row) return false;
    row.dispatchEvent(new MouseEvent('dblclick', { bubbles: true, cancelable: true }));
    return true;
  })()`);
  if (!found) throw new Error(`no row named ${name} on the first page`);
  await page.waitFor(`!!document.querySelector('[data-testid="preview"]')`, 15000);
  await waitName(page, name);
  await page.waitFor(`!document.querySelector('.ant-zoom-appear-active, .ant-zoom-enter-active')`, 10000);
  await waitMedia(page);
  // A script's dispatched double click is not a user gesture, and the browser then refuses play() (the playlist's next file
  // would never start). A real click on the viewer's own name gives the page that gesture, and changes nothing else.
  const name_ = await page.evaluate(`(() => { const r = document.querySelector('[data-testid="preview"] header p').getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
  await click(page, name_);
  await sleep(200);
}

// state reads what the player and the panel show now.
const state = (page) =>
  page.evaluate(`(() => {
    const v = document.querySelector(${q(MEDIA)});
    const titles = [...document.querySelectorAll('[data-testid="media-queue"] .media-title bdi')].map((b) => b.textContent);
    return {
      name: document.querySelector('[data-testid="preview"] header bdi')?.textContent ?? null,
      paused: v ? v.paused : null,
      t: v ? v.currentTime : null,
      dur: v ? v.duration : null,
      rate: v ? v.playbackRate : null,
      vol: v ? v.volume : null,
      muted: v ? v.muted : null,
      loop: v ? v.loop : null,
      count: document.querySelector('[data-testid="media-count"]')?.textContent ?? null,
      status: document.querySelector('[data-testid="media-screen"] [role="status"]')?.textContent ?? null,
      alert: document.querySelector('[data-testid="media-screen"] .ant-alert')?.textContent ?? null,
      titles,
      current: document.querySelector('[data-testid="media-queue"] .media-title[aria-current="true"] bdi')?.textContent ?? null,
    };
  })()`);

// waitName waits until the viewer shows this file.
const waitName = (page, name, ms = 15000) =>
  page.waitFor(`document.querySelector('[data-testid="preview"] header bdi')?.textContent === ${q(name)}`, ms);

// waitMedia waits until the file on screen can play (its element has its data).
const waitMedia = (page, ms = 20000) => page.waitFor(`(() => { const v = document.querySelector(${q(MEDIA)}); return !!v && v.readyState >= 2; })()`, ms);

// finish moves the file on screen to its last moment and lets it run out: the end is a real ended event.
const finish = (page) =>
  page.evaluate(`(() => { const v = document.querySelector(${q(MEDIA)}); v.currentTime = Math.max(0, v.duration - 0.2); v.play().catch(() => undefined); return true; })()`);

// closeViewer closes the viewer with its own Close button, found by place (the first button of its header).
async function closeViewer(page) {
  await page.evaluate(`document.querySelector('[data-testid="preview"] header button').click()`);
  await page.waitFor(`!document.querySelector('[data-testid="preview"]')`, 10000);
}

// stopMini ends the playback the mini bar holds, with the bar's own close button (its last button, in every language). A
// step that needs the page without a bar at its bottom calls it; with no bar shown it does nothing.
export async function stopMini(page) {
  const shown = await page.evaluate(`!!document.querySelector('[data-testid="media-mini"]')`);
  if (!shown) return;
  await page.evaluate(`[...document.querySelectorAll('.media-mini .media-mini-btn')].pop().click()`);
  await page.waitFor(`!document.querySelector('[data-testid="media-mini"]')`, 10000);
}

// openFolderView opens a folder's listing in List view with nothing selected, and waits for its first rows.
async function openFolderView(page, folderURL, firstName) {
  // The list's view and sort are kept per browser: an earlier check may have sorted it otherwise, so the playlist's order
  // (the listing's order) is set to the default here.
  await page.goto(folderURL);
  await page.evaluate(`localStorage.setItem('selfcloud.files', JSON.stringify({ view: 'list', sort: { key: 'name', dir: 'asc' } }))`);
  await page.goto(folderURL);
  // The view toggle shows the view it switches to: data-view "grid" means the page is in List view already.
  await page.waitFor(`!!document.querySelector('button[data-view]')`, 15000);
  await page.evaluate(`(() => { const b = document.querySelector('button[data-view]'); if (b.dataset.view === 'list') b.click(); })()`);
  await page.waitFor(`!!document.querySelector('[role="row"][aria-label=${JSON.stringify(firstName)}]')`, 15000);
}

// playItem plays the file with this name from the folder listing, by a double click on its row.
async function playItem(page, name, { wait = true } = {}) {
  await dblClick(page, await itemPoint(page, name));
  await page.waitFor(`!!document.querySelector('[data-testid="preview"]')`, 15000);
  await waitName(page, name);
  // The viewer zooms in: positions measured while it does are stale. Wait until it has settled.
  await page.waitFor(`!document.querySelector('.ant-zoom-appear-active, .ant-zoom-enter-active')`, 10000);
  if (wait) await waitMedia(page);
}

// setLanguage and setPhone are the run's own helpers, copied here for this file.
async function setPhone(page, on) {
  if (on) {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 800, deviceScaleFactor: 1, mobile: true });
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
  } else {
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: false });
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
  }
}

async function setLanguage(page, name) {
  await page.waitFor(`!!document.querySelector('[data-testid="language"]')`, 10000);
  await page.evaluate(`document.querySelector('[data-testid="language"]').click()`);
  await page.waitFor(`[...document.querySelectorAll('.ant-dropdown-menu-item')].some((x) => x.textContent.trim() === ${q(name)})`, 10000);
  await page.evaluate(`[...document.querySelectorAll('.ant-dropdown-menu-item')].find((x) => x.textContent.trim() === ${q(name)}).click()`);
  await sleep(800);
}

// buttonByLabel clicks the button whose accessible label starts with the text given (the labels change by language, so
// the check passes the English one only where the screen is in English).
const clickLabel = (page, label) =>
  page.evaluate(`(() => { const b = [...document.querySelectorAll('[data-testid="media-queue"] button, [data-testid="preview"] button')].find((x) => (x.getAttribute('aria-label') ?? x.textContent.trim()).startsWith(${q(label)})); if (!b) return false; b.click(); return true; })()`);

// shotsX1 saves the viewer with the playlist open, for a video and an audio file, at desktop and phone width, in light and
// dark, in English and Hebrew. It is for the owner's review; nothing is checked here.
async function shotsX1(page, { folderURL, lookURL, dir, setTheme }) {
  await mkdir(dir, { recursive: true });
  for (const theme of ['Light', 'Dark']) {
    await page.goto(folderURL);
    await setTheme(page, theme);
    for (const lang of ['English', 'עברית']) {
      await setLanguage(page, lang);
      for (const width of ['desktop', 'phone']) {
        if (width === 'phone') await setPhone(page, true);
        const tag = `${width}-${theme.toLowerCase()}-${lang === 'English' ? 'en' : 'he'}`;
        for (const [folder, file, kind] of [[folderURL, 'clip.webm', 'video'], [folderURL, 'tone.ogg', 'audio'], [lookURL, 'bright.webm', 'bright']]) {
          await page.goto(folder);
          await page.waitFor(`!!document.querySelector('[role="row"][aria-label="${file}"]')`, 15000);
          await dblClick(page, await itemPoint(page, file));
          await page.waitFor(`!!document.querySelector('[data-testid="preview"]')`, 15000);
          await waitMedia(page);
          await page.evaluate(`document.querySelector(${q(MEDIA)}).play().then(() => true, () => false)`);
          await sleep(1200);
          const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
          await writeFile(join(dir, `${kind}-${tag}.png`), Buffer.from(data, 'base64'));
          await closeViewer(page);
        }
        if (width === 'phone') await setPhone(page, false);
      }
      await setLanguage(page, 'English').catch(() => undefined);
    }
  }
  await setTheme(page, 'Light');
}

// playerChecks runs the x1 checks. h is the run's helper set; ctx carries the page, the server and the folder's owner.
export async function playerChecks(h, { page, base, accountKey, teamA, linkMints, setTheme }) {
  const { check, api, rawPut } = h;
  const failures = [];
  const step = async (name, fn) => {
    try {
      await fn();
    } catch (err) {
      failures.push(`${name}: ${err.message}`);
      console.log(`FAIL ${name}: ${err.message}`);
    }
  };
  const put = (folder, name, body, type) =>
    rawPut(base, `/api/v1/folders/${teamA}/files?path=${encodeURIComponent(`${folder}/${name}`)}`, accountKey, body, type);
  const tone = await readFile(join(media, 'tone.ogg'));

  // ---- the folders: 52 tracks (more than one page of a folder), four tracks, a mixed folder, ten broken files ----
  for (const dirPath of ['/x1-list', '/x1-four', '/x1-mixed', '/x1-broken', '/x1-look']) {
    await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: dirPath, parents: false });
  }
  for (let i = 1; i <= 52; i++) await put('/x1-list', `track-${String(i).padStart(2, '0')}.ogg`, tone, 'audio/ogg');
  for (const n of ['1-one', '2-two', '3-three', '4-four']) await put('/x1-four', `${n}.ogg`, tone, 'audio/ogg');
  await put('/x1-mixed', '1-ok.ogg', tone, 'audio/ogg');
  await put('/x1-mixed', '2-bad.mp4', BROKEN, 'video/mp4');
  await put('/x1-mixed', '3-ok.ogg', tone, 'audio/ogg');
  for (let i = 1; i <= 10; i++) await put('/x1-broken', `bad-${String(i).padStart(2, '0')}.mp4`, BROKEN, 'video/mp4');
  // The look folder: a solid white video (the brightest picture behind the bar), a track with a long name, and a short track.
  await put('/x1-look', 'bright.webm', await readFile(join(media, 'bright.webm')), 'video/webm');
  await put('/x1-look', 'a-very-long-track-name-to-check-that-a-row-stays-on-one-line-at-every-width.ogg', tone, 'audio/ogg');
  await put('/x1-look', 'short.ogg', tone, 'audio/ogg');
  const root = (await api(base, 'GET', `/api/v1/folders/${teamA}/children?path=%2F`, accountKey)).items;
  const id = (name) => root.find((n) => n.name === name)?.id;
  const x1List = id('x1-list');
  const x1Four = id('x1-four');
  const x1Mixed = id('x1-mixed');
  const x1Broken = id('x1-broken');
  const mediaTest = id('media-test');
  const urlOf = (fid) => `${page.base}/files/${teamA}/${fid}`;
  await setPhone(page, false);

  // ---- 1. a playlist of more than 50 files: the end of the first page plays the first file of the second ----
  // The listing has its first page only until it is scrolled: opened from track-50 without scrolling to it, the playlist starts
  // with that page alone, so the second page is read by the playlist itself, with the listing's sort. The viewport is short
  // here, so the listing shows the same first rows on every run; the desktop viewport is put back after the step.
  await step('a playlist of 52 reads its second page when the first ends', async () => {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 600, deviceScaleFactor: 1, mobile: false });
    try {
      await openFolderView(page, urlOf(x1List), 'track-01.ogg');
      await playItemUnscrolled(page, 'track-50.ogg');
      let s = await state(page);
      check(s.titles.length === 50 && s.current === 'track-50.ogg', `the playlist starts with the first page of the folder (${s.titles.length} rows, 50 expected), on track-50`);
      await finish(page);
      await waitName(page, 'track-51.ogg');
      s = await state(page);
      check(s.name === 'track-51.ogg' && s.count === '51 / 52' && s.current === 'track-51.ogg', `track-51 plays from the second page, counted 51 / 52 (${s.count})`);
      await closeViewer(page);
    } finally {
      await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    }
  });

  // ---- 1a. a real double click opens the file, and it plays at once, with no press ----
  // The owner's decision: the first file opened starts on open. The double click is the browser's own input, so the browser
  // allows the start, and no note shows.
  await step('a double click opens the first file and it plays at once, with no press', async () => {
    await openFolderView(page, urlOf(x1Four), '1-one.ogg');
    await dblClick(page, await itemPoint(page, '1-one.ogg'));
    await page.waitFor(`!!document.querySelector('[data-testid="preview"]')`, 15000);
    await waitName(page, '1-one.ogg');
    await waitMedia(page);
    // Bounded: the file has to be moving within 10 s, or the wait fails the check below.
    await page.waitFor(`(() => { const v = document.querySelector(${q(MEDIA)}); return !!v && !v.paused && v.currentTime > 0; })()`, 10000).catch(() => undefined);
    check(await page.evaluate(`!document.querySelector(${q(MEDIA)}).paused`), 'a real double click plays the first file, with no press');
    check(await page.evaluate(`!document.querySelector('[data-testid="media-press"]')`), 'no press note shows when the browser allows the start');
    await closeViewer(page);
  });

  // ---- 1b. a play the browser refuses for want of a press: a note by the bar, cleared by a real press ----
  // A fresh document has no user gesture. The file opens from a script's double click, so the browser refuses the start
  // (the first file starts on open, and that start is refused too) and the bar says the browser waits for a press. A
  // trusted click on Play then plays the file, and the note goes.
  await step('a play the browser refuses shows a note by the bar, and a real press plays and clears it', async () => {
    await openFolderView(page, urlOf(x1Four), '1-one.ogg');
    await page.evaluate(`(() => { document.querySelector('[role="row"][aria-label="1-one.ogg"]').dispatchEvent(new MouseEvent('dblclick', { bubbles: true, cancelable: true })); return true; })()`);
    await page.waitFor(`!!document.querySelector('[data-testid="preview"]')`, 15000);
    await waitName(page, '1-one.ogg');
    // The viewer zooms in: the Play button's place is read only once the zoom has settled.
    await page.waitFor(`!document.querySelector('.ant-zoom-appear-active, .ant-zoom-enter-active')`, 10000);
    await waitMedia(page);
    await page.evaluate(`[...document.querySelectorAll('[data-testid="media-controls"] button')].find((b) => b.getAttribute('aria-label') === 'Play').click()`);
    await page.waitFor(`!!document.querySelector('[data-testid="media-press"]')`, 10000);
    check(await page.evaluate(`document.querySelector(${q(MEDIA)}).paused`), 'the scripted Play is refused: the file stays paused, and the bar shows the note');
    const at = await page.evaluate(`(() => { const b = [...document.querySelectorAll('[data-testid="media-controls"] button')].find((x) => x.getAttribute('aria-label') === 'Play'); const r = b.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
    await click(page, at);
    await page.waitFor(`!document.querySelector('[data-testid="media-press"]')`, 10000);
    check(await page.evaluate(`!document.querySelector(${q(MEDIA)}).paused`), 'a real press plays the file, and the note is gone');
    await closeViewer(page);
  });

  // ---- 1c. a tablet with touch: the volume rail is at least 100 px wide ----
  // Between the phone's width (under 600 px, the rail takes the bar's full width) and a desktop, the rail must still be
  // long enough to set a volume with a finger. The width is read from the rail the slider draws.
  await step('on a tablet with touch the volume rail is at least 100 px wide', async () => {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 768, height: 1024, deviceScaleFactor: 1, mobile: false });
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
    try {
      await openFolderView(page, urlOf(x1Four), '1-one.ogg');
      await playItem(page, '1-one.ogg');
      const rail = await page.evaluate(`(() => { const r = document.querySelector('.media-volume-slider .ant-slider-rail'); return r ? r.getBoundingClientRect().width : null; })()`);
      check(rail !== null && rail >= 100, `the volume rail is ${rail} px wide at 768 px with touch (100 px at least)`);
      await closeViewer(page);
    } finally {
      await page.send('Emulation.setTouchEmulationEnabled', { enabled: false });
      await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    }
  });

  // ---- 2. track end moves to the next file, and Next and Previous step through the playlist ----
  await step('the track end moves to the next file, and Previous goes back', async () => {
    await openFolderView(page, urlOf(x1Four), '1-one.ogg');
    await playItem(page, '1-one.ogg');
    await finish(page);
    await waitName(page, '2-two.ogg');
    // The bar belongs to the file that has mounted: wait for its element before the bar is read.
    await waitMedia(page);
    const previous = await page.evaluate(`(() => { const b = [...document.querySelectorAll('[data-testid="media-controls"] button')].find((x) => (x.getAttribute('aria-label') ?? '').startsWith('Previous')); if (!b) return [...document.querySelectorAll('[data-testid="media-controls"] button, [data-testid="media-stage"]')].map((x) => x.getAttribute('aria-label') ?? x.tagName + ' ' + x.className); b.click(); return null; })()`);
    if (previous) throw new Error(`no Previous on the bar: ${JSON.stringify(previous)}`);
    await waitName(page, '1-one.ogg');
    await waitMedia(page);
    check((await state(page)).name === '1-one.ogg', 'Previous goes back to 1-one.ogg');
    await page.evaluate(`[...document.querySelectorAll('[data-testid="media-controls"] button')].find((b) => (b.getAttribute('aria-label') ?? '').startsWith('Next')).click()`);
    await waitName(page, '2-two.ogg');
    const after = await state(page);
    check(after.name === '2-two.ogg' && after.current === '2-two.ogg', `the track end plays the next file, and Next moves on to 2-two.ogg (on ${after.name})`);
    await closeViewer(page);
  });

  // ---- 3. shuffle: four files, each once before any repeats ----
  await step('shuffle plays every file once before any repeats', async () => {
    await openFolderView(page, urlOf(x1Four), '1-one.ogg');
    await playItem(page, '1-one.ogg');
    await clickLabel(page, 'Shuffle');
    const seen = [(await state(page)).name];
    for (let i = 0; i < 3; i++) {
      await finish(page);
      await page.waitFor(`document.querySelector('[data-testid="preview"] header bdi')?.textContent !== ${q(seen[seen.length - 1])}`, 15000);
      await waitMedia(page);
      seen.push((await state(page)).name);
    }
    check(new Set(seen).size === 4, `shuffle played four different files: ${seen.join(', ')}`);
    await closeViewer(page);
  });

  // ---- 4. repeat one, repeat all and repeat off ----
  await step('repeat one loops the file, repeat all starts again, repeat off stops at the end', async () => {
    await openFolderView(page, urlOf(x1Four), '1-one.ogg');
    await playItem(page, '1-one.ogg');
    await clickLabel(page, 'Repeat:');
    await clickLabel(page, 'Repeat:');
    let s = await state(page);
    check(s.loop === true, `repeat one: the element loops (${s.loop})`);
    await finish(page);
    await sleep(1500);
    s = await state(page);
    check(s.name === '1-one.ogg' && s.t < 2, `repeat one: the file starts again at its beginning (${s.t}s)`);
    await closeViewer(page);

    await playItem(page, '4-four.ogg');
    await clickLabel(page, 'Repeat:');
    await finish(page);
    await waitName(page, '1-one.ogg');
    check((await state(page)).name === '1-one.ogg', 'repeat all: after the last file the first plays');
    await closeViewer(page);

    await playItem(page, '4-four.ogg');
    await finish(page);
    await page.waitFor(`/The playlist has ended\\./.test(document.querySelector('[data-testid="media-screen"]')?.textContent ?? '')`, 15000);
    s = await state(page);
    check(s.name === '4-four.ogg', 'repeat off: the end of the list stops playback with its message');
    await closeViewer(page);
  });

  // ---- 4b. Space: the player's on a new viewer and after a click on its bar; a control the keyboard reached keeps its Space ----
  await step('Space plays and pauses the player, after a click on its bar too, and a new viewer stays open', async () => {
    await openFolderView(page, urlOf(x1Four), '1-one.ogg');
    await playItem(page, '1-one.ogg');
    const viewerOpen = () => page.evaluate(`!!document.querySelector('[data-testid="preview"]')`);
    const paused = async () => (await state(page)).paused;
    const start = await paused();
    await key(page, ' ', 'Space', 32);
    await sleep(300);
    const afterSpace = await paused();
    check(afterSpace !== start && (await viewerOpen()), `Space on a new viewer plays or pauses the player (paused ${start} -> ${afterSpace}), and the viewer stays open`);
    // A mouse click on the bar's own button: the button keeps the focus, but not the keyboard's, so Space is the player's again.
    await page.evaluate(`(() => { const b = [...document.querySelectorAll('[data-testid="media-controls"] button')].find((x) => /^(Play|Pause)$/.test(x.getAttribute('aria-label') ?? '')); b.click(); return true; })()`);
    await sleep(300);
    const afterClick = await paused();
    await key(page, ' ', 'Space', 32);
    await sleep(300);
    check((await paused()) !== afterClick, `after a click on the bar, Space plays or pauses the player again (paused ${afterClick} -> ${await paused()})`);
    // A control the keyboard reached keeps its own Space: the Mute button, brought into focus by a key press, toggles mute.
    const muted = async () => (await state(page)).muted;
    const mutedBefore = await muted();
    const pausedBefore = await paused();
    await page.evaluate(`(() => { const b = [...document.querySelectorAll('[data-testid="media-controls"] button')].find((x) => /^(Mute|Unmute)$/.test(x.getAttribute('aria-label') ?? '')); b.focus(); return true; })()`);
    await key(page, ' ', 'Space', 32);
    await sleep(300);
    check((await muted()) !== mutedBefore && (await paused()) === pausedBefore, `a button the keyboard holds toggles with its Space: mute ${mutedBefore} -> ${await muted()}, play state kept`);
    // Unmute again: the mute is kept in the browser, and the keys check after this one starts unmuted.
    await page.evaluate(`(() => { const b = [...document.querySelectorAll('[data-testid="media-controls"] button')].find((x) => /^(Mute|Unmute)$/.test(x.getAttribute('aria-label') ?? '')); b.click(); return true; })()`);
    await sleep(200);
    // The style tags antd writes (the bar's own ConfigProvider included) all carry the page's nonce, as the strict policy needs.
    const styles = await page.evaluate(`(() => { const all = [...document.querySelectorAll('style[data-token-hash], style[data-css-hash]')]; return { total: all.length, bare: all.filter((s) => !s.nonce && !s.getAttribute('nonce')).length }; })()`);
    check(styles.total > 0 && styles.bare === 0, `antd's style tags carry the page's nonce (${styles.bare} of ${styles.total} without)`);
    await closeViewer(page);
    // The focus goes back to the page after the viewer closes, not to nothing. The page moves it on the next animation frame,
    // so the check waits up to 2 s for the focus to leave the body; a focus that never comes back still fails.
    await page.waitFor(`document.activeElement !== document.body`, 2000).catch(() => undefined);
    const back = await page.evaluate(`(() => { const a = document.activeElement; return a && a !== document.body ? a.tagName + ' ' + (a.getAttribute('aria-label') ?? a.textContent.trim().slice(0, 40)) : 'BODY'; })()`);
    check(back !== 'BODY', `after the viewer closes the focus is on the page (${back})`);
  });

  // ---- 5. reorder by drag, and by Alt with the arrow keys; the order is the one that plays ----
  await step('a dragged file plays in its new place, and so does one moved by the keyboard', async () => {
    await openFolderView(page, urlOf(x1Four), '1-one.ogg');
    await playItem(page, '1-one.ogg');
    const rows = () => state(page).then((s) => s.titles);
    check(JSON.stringify(await rows()) === JSON.stringify(['1-one.ogg', '2-two.ogg', '3-three.ogg', '4-four.ogg']), 'the panel lists the folder in its order');
    // Drag four's grip up to the row of one, in steps, as a hand does.
    const grip = await page.evaluate(`(() => { const r = [...document.querySelectorAll('[data-testid="media-row"]')].find((x) => x.querySelector('.media-title bdi')?.textContent === '4-four.ogg'); r.querySelector('.media-grip').scrollIntoView({ block: 'center' }); const g = r.querySelector('.media-grip').getBoundingClientRect(); return { x: g.left + g.width / 2, y: g.top + g.height / 2 }; })()`);
    const target = await page.evaluate(`(() => { const r = [...document.querySelectorAll('[data-testid="media-row"]')].find((x) => x.querySelector('.media-title bdi')?.textContent === '1-one.ogg'); const g = r.getBoundingClientRect(); return { x: g.left + g.width / 2, y: g.top + g.height / 2 }; })()`);
    await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: grip.x, y: grip.y });
    await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: grip.x, y: grip.y, button: 'left', buttons: 1, clickCount: 1 });
    for (let i = 1; i <= 10; i++) {
      const y = grip.y + ((target.y - grip.y) * i) / 10;
      await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: grip.x, y, button: 'left', buttons: 1 });
      await sleep(40);
    }
    await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: grip.x, y: target.y, button: 'left', buttons: 0, clickCount: 1 });
    await sleep(200);
    check(JSON.stringify(await rows()) === JSON.stringify(['4-four.ogg', '1-one.ogg', '2-two.ogg', '3-three.ogg']), `drag moved four to the top: ${(await rows()).join(', ')}`);
    // Alt+Up on three moves it above two (the row keeps the focus).
    await page.evaluate(`[...document.querySelectorAll('[data-testid="media-row"]')].find((x) => x.querySelector('.media-title bdi')?.textContent === '3-three.ogg').querySelector('.media-grip').focus()`);
    await key(page, 'ArrowUp', 'ArrowUp', 38, 1);
    check(JSON.stringify(await rows()) === JSON.stringify(['4-four.ogg', '1-one.ogg', '3-three.ogg', '2-two.ogg']), `Alt+Up moved three up one place: ${(await rows()).join(', ')}`);
    // Playing from one: the next file is the one after it in the new order.
    await page.evaluate(`[...document.querySelectorAll('[data-testid="media-queue"] .media-title')].find((b) => b.textContent === '1-one.ogg').click()`);
    await waitName(page, '1-one.ogg');
    await waitMedia(page);
    await finish(page);
    await waitName(page, '3-three.ogg');
    check((await state(page)).name === '3-three.ogg', 'the file after one, in the new order, is three');
    await closeViewer(page);
  });

  // ---- 6. a file that cannot play is marked and skipped after a pause; the playlist stops when all of them fail ----
  await step('a broken file is skipped after a pause, and the next file plays', async () => {
    await openFolderView(page, urlOf(x1Mixed), '1-ok.ogg');
    const before = linkMints();
    await playItem(page, '1-ok.ogg');
    await finish(page);
    await page.waitFor(`!!document.querySelector('[data-testid="media-screen"] .ant-alert-warning')`, 20000);
    const warning = await page.evaluate(`document.querySelector('[data-testid="media-screen"] .ant-alert-warning')?.textContent ?? ''`);
    check(/Skipping it\./.test(warning), `the broken file says that it is skipped (${warning.trim()})`);
    await waitName(page, '3-ok.ogg', 20000);
    await waitMedia(page);
    const mints = linkMints() - before;
    check(mints <= 5, `links asked for: ${mints} (one per file played, one renewal each for the broken file)`);
    const s = await state(page);
    check(s.titles.includes('2-bad.mp4') && s.name === '3-ok.ogg', 'the broken file stays in the panel, marked, and ok-2 plays');
    await closeViewer(page);
  });

  await step('when every file fails, the playlist stops with its message, with a bounded number of requests', async () => {
    await openFolderView(page, urlOf(x1Broken), 'bad-01.mp4');
    const before = linkMints();
    await playItem(page, 'bad-01.mp4', { wait: false });
    await page.waitFor(`/several files in a row could not play/.test(document.querySelector('[data-testid="media-screen"]')?.textContent ?? '')`, 90000);
    const atStop = linkMints() - before;
    await sleep(5000);
    const later = linkMints() - before;
    check(later === atStop, `no link is asked for after the stop (${atStop} at the stop, ${later} later)`);
    check(later <= 22, `links asked for by ten broken files: ${later} (at most one renewal each)`);
    await closeViewer(page);
  });

  // ---- 7. a link per file played: a normal run asks for one link per file, and no renewal ----
  await step('a run of three files asks for one link each', async () => {
    await openFolderView(page, urlOf(x1Four), '1-one.ogg');
    const before = linkMints();
    await playItem(page, '1-one.ogg');
    await finish(page);
    await waitName(page, '2-two.ogg');
    await waitMedia(page);
    await finish(page);
    await waitName(page, '3-three.ogg');
    await waitMedia(page);
    const mints = linkMints() - before;
    check(mints === 3, `three files played, ${mints} link requests`);
    await closeViewer(page);
  });

  // ---- 8. the bar: seek, speed, volume kept between files, and the keys ----
  await step('seek with the bar, pick a speed, and keep the volume for the next file', async () => {
    await openFolderView(page, urlOf(x1List), 'track-01.ogg');
    await playItem(page, 'track-01.ogg');
    const bar = await centre(page, '.media-seek .ant-slider');
    await click(page, { x: bar.left + bar.width * 0.5, y: bar.y });
    await sleep(800);
    const mid = await state(page);
    check(mid.t > 0.5 && mid.dur > 0, `a click on the bar seeks into the file (${mid.t}s)`);
    await click(page, await centre(page, '.media-speed .ant-select-selector'));
    await page.waitFor(`[...document.querySelectorAll('.ant-select-item-option')].some((o) => o.textContent.trim() === '2×')`, 10000);
    await page.evaluate(`[...document.querySelectorAll('.ant-select-item-option')].find((o) => o.textContent.trim() === '2×').click()`);
    await sleep(300);
    check((await state(page)).rate === 2, 'the speed menu sets 2x');
    await page.evaluate(`document.querySelector(${q(MEDIA)}).playbackRate = 1`);
    await click(page, await centre(page, '[data-testid="media-stage"]'));
    await key(page, 'ArrowDown', 'ArrowDown', 40);
    await key(page, 'ArrowDown', 'ArrowDown', 40);
    const vol = (await state(page)).vol;
    check(Math.abs(vol - 0.9) < 0.01, `two presses of Down take the volume to 0.9 (${vol})`);
    await closeViewer(page);
    await playItem(page, 'track-02.ogg');
    const next = (await state(page)).vol;
    check(Math.abs(next - 0.9) < 0.01, `the next file starts at the same volume (${next})`);
    await closeViewer(page);
  });

  await step('the keys: Space, K, J, L, the arrows, M and F', async () => {
    await openFolderView(page, urlOf(mediaTest), 'clip.webm');
    await playItem(page, 'clip.webm');
    await click(page, await centre(page, '[data-testid="media-stage"]'));
    await sleep(200);
    const found = (await state(page)).paused;
    await key(page, ' ', 'Space', 32);
    check((await state(page)).paused === !found, 'Space toggles play');
    await key(page, 'k', 'KeyK', 75);
    check((await state(page)).paused === found, 'K toggles play back');
    const at = (await state(page)).t;
    await key(page, 'l', 'KeyL', 76);
    await sleep(300);
    const afterL = (await state(page)).t;
    check(afterL - at > 8, `L seeks ten seconds on (${at.toFixed(1)} to ${afterL.toFixed(1)})`);
    await key(page, 'j', 'KeyJ', 74);
    await sleep(300);
    check((await state(page)).t < afterL - 8, 'J seeks ten seconds back');
    const beforeArrow = (await state(page)).t;
    await key(page, 'ArrowRight', 'ArrowRight', 39);
    await sleep(300);
    const afterArrow = (await state(page)).t;
    check(afterArrow - beforeArrow > 4, `the right arrow seeks five seconds on (${beforeArrow.toFixed(1)} to ${afterArrow.toFixed(1)})`);
    await key(page, 'm', 'KeyM', 77);
    check((await state(page)).muted === true, 'M mutes');
    await key(page, 'm', 'KeyM', 77);
    check((await state(page)).muted === false, 'M unmutes');
    await key(page, 'f', 'KeyF', 70);
    await sleep(400);
    const full = await page.evaluate(`document.fullscreenElement?.getAttribute('data-testid') ?? null`);
    check(full === 'media-stage', `F makes the stage full screen (${full})`);
    await key(page, 'f', 'KeyF', 70);
    await sleep(300);
    check((await page.evaluate(`document.fullscreenElement === null`)) === true, 'F again leaves full screen');
    await closeViewer(page);
  });

  // ---- 9. a phone, in Hebrew: the bar reads left to right, the targets are 44 pixels, and the page does not scroll sideways ----
  await step('on a phone in Hebrew the bar reads left to right and nothing scrolls sideways', async () => {
    await setPhone(page, true);
    await setLanguage(page, 'עברית');
    await openFolderView(page, urlOf(x1List), 'track-04.ogg');
    await playItem(page, 'track-04.ogg');
    const m = await page.evaluate(`(() => {
      const times = [...document.querySelectorAll('[data-testid="media-controls"] .media-time')];
      const buttons = [...document.querySelectorAll('[data-testid="media-controls"] .media-btn')];
      const controls = document.querySelector('[data-testid="media-controls"]');
      return {
        ltr: getComputedStyle(controls).direction === 'ltr',
        timesInOrder: times.length === 2 && times[0].getBoundingClientRect().left < times[1].getBoundingClientRect().left,
        smallest: Math.min(...buttons.map((b) => b.getBoundingClientRect().height)),
        // The speed list and each slider handle are touch targets: their boxes are read, not their drawn ring.
        speed: document.querySelector('[data-testid="media-controls"] .media-speed')?.getBoundingClientRect().height ?? 0,
        handles: [...document.querySelectorAll('[data-testid="media-controls"] .ant-slider-handle')].map((h) => {
          const r = h.getBoundingClientRect();
          return { w: r.width, h: r.height };
        }),
        overflow: document.documentElement.scrollWidth - window.innerWidth,
      };
    })()`);
    check(m.ltr && m.timesInOrder, 'the bar reads left to right in Hebrew: its times keep their order');
    check(m.smallest >= 44, `the bar's buttons are at least 44 px tall on a phone (${m.smallest})`);
    check(m.speed >= 44, `the speed list is at least 44 px tall on a phone (${m.speed})`);
    check(m.handles.length >= 2 && m.handles.every((h) => h.w >= 44 && h.h >= 44), `every slider handle is at least 44 by 44 px on a phone (${JSON.stringify(m.handles)})`);
    check(m.overflow <= 0, `no sideways scroll on a phone (${m.overflow} px over)`);
    await closeViewer(page);
    await setLanguage(page, 'English');
    await setPhone(page, false);
  });

  // ---- 9b. the speed list: a key with it focused still plays (its input is not a field), and in full screen its list opens on
  // the stage, which is the only thing the browser shows then ----
  await step('the speed list keeps the keys, and opens on the stage in full screen', async () => {
    await openFolderView(page, urlOf(mediaTest), 'clip.webm');
    await playItem(page, 'clip.webm');
    await page.evaluate(`document.querySelector('.media-speed input').focus()`);
    const before = (await state(page)).paused;
    await key(page, 'k', 'KeyK', 75);
    check((await state(page)).paused === !before, 'K still plays and pauses with the speed list focused');
    await key(page, 'f', 'KeyF', 70);
    await sleep(400);
    const full = await page.evaluate(`document.fullscreenElement?.getAttribute('data-testid') ?? null`);
    check(full === 'media-stage', `F makes the stage full screen with the speed list in place (${full})`);
    await click(page, await centre(page, '.media-speed .ant-select-selector'));
    await page.waitFor(`!!document.querySelector('.ant-select-dropdown:not(.ant-select-dropdown-hidden)')`, 5000);
    const inside = await page.evaluate(`(() => { const d = document.querySelector('.ant-select-dropdown:not(.ant-select-dropdown-hidden)'); return !!d && document.querySelector('[data-testid="media-stage"]').contains(d); })()`);
    check(inside, 'in full screen the speed list opens on the stage');
    await click(page, await centre(page, '.media-speed .ant-select-selector'));
    await page.evaluate(`document.exitFullscreen().then(() => true, () => false)`);
    await sleep(300);
    await closeViewer(page);
  });

  // ---- 10. the look: the bar's icons and time read over a white frame, the row and the audio panel read in both themes,
  // the sliders run left to right in Hebrew, one-line rows at 390 px and 1280 px, and the viewer's edges on a phone ----
  // soft records a result and goes on, so one run shows every gap (check stops at its first failure).
  const soft = (ok, what) => {
    console.log(`${ok ? 'PASS' : 'FAIL'} ${what}`);
    if (!ok) failures.push(what);
  };
  const lookURL = urlOf(id('x1-look'));
  // LOOK is page-side code the measurements share: colours as numbers, their contrast, and the colour behind the bar.
  const LOOK = `
    const rgba = (s) => { const m = s.match(/[\\d.]+/g).map(Number); return { r: m[0], g: m[1], b: m[2], a: m.length > 3 ? m[3] : 1 }; };
    const lin = (v) => { const c = v / 255; return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); };
    const lum = (c) => 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b);
    const contrast = (a, b) => { const x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
    // A video's bar is a scrim over the picture. The brightest picture is white, so the scrim is measured over white.
    const overWhite = (c) => ({ r: c.a * c.r + (1 - c.a) * 255, g: c.a * c.g + (1 - c.a) * 255, b: c.a * c.b + (1 - c.a) * 255, a: 1 });
  `;
  const measureBar = () =>
    page.evaluate(`(() => {
      ${LOOK}
      const controls = document.querySelector('[data-testid="media-controls"]');
      const video = controls.parentElement.dataset.kind === 'video';
      const barBg = rgba(getComputedStyle(controls).backgroundColor);
      const behind = video ? overWhite(barBg) : barBg;
      const buttons = [...controls.querySelectorAll('.media-btn')].map((b) => {
        const own = rgba(getComputedStyle(b).backgroundColor);
        const back = own.a >= 1 ? own : behind;
        return { label: b.getAttribute('aria-label'), ratio: contrast(rgba(getComputedStyle(b.querySelector('svg')).color), back) };
      });
      const time = contrast(rgba(getComputedStyle(controls.querySelector('.media-time')).color), behind);
      const row = document.querySelector('[data-testid="media-queue"] .media-row--on');
      const rowBg = rgba(getComputedStyle(row).backgroundColor);
      const rowText = contrast(rgba(getComputedStyle(row.querySelector('.media-title')).color), rowBg);
      const rowState = contrast(rgba(getComputedStyle(row.querySelector('.media-state')).color), rowBg);
      const rect = (s) => document.querySelector(s).getBoundingClientRect();
      const seek = rect('.media-seek .ant-slider-rail');
      const seekH = rect('.media-seek .ant-slider-handle');
      const vol = rect('.media-volume .ant-slider-rail');
      const volH = rect('.media-volume .ant-slider-handle');
      return {
        video,
        buttons,
        time,
        panelLum: lum(barBg),
        rowText,
        rowState,
        seekAtStart: seekH.left + seekH.width / 2 < seek.left + seek.width / 2,
        volAtFull: volH.left + volH.width / 2 > vol.left + vol.width / 2,
      };
    })()`);
  // setAt puts the file on screen at its start, at full volume and unmuted, and pauses it, so the bar is read at rest.
  const setAt = (pause) =>
    page.evaluate(`(() => { const m = document.querySelector(${q(MEDIA)}); m.currentTime = 0; m.volume = 1; m.muted = false; if (${pause}) m.pause(); return true; })()`);

  // A check before this one may have failed inside its phone step, so the desktop width and English come back first.
  await setPhone(page, false);
  await setLanguage(page, 'English');
  for (const theme of ['Light', 'Dark']) {
    for (const lang of ['English', 'עברית']) {
      const tag = `${theme} ${lang}`;
      await step(`the bar, the row and the audio panel read in ${tag}`, async () => {
        // The theme's switch is read in English, so the language is English while it is chosen, then it is the round's.
        await setLanguage(page, 'English');
        await setTheme(page, theme);
        await setLanguage(page, lang);
        await openFolderView(page, lookURL, 'bright.webm');
        await playItem(page, 'bright.webm');
        await setAt(true);
        await sleep(400);
        const v = await measureBar();
        for (const b of v.buttons) soft(b.ratio >= 3, `${tag}: video control "${b.label}" icon reads ${b.ratio.toFixed(2)}:1 over the bar (need 3:1)`);
        soft(v.time >= 4.5, `${tag}: the time reads ${v.time.toFixed(2)}:1 over a white frame (need 4.5:1)`);
        soft(v.rowText >= 4.5 && v.rowState >= 4.5, `${tag}: the playing row reads ${v.rowText.toFixed(2)}:1, its mark ${v.rowState.toFixed(2)}:1 (need 4.5:1)`);
        await closeViewer(page);

        await playItem(page, 'short.ogg');
        await setAt(false);
        await sleep(400);
        const a = await measureBar();
        soft(theme === 'Dark' ? a.panelLum < 0.1 : a.panelLum > 0.7, `${tag}: the audio panel has luminance ${a.panelLum.toFixed(3)} (dark needs under 0.1, light over 0.7)`);
        for (const b of a.buttons) soft(b.ratio >= 3, `${tag}: audio control "${b.label}" icon reads ${b.ratio.toFixed(2)}:1 (need 3:1)`);
        soft(a.rowText >= 4.5 && a.rowState >= 4.5, `${tag}: the audio row reads ${a.rowText.toFixed(2)}:1, its mark ${a.rowState.toFixed(2)}:1 (need 4.5:1)`);
        soft(a.seekAtStart, `${tag}: at the start the seek thumb is left of the bar's middle`);
        soft(a.volAtFull, `${tag}: at full volume the volume thumb is right of the bar's middle`);
        await closeViewer(page);
      });
    }
  }
  await step('the theme and the language are put back', async () => {
    await setLanguage(page, 'English');
    await setTheme(page, 'Light');
  });

  await step('a playlist row is one line tall at 390 px and at 1280 px, with a long name', async () => {
    for (const phone of [true, false]) {
      await setPhone(page, phone);
      await openFolderView(page, lookURL, 'bright.webm');
      await playItem(page, 'bright.webm');
      const r = await page.evaluate(`(() => {
        const row = [...document.querySelectorAll('[data-testid="media-row"]')].find((x) => (x.querySelector('.media-title bdi')?.textContent ?? '').startsWith('a-very-long'));
        if (!row) return null;
        return { row: row.getBoundingClientRect().height, name: row.querySelector('.media-title bdi').getBoundingClientRect().height };
      })()`);
      soft(r !== null && r.row <= 56 && r.name <= 28, `${phone ? 390 : 1280} px: the long-named row is ${r?.row} px tall, its name line ${r?.name} px (one line: 56 px and 28 px at most)`);
      await closeViewer(page);
    }
    await setPhone(page, false);
  });

  await step('on a phone in Hebrew the viewer has no clipped control, one scroller at most, and the bar is not in a scroller', async () => {
    await setPhone(page, true);
    await setLanguage(page, 'עברית');
    await openFolderView(page, lookURL, 'bright.webm');
    await playItem(page, 'bright.webm');
    const e = await page.evaluate(`(() => {
      const root = document.querySelector('[data-testid="preview"]');
      const modal = root.matches('.ant-modal-content') ? root : (root.querySelector('.ant-modal-content') ?? root);
      const box = modal.getBoundingClientRect();
      const buttons = [...root.querySelectorAll('button'), ...document.querySelectorAll('[data-testid="media-controls"] button')].filter((b) => b.getBoundingClientRect().width > 0);
      const clipped = buttons
        .filter((b) => { const r = b.getBoundingClientRect(); return r.left < box.left - 0.5 || r.right > box.right + 0.5; })
        .map((b) => b.getAttribute('aria-label') ?? b.textContent.trim());
      const scrolls = (el) => { const s = getComputedStyle(el); return (s.overflowY === 'auto' || s.overflowY === 'scroll') && el.scrollHeight > el.clientHeight + 1; };
      const scrollers = [...root.querySelectorAll('*')].filter(scrolls).length;
      let around = 0;
      for (let el = document.querySelector('[data-testid="media-controls"]').parentElement; el; el = el.parentElement) if (scrolls(el)) around++;
      return {
        clipped,
        scrollers,
        around,
        overflow: document.documentElement.scrollWidth - window.innerWidth,
        steps: document.querySelectorAll('[data-testid="preview"] .media-steps').length,
        headerButtons: document.querySelectorAll('[data-testid="preview"] header button').length,
      };
    })()`);
    soft(e.clipped.length === 0, `390 px, Hebrew: no control clipped by the viewer's edge (${e.clipped.join(', ') || 'none'})`);
    soft(e.scrollers <= 1, `390 px, Hebrew: one vertical scroller at most (${e.scrollers})`);
    soft(e.around === 0, `390 px, Hebrew: the bar is not inside a scroller (${e.around} above it)`);
    soft(e.overflow <= 0, `390 px, Hebrew: no sideways scroll (${e.overflow} px over)`);
    soft(e.steps === 0, `a video has no second Previous and Next below the player (${e.steps} steps rows)`);
    soft(e.headerButtons >= 2, `the viewer header holds Download beside the count (${e.headerButtons} buttons)`);
    await closeViewer(page);
    await setLanguage(page, 'English');
    await setPhone(page, false);
  });

  // ---- 11. screenshots for the owner's review, when the run asks for them ----
  if (process.env.SELFCLOUD_E2E_SHOTS_X1) {
    await step('the screenshots of the player', async () => {
      await shotsX1(page, { folderURL: urlOf(mediaTest), lookURL: urlOf(id('x1-look')), dir: process.env.SELFCLOUD_E2E_SHOTS_X1, setTheme });
    });
  }

  if (failures.length > 0) throw new Error(`${failures.length} player check(s) failed: ${failures.join(' | ')}`);
}
