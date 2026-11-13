// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice ce1 (decision 115): the read-only code viewer in the preview. It runs in the shared run (run.mjs), signed in as the
// member, or alone with SELFCLOUD_E2E_ONLY=ce1. The member's My files gets five files: a JavaScript file, a Go file stored as
// application/octet-stream (the name decides, after the sniff), a log over 1 MiB, a file with a line of 3,000 characters, and
// a Latin-1 text file. Checked: colours (a token class, in its colour) and line numbers; the style sheets carry the nonce; Find
// opens the find panel and Escape closes the panel, not the viewer; the theme switched while the viewer is open (System, with
// the device's scheme changed) repaints it in place; the not-UTF-8 notice and its Download button; the truncated notice above
// 1 MiB; at 375 px a long line scrolls in the code area, not the page;
// in Hebrew the gutter stays on the left; a Go file in Team B (read-only for the member) under the no-download rule shows the
// restricted notice after one refused read. No console error and no content-policy violation over the whole section, but for
// that one 403, which the run expects by name. The seeded files are removed after.
import { readFileSync } from 'node:fs';
import { closePreview, setLanguage, setTheme, sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);
// The restricted notice, as the English catalogue words it.
const RESTRICTED = JSON.parse(readFileSync(new URL('../src/i18n/en.json', import.meta.url), 'utf8'))['files.err.downloadRestricted'];
const PREVIEW = '[data-testid="preview"]';
const EDITOR = `${PREVIEW} .cm-editor`;

// The colours of lib/code/theme.ts and the card colours of theme/tokens.ts, as the browser reports them.
const LIGHT_KEYWORD = 'rgb(207, 34, 46)';
const DARK_KEYWORD = 'rgb(255, 123, 114)';
const LIGHT_GROUND = 'rgb(255, 255, 255)';
const DARK_GROUND = 'rgb(13, 33, 66)';

const JS = [
  '// The greeting, and a sum.',
  'const greeting = "hello";',
  'export function add(a, b) {',
  '  return a + b + 42;',
  '}',
  '',
].join('\n');
const GO = 'package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println("hi")\n}\n';

// openFile opens a file of the listing (its row, or its tile when an earlier section left the tiles on) by a double click, and
// waits for the code viewer to show its text.
async function openFile(page, name) {
  const item = `[role="row"][aria-label=${q(name)}], [role="option"][aria-label=${q(name)}]`;
  await page.waitFor(`!!document.querySelector(${q(item)})`, 15000);
  await page.evaluate(`document.querySelector(${q(item)}).dispatchEvent(new MouseEvent('dblclick', { bubbles: true })), true`);
  await page.waitFor(`!!document.querySelector(${q(EDITOR)}) && document.querySelectorAll(${q(`${EDITOR} .cm-line`)}).length > 0`, 20000);
}

// colourOf reads the colour of the first element the selector matches, or null.
const colourOf = (page, selector) =>
  page.evaluate(`(() => { const el = document.querySelector(${q(selector)}); return el ? getComputedStyle(el).color : null; })()`);
const groundOf = (page) =>
  page.evaluate(`(() => { const el = document.querySelector(${q(EDITOR)}); return el ? getComputedStyle(el).backgroundColor : null; })()`);

// scheme sets the device's colour scheme the page sees, or clears it.
const scheme = (page, value) =>
  page.send('Emulation.setEmulatedMedia', { media: '', features: value ? [{ name: 'prefers-color-scheme', value }] : [] });

// lockedIDs is run.mjs's set of the nodes this section locks: its "restricted read" entry expects an inline 403 only for those.
export async function ce1Checks(h, { page, base, accountKey, MEMBER, teamB, locked: lockedIDs = new Set() }) {
  const { check, api, navigateTo } = h;
  const before = page.errors.length;
  const suffix = Date.now().toString(36);
  const names = {
    js: `ce1-app-${suffix}.js`,
    go: `ce1-main-${suffix}.go`,
    big: `ce1-big-${suffix}.log`,
    wide: `ce1-wide-${suffix}.min.js`,
    latin: `ce1-latin-${suffix}.txt`,
  };
  const locked = `ce1-locked-${suffix}.go`;
  let pf = null;
  let lockedID = null;
  try {
    const me = await page.evaluate(`fetch('/api/v1/me', { credentials: 'same-origin' }).then((r) => r.json())`);
    check(me.user?.email === MEMBER.email && typeof me.personal_folder_id === 'string', `the code viewer's checks run as the member (${me.user?.email})`);
    pf = me.personal_folder_id;
    const put = (name, body, type) => h.rawPut(base, `/api/v1/folders/${pf}/files?path=${encodeURIComponent(`/${name}`)}`, accountKey, body, type);
    await put(names.js, JS, 'application/javascript');
    await put(names.go, GO, 'application/octet-stream');
    // Over 1 MiB: 24,000 lines of 50 bytes.
    await put(names.big, Array.from({ length: 24000 }, (_, i) => `line ${String(i + 1).padStart(6, '0')} of the big log, padded to fifty.`.slice(0, 49) + '\n').join(''), 'text/plain');
    await put(names.wide, `const wide = "${'w'.repeat(3000)}";\n`, 'application/javascript');
    // Latin-1 "café" and a line break: not UTF-8. An upload is not checked for text (decision 113).
    await put(names.latin, new Uint8Array([0x63, 0x61, 0x66, 0xe9, 0x0a]), 'text/plain');

    await navigateTo(page, `/files/${pf}`);

    // ---- Colours and line numbers, light, with the nonce on CodeMirror's style sheets.
    await setTheme(page, 'Light');
    await openFile(page, names.js);
    await page.waitFor(`!!document.querySelector(${q(`${EDITOR} .tok-keyword`)})`, 15000);
    check(true, 'a JavaScript file opens in the code viewer, coloured by its language (a token class is present)');
    const keyword = await colourOf(page, `${EDITOR} .tok-keyword`);
    check(keyword === LIGHT_KEYWORD, `a keyword takes the light keyword colour (${keyword})`);
    const numbers = await page.evaluate(`[...document.querySelectorAll(${q(`${EDITOR} .cm-lineNumbers .cm-gutterElement`)})].map((e) => e.textContent.trim()).filter(Boolean)`);
    check(numbers.includes('1') && numbers.includes('5'), `the gutter numbers the lines (${numbers.join(',')})`);
    check(!(await page.evaluate(`!!document.querySelector(${q(`${PREVIEW} pre`)})`)), 'the code viewer replaces the plain text block');
    const sheets = await page.evaluate(`(() => { const s = [...document.querySelectorAll('style')].filter((x) => (x.textContent || '').includes('.cm-scroller')); return { count: s.length, nonced: s.filter((x) => !!x.nonce).length }; })()`);
    check(sheets.count > 0 && sheets.nonced === sheets.count, `CodeMirror's style sheets carry the page's nonce (${sheets.nonced} of ${sheets.count})`);

    // ---- Find opens the panel; Escape closes the panel and leaves the viewer open.
    // The dialog takes the focus when it finishes opening; wait for that before the find field, so it is not taken back.
    await page.waitFor(`document.activeElement?.hasAttribute('data-preview-stage')`, 10000);
    await page.evaluate(`document.querySelector(${q(`${PREVIEW} .find-btn`)}).click(), true`);
    const search = `${EDITOR} .cm-search input[name="search"]`;
    await page.waitFor(`!!document.querySelector(${q(search)})`, 10000);
    check(true, 'the header\'s Find icon opens the find panel');
    await page.evaluate(`document.querySelector(${q(search)}).focus(), true`);
    await page.waitFor(`document.activeElement === document.querySelector(${q(search)})`, 10000);
    await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
    await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
    await page.waitFor(`!document.querySelector(${q(`${EDITOR} .cm-search`)})`, 10000);
    await sleep(300);
    check(await page.evaluate(`!!document.querySelector(${q(EDITOR)})`), 'Escape in the find panel closes the panel, and the viewer stays open');
    await closePreview(page);

    // ---- Dark, switched while the viewer is open: the theme follows the device, and the device's scheme changes.
    await scheme(page, 'light');
    await setTheme(page, 'System');
    await page.waitFor(`document.documentElement.dataset.theme === 'light'`, 10000);
    await openFile(page, names.js);
    await page.waitFor(`!!document.querySelector(${q(`${EDITOR} .tok-keyword`)})`, 15000);
    const lightGround = await groundOf(page);
    check(lightGround === LIGHT_GROUND, `the code area is on the light card (${lightGround})`);
    const marked = await page.evaluate(`(() => { const el = document.querySelector(${q(EDITOR)}); el.dataset.ce1Mark = 'kept'; return true; })()`);
    await scheme(page, 'dark');
    await page.waitFor(`document.documentElement.dataset.theme === 'dark' && document.querySelector(${q(EDITOR)})?.classList.contains('sc-code-dark')`, 10000);
    const darkKeyword = await colourOf(page, `${EDITOR} .tok-keyword`);
    const darkGround = await groundOf(page);
    check(darkKeyword === DARK_KEYWORD && darkGround === DARK_GROUND, `dark repaints the code in place: keyword ${darkKeyword}, ground ${darkGround}`);
    check(marked && (await page.evaluate(`document.querySelector(${q(EDITOR)})?.dataset.ce1Mark === 'kept'`)), 'the theme changes without drawing the editor again');
    await scheme(page, 'light');
    await page.waitFor(`document.querySelector(${q(EDITOR)})?.classList.contains('sc-code-light')`, 10000);
    check((await colourOf(page, `${EDITOR} .tok-keyword`)) === LIGHT_KEYWORD, 'and back to light, in place');
    await closePreview(page);
    await scheme(page, null);
    await setTheme(page, 'Light');

    // ---- A Go file stored as application/octet-stream: the name decides once the sniff finds text.
    await openFile(page, names.go);
    await page.waitFor(`!!document.querySelector(${q(`${EDITOR} .tok-keyword`)})`, 15000);
    const goText = await page.evaluate(`document.querySelector(${q(`${EDITOR} .cm-line`)}).textContent`);
    check(goText === 'package main', `a .go file stored as octet-stream opens as Go code (${goText})`);
    await closePreview(page);

    // ---- A file that is not UTF-8 opens read-only, with a notice and a Download button (design-ce 2.3).
    await openFile(page, names.latin);
    await page.waitFor(`!!document.querySelector(${q(`${PREVIEW} .ant-alert`)})`, 10000);
    const latin = await page.evaluate(`(() => {
      const alert = document.querySelector(${q(`${PREVIEW} .ant-alert`)});
      const line = document.querySelector(${q(`${EDITOR} .cm-line`)});
      return { notice: alert.textContent, buttons: [...alert.querySelectorAll('button')].map((b) => b.textContent.trim()), line: line ? line.textContent : null };
    })()`);
    check(
      latin.notice.includes('This file is not UTF-8 text.') && latin.buttons.includes('Download') && latin.line === 'caf�',
      `a Latin-1 file opens read-only with the not-UTF-8 notice and a Download button (${JSON.stringify(latin)})`,
    );
    await closePreview(page);

    // ---- Over 1 MiB: the first 1 MiB, and the notice.
    await openFile(page, names.big);
    const notice = await page.evaluate(`document.querySelector(${q(PREVIEW)}).textContent.includes('Showing the first 1 MB.')`);
    check(notice, 'a file over 1 MiB shows its first 1 MiB with the truncated notice');
    await closePreview(page);

    // ---- 375 px: a line of 3,000 characters turns wrapping off, and the code area scrolls sideways, not the page.
    await openFile(page, names.wide);
    await page.send('Emulation.setDeviceMetricsOverride', { width: 375, height: 812, deviceScaleFactor: 1, mobile: true });
    await sleep(400);
    const phone = await page.evaluate(`(() => {
      const doc = document.documentElement;
      const stage = document.querySelector('${PREVIEW} [data-preview-stage]');
      const content = document.querySelector('${PREVIEW} .ant-modal-content');
      const scroller = document.querySelector('${EDITOR} .cm-scroller');
      const editor = document.querySelector(${q(EDITOR)});
      return {
        page: doc.scrollWidth - doc.clientWidth,
        stage: stage.scrollWidth - stage.clientWidth,
        modal: content.scrollWidth - content.clientWidth,
        editorRight: Math.round(editor.getBoundingClientRect().right),
        width: innerWidth,
        wraps: document.querySelector('${EDITOR} .cm-content').classList.contains('cm-lineWrapping'),
        scrolls: scroller.scrollWidth > scroller.clientWidth,
      };
    })()`);
    check(!phone.wraps && phone.scrolls, `a line of 3,000 characters turns wrapping off, and the code area scrolls sideways (${JSON.stringify(phone)})`);
    check(phone.page <= 0 && phone.stage <= 0 && phone.modal <= 0 && phone.editorRight <= phone.width, `no page-level sideways scroll at 375 px (${JSON.stringify(phone)})`);
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    await sleep(300);
    await closePreview(page);

    // ---- Hebrew: the page is right to left, the code area left to right, and the gutter stays on the left.
    await setLanguage(page, 'עברית');
    await page.waitFor(`document.documentElement.dir === 'rtl'`, 10000);
    await openFile(page, names.js);
    const he = await page.evaluate(`(() => {
      const gutters = document.querySelector('${EDITOR} .cm-gutters').getBoundingClientRect();
      const content = document.querySelector('${EDITOR} .cm-content').getBoundingClientRect();
      return { gutter: Math.round(gutters.left), content: Math.round(content.left), dir: document.querySelector('${PREVIEW} [data-testid="code-view"]').getAttribute('dir'), find: document.querySelector('${PREVIEW} .find-btn')?.getAttribute('aria-label') ?? null };
    })()`);
    check(he.dir === 'ltr' && he.gutter < he.content, `in Hebrew the code area is left to right and the gutter stays on the left (${JSON.stringify(he)})`);
    check(he.find === 'חיפוש', `the Find icon's name is in Hebrew (${he.find})`);
    await closePreview(page);
    await setLanguage(page, 'English');

    // ---- A viewer the no-download rule binds: Team B is read-only for the member, and its Go file, stored as octet-stream,
    // may not be read even inline. The sniff's read is refused (403), and the viewer says so in place of the text.
    await h.rawPut(base, `/api/v1/folders/${teamB}/files?path=${encodeURIComponent(`/${locked}`)}`, accountKey, GO, 'application/octet-stream');
    const inB = await api(base, 'GET', `/api/v1/folders/${teamB}/children`, accountKey);
    lockedID = inB.items.find((n) => n.name === locked)?.id ?? null;
    await api(base, 'PATCH', `/api/v1/nodes/${lockedID}`, accountKey, { no_download: true });
    // Named before the read, so its 403 is the one expected.
    lockedIDs.add(lockedID);
    const refusedBefore = page.expectedAnswers.get('restricted read')?.length ?? 0;
    await navigateTo(page, `/files/${teamB}`);
    const lockedRow = `[role="row"][aria-label=${q(locked)}], [role="option"][aria-label=${q(locked)}]`;
    await page.waitFor(`!!document.querySelector(${q(lockedRow)})`, 15000);
    await page.evaluate(`document.querySelector(${q(lockedRow)}).dispatchEvent(new MouseEvent('dblclick', { bubbles: true })), true`);
    await page.waitFor(`!!document.querySelector(${q(`${PREVIEW} .ant-alert`)})`, 15000);
    await sleep(300);
    const refused = {
      alert: await page.evaluate(`document.querySelector(${q(`${PREVIEW} .ant-alert`)}).textContent`),
      editor: await page.evaluate(`!!document.querySelector(${q(EDITOR)})`),
      answers: (page.expectedAnswers.get('restricted read')?.length ?? 0) - refusedBefore,
    };
    check(
      refused.alert === RESTRICTED && !refused.editor && refused.answers === 1,
      `a restricted viewer's code file shows the restricted notice, not the text, after one refused read (${JSON.stringify(refused)})`,
    );
    await closePreview(page);

    // ---- The whole section: no console error, no content-policy violation.
    const errors = page.errors.slice(before);
    check(errors.length === 0, `the code viewer logs no console error and no content-policy violation (${errors.join(' | ') || 'none'})`);
  } finally {
    await scheme(page, null).catch(() => undefined);
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false }).catch(() => undefined);
    await setLanguage(page, 'English').catch(() => undefined);
    await setTheme(page, 'Light').catch(() => undefined);
    if (pf) {
      const listed = await api(base, 'GET', `/api/v1/folders/${pf}/children`, accountKey).catch(() => null);
      for (const n of listed?.items ?? []) {
        if (Object.values(names).includes(n.name)) await api(base, 'DELETE', `/api/v1/nodes/${n.id}`, accountKey).catch(() => undefined);
      }
    }
    if (lockedID) await api(base, 'DELETE', `/api/v1/nodes/${lockedID}`, accountKey).catch(() => undefined);
  }
}
