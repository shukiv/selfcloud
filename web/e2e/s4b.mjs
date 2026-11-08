// SPDX-License-Identifier: AGPL-3.0-or-later

// The slice s4b checks: the preview of an office document in the viewer, the export, the failed state of a conversion, and
// the touch targets. The server's converter is the fake in convertFake.mjs, which the run starts and points the server at.
// Documents are uploaded through the API. The checks read what the browser shows, and what the routes answer to the page.
// SELFCLOUD_S4B_SHOTS names a folder for screenshots: desktop and phone, light and dark, English and Hebrew.
import { mkdir, readdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { setThemeByPlace } from './media.mjs';
import { pickMenuEntry } from './menu.mjs';

const q = (v) => JSON.stringify(v);

// The words the checks read, in each language the checks use. The Hebrew words are a machine draft, as the catalogue's are.
const WORDS = {
  en: {
    preview: 'Preview',
    export: 'Export',
    close: 'Close',
    hint: 'Show this document as a PDF. The file itself is not changed.',
    failed: 'The conversion failed, so this copy cannot be made.',
    notConvertible: 'This type of file cannot be converted.',
    ready: 'The copy is ready and the download has started.',
  },
  he: {
    preview: 'תצוגה מקדימה',
    export: 'ייצוא',
    failed: 'ההמרה נכשלה, ולכן אי אפשר להכין את העותק.',
    notConvertible: 'לא ניתן להמיר סוג קובץ זה.',
  },
};

// shot writes a PNG of the page when SELFCLOUD_S4B_SHOTS is set. It waits for the page's transitions to end first, since a frame
// taken during antd's 0.2 s transition shows a half-open panel.
async function shot(page, name) {
  const dir = process.env.SELFCLOUD_S4B_SHOTS;
  if (!dir) return;
  await new Promise((r) => setTimeout(r, 1000));
  const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
  await mkdir(dir, { recursive: true });
  await writeFile(join(dir, name), Buffer.from(data, 'base64'));
}

// waitForFile waits until the browser has saved a file with this ending in dir, and returns its path.
async function waitForFile(dir, ending, timeoutMs = 20000) {
  for (let i = 0; i < timeoutMs / 200; i++) {
    const found = (await readdir(dir)).filter((f) => f.endsWith(ending));
    if (found.length > 0) return join(dir, found[0]);
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`the browser saved no ${ending} file`);
}

// listView shows the list, where the rows are found by their names.
async function listView(page) {
  await page.waitFor(`!!document.querySelector('[data-testid="view-toggle"]')?.dataset.view`, 10000);
  await page.evaluate(`(() => { const b = document.querySelector('[data-testid="view-toggle"]'); if (b.dataset.view === 'list') b.click(); })()`);
}

// clickIn clicks the button of the scope whose text is exactly text, once it is there.
async function clickIn(page, scope, text, timeout = 15000) {
  await page.waitFor(
    `(() => { const b = [...document.querySelectorAll(${q(scope + ' button')})].find((x) => (x.textContent.trim() === ${q(text)} || x.getAttribute('aria-label') === ${q(text)}) && !x.disabled); if (!b) return false; b.click(); return true; })()`,
    timeout,
  );
}

// lastModal is the expression for the modal that opened last, which is the one on top.
const LAST_MODAL = `[...document.querySelectorAll('.ant-modal')].at(-1)`;

// pickMenuItem chooses the open menu's item whose text is exactly text, waiting for it to appear and clicking in one step.
async function pickMenuItem(page, text) {
  await page.waitFor(
    `(() => { const i = [...document.querySelectorAll('.ant-dropdown-menu-item')].find((x) => x.textContent.trim() === ${q(text)}); if (!i) return false; i.click(); return true; })()`,
    10000,
  );
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// setViewport emulates the window size and waits until the page has taken it: its width is the new one, and a moment has passed
// for the layout to settle. A window resize closes a menu that is open at a point (the menu's trigger closes on resize, in
// @rc-component/trigger), so a click made while the page is still resizing would race the close.
async function setViewport(page, width, height, mobile) {
  await page.send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile });
  await page.waitFor(`window.innerWidth === ${width}`, 10000);
  await sleep(200);
}

// pickSegment chooses a theme in the theme switch by its label, once the switch is on the page.
// The options are system, light and dark, in that order, whatever the language: the choice is made by its place.
async function pickSegment(page, value) {
  await setThemeByPlace(page, { system: 0, light: 1, dark: 2 }[value], value);
}

// closeTopModal closes the dialog on top (the export dialog, over the viewer) by its Close button, and waits until only the
// viewer is open.
async function closeTopModal(page) {
  await page.evaluate(`(() => { const m = ${LAST_MODAL}; [...m.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Close')?.click(); return true; })()`);
  await page.waitFor(`document.querySelectorAll('.ant-modal').length === 1`, 10000);
}

// bodyHas says whether the page's text holds text.
const bodyHas = (text) => `document.body.innerText.includes(${q(text)})`;

// openViewer opens an office file's preview from its row's menu. A double click on an office file opens the editor in its own
// tab (decision 102), and the preview is the menu's Preview entry.
// The row is only focused, not clicked: a click with no pointer event reads the last pointer's type (a finger's, from an earlier phone
// step), and a finger's tap on an office file opens it. The More button stops its click, so the row's click is not needed.
async function openViewer(h, page, name) {
  await h.rowAppears(page, name);
  // A menu still on its way out (the theme menu after a theme pick closes with a motion, and setThemeByPlace does not wait for it)
  // must be gone before the More click: the click is made when no menu is shown.
  await page.waitFor(`!document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden)')`, 10000);
  await page.evaluate(`(() => { const row = document.querySelector(${JSON.stringify(`[role="row"][aria-label="${name}"]`)}); row.focus(); const b = row && row.querySelector('button[aria-haspopup="menu"]'); if (b) b.click(); return !!b; })()`);
  // The entry's label is in the page's language (English or Hebrew, the two the checks use).
  const lang = (await page.evaluate(`document.documentElement.lang`)) === 'he' ? 'he' : 'en';
  await pickMenuEntry(page, WORDS[lang].preview);
  await page.waitFor(`!!document.querySelector('[data-testid="preview"]')`, 15000);
}

// closeViewer closes the viewer by its first header button, which is Close in every language.
async function closeViewer(page) {
  const answer = await page.evaluate(`(() => {
    const b = document.querySelector('[data-testid="preview"] header button');
    if (!b) return 'no close button; ' + document.querySelectorAll('[data-testid="preview"]').length + ' viewers; at ' + location.href + ' modals ' + document.querySelectorAll('.ant-modal').length + ' ' + (performance.now() | 0);
    b.click();
    return 'closed';
  })()`);
  if (answer !== 'closed') throw new Error(`the viewer could not be closed: ${answer}`);
  await page.waitFor(`!document.querySelector('[data-testid="preview"]')`, 10000);
}

// noRawIDs checks the text on the page shows no internal ID of a file, folder, user or conversion.
async function noRawIDs(page) {
  return page.evaluate(`!/\\b(nod|drv|fld|usr)_[a-z0-9]{8,}/.test(document.body.innerText)`);
}

// until waits for an expression in the page to be true, up to 15 seconds: the condition is waited for, never a fixed time (fu3b).
async function until(page, expr, what) {
  const deadline = Date.now() + 15000;
  for (;;) {
    if (await page.evaluate(`!!(${expr})`).catch(() => false)) return;
    if (Date.now() > deadline) throw new Error(`timed out waiting for: ${what}`);
    await new Promise((r) => setTimeout(r, 50));
  }
}

// setCoarse makes the page's primary pointer a touch pointer, or takes that away. Touch emulation is what makes
// (pointer: coarse) match in the browser; the media feature alone did not.
async function setCoarse(page, on) {
  await page.send('Emulation.setTouchEmulationEnabled', { enabled: on, maxTouchPoints: 1 });
  await page.send('Emulation.setEmulatedMedia', { features: on ? [{ name: 'hover', value: 'none' }] : [] });
  // The media change reaches the page on a later frame, under load too: the page's own query says when it has (fu3b, item 8: a
  // fixed wait let the first measure run before the change, at 9 px instead of 44).
  await until(page, `matchMedia('(pointer: coarse)').matches === ${on ? 'true' : 'false'}`, 'the coarse pointer media');
  // Two frames pass before anything is opened on top of the new pointer.
  await page.evaluate(`new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(() => r(true))))`);
}

// touchTargets measures the preview and export buttons of the viewer in the three states that change them: at rest, under the
// pointer, and with keyboard focus. Each height is in CSS pixels.
// The viewer opens with the dialog's zoom, which moves and scales its buttons for a few frames. A button read during the zoom
// is in the wrong place and a little short: the stack gate fu3stacka sent the pointer to a header button's place mid-zoom
// (750, 327; it came to rest at 1168, 12) and read 43 px. So each read waits for the viewer to settle: two buttons or more,
// no animation running on a button or on anything around it, and every button where it was two frames before.
const CONVERT = '[data-testid="preview"] .convert-btn';
const nameOf = `(b) => b.getAttribute('aria-label') || b.textContent.trim()`;
// byName finds a button by its name (its label, or its text), not by its place: a re-render may change the places.
const byName = (name) => `[...document.querySelectorAll(${JSON.stringify(CONVERT)})].find((b) => (${nameOf})(b) === ${JSON.stringify(name)})`;
const SETTLED = `new Promise((resolve) => {
  const where = () => [...document.querySelectorAll(${JSON.stringify(CONVERT)})].map((b) => { const r = b.getBoundingClientRect(); return [r.left, r.top, r.width, r.height].join(','); }).join('|');
  const before = where();
  requestAnimationFrame(() => requestAnimationFrame(() => {
    const buttons = [...document.querySelectorAll(${JSON.stringify(CONVERT)})];
    // An animation counts until it has finished: antd's zoom is first attached paused at its first frame (scale 0.2), and is
    // only set running a little later. A wait that looked only for a running animation read 9 px there (gate fu3stackb).
    const moving = document.getAnimations().some((a) => a.playState !== 'finished' && a.effect && a.effect.target instanceof Element && buttons.some((b) => a.effect.target.contains(b)));
    // Nothing around a button is scaled: the button and every box that holds it are at their full size.
    const scaled = buttons.some((b) => { for (let e = b; e; e = e.parentElement) { const t = getComputedStyle(e).transform; if (t !== 'none') { const m = new DOMMatrixReadOnly(t); if (Math.abs(m.a - 1) > 0.001 || Math.abs(m.d - 1) > 0.001) return true; } } return false; });
    // No box around a button is in an antd motion. antd puts its motion class (ant-zoom-appear and its steps) on the dialog from
    // the first frame it draws and takes it away when the zoom has ended. Its first step has no transform and no animation, so
    // the two checks above pass there; the zoom then starts, at scale 0.2 (9 px read, gate fu3stackb and this machine).
    const inMotion = buttons.some((b) => { for (let e = b; e; e = e.parentElement) { if (/(^|\\s)[\\w-]+-(appear|enter|leave)(\\s|$)/.test(String(e.className))) return true; } return false; });
    resolve(buttons.length >= 2 && !moving && !scaled && !inMotion && where() === before);
  }));
})`;

// settle waits, up to 15 seconds, for the viewer to settle (SETTLED). Each try reads two frames, so it is never a fixed wait.
async function settle(page, what) {
  const deadline = Date.now() + 15000;
  while (!(await page.evaluate(SETTLED).catch(() => false))) {
    if (Date.now() > deadline) throw new Error(`timed out waiting for: the viewer to settle (${what})`);
  }
}

async function touchTargets(h, page) {
  await settle(page, 'before the buttons are read');
  const names = await page.evaluate(`[...document.querySelectorAll(${JSON.stringify(CONVERT)})].map(${nameOf})`);
  if (new Set(names).size !== names.length) throw new Error(`two preview buttons share a name: ${names.join(' / ')}`);
  const height = (name) => page.evaluate(`Math.round(${byName(name)}.getBoundingClientRect().height)`);
  const rest = [];
  for (const name of names) rest.push(await height(name));
  // The pointer is moved over each button, and the button is measured while it is under the pointer. The pointer is sent again
  // on each try, to where the button is then, so a button that moved is still found.
  const hover = [];
  for (const name of names) {
    const deadline = Date.now() + 15000;
    for (;;) {
      await settle(page, `before the pointer goes to ${name}`);
      const box = await page.evaluate(`(() => { const r = ${byName(name)}.getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 }; })()`);
      await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: box.x, y: box.y });
      await page.evaluate(`new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(() => r(true))))`);
      if (await page.evaluate(`(() => { const b = ${byName(name)}; return b.matches(':hover') && b.getAnimations().length === 0; })()`)) break;
      if (Date.now() > deadline) throw new Error(`timed out waiting for: the pointer over ${name}, its transition done`);
    }
    hover.push(await height(name));
  }
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 0, y: 0 });
  const focus = [];
  for (const name of names) {
    await page.evaluate(`${byName(name)}.focus(), true`);
    await until(page, `document.activeElement === ${byName(name)} && ${byName(name)}.getAnimations().length === 0`, `${name} has the focus`);
    focus.push(await height(name));
  }
  const count = names.length;
  h.check(
    count >= 2 && [...rest, ...hover, ...focus].every((px) => px >= 44),
    `the preview and export buttons are 44 px tall on a coarse pointer, at rest, under the pointer and focused (rest ${rest}, hover ${hover}, focus ${focus})`,
  );
}

export async function s4bChecks(h, { page, base, accountKey, teamA, dir, fake, refused }) {
  const { check } = h;
  const DOCX = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document';
  const DOC = 'application/msword';
  const downloads = join(dir, 's4b-downloads');
  await mkdir(downloads, { recursive: true });

  // The documents: a letter that converts, a document the fake refuses, and an old binary format that is not convertible.
  await h.api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: '/s4b-docs', parents: false });
  const docx = (text) => Buffer.concat([Buffer.from('504b0304', 'hex'), Buffer.from(`${text}\n`)]);
  await h.rawPut(base, `/api/v1/folders/${teamA}/files?path=%2Fs4b-docs%2Fletter.docx`, accountKey, docx('a letter'), DOCX);
  await h.rawPut(base, `/api/v1/folders/${teamA}/files?path=%2Fs4b-docs%2Fbroken.docx`, accountKey, docx('broken'), DOCX);
  await h.rawPut(base, `/api/v1/folders/${teamA}/files?path=%2Fs4b-docs%2Fold.doc`, accountKey, Buffer.from('d0cf11e0a1b11ae1', 'hex'), DOC);

  // The server says conversion is offered. The page learns it from there, and not from a guess.
  const info = await (await fetch(`${base}/api/v1/info`)).json();
  check(Array.isArray(info.features) && info.features.includes('office_convert'), 'the server offers document previews and exports (/info features)');

  await setViewport(page, 1280, 800, false);
  await page.goto(`${page.base}/files/${teamA}`);
  await listView(page);
  await h.rowAppears(page, 's4b-docs');
  await h.openRow(page, 's4b-docs');
  await h.rowAppears(page, 'letter.docx');
  const listed = await h.api(base, 'GET', `/api/v1/folders/${teamA}/children?path=%2Fs4b-docs`, accountKey);
  const idOf = (name) => listed.items.find((n) => n.name === name).id;
  const letterID = idOf('letter.docx');

  // 1. Desktop, light, English. The card offers the preview and the export; the preview is a PDF in a frame.
  await openViewer(h, page, 'letter.docx');
  await page.waitFor(`${bodyHas(WORDS.en.hint)}`, 10000);
  check(await page.evaluate(`!!${LAST_MODAL} && [...document.querySelectorAll('[data-testid="preview"] button')].some((b) => b.textContent.trim() === 'Preview')`), 'an office document shows a Preview button in the viewer');
  check(await noRawIDs(page), 'the viewer shows no raw ID');
  await shot(page, 'desktop-light-en-card.png');

  await clickIn(page, '[data-testid="preview"]', WORDS.en.preview);
  await page.waitFor(`!!document.querySelector('[data-testid="preview"] iframe')`, 30000);
  const src = await page.evaluate(`document.querySelector('[data-testid="preview"] iframe').getAttribute('src')`);
  check(src === `/api/v1/nodes/${letterID}/preview?format=pdf`, 'the frame shows the preview at its own address');
  const got = await page.evaluate(`(async () => {
    const r = await fetch(${q(src)}, { credentials: 'same-origin', cache: 'no-store' });
    const b = new Uint8Array(await r.arrayBuffer());
    return { status: r.status, type: r.headers.get('content-type'), nosniff: r.headers.get('x-content-type-options'),
      csp: r.headers.get('content-security-policy') ?? '', magic: String.fromCharCode(...b.slice(0, 5)) };
  })()`);
  check(
    got.status === 200 && got.type === 'application/pdf' && got.nosniff === 'nosniff' && got.csp.includes('sandbox') && got.magic === '%PDF-',
    `the PDF is served as a PDF, with nosniff and the sandbox policy (${got.status} ${got.type}, magic ${got.magic})`,
  );
  await page.waitFor(`!!document.querySelector('[data-testid="preview"] iframe')`, 5000);
  await shot(page, 'desktop-light-en-pdf.png');

  // Export as ODT (the same family): the menu lists the formats of the family, and the download starts when the copy is ready.
  await page.send('Page.setDownloadBehavior', { behavior: 'allow', downloadPath: downloads });
  await clickIn(page, '[data-testid="preview"]', WORDS.en.export);
  await page.waitFor(`[...document.querySelectorAll('.ant-dropdown-menu-item')].length >= 3`, 10000);
  const formats = await page.evaluate(`[...document.querySelectorAll('.ant-dropdown-menu-item')].map((i) => i.textContent.trim())`);
  check(JSON.stringify(formats) === JSON.stringify(['PDF', 'DOCX', 'ODT']), `a Word document offers PDF and its family (${formats.join(', ')})`);
  await page.evaluate(`[...document.querySelectorAll('.ant-dropdown-menu-item')].find((i) => i.textContent.trim() === 'ODT').click(), true`);
  await page.waitFor(`${LAST_MODAL}?.textContent.includes('ODT')`, 10000);
  await page.waitFor(`(() => { const m = ${LAST_MODAL}; return !!m && m.innerText.includes(${q(WORDS.en.ready)}); })()`, 30000);
  const saved = await readFile(await waitForFile(downloads, '.odt'));
  check(saved.subarray(0, 2).toString('latin1') === 'PK' && saved.length > 0, `the export is downloaded as letter.odt (${saved.length} bytes)`);
  check(fake.hits.some((x) => x.format === 'odt' && x.source === 'docx'), 'the converter was asked for ODT from the DOCX');
  await closeTopModal(page);

  // A failed export: the converter refuses, and the dialog says the conversion failed. It does not spin.
  // The node is named as refused before the refusal happens, so its 415 is the expected one.
  refused.add(letterID);
  fake.refuse(true);
  await clickIn(page, '[data-testid="preview"]', WORDS.en.export);
  await page.waitFor(`[...document.querySelectorAll('.ant-dropdown-menu-item')].length >= 3`, 10000);
  await page.evaluate(`[...document.querySelectorAll('.ant-dropdown-menu-item')].find((i) => i.textContent.trim() === 'DOCX').click(), true`);
  await page.waitFor(`(() => { const m = ${LAST_MODAL}; return !!m && m.innerText.includes(${q(WORDS.en.failed)}); })()`, 30000);
  const failedDialog = await page.evaluate(`(() => { const m = ${LAST_MODAL}; return { spinner: !!m.querySelector('.ant-spin'), english: m.innerText.includes('could not be exported') }; })()`);
  check(!failedDialog.spinner && !failedDialog.english, 'a failed export says the conversion failed, with no spinner and no server English');
  check(await noRawIDs(page), 'the failed export shows no raw ID');
  await shot(page, 'desktop-light-en-failed.png');
  await closeTopModal(page);

  // A preview the converter refuses: the viewer says it failed, once, and asks nothing more.
  await closeViewer(page);
  refused.add(idOf('broken.docx'));
  await openViewer(h, page, 'broken.docx');
  await clickIn(page, '[data-testid="preview"]', WORDS.en.preview);
  await page.waitFor(`${bodyHas(WORDS.en.failed)}`, 30000);
  check(true, 'a refused preview says the conversion failed');
  fake.refuse(false);
  await closeViewer(page);

  // A file type the converter does not take: the viewer says so, and the server is asked once.
  refused.add(idOf('old.doc'));
  await openViewer(h, page, 'old.doc');
  await clickIn(page, '[data-testid="preview"]', WORDS.en.preview);
  await page.waitFor(`${bodyHas(WORDS.en.notConvertible)}`, 30000);
  check(true, 'an old binary document says it cannot be converted');
  await closeViewer(page);

  // Touch targets: the letter's buttons are measured on a coarse pointer, at rest, under the pointer and focused.
  await setCoarse(page, true);
  await openViewer(h, page, 'letter.docx');
  await page.waitFor(`!!document.querySelector('[data-testid="preview"] .convert-btn')`, 10000);
  const coarse = await page.evaluate(`matchMedia('(pointer: coarse)').matches`);
  check(coarse === true, `the emulated pointer is coarse (${coarse})`);
  await touchTargets(h, page);
  await closeViewer(page);
  await setCoarse(page, false);

  // Phone width, light then dark, English. Dark is set on the desktop first: the theme switch is not shown at phone width.
  await setViewport(page, 375, 812, true);
  await setCoarse(page, true);
  await openViewer(h, page, 'letter.docx');
  const overflow = await page.evaluate(`document.documentElement.scrollWidth - document.documentElement.clientWidth`);
  check(overflow <= 0, `the viewer's card has no horizontal scroll at 375 px (overflow ${overflow}px)`);
  await touchTargets(h, page);
  await shot(page, 'phone-light-en-card.png');
  await closeViewer(page);

  // Dark, English, on the desktop; then the phone in dark.
  await setViewport(page, 1280, 800, false);
  await page.waitFor(`!!document.querySelector('[data-testid="theme"]')`, 10000);
  await pickSegment(page, 'dark');
  await page.waitFor(`document.documentElement.dataset.theme === 'dark'`, 10000);
  await openViewer(h, page, 'letter.docx');
  await shot(page, 'desktop-dark-en-card.png');
  await closeViewer(page);
  await setViewport(page, 375, 812, true);
  await openViewer(h, page, 'letter.docx');
  await shot(page, 'phone-dark-en-card.png');
  await closeViewer(page);

  // Hebrew, on the phone in dark: the language switch is shown at phone width.
  await page.evaluate(`(() => { document.querySelector('[data-testid="language"]').click(); return true; })()`);
  await page.waitFor(`[...document.querySelectorAll('.ant-dropdown-menu-item')].some((i) => i.textContent.trim() === 'עברית')`, 10000);
  await pickMenuItem(page, 'עברית');
  await page.waitFor(`document.documentElement.dir === 'rtl' && document.documentElement.lang === 'he'`, 10000);
  await openViewer(h, page, 'letter.docx');
  await page.waitFor(`[...document.querySelectorAll('[data-testid="preview"] button')].some((b) => b.textContent.trim() === ${q(WORDS.he.preview)})`, 10000);
  await shot(page, 'phone-dark-he-card.png');
  await closeViewer(page);

  // Hebrew, on the desktop in light; then the phone in light.
  await setViewport(page, 1280, 800, false);
  await page.waitFor(`!!document.querySelector('[data-testid="theme"]')`, 10000);
  await pickSegment(page, 'light');
  await page.waitFor(`document.documentElement.dataset.theme === 'light'`, 10000);
  await openViewer(h, page, 'letter.docx');
  await shot(page, 'desktop-light-he-card.png');
  await clickIn(page, '[data-testid="preview"]', WORDS.he.preview);
  await page.waitFor(`!!document.querySelector('[data-testid="preview"] iframe')`, 30000);
  await closeViewer(page);
  await setViewport(page, 375, 812, true);
  await openViewer(h, page, 'letter.docx');
  await shot(page, 'phone-light-he-card.png');
  await closeViewer(page);

  // Back to English on the desktop, so the checks that follow read English.
  await setCoarse(page, false);
  await setViewport(page, 1280, 800, false);
  await page.evaluate(`(() => { document.querySelector('[data-testid="language"]').click(); return true; })()`);
  await page.waitFor(`[...document.querySelectorAll('.ant-dropdown-menu-item')].some((i) => i.textContent.trim() === 'English')`, 10000);
  await pickMenuItem(page, 'English');
  await page.waitFor(`document.documentElement.dir === 'ltr'`, 10000);
  check(true, 'the s4b checks left the page in English and light');
}
