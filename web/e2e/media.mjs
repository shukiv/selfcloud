// SPDX-License-Identifier: AGPL-3.0-or-later

// The media checks of the file browser: a double click opens the file under the pointer whatever was selected, one
// tap on a phone opens a file or a folder, a selection leaves the listing where it was, the play marks and the actions
// they name, real playback of a video, an audio file and a picture, a long video that keeps playing when its signed
// link expires, and a file this browser cannot play. Mouse and touch go through the browser's own input events.
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { pngOf } from './s4a.mjs';

const media = fileURLToPath(new URL('./media/', import.meta.url));
const q = (v) => JSON.stringify(v);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// The file that is not media, though its name says it is: an MP4 box header, then bytes no decoder reads.
const BROKEN = Buffer.concat([
  Buffer.from([0, 0, 0, 16, 0x66, 0x74, 0x79, 0x70, 0x69, 0x73, 0x6f, 0x6d, 0, 0, 2, 0]),
  Buffer.from(Array.from({ length: 4096 }, (_, i) => (i * 131 + 7) & 255)),
]);

// itemSel selects the listing's row (list view) or tile (tiles view) of the item with this name.
const itemSel = (name) => `[role="row"][aria-label=${q(name)}], [role="option"][aria-label=${q(name)}]`;

// point gives the centre of an item's name, scrolled into view, in viewport coordinates.
async function point(page, name) {
  const p = await page.evaluate(`(() => {
    const row = document.querySelector(${q(itemSel(name))});
    if (!row) return null;
    row.scrollIntoView({ block: 'center' });
    const r = (row.querySelector('bdi') ?? row).getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  })()`);
  if (!p) throw new Error(`no item named ${name}`);
  return p;
}

// dblClick sends a mouse double click the way Chrome does: press and release, then press and release again with the
// click count at two, a tenth of a second apart unless gap says otherwise. The browser picks the target under the pointer,
// as it does for a person.
async function dblClick(page, { x, y }, gap = 100) {
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
  for (const clickCount of [1, 2]) {
    await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', buttons: 1, clickCount });
    await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', buttons: 0, clickCount });
    await sleep(gap);
  }
}

// click is one mouse click.
async function click(page, { x, y }) {
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
  await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', buttons: 1, clickCount: 1 });
  await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', buttons: 0, clickCount: 1 });
}

// tap is one touch on a phone: a finger down and up. The browser makes the click from it.
async function tap(page, { x, y }) {
  await page.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] });
  await sleep(60);
  await page.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
}

// openedName waits for the preview to open and returns the name it shows, or null when none opens.
async function openedName(page) {
  await sleep(900);
  return page.evaluate(`(() => {
    const d = document.querySelector('[data-testid="preview"]');
    return d ? (d.querySelector('header bdi')?.textContent ?? '') : null;
  })()`);
}

// browserTitle is the name of the folder the browser shows.
const browserTitle = (page) => page.evaluate(`document.querySelector('#browser-title')?.textContent ?? ''`);

async function closePreview(page) {
  // The header's first button is Close, in every language.
  const clicked = await page.evaluate(`(() => { const b = document.querySelector('[data-testid="preview"] header button'); if (!b) return 'no Close button'; b.click(); return 'clicked the first header button'; })()`);
  try {
    await page.waitFor(`!document.querySelector('[data-testid="preview"]')`, 10000);
  } catch (err) {
    const state = await page.evaluate(`(() => ({ previews: document.querySelectorAll('[data-testid="preview"]').length, width: innerWidth, title: document.querySelector('[data-testid="preview"] header bdi')?.textContent ?? null, buttons: [...document.querySelectorAll('[data-testid="preview"] button')].map((b) => b.textContent.trim()) }))()`);
    throw new Error(`${err.message}; ${clicked}; ${JSON.stringify(state)}`);
  }
}

// barButtons is the text of the buttons in the selection bar, which is there while something is selected. The bar
// is found by its test id, so the check reads the same in every language.
const barButtons = (page) =>
  page.evaluate(`[...document.querySelectorAll('[data-testid="selection-bar"] button')].map((b) => b.textContent.trim()).filter(Boolean)`);

// clearSelection drops the selection, through the bar's own last button (Clear selection), when the bar is there.
async function clearSelection(page) {
  if (!(await page.evaluate(`!!document.querySelector('[data-testid="selection-bar"]')`))) return;
  await page.evaluate(`(() => { const b = [...document.querySelectorAll('[data-testid="selection-bar"] button')].pop(); b.click(); return true; })()`);
  await page.waitFor(`!document.querySelector('[data-testid="selection-bar"]')`, 10000);
}

// docTop is the top of an item's row or tile in the document, so a scroll does not read as a move.
const docTop = (page, name) =>
  page.evaluate(`(() => { const el = document.querySelector(${q(itemSel(name))}); return el ? el.getBoundingClientRect().top + window.scrollY : null; })()`);

// setPhone turns the phone emulation on or off: a 390 pixel touch screen, or the desktop window.
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

// The expiry check asks for links that live six seconds, and throttles the connection so the browser does not finish
// the file before the link ends. The rewrite happens in the browser's network layer; the server still checks the link
// it signed, and the run counts the links asked for.
const SHORT_TTL = 6;

// setTheme picks Light or Dark from the top bar's theme switch, which is read in English.
async function setTheme(page, name) {
  await page.waitFor(`[...document.querySelectorAll('[data-testid="theme"] .ant-segmented-item-label')].some((x) => x.textContent.trim() === ${q(name)})`, 15000);
  await page.evaluate(`[...document.querySelectorAll('[data-testid="theme"] .ant-segmented-item-label')].find((x) => x.textContent.trim() === ${q(name)}).click()`);
  await page.waitFor(`document.documentElement.dataset.theme === ${q(name.toLowerCase())}`, 10000);
}

// shot saves the page as a PNG, in dir.
async function shot(page, dir, name) {
  const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
  await writeFile(join(dir, name), Buffer.from(data, 'base64'));
}

// shots saves the pictures of the browser the owner sees: the listing with a selection (its bar in place), and a
// video playing, for each width, theme and language.
async function shots(page, { folderURL, dir }) {
  await mkdir(dir, { recursive: true });
  for (const theme of ['Light', 'Dark']) {
    await page.goto(folderURL);
    await setTheme(page, theme);
    for (const lang of ['English', 'עברית']) {
      await setLanguage(page, lang);
      for (const width of ['desktop', 'phone']) {
        if (width === 'phone') await setPhone(page, true);
        const tag = `${width}-${theme.toLowerCase()}-${lang === 'English' ? 'en' : 'he'}`;
        await page.goto(folderURL);
        await page.waitFor(`!!document.querySelector('[role="row"][aria-label="clip.webm"]')`, 15000);
        await page.evaluate(`document.querySelector('[role="row"][aria-label="clip.webm"]').click()`);
        await page.waitFor(`!!document.querySelector('[data-testid="selection-bar"]')`, 10000);
        await sleep(400);
        await shot(page, dir, `listing-${tag}.png`);
        await clearSelection(page);
        await dblClick(page, await point(page, 'clip.webm'));
        await page.waitFor(`(() => { const v = document.querySelector('[data-testid="preview"] video'); return !!v && v.readyState >= 2; })()`, 20000);
        await page.evaluate(`document.querySelector('[data-testid="preview"] video').play().then(() => true, () => false)`);
        await sleep(2000);
        await shot(page, dir, `video-${tag}.png`);
        await closePreview(page);
        if (width === 'phone') await setPhone(page, false);
      }
      await setLanguage(page, 'English').catch(() => undefined);
    }
  }
  await setTheme(page, 'Light');
}

export async function mediaChecks(h, { page, base, accountKey, teamA }) {
  const { check, api, rawPut, clickButton } = h;
  const failures = [];
  // step runs one group of checks. A failed check is recorded and the rest still run, so one run shows every gap.
  const step = async (name, fn) => {
    try {
      await fn();
    } catch (err) {
      failures.push(`${name}: ${err.message}`);
      console.log(`FAIL ${name}: ${err.message}`);
    }
  };

  // linkHook, when set, answers the browser's paused requests for links: the expiry check sets it, and every other
  // request goes on unchanged.
  let linkHook = null;
  page.mintTimes = [];
  page.on('Fetch.requestPaused', (p) => {
    if (linkHook) return linkHook(p);
    return page.send('Fetch.continueRequest', { requestId: p.requestId });
  });
  const linkMints = () => page.sent.filter((r) => r.method === 'POST' && /\/api\/v1\/nodes\/[^/]+\/links$/.test(r.url)).length;

  // ---- the files: a folder with a video, an audio file, a picture, a text file, a broken video and a folder ----
  await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: '/media-test', parents: false });
  await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: '/media-test/Sub', parents: false });
  const put = (name, body, type) =>
    rawPut(base, `/api/v1/folders/${teamA}/files?path=${encodeURIComponent(`/media-test/${name}`)}`, accountKey, body, type);
  await put('clip.webm', await readFile(join(media, 'clip.webm')), 'video/webm');
  await put('tone.ogg', await readFile(join(media, 'tone.ogg')), 'audio/ogg');
  await put('pic.png', pngOf(120, 80, (x, y) => [x * 2, y * 3, 180]), 'image/png');
  await put('notes.txt', Buffer.from('plain text, shown as text\n'), 'text/plain');
  await put('broken.mp4', BROKEN, 'video/mp4');
  // A file inside the folder Sub: a double tap that opens Sub must not open this one, which the second tap would reach.
  await rawPut(base, `/api/v1/folders/${teamA}/files?path=${encodeURIComponent('/media-test/Sub/inside.txt')}`, accountKey, Buffer.from('inside Sub\n'), 'text/plain');
  const root = (await api(base, 'GET', `/api/v1/folders/${teamA}/children?path=%2F`, accountKey)).items;
  const folderID = root.find((n) => n.name === 'media-test').id;
  const folderURL = `${page.base}/files/${teamA}/${folderID}`;
  const clipID = (await api(base, 'GET', `/api/v1/folders/${teamA}/children?path=%2Fmedia-test`, accountKey)).items.find((n) => n.name === 'clip.webm').id;

  // open loads the folder afresh, in the view asked for, with nothing selected.
  const open = async (view) => {
    await page.goto(folderURL);
    // The view buttons are the first two of the view group, so they are found by place: their words change with the language.
    const at = view === 'List' ? 0 : 1;
    await page.waitFor(`!!document.querySelectorAll('div[role="group"] > button[aria-pressed]')[${at}]`, 15000);
    await page.evaluate(`document.querySelectorAll('div[role="group"] > button[aria-pressed]')[${at}].click()`);
    await page.waitFor(`!!document.querySelector(${q(itemSel('pic.png'))})`, 15000);
    await clearSelection(page);
  };

  // ---- 1. a double click with nothing selected: the first row, a lower row, and a folder, in both views ----
  for (const view of ['List', 'Tiles']) {
    for (const name of ['broken.mp4', 'tone.ogg']) {
      await step(`double-click ${name} in ${view}`, async () => {
        await open(view);
        await dblClick(page, await point(page, name));
        const got = await openedName(page);
        check(got === name, `double-click on ${name} with nothing selected, in ${view}, opens ${name} (opened ${got ?? 'nothing'})`);
        await closePreview(page);
      });
    }
    await step(`double-click the folder in ${view}`, async () => {
      await open(view);
      await dblClick(page, await point(page, 'Sub'));
      await sleep(600);
      const title = await browserTitle(page);
      check(title === 'Sub', `double-click on the folder Sub, in ${view}, opens it (shows ${title || 'nothing'})`);
    });
  }

  // ---- 2. on a phone one tap opens a file or a folder, and the checkbox selects ----
  await step('one tap on a phone', async () => {
    await setPhone(page, true);
    await open('List');
    await tap(page, await point(page, 'tone.ogg'));
    const got = await openedName(page);
    check(got === 'tone.ogg', `one tap on tone.ogg on a phone opens it (opened ${got ?? 'nothing'})`);
    await closePreview(page);
    await open('List');
    await tap(page, await point(page, 'Sub'));
    await sleep(600);
    const title = await browserTitle(page);
    check(title === 'Sub', `one tap on the folder Sub on a phone opens it (shows ${title || 'nothing'})`);
    await open('Tiles');
    await tap(page, await point(page, 'tone.ogg'));
    const tile = await openedName(page);
    check(tile === 'tone.ogg', `one tap on a tile on a phone opens the file (opened ${tile ?? 'nothing'})`);
    await closePreview(page);
    await open('List');
    const box = await page.evaluate(`(() => { const r = document.querySelector(${q(itemSel('pic.png'))}).querySelector('input[type="checkbox"]')?.getBoundingClientRect(); return r ? { x: r.left + r.width / 2, y: r.top + r.height / 2 } : null; })()`);
    check(box !== null, 'a phone list has a checkbox for each row');
    await tap(page, box);
    await sleep(300);
    const picked = await page.evaluate(`[...document.querySelectorAll('p')].some((p) => p.textContent.trim() === '1 selected')`);
    check(picked, 'a tap on the checkbox selects the row on a phone');
    await clearSelection(page);
    // A tile has no checkbox on the desktop; on a finger's screen it has one, so a tile can be selected too.
    await open('Tiles');
    const tileBox = await page.evaluate(`(() => { const r = document.querySelector('[role="option"][aria-label="pic.png"] input[type="checkbox"]')?.getBoundingClientRect(); return r ? { x: r.left + r.width / 2, y: r.top + r.height / 2 } : null; })()`);
    check(tileBox !== null, 'a phone tile has a checkbox');
    await tap(page, tileBox);
    await sleep(300);
    check(await page.evaluate(`[...document.querySelectorAll('p')].some((p) => p.textContent.trim() === '1 selected')`), 'a tap on a tile checkbox selects the tile on a phone');
    await clearSelection(page);
    await setPhone(page, false);
  });

  // ---- 2b. a finger's double tap on a folder opens the folder, and not a row the second tap reaches inside it ----
  await step('a double tap on a folder opens the folder only', async () => {
    await setPhone(page, true);
    await open('List');
    const at = await point(page, 'Sub');
    await tap(page, at);
    await sleep(120);
    await tap(page, at);
    const got = await openedName(page);
    const title = await browserTitle(page);
    check(title === 'Sub', `a double tap on the folder Sub opens it (shows ${title || 'nothing'})`);
    check(got === null, `the second tap of a double tap opens no file inside the folder (opened ${got ?? 'nothing'})`);
    if (got !== null) await closePreview(page);
    await setPhone(page, false);
  });

  // ---- 2c. at phone width the selection bar is one row: the main action, a More menu and Clear ----
  await step('the selection bar is one row on a phone', async () => {
    await setPhone(page, true);
    await open('List');
    await click(page, await point(page, 'clip.webm'));
    await page.waitFor(`!!document.querySelector('[data-testid="selection-bar"]')`, 10000);
    await sleep(300);
    const bar = await page.evaluate(`(() => { const b = document.querySelector('[data-testid="selection-bar"]'); return b ? b.getBoundingClientRect().height : null; })()`);
    check(bar !== null && bar <= 64, `the selection bar on a phone is one row (${bar} px tall)`);
    const lines = await page.evaluate(`(() => { const r = document.createRange(); r.selectNodeContents(document.querySelector('[data-testid="selection-bar"] p')); return new Set([...r.getClientRects()].map((x) => Math.round(x.top))).size; })()`);
    check(lines === 1, `the count on a phone's selection bar is one line, with no word split (${lines} lines)`);
    check(await page.evaluate(`!!document.querySelector('[data-testid="selection-bar"] button[aria-label="Clear selection"]')`), "a phone's Clear button keeps its name for a screen reader");
    await page.evaluate(`[...document.querySelectorAll('[data-testid="selection-bar"] button')].find((b) => b.textContent.trim() === 'More').click()`);
    await page.waitFor(`[...document.querySelectorAll('.ant-dropdown-menu-item')].some((x) => x.textContent.trim() === 'Details')`, 10000);
    const danger = await page.evaluate(`(() => { const d = [...document.querySelectorAll('.ant-dropdown-menu-item')].find((x) => x.textContent.trim() === 'Delete'); return d ? d.classList.contains('ant-dropdown-menu-item-danger') : null; })()`);
    check(danger === true, `Delete is in the More menu, marked as destructive (${danger})`);
    await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
    await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
    await clearSelection(page);
    await setPhone(page, false);
  });

  // ---- 2d. the bar appears under the pointer: in a short window the last rows sit where the bar lands, and the second
  // click of a double click still reaches the row, not a button on the bar ----
  await step('a double click on a row where the bar lands opens the row', async () => {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 460, deviceScaleFactor: 1, mobile: false });
    await open('List');
    for (const [name, gap] of [['tone.ogg', 100], ['pic.png', 300]]) {
      const box = await page.evaluate(`(() => {
        const row = document.querySelector(${q(itemSel(name))});
        row.scrollIntoView({ block: 'end' });
        const r = (row.querySelector('bdi') ?? row).getBoundingClientRect();
        return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
      })()`);
      await dblClick(page, box, gap);
      await sleep(900);
      const got = await openedName(page);
      const dialog = await page.evaluate(`[...document.querySelectorAll('.ant-modal-content')].map((d) => d.innerText.split('\\n')[0]).join('|')`);
      check(got === name, `a double click on ${name} under the bar's place, ${gap} ms apart, opens it (opened ${got ?? 'nothing'}; dialog "${dialog}")`);
      if (got) await closePreview(page);
      await clearSelection(page);
    }
    // Once settled the bar takes clicks: a real mouse click on Clear selection reaches the button.
    await click(page, await point(page, 'clip.webm'));
    await page.waitFor(`!!document.querySelector('[data-testid="selection-bar"]')`, 10000);
    await sleep(800);
    const clear = await page.evaluate(`(() => {
      const b = [...document.querySelectorAll('[data-testid="selection-bar"] button')].pop();
      b.scrollIntoView({ block: 'nearest' });
      const r = b.getBoundingClientRect();
      return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
    })()`);
    await click(page, clear);
    await sleep(300);
    check(await page.evaluate(`!document.querySelector('[data-testid="selection-bar"]')`), 'a settled selection bar takes a real mouse click');
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
  });

  // ---- 3. a selection moves nothing: the rows keep their place, at desktop and phone width, in English and Hebrew ----
  const layoutCheck = async (label) => {
    for (const view of ['List', 'Tiles']) {
      await step(`selecting keeps the listing still (${label}, ${view})`, async () => {
        await open(view);
        const before = await docTop(page, 'pic.png');
        await click(page, await point(page, 'clip.webm'));
        await sleep(300);
        const after = await docTop(page, 'pic.png');
        check(before !== null && after !== null && Math.abs(before - after) < 0.5, `a row does not move when selected (${label}, ${view}: ${before} to ${after})`);
        check((await barButtons(page)).length > 0, `the selection bar is on the page (${label}, ${view})`);
      });
    }
  };
  await layoutCheck('desktop');
  await setPhone(page, true);
  await layoutCheck('phone');
  await setPhone(page, false);
  await step('the selection bar in Hebrew', async () => {
    await page.goto(folderURL);
    await setLanguage(page, 'עברית');
    await layoutCheck('Hebrew');
    await setLanguage(page, 'English');
  });

  // ---- 4. the play marks, and the actions they name ----
  await step('the marks and the action labels', async () => {
    await open('List');
    const mark = (name) =>
      page.evaluate(`(() => { const m = document.querySelector(${q(itemSel(name))})?.querySelector('[data-testid="play-mark"]'); return m ? { kind: m.dataset.kind, label: m.textContent.trim(), w: m.getBoundingClientRect().width } : null; })()`);
    const video = await mark('clip.webm');
    check(video && video.kind === 'video' && video.w > 0 && /Video/.test(video.label), `a video's row shows the play mark with its label (${JSON.stringify(video)})`);
    const audio = await mark('tone.ogg');
    check(audio && audio.kind === 'audio', `an audio file's row shows the play mark (${JSON.stringify(audio)})`);
    check((await mark('pic.png')) === null, 'a picture shows no play mark');
    await click(page, await point(page, 'clip.webm'));
    await sleep(200);
    check((await barButtons(page)).includes('Play'), `a selected video offers Play (${(await barButtons(page)).join(' / ')})`);
    await clearSelection(page);
    await click(page, await point(page, 'pic.png'));
    await sleep(200);
    check((await barButtons(page)).includes('View'), 'a selected picture offers View');
    await clearSelection(page);
    await click(page, await point(page, 'notes.txt'));
    await sleep(200);
    check((await barButtons(page)).includes('Preview'), 'a selected text file offers Preview');
    await clearSelection(page);
  });

  // ---- 5. Enter on a focused row opens it ----
  await step('Enter opens the focused row', async () => {
    await open('List');
    await page.evaluate(`(() => { document.querySelector(${q(itemSel('tone.ogg'))}).focus(); return true; })()`);
    await sleep(100);
    await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
    await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
    const got = await openedName(page);
    check(got === 'tone.ogg', `Enter on the focused row opens tone.ogg (opened ${got ?? 'nothing'})`);
    await closePreview(page);
  });

  // ---- 6. real playback: a video, an audio file and a picture ----
  await step('a video plays', async () => {
    await open('List');
    await dblClick(page, await point(page, 'clip.webm'));
    await page.waitFor(`(() => { const v = document.querySelector('[data-testid="preview"] video'); return !!v && v.readyState >= 2; })()`, 20000);
    await page.evaluate(`document.querySelector('[data-testid="preview"] video').play().then(() => true, () => false)`);
    const first = await page.evaluate(`document.querySelector('[data-testid="preview"] video').currentTime`);
    await sleep(1500);
    const v = await page.evaluate(`(() => { const e = document.querySelector('[data-testid="preview"] video'); return { t: e.currentTime, paused: e.paused, err: e.error?.code ?? null }; })()`);
    check(!v.paused && v.t > first + 0.5 && v.err === null, `the video plays: its time moves from ${first.toFixed(2)} to ${v.t.toFixed(2)}`);
    await closePreview(page);
  });
  await step('an audio file plays', async () => {
    await open('List');
    await dblClick(page, await point(page, 'tone.ogg'));
    await page.waitFor(`(() => { const a = document.querySelector('[data-testid="preview"] audio'); return !!a && a.readyState >= 2; })()`, 20000);
    await page.evaluate(`document.querySelector('[data-testid="preview"] audio').play().then(() => true, () => false)`);
    const first = await page.evaluate(`document.querySelector('[data-testid="preview"] audio').currentTime`);
    await sleep(1200);
    const a = await page.evaluate(`(() => { const e = document.querySelector('[data-testid="preview"] audio'); return { t: e.currentTime, paused: e.paused }; })()`);
    check(!a.paused && a.t > first + 0.5, `the audio file plays: its time moves from ${first.toFixed(2)} to ${a.t.toFixed(2)}`);
    await closePreview(page);
  });
  await step('a picture shows', async () => {
    await open('List');
    await dblClick(page, await point(page, 'pic.png'));
    await page.waitFor(`(() => { const i = document.querySelector('[data-testid="preview"] img'); return !!i && i.complete; })()`, 20000);
    const w = await page.evaluate(`document.querySelector('[data-testid="preview"] img').naturalWidth`);
    check(w === 120, `the picture shows at its own width (${w})`);
    await closePreview(page);
  });

  // ---- 7. a long video keeps playing when its link ends. The browser's first range is answered from this run, with the
  // start of the file, so the browser needs the rest after the link has ended. Its next range for that link is held
  // until the link is over, and the server refuses it; the player then asks for a new link and goes on from where it was.
  await step('a video keeps playing past its link', async () => {
    await open('List');
    const bytes = await readFile(join(media, 'clip.webm'));
    const PIECE = 24 * 1024;
    let piece = false;
    let heldLink = null;
    let heldEnd = 0;
    linkHook = async (p) => {
      const req = p.request;
      if (req.method === 'POST' && /\/api\/v1\/nodes\/[^/]+\/links$/.test(req.url)) {
        const body = JSON.parse(req.postData || '{}');
        body.ttl = SHORT_TTL;
        page.mintTimes.push(Date.now());
        await page.send('Fetch.continueRequest', { requestId: p.requestId, postData: Buffer.from(JSON.stringify(body)).toString('base64') });
        return;
      }
      const range = req.headers?.Range ?? req.headers?.range ?? '';
      if (req.method === 'GET' && !piece && range === 'bytes=0-') {
        piece = true;
        heldLink = req.url;
        heldEnd = page.mintTimes[page.mintTimes.length - 1] + SHORT_TTL * 1000;
        await page.send('Fetch.fulfillRequest', {
          requestId: p.requestId,
          responseCode: 206,
          responseHeaders: [
            { name: 'Content-Type', value: 'video/webm' },
            { name: 'Accept-Ranges', value: 'bytes' },
            { name: 'Content-Range', value: `bytes 0-${PIECE - 1}/${bytes.length}` },
            { name: 'Content-Length', value: String(PIECE) },
          ],
          body: bytes.subarray(0, PIECE).toString('base64'),
        });
        return;
      }
      if (req.method === 'GET' && req.url === heldLink && Date.now() < heldEnd + 1500) {
        await sleep(heldEnd + 1500 - Date.now());
      }
      await page.send('Fetch.continueRequest', { requestId: p.requestId });
    };
    try {
      await page.send('Fetch.enable', {
        patterns: [
          { urlPattern: '*/api/v1/links/*', requestStage: 'Request' },
          { urlPattern: '*/api/v1/nodes/*/links', requestStage: 'Request' },
        ],
      });
      const before = page.mintTimes.length;
      await dblClick(page, await point(page, 'clip.webm'));
      await page.waitFor(`(() => { const v = document.querySelector('[data-testid="preview"] video'); return !!v && v.readyState >= 1; })()`, 30000);
      await page.evaluate(`document.querySelector('[data-testid="preview"] video').play().then(() => true, () => false)`);
      // Playback runs on past the first link's end.
      await page.waitFor(`(() => { const v = document.querySelector('[data-testid="preview"] video'); return !!v && v.currentTime > ${SHORT_TTL + 2}; })()`, 30000);
      check(page.mintTimes.length > before, `the player asked for a new link once the first one had ended (${page.mintTimes.length - before} asked)`);
      const a = await page.evaluate(`(() => { const v = document.querySelector('[data-testid="preview"] video'); return { t: v.currentTime, paused: v.paused, err: v.error?.code ?? null }; })()`);
      await sleep(1500);
      const b = await page.evaluate(`(() => { const v = document.querySelector('[data-testid="preview"] video'); return { t: v.currentTime, paused: v.paused, err: v.error?.code ?? null }; })()`);
      check(a.err === null && !b.paused && b.t > a.t + 0.5 && b.t > SHORT_TTL, `the video keeps playing past its link: ${a.t.toFixed(2)} to ${b.t.toFixed(2)}`);
      check(!(await page.evaluate(`!!document.querySelector('[data-testid="preview"] [role="alert"]')`)), 'no error is shown while the link is renewed');
      check((page.expectedAnswers.get('link ended')?.length ?? 0) >= 1, 'the server refused the request for the ended link, and the browser logged it');
    } finally {
      linkHook = null;
      await page.send('Fetch.disable');
    }
    await closePreview(page);
  });

  // ---- 8. a file this browser cannot play says so, offers Download, and is not asked for again ----
  await step('a file the browser cannot play says so', async () => {
    await open('List');
    const before = linkMints();
    await dblClick(page, await point(page, 'broken.mp4'));
    try {
      await page.waitFor(`!!document.querySelector('[data-testid="preview"] [role="alert"]')`, 20000);
    } catch (err) {
      const state = await page.evaluate(`(() => { const v = document.querySelector('[data-testid="preview"] video'); return { video: v ? { src: v.getAttribute('src'), err: v.error?.code ?? null, ready: v.readyState, network: v.networkState } : null, text: (document.querySelector('[data-testid="preview"]')?.innerText ?? '').slice(0, 200) }; })()`);
      throw new Error(`${err.message}; mints ${linkMints() - before}; ${JSON.stringify(state)}`);
    }
    await sleep(1500);
    const msg = await page.evaluate(`document.querySelector('[data-testid="preview"] [role="alert"]')?.textContent ?? ''`);
    check(/can.?t play this format/i.test(msg), `a broken video says this browser cannot play it (${msg})`);
    check((await page.evaluate(`[...document.querySelectorAll('[data-testid="preview"] button')].map((b) => b.textContent.trim())`)).includes('Download'), 'the card offers Download');
    // Two links in all: the first for the preview, and one renewal, which fails the same way, so the format is the cause.
    check(linkMints() - before === 2, `the broken file asked for ${linkMints() - before} links, not the two it should (the first, and one renewal)`);
    await closePreview(page);
  });

  // ---- 9. the address a share mail uses opens the video too ----
  await step('the node address opens a video', async () => {
    await page.goto(`${page.base}/n/${clipID}`);
    await page.waitFor(`!!document.querySelector('[data-testid="preview"] video')`, 20000);
    await page.evaluate(`document.querySelector('[data-testid="preview"] video').pause()`);
  });

  // With SELFCLOUD_E2E_SHOTS set to a folder, the run also saves the listing with a selection and a playing video, at
  // desktop and phone width, in light and dark, in English and Hebrew. Nothing is checked in this step.
  if (process.env.SELFCLOUD_E2E_SHOTS) {
    await shots(page, { folderURL, dir: process.env.SELFCLOUD_E2E_SHOTS });
  }

  // The checks after this one expect the desktop window and no selection.
  await setPhone(page, false);
  await page.goto(folderURL).catch(() => undefined);
  if (failures.length > 0) throw new Error(`${failures.length} media check(s) failed: ${failures.join(' | ')}`);
}
