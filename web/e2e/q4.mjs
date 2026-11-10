// SPDX-License-Identifier: AGPL-3.0-or-later

// The search page's slice q4 checks. A member types in the browser, through the browser's own key events: a burst of letters is
// one request; a name search finds names, a text search finds words inside files, and a snippet marks its match; a filter is a
// chip that removes itself; an empty answer says so; Hebrew turns the page right to left and translates its strings; a phone
// has the box on the page, and its filter fields are touch-sized; a file whose name and text hold markup shows the markup as
// text; the result links and the Clear filters button reach 4.5 to 1 in both themes; a failed request says it could not reach
// the server; and past the limit of sixty a minute the page says so in a sentence. With SELFCLOUD_Q4_SHOTS set (a folder, or 1
// for the slice's own folder), the run also saves the pictures of the page: desktop and phone, English and Hebrew, light and dark.
import { mkdir, stat, writeFile } from 'node:fs/promises';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { setLanguage, setPhone, setTheme } from './media.mjs';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const q = (v) => JSON.stringify(v);

const BOX = 'Search names and text in files';
const HEBREW_BOX = 'חיפוש בשמות ובטקסט של קבצים';
// The results list is found by its test id: its label is in the language of the page.
const RESULTS = '[data-testid="search-results"]';
const SEARCH_PATH = '/api/v1/search';
const WARNING_CONTRAST = 4.5;

// The files the slice searches for: a name, a Hebrew name, and a text whose words are not in its name.
const FILES = [
  { name: 'Q4 budget.txt', text: 'Quarterly figures for the harbour office.\n' },
  { name: 'דוח שנתי.txt', text: 'The annual report of the school, in plain words.\n' },
  { name: 'Harbour plan.txt', text: 'The quarterly ledger mentions the harbour contract renewal.\n' },
];

// The file whose name and text are markup. Its name has no slash, so the server takes it; its text is found by its first word.
const MARKUP = {
  name: 'zmarkupq <img src=x onerror=window.__pwned=1>.txt',
  text: 'zmarkupq <img src=x onerror="window.__pwned=1">\n',
};

// The pictures the run saves, in the order they are taken.
const PICTURES = ['text-desktop-en-light.png'];
for (const theme of ['light', 'dark']) {
  for (const lang of ['en', 'he']) {
    for (const kind of ['desktop', 'phone']) PICTURES.push(`${kind}-${lang}-${theme}.png`);
  }
}

// searchRequests counts the requests the page has made to the search route.
const searchRequests = (page) => page.sent.filter((r) => r.url.includes(SEARCH_PATH)).length;

// resultNames lists the names of the rows of the results list, in order.
const resultNames = (page) =>
  page.evaluate(`[...document.querySelectorAll(${q(RESULTS + ' tbody tr[data-id]')})].map((li) => li.querySelector('bdi')?.textContent ?? '')`);

// alertShows says whether an alert on the page shows the text.
const alertShows = (page, text) =>
  page.evaluate(`[...document.querySelectorAll('[role="alert"]')].some((a) => a.textContent.includes(${q(text)}))`);

// chipShows says whether a filter chip on the page shows the text.
const chipShows = (page, text) =>
  page.evaluate(`[...document.querySelectorAll('[data-testid="search-chip"]')].some((c) => c.textContent.includes(${q(text)}))`);

// typeKeys sends each character as a key press to the focused element, as a person's keyboard does, a little apart.
async function typeKeys(page, text, gap = 30) {
  for (const ch of [...text]) {
    await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: ch, text: ch, unmodifiedText: ch });
    await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: ch });
    await sleep(gap);
  }
}

// clearKeys deletes what the focused element holds, one Backspace at a time.
async function clearKeys(page) {
  const n = await page.evaluate(`(document.activeElement?.value ?? '').length`);
  for (let i = 0; i < n; i++) {
    await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Backspace', code: 'Backspace', windowsVirtualKeyCode: 8, nativeVirtualKeyCode: 8 });
    await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Backspace', code: 'Backspace', windowsVirtualKeyCode: 8, nativeVirtualKeyCode: 8 });
  }
}

// focusBox focuses the search box the selector names, and waits for it.
// The box is focused only when the browser says it has the focus: a layout change can re-mount it and drop the focus, and keys
// sent then go nowhere.
async function focusBox(page, label) {
  const sel = q(`input[aria-label=${q(label)}]`);
  await page.waitFor(
    `(() => { const el = document.querySelector(${sel}); if (!el) return false; el.focus(); return document.activeElement === el; })()`,
    10000,
  );
}

// typeWords replaces what the box holds with the words, typed.
async function typeWords(page, label, words) {
  await focusBox(page, label);
  await clearKeys(page);
  await typeKeys(page, words);
}

// pickOption chooses one option of the antd select whose label is the given one, as a person does with the mouse.
async function pickOption(page, label, text) {
  await page.waitFor(`!!document.querySelector(${q(`input[aria-label=${q(label)}]`)})`, 10000);
  await page.evaluate(`(() => {
    const root = document.querySelector(${q(`input[aria-label=${q(label)}]`)}).closest('.ant-select');
    root.querySelector('.ant-select-selector').dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    return true;
  })()`);
  await page.waitFor(`[...document.querySelectorAll('.ant-select-item-option')].some((o) => o.textContent.trim() === ${q(text)})`, 10000);
  await page.evaluate(`[...document.querySelectorAll('.ant-select-item-option')].find((o) => o.textContent.trim() === ${q(text)}).click(), true`);
}

// clickButtonText clicks the button whose text is the given one.
async function clickButtonText(page, text) {
  await page.waitFor(
    `(() => { const b = [...document.querySelectorAll('button')].find((x) => x.textContent.trim() === ${q(text)} && !x.disabled); if (!b) return false; b.click(); return true; })()`,
    10000,
  );
}

// bodyHas waits until the page shows the text.
async function bodyHas(page, text, timeout = 10000) {
  try {
    await page.waitFor(`document.body.textContent.includes(${q(text)})`, timeout);
  } catch (err) {
    // A failure names what the page showed instead, so a wrong sentence is seen at once.
    const seen = await page.evaluate(`JSON.stringify({
      where: location.pathname + location.search,
      alerts: [...document.querySelectorAll('[role="alert"]')].map((a) => a.textContent),
      results: document.querySelector('[data-testid="search-results"]')?.textContent.slice(0, 80) ?? null,
    })`);
    throw new Error(`${err.message}; the page showed ${seen}`);
  }
}

// contrastReport measures the colour of the result link, the Clear filters button and, when there is one, the marked part of a
// snippet, against the colour behind each: the first background that is not transparent, going up from the element. It runs in the
// page; the ratios follow the WCAG relative-luminance formula.
function contrastReport(linkSel, clearText, markSel) {
  const parse = (c) => {
    const m = /rgba?\(([^)]+)\)/.exec(c);
    if (!m) return null;
    const p = m[1].split(/[\s,/]+/).filter(Boolean).map(Number);
    return { r: p[0], g: p[1], b: p[2], a: p.length > 3 ? p[3] : 1 };
  };
  const lum = (c) => {
    const f = (v) => {
      const x = v / 255;
      return x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4;
    };
    return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b);
  };
  const backdrop = (el) => {
    for (let e = el; e; e = e.parentElement) {
      const bg = parse(getComputedStyle(e).backgroundColor);
      if (bg && bg.a > 0) return bg;
    }
    return { r: 255, g: 255, b: 255, a: 1 };
  };
  const ratio = (el) => {
    const fg = parse(getComputedStyle(el).color);
    const [x, y] = [lum(fg), lum(backdrop(el))].sort((p, s) => s - p);
    return Math.round(((x + 0.05) / (y + 0.05)) * 100) / 100;
  };
  const link = document.querySelector(linkSel);
  const clear = [...document.querySelectorAll('button')].find((b) => (b.textContent.trim() === clearText || b.getAttribute('aria-label') === clearText));
  const mark = markSel ? document.querySelector(markSel) : null;
  return {
    link: link ? ratio(link) : null,
    clear: clear ? ratio(clear) : null,
    mark: mark ? ratio(mark) : null,
  };
}

// shot saves the page as a PNG in the slice's folder, when that is asked for.
async function shot(page, dir, name) {
  if (!dir) return;
  await sleep(800);
  const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
  await writeFile(join(dir, name), Buffer.from(data, 'base64'));
}

// shotsDir is the folder for the pictures, or null when the run does not ask for them.
async function shotsDir() {
  const v = process.env.SELFCLOUD_Q4_SHOTS;
  if (!v) return null;
  const dir = v === '1' ? join(homedir(), '.local', 'share', 'selfcloud-plans', 'shots-q4') : v;
  await mkdir(dir, { recursive: true });
  return dir;
}

// waitIndexed waits until a text search finds the word: the text of a file is indexed by a job after the upload.
async function waitIndexed(api, base, accountKey, word) {
  const until = Date.now() + 30000;
  for (;;) {
    const found = await api(base, 'GET', `${SEARCH_PATH}?in=content&q=${encodeURIComponent(word)}&limit=5`, accountKey);
    if (found.items.length >= 1) return;
    if (Date.now() > until) throw new Error(`the text with the word ${word} was not indexed in 30 seconds`);
    await sleep(300);
  }
}

export async function q4Checks(h, { page, base, accountKey, teamA }) {
  const { check, api, rawPut, navigateTo } = h;
  const dir = await shotsDir();
  const started = Date.now();

  // The files: names, a Hebrew name, a text found only by its words, and a file whose name and text are markup. The text is
  // indexed by a job, so the run waits for it to appear in a search before it types one.
  for (const f of [...FILES, MARKUP]) {
    await rawPut(base, `/api/v1/folders/${teamA}/files?path=${encodeURIComponent('/' + f.name)}`, accountKey, f.text);
  }
  await waitIndexed(api, base, accountKey, 'ledger');
  await waitIndexed(api, base, accountKey, 'zmarkupq');

  // The page opens from the top bar: the box is on every page, and a word opens the page with the word in the address.
  await navigateTo(page, '/files');
  await page.waitFor(`!!document.querySelector(${q(`input[aria-label=${q(BOX)}]`)})`, 10000);
  const before = searchRequests(page);
  await page.evaluate(`(document.querySelector(${q(`input[aria-label=${q(BOX)}]`)}).focus(), true)`);
  await typeKeys(page, 'budget', 30);
  await page.waitFor(`location.pathname === '/search' && location.search.includes('q=budget')`, 10000);
  await sleep(700);
  const burst = searchRequests(page) - before;
  check(burst === 1, `typing a word sends one search for the burst, not one per letter (${burst} sent)`);

  // A name search finds the name, and the row names the file, never an ID.
  await page.waitFor(`document.querySelector(${q(RESULTS)})?.textContent.includes('Q4 budget.txt')`, 10000);
  const names = await resultNames(page);
  check(names.includes('Q4 budget.txt'), `a name search finds the file by its name (${JSON.stringify(names)})`);
  const bodyText = await page.evaluate(`document.body.textContent`);
  check(!/\bnod_[a-z0-9]+/.test(bodyText), 'the results show no node ID');

  // A filter is a chip. Its close button removes that filter alone, and the search is sent again without it.
  await pickOption(page, 'Type', 'Files');
  await page.waitFor(`[...document.querySelectorAll('[data-testid="search-chip"]')].some((c) => c.textContent.includes('Type: Files'))`, 10000);
  check(await chipShows(page, 'Type: Files'), 'a type filter shows as a chip with its value');
  const beforeRemove = searchRequests(page);
  await page.evaluate(`(document.querySelector('[aria-label="Remove filter Type: Files"]').click(), true)`);
  await page.waitFor(`![...document.querySelectorAll('[data-testid="search-chip"]')].some((c) => c.textContent.includes('Type: Files'))`, 10000);
  await page.waitFor(`!location.search.includes('kind=')`, 10000);
  check(searchRequests(page) > beforeRemove, 'removing the chip sends the search again, without that filter');

  // A text search: the words are found inside the file, and the match is marked in the snippet.
  await clickButtonText(page, 'Text in files');
  await page.waitFor(`location.search.includes('in=content')`, 10000);
  await typeWords(page, BOX, 'ledger');
  await page.waitFor(`!!document.querySelector(${q(RESULTS + ' mark')})`, 15000);
  const marked = await page.evaluate(`document.querySelector(${q(RESULTS + ' mark')}).textContent`);
  check(marked === 'ledger', `a text search marks the word it found in the snippet (${marked})`);
  const textNames = await resultNames(page);
  check(textNames.includes('Harbour plan.txt'), `a text search lists the file the word is in, by its name (${JSON.stringify(textNames)})`);
  const img = await page.evaluate(`document.querySelectorAll(${q(RESULTS + ' img')}).length`);
  check(img === 0, `a snippet carries no markup (${img} img elements)`);
  const lightText = await page.evaluate(`(${contrastReport.toString()})(${q(RESULTS + ' tbody a')}, 'Clear filters', ${q(RESULTS + ' mark')})`);
  check(
    lightText.link >= WARNING_CONTRAST && lightText.mark >= WARNING_CONTRAST,
    `light: a result name and a snippet mark reach ${WARNING_CONTRAST}:1 (${JSON.stringify(lightText)})`,
  );
  await shot(page, dir, 'text-desktop-en-light.png');

  // Markup: the file's name and its text hold an image tag with an error handler. The page shows them as text: no img element
  // exists, the handler never runs, and the tag is there to read.
  await typeWords(page, BOX, 'zmarkupq');
  await page.waitFor(`document.querySelector(${q(RESULTS)})?.textContent.includes('<img src=x onerror=window.__pwned=1>')`, 15000);
  const markupImgs = await page.evaluate(`document.querySelectorAll(${q(RESULTS + ' img')}).length`);
  check(markupImgs === 0, `a file whose name and text are markup shows no img element (${markupImgs} found)`);
  const ran = await page.evaluate(`typeof window.__pwned`);
  check(ran === 'undefined', `the markup's error handler did not run (window.__pwned is ${ran})`);
  const literal = await page.evaluate(`document.querySelector(${q(RESULTS)}).textContent.includes('<img src=x')`);
  check(literal === true, 'the markup shows as literal text in the results');
  await clickButtonText(page, 'Names');

  // An empty answer says so, with no count.
  await typeWords(page, BOX, 'zzqxv');
  await bodyHas(page, 'Nothing matches these words and filters.');
  const emptyText = await page.evaluate(`document.body.textContent`);
  check(!/\b0 results?\b/.test(emptyText), 'an empty answer says nothing matches, and no count');

  // Hebrew: a Hebrew word finds a Hebrew name, and the page turns right to left with its strings in Hebrew.
  await typeWords(page, BOX, 'שנתי');
  await page.waitFor(`document.querySelector(${q(RESULTS)})?.textContent.includes('דוח שנתי.txt')`, 10000);
  const hebrewNames = await resultNames(page);
  check(hebrewNames.includes('דוח שנתי.txt'), `a Hebrew word finds the Hebrew name (${JSON.stringify(hebrewNames)})`);
  await setLanguage(page, 'עברית');
  await page.waitFor(`document.documentElement.dir === 'rtl' && !!document.querySelector(${q(`input[aria-label=${q(HEBREW_BOX)}]`)})`, 10000);
  const rtl = await page.evaluate(`document.documentElement.dir`);
  check(rtl === 'rtl', `in Hebrew the page is right to left (dir ${rtl})`);
  await page.waitFor(`document.querySelector(${q(RESULTS)})?.textContent.includes('דוח שנתי.txt')`, 10000);
  await setLanguage(page, 'English');
  await page.waitFor(`document.documentElement.dir === 'ltr' && !!document.querySelector(${q(`input[aria-label=${q(BOX)}]`)})`, 10000);

  // Pictures: desktop and phone, English and Hebrew, light and dark, on a page with results and a filter chip.
  await typeWords(page, BOX, 'budget');
  await page.waitFor(`document.querySelector(${q(RESULTS)})?.textContent.includes('Q4 budget.txt')`, 10000);
  await pickOption(page, 'Type', 'Files');
  await page.waitFor(`[...document.querySelectorAll('[data-testid="search-chip"]')].some((c) => c.textContent.includes('Type: Files'))`, 10000);
  // The theme switch is in the top bar of a wide window only, so the theme is chosen first, at desktop width.
  for (const theme of ['Light', 'Dark']) {
    await setPhone(page, false);
    await setTheme(page, theme);
    await sleep(600);
    await page.waitFor(`!!document.querySelector(${q('[data-testid="search-chip"]')})`, 10000);
    const report = await page.evaluate(`(${contrastReport.toString()})(${q(RESULTS + ' tbody a')}, 'Clear filters', null)`);
    check(
      report.link >= WARNING_CONTRAST && report.clear >= WARNING_CONTRAST,
      `${theme.toLowerCase()}: a result name and Clear filters reach ${WARNING_CONTRAST}:1 (${JSON.stringify(report)})`,
    );
    for (const hebrew of [false, true]) {
      if (hebrew) await setLanguage(page, 'עברית');
      for (const phone of [false, true]) {
        await setPhone(page, phone);
        await page.waitFor(`!!document.querySelector(${q(RESULTS)})`, 10000);
        await shot(page, dir, `${phone ? 'phone' : 'desktop'}-${hebrew ? 'he' : 'en'}-${theme.toLowerCase()}.png`);
      }
      await setPhone(page, false);
      if (hebrew) await setLanguage(page, 'English');
    }
  }
  await setTheme(page, 'Light');
  if (dir) {
    const missing = [];
    for (const name of PICTURES) {
      const saved = await stat(join(dir, name)).then((s) => s.size > 0 && s.mtimeMs >= started, () => false);
      if (!saved) missing.push(name);
    }
    check(missing.length === 0, `the ${PICTURES.length} pictures are saved in ${dir} (missing: ${JSON.stringify(missing)})`);
  }

  // Phone: the page has its own box, the top bar has only the button, the filter fields are touch-sized, and the page does not
  // scroll sideways.
  await setPhone(page, true);
  await page.waitFor(`!!document.querySelector(${q(`input[aria-label=${q(BOX)}]`)})`, 10000);
  // The top bar switches to its phone form a moment after the page does (the two read the width in turn), so it is waited for.
  await page.waitFor(`document.querySelectorAll('header input').length === 0`, 10000);
  const topInputs = await page.evaluate(`document.querySelectorAll('header input').length`);
  check(topInputs === 0, `at phone width the top bar has the button, not the box (${topInputs} inputs in it)`);
  await page.waitFor(`document.querySelectorAll('.search-filter').length > 0`, 10000);
  const touch = JSON.parse(
    await page.evaluate(`JSON.stringify((() => {
      const heights = [...document.querySelectorAll('.search-filter')].map((el) =>
        Math.round((el.classList.contains('ant-select') ? el.querySelector('.ant-select-selector') : el).getBoundingClientRect().height));
      return { coarse: matchMedia('(pointer: coarse)').matches, heights };
    })())`),
  );
  check(touch.coarse, 'the phone emulation reports a coarse pointer');
  check(
    touch.heights.length > 0 && touch.heights.every((h) => h >= 44),
    `at phone width every search filter field is at least 44 px high (${JSON.stringify(touch.heights)})`,
  );
  const overflow = await page.evaluate(`document.documentElement.scrollWidth - document.documentElement.clientWidth`);
  check(overflow <= 0, `at phone width the search page fits and does not scroll sideways (overflow ${overflow}px)`);
  await setPhone(page, false);
  // Back at desktop width the top bar's box is there again, and stays put before the next keys go to it.
  await page.waitFor(`document.querySelectorAll(${q(`input[aria-label=${q(BOX)}]`)}).length === 1`, 10000);
  await sleep(400);

  // A failed request: the browser cannot reach the server, and the page says so, and keeps no old results under the new words.
  const unreachable = 'Could not reach the server. Check your connection and try again.';
  await page.send('Network.emulateNetworkConditions', { offline: true, latency: 0, downloadThroughput: 0, uploadThroughput: 0 });
  await typeWords(page, BOX, 'offline');
  await bodyHas(page, unreachable, 15000);
  check(await alertShows(page, unreachable), 'a failed request shows the could-not-reach sentence in an alert');
  await page.send('Network.emulateNetworkConditions', { offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
  await typeWords(page, BOX, 'budget');
  await page.waitFor(`document.querySelector(${q(RESULTS)})?.textContent.includes('Q4 budget.txt')`, 15000);
  const afterOffline = await resultNames(page);
  check(afterOffline.includes('Q4 budget.txt'), `the search works again once the connection is back (${JSON.stringify(afterOffline)})`);

  // The limit: past sixty searches a minute the server refuses, and the page says so in a sentence. The bucket holds sixty
  // searches and refills one a second, and a search takes its token when it reaches the server. The page's own search is held
  // before it reaches the server (the CDP Fetch domain pauses it until the run releases it), so it cannot take a token by
  // chance. The run empties the bucket, then waits for the refill: it sends searches until one is refused (the bucket is then
  // empty), and then until one is answered, which is the search that takes the refilled token. The bucket is empty again at
  // that moment. The held search is released at once, so it reaches the server within a round trip of that take, and the next
  // token is a second away: the page's search meets an empty bucket. The window left is that round trip. A page search gets
  // through only if it reaches the server a second or more after the take, which needs a stall that long; then the alert is
  // missing and the step fails, as the race did, so no pass can come from a refill.
  const limited = 'Too many searches. Wait a minute, then try again.';
  // flood sends searches as the page's own script does, and counts the refusals.
  const flood = () =>
    page.evaluate(`(async () => {
      let n = 0;
      for (let i = 0; i < 150; i++) {
        const r = await fetch('${SEARCH_PATH}?q=budget&limit=1', { headers: { Accept: 'application/json' } });
        if (r.status === 429) n++;
      }
      return n;
    })()`);
  // drain sends searches one after another. The first refusal means the bucket holds less than one token: the bucket is
  // empty. The next answer after that is the search that takes the refilled token, so the bucket is empty again the moment it
  // returns. drain returns the refusals it saw.
  const drain = () =>
    page.evaluate(`(async () => {
      let n = 0;
      let empty = false;
      const until = Date.now() + 10000;
      for (;;) {
        if (Date.now() > until) throw new Error('the bucket did not empty and refill in 10 seconds');
        const r = await fetch('${SEARCH_PATH}?q=budget&limit=1', { headers: { Accept: 'application/json' } });
        await r.text();
        if (r.status === 200) {
          if (empty) return n;
        } else {
          n++;
          empty = true;
        }
      }
    })()`);
  // holdLimited keeps the page's search paused, and only that one: the pattern names its word.
  const held = [];
  const holdLimited = (p) => {
    if (p.request.url.includes('q=limited')) held.push(p.requestId);
  };
  await page.send('Fetch.enable', { patterns: [{ urlPattern: '*q=limited*', requestStage: 'Request' }] });
  page.on('Fetch.requestPaused', holdLimited);
  try {
    await typeWords(page, BOX, 'limited');
    // The page sends its search once its pause is over; the run waits for the paused request to arrive before it drains.
    for (let i = 0; held.length === 0; i++) {
      if (i > 100) throw new Error('the page did not send its search for the word limited in 10 seconds');
      await sleep(100);
    }
    const drained = await drain();
    for (const requestId of held.splice(0)) await page.send('Fetch.continueRequest', { requestId });
    const after = await flood();
    const refused = drained + after;
    check(refused >= 1, `the server refuses searches past its limit (${refused} refused)`);
    await bodyHas(page, limited, 15000);
    check(await alertShows(page, limited), 'past the limit the page shows the sentence in an alert');
    const logged = page.expectedAnswers.get('rate limited search')?.length ?? 0;
    check(logged >= 1, `the refused searches were logged by the browser, and counted (${logged})`);
  } finally {
    // The section leaves no paused request and no listener behind: a later section must not see either.
    page.off('Fetch.requestPaused', holdLimited);
    for (const requestId of held.splice(0)) await page.send('Fetch.continueRequest', { requestId });
    await page.send('Fetch.disable');
  }
}
