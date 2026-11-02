// SPDX-License-Identifier: AGPL-3.0-or-later

// The slice r7b checks: the bell with the unread count, the notifications page and the email switch, in a real browser.
// The owner names the member in a comment; the member's bell count rises by one on the next load, the member opens the
// notice and lands on the file with its comments shown, and the count falls back. The email switch is saved, and it stays
// after a reload. Phone width, Hebrew and both themes are checked. With SELFCLOUD_R7B_SHOTS set to a folder, the run also
// saves the pictures for the slice.
import { mkdir } from 'node:fs/promises';
import { click, setLanguage, setPhone, setThemeByPlace, shot, sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);
const details = '[data-testid="details"]';
const bellSel = '[data-testid="notifications-bell"]';

// centre gives the middle of the element a JavaScript expression finds, scrolled into view, or null.
async function centre(page, expr) {
  return page.evaluate(`(() => { const el = ${expr}; if (!el) return null; el.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' }); const r = el.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
}

// pressButton presses the button with the given text inside the scope selector, as a person does.
async function pressButton(page, text, scope = details) {
  const at = await centre(page, `[...document.querySelectorAll(${q(`${scope} button`)})].find((b) => b.textContent.trim() === ${q(text)} && !b.disabled)`);
  if (!at) throw new Error(`no button "${text}" in ${scope}`);
  await click(page, at);
  await sleep(250);
}

// Expressions that find a text field by its label's words.
const labelled = (words) =>
  `document.getElementById([...document.querySelectorAll('label')].find((l) => l.textContent.trim().startsWith(${q(words)}))?.htmlFor ?? '')`;

// typeInto clicks the field and types the text as keyboard input does.
async function typeInto(page, field, text) {
  const at = await centre(page, field);
  if (!at) throw new Error('no such field');
  await click(page, at);
  const focused = await page.evaluate(`document.activeElement === (${field})`);
  if (!focused) throw new Error('the click did not focus the field');
  await page.send('Input.insertText', { text });
  await sleep(150);
}

// pickMention types letters into the mention box and presses the suggestion with the given name.
async function pickMention(page, name, letters) {
  await typeInto(page, labelled('Name someone'), letters);
  await page.waitFor(`[...document.querySelectorAll('[role="option"]')].some((o) => o.textContent.trim() === ${q(name)})`, 15000);
  const at = await centre(page, `[...document.querySelectorAll('[role="option"]')].find((o) => o.textContent.trim() === ${q(name)})`);
  await click(page, at);
  await sleep(200);
}

// openDetails opens the file in its folder and presses Details, as a person does.
async function openDetails(h, page, folderURL, name) {
  await page.goto(folderURL);
  await h.rowAppears(page, name);
  await h.selectRow(page, name);
  await h.clickButton(page, 'Details');
  await page.waitFor(`!!document.querySelector(${q(details)})`, 15000);
  await page.waitFor(`document.querySelectorAll(${q(`${details} [role="tab"]`)}).length > 0`, 15000);
  await sleep(700);
}

// waitCount waits until the bell shows the given count ("0" when nothing is unread, and no badge is drawn).
const countText = (page) => page.evaluate(`document.querySelector('[data-testid="notifications-count"]')?.textContent ?? '0'`);
const waitCount = (page, want) =>
  page.waitFor(`(document.querySelector('[data-testid="notifications-count"]')?.textContent ?? '0') === ${q(String(want))}`, 15000);

// noticeButtons are the lines of the notifications page, as their text.
const noticeTexts = (page) =>
  page.evaluate(`[...document.querySelectorAll('[data-testid="notice"] button')].map((b) => b.textContent)`);

// ownerMentions signs in as the owner, names the member in a new comment on the file, and signs the member back in.
async function ownerMentions(h, page, folderURL, fileName, MEMBER, OWNER, text) {
  await h.signOut(page);
  await h.signIn(page, OWNER);
  try {
    await openDetails(h, page, folderURL, fileName);
    await typeInto(page, labelled('New comment'), `${text} `);
    await pickMention(page, MEMBER.name, 'Mat');
    await pressButton(page, 'Post');
    await page.waitFor(`[...document.querySelectorAll(${q(`${details} p bdi`)})].some((b) => b.textContent.includes(${q(text)}))`, 15000);
  } finally {
    await h.signOut(page);
    await h.signIn(page, MEMBER);
  }
}

// setThemeAt picks the theme at a place in the top bar's menu: 0 system, 1 light, 2 dark (the order the menu gives them). The
// place is used, not the English words, so the pick works in every language (decision 68, review fix).
async function setThemeAt(page, place, want) {
  await setThemeByPlace(page, place, want);
}

// r7bChecks runs the slice as the member, after the owner's comments. run.mjs leaves the browser signed in as the member
// before the slice gate, so the checks sign in as the owner to comment, and end signed in as the member, as the next checks
// of a full run expect.
export async function r7bChecks(h, args) {
  const { page, base, accountKey, accountID, memberID, OWNER, MEMBER, signIn, signOut } = { ...args, ...h };
  const failures = [];
  const step = async (label, fn) => {
    try {
      await fn();
    } catch (err) {
      failures.push(`${label}: ${err.message}`);
      console.log(`FAIL ${label}: ${err.message}`);
    }
  };
  const check = h.check;
  const errorsBefore = page.errors.length;
  const shotDir = process.env.SELFCLOUD_R7B_SHOTS;

  // A folder of its own with one text file; the member may write to it, so the member is a commenter there.
  const talk = (await h.api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'R7b Talk' })).id;
  await h.api(base, 'PUT', `/api/v1/accounts/${accountID}/folders/${talk}/grants/${memberID}`, accountKey, { level: 'write' });
  await h.rawPut(base, `/api/v1/folders/${talk}/files?path=%2Fplan.txt`, accountKey, 'The plan.\n', 'text/plain');
  const folderURL = `${page.base}/files/${talk}`;

  let before = '0';
  await step('the member reads the bell before any mention', async () => {
    await h.signOut(page);
    await h.signIn(page, MEMBER);
    await page.waitFor(`!!document.querySelector(${q(bellSel)})`, 15000);
    await sleep(900);
    before = await countText(page);
    check(/^\d+$/.test(before), `the bell shows a number of unread notifications before the mention (${before})`);
  });
  const baseline = Number(before);

  await step('a mention arrives: the member sees one more unread on the next load of the page', async () => {
    await ownerMentions(h, page, folderURL, 'plan.txt', MEMBER, OWNER, 'Please look at the plan');
    await page.goto(`${page.base}/files`);
    await waitCount(page, baseline + 1);
    const label = await page.evaluate(`document.querySelector(${q(bellSel)})?.getAttribute('aria-label') ?? ''`);
    check(label.includes(`${baseline + 1} unread`), `the bell is named with the count for a screen reader (${label})`);
    const live = await page.evaluate(`document.querySelector('[role="status"][aria-live="polite"]')?.textContent ?? ''`);
    check(live.includes(`${baseline + 1} unread`), `the count is in the polite live region (${live})`);
  });

  await step('the notifications page lists the mention as a sentence, with no raw ID, newest first', async () => {
    await click(page, await centre(page, `document.querySelector(${q(bellSel)})`));
    await page.waitFor(`!!document.querySelector('[data-testid="notice"]')`, 15000);
    const lines = await noticeTexts(page);
    const first = lines[0] ?? '';
    check(first.includes(`${OWNER.name} mentioned you in plan.txt`) && first.includes('Unread'), `the first line says who did what on which file, and that it is unread (${first.slice(0, 120)})`);
    const body = await page.evaluate(`document.body.textContent`);
    check(!/nod_|ntf_|usr_/.test(body), 'no raw ID is on the page');
  });

  await step('the email switch is on by default, goes off, and stays off after a reload', async () => {
    const state = () => page.evaluate(`document.querySelector('[role="switch"]')?.getAttribute('aria-checked') ?? ''`);
    await page.waitFor(`document.querySelector('[role="switch"]')?.getAttribute('aria-checked') === 'true'`, 15000);
    check((await state()) === 'true', 'the email switch is on by default');
    await click(page, await centre(page, `document.querySelector('[role="switch"]')`));
    await page.waitFor(`document.querySelector('[role="switch"]')?.getAttribute('aria-checked') === 'false'`, 15000);
    await page.goto(`${page.base}/notifications`);
    await page.waitFor(`document.querySelector('[role="switch"]')?.getAttribute('aria-checked') === 'false'`, 15000);
    check((await state()) === 'false', 'the switch is off after a reload: the choice was saved');
    await click(page, await centre(page, `document.querySelector('[role="switch"]')`));
    await page.waitFor(`document.querySelector('[role="switch"]')?.getAttribute('aria-checked') === 'true'`, 15000);
    check((await state()) === 'true', 'the switch is on again');
  });

  await step('opening the notice marks it read, shows the file with its comments, and the count falls back', async () => {
    await page.evaluate(`document.querySelector('[data-testid="notice"] button')?.click()`);
    await page.waitFor(`!!document.querySelector(${q(details)})`, 15000);
    await page.waitFor(`[...document.querySelectorAll(${q(`${details} p bdi`)})].some((b) => b.textContent.includes('Please look at the plan'))`, 15000);
    const shown = await page.evaluate(`[...document.querySelectorAll(${q(`${details} [role="tab"]`)})].find((t) => t.getAttribute('aria-selected') === 'true')?.textContent.trim() ?? ''`);
    check(shown === 'Comments', `the file opens on its Comments tab (${shown})`);
    await waitCount(page, baseline);
    check((await countText(page)) === String(baseline), `the bell is back to ${baseline} once the notice is read`);
  });

  await step('at phone width the page does not scroll sideways, and the bell and the switch are 44 px on a finger', async () => {
    await setPhone(page, true);
    await page.goto(`${page.base}/notifications`);
    await page.waitFor(`document.querySelector('[role="switch"]') !== null`, 15000);
    await sleep(400);
    const overflow = await page.evaluate(`document.documentElement.scrollWidth - window.innerWidth`);
    check(overflow <= 0, `at phone width there is no sideways scroll (overflow ${overflow}px)`);
    const sizes = await page.evaluate(`(() => { const b = document.querySelector(${q(bellSel)}); const label = document.querySelector('[role="switch"]')?.closest('label'); return { coarse: matchMedia('(pointer: coarse)').matches, bell: b ? b.getBoundingClientRect().height : 0, label: label ? label.getBoundingClientRect().height : 0 }; })()`);
    check(!sizes.coarse || (sizes.bell >= 44 && sizes.label >= 44), `on a finger the bell and the switch row are 44 px (${JSON.stringify(sizes)})`);
    const line = await page.evaluate(`document.querySelector('[data-testid="notice"] button')?.getBoundingClientRect().height ?? 0`);
    check(!sizes.coarse || line >= 44, `on a finger a line of the list is 44 px high (${line})`);
    await setPhone(page, false);
  });

  await step('in Hebrew the page is right to left and carries its Hebrew title', async () => {
    await page.goto(`${page.base}/notifications`);
    await setLanguage(page, 'עברית');
    await page.waitFor(`document.documentElement.dir === 'rtl'`, 10000);
    const heading = await page.evaluate(`document.querySelector('h1')?.textContent ?? ''`);
    check(heading === 'התראות', `Hebrew names the page (${heading})`);
    await setLanguage(page, 'English');
  });

  // One more mention, left unread on purpose: the pictures show an unread line and a count in the bar.
  await step('a second mention arrives, and the pictures show it unread', async () => {
    await ownerMentions(h, page, folderURL, 'plan.txt', MEMBER, OWNER, 'A second look, please');
    await page.goto(`${page.base}/notifications`);
    await waitCount(page, baseline + 1);
    if (shotDir) {
      await mkdir(shotDir, { recursive: true });
      const desktop = async () => {
        await setPhone(page, false);
        await page.waitFor(`!!document.querySelector('[data-testid="theme"]')`, 15000);
      };
      const phone = async () => {
        await setPhone(page, true);
        await page.waitFor(`!document.querySelector('[data-testid="theme"]')`, 15000);
      };
      const picture = async (name, lang, theme) => {
        await setLanguage(page, lang);
        await page.waitFor(`document.documentElement.dir === ${q(lang === 'עברית' ? 'rtl' : 'ltr')}`, 10000);
        await page.waitFor(`document.querySelectorAll('[data-testid="notice"]').length > 0`, 15000);
        await sleep(400);
        await shot(page, shotDir, name);
      };
      await desktop();
      await setThemeAt(page, 1, 'light');
      await picture('r7b-desktop-en-light.png', 'English', 'light');
      await setThemeAt(page, 2, 'dark');
      await picture('r7b-desktop-en-dark.png', 'English', 'dark');
      await picture('r7b-desktop-he-dark.png', 'עברית', 'dark');
      await setThemeAt(page, 1, 'light');
      await picture('r7b-desktop-he-light.png', 'עברית', 'light');
      await setLanguage(page, 'English');
      await phone();
      await picture('r7b-phone-en-light.png', 'English', 'light');
      await picture('r7b-phone-he-light.png', 'עברית', 'light');
      await setLanguage(page, 'English');
      await desktop();
      await setThemeAt(page, 2, 'dark');
      await phone();
      await picture('r7b-phone-en-dark.png', 'English', 'dark');
      await picture('r7b-phone-he-dark.png', 'עברית', 'dark');
      await setLanguage(page, 'English');
      await desktop();
      await setThemeAt(page, 1, 'light');
    }
  });

  await step('the unread notice is opened again, and the count falls back to the same number as before', async () => {
    await page.goto(`${page.base}/notifications`);
    await page.waitFor(`!!document.querySelector('[data-testid="notice"] button')`, 15000);
    await page.evaluate(`document.querySelector('[data-testid="notice"] button')?.click()`);
    await page.waitFor(`!!document.querySelector(${q(details)})`, 15000);
    await waitCount(page, baseline);
  });

  await step('no console error was logged during the r7b checks', async () => {
    // A 403 from the comments or the who-viewed route is the answer a switch gives on purpose (r7 says the same): the
    // member does not manage the file, so the who-viewed list is refused and logged as a failed request.
    const switchAnswer = (e) => e.includes('403') && (e.includes('/comments') || e.includes('/viewers'));
    const fresh = page.errors.slice(errorsBefore).filter((e) => !switchAnswer(e));
    check(fresh.length === 0, `the browser logged no error in the r7b checks (${fresh.join(' | ') || 'none'})`);
  });

  // The run ends signed in as the member, whatever the checks did.
  await h.signOut(page).catch(() => undefined);
  await h.signIn(page, MEMBER);
  if (failures.length > 0) throw new Error(`r7b checks failed: ${failures.join('; ')}`);
}
