// SPDX-License-Identifier: AGPL-3.0-or-later

// The slice r7a checks: comments, activity and who-viewed in the file details, and the owner's two switches, in a real
// browser. Two people talk: the owner and the member take turns on one file. Typing is sent as input events
// (Input.insertText), and every press is a mouse event at the element's place. A comment body with markup is shown as the
// text it is. A send the server refuses keeps the typed text. Phone width, Hebrew and both themes are checked. With
// SELFCLOUD_R7_SHOTS set to a folder, the run also saves the pictures for the slice.
import { join } from 'node:path';
import { mkdir } from 'node:fs/promises';
import { accountName, click, setLanguage, setPhone, setThemeByPlace, shot, sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);
const details = '[data-testid="details"]';

// centre gives the middle of the element a JavaScript expression finds, scrolled into view, or null.
async function centre(page, expr) {
  return page.evaluate(`(() => { const el = ${expr}; if (!el) return null; el.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' }); const r = el.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
}

// pressButton presses the button with the given text inside the scope selector, as a person does.
async function pressButton(page, text, scope = details) {
  const at = await centre(page, `[...document.querySelectorAll(${q(`${scope} button`)})].find((b) => (b.textContent.trim() === ${q(text)} || (b.getAttribute('aria-label') ?? '').startsWith(${q(`${text}:`)})) && !b.disabled)`);
  if (!at) throw new Error(`no button "${text}" in ${scope}`);
  await click(page, at);
  await sleep(250);
}

// pressTab presses the tab of the details drawer with the given name.
async function pressTab(page, name) {
  const at = await centre(page, `[...document.querySelectorAll(${q(`${details} [role="tab"]`)})].find((t) => t.textContent.trim() === ${q(name)})`);
  if (!at) throw new Error(`no tab "${name}"`);
  await click(page, at);
  await sleep(300);
}

// Expressions that find a text field: by its label's words (the label names the field), or by its aria-label.
const labelled = (words) =>
  `document.getElementById([...document.querySelectorAll('label')].find((l) => l.textContent.trim().startsWith(${q(words)}))?.htmlFor ?? '')`;
const ariaLabelled = (label) => `document.querySelector(${q(`textarea[aria-label="${label}"]`)})`;

// typeInto clicks the field and types the text as keyboard input does.
async function typeInto(page, field, text) {
  const at = await centre(page, field);
  if (!at) throw new Error('no such field');
  await click(page, at);
  const focused = await page.evaluate(`(() => { const el = ${field}; const a = document.activeElement; return { same: a === el, tag: a?.tagName, label: a?.getAttribute('aria-label') ?? a?.id ?? '' }; })()`);
  if (!focused.same) throw new Error(`the click did not focus the field (focus on ${focused.tag} ${focused.label})`);
  await page.send('Input.insertText', { text });
  await sleep(150);
}

// setThemeAt picks the theme at a place in the top bar's menu: 0 system, 1 light, 2 dark (the order the menu gives them). The
// place is used, not the English words, so the pick works in every language (decision 68, review fix).
async function setThemeAt(page, place, want) {
  await setThemeByPlace(page, place, want);
}

const valueOf = (page, field) => page.evaluate(`(${field})?.value ?? null`);
const tabNames = (page) => page.evaluate(`[...document.querySelectorAll(${q(`${details} [role="tab"]`)})].map((t) => t.textContent.trim())`);

// scrollToComments brings the comments section of the drawer to its top, so a picture shows the threads and not only the composer.
const scrollToComments = (page) =>
  page.evaluate(`[...document.querySelectorAll('${details} section[aria-label]')].pop()?.scrollIntoView({ block: 'start', behavior: 'instant' })`).then(() => sleep(300));

// openDetails opens the file in its folder and presses Details, as a person does.
async function openDetails(h, page, folderURL, name) {
  await page.goto(folderURL);
  await h.rowAppears(page, name);
  await h.selectRow(page, name);
  await h.clickButton(page, 'Details');
  await page.waitFor(`!!document.querySelector(${q(details)})`, 15000);
  await page.waitFor(`document.querySelectorAll(${q(`${details} [role="tab"]`)}).length > 0`, 15000);
  // The drawer slides in; a press made while it moves lands beside its target.
  await sleep(700);
}

// pickMention types letters into the mention box and presses the suggestion with the given name.
async function pickMention(page, name, letters) {
  await typeInto(page, labelled('Name someone'), letters);
  await page.waitFor(`[...document.querySelectorAll('[role="option"]')].some((o) => o.textContent.trim() === ${q(name)})`, 15000);
  const at = await centre(page, `[...document.querySelectorAll('[role="option"]')].find((o) => o.textContent.trim() === ${q(name)})`);
  await click(page, at);
  await sleep(200);
}

// The bodies of the top-level comments, and of the replies, as the drawer shows them.
const commentBodies = (page) =>
  page.evaluate(`[...document.querySelectorAll(${q(`${details} ul[aria-label="Comments"] > li article[data-testid="comment"] p bdi`)})].map((b) => b.textContent)`);
const replyBodies = (page) =>
  page.evaluate(`[...document.querySelectorAll(${q(`${details} [data-testid="comment-reply"] p bdi`)})].map((b) => b.textContent)`);


// r7Checks runs the slice as the owner, and then as the member in the same browser. run.mjs leaves the browser signed in as
// the member before the slice gate, so the checks start by signing in as the owner, and end by signing the member back in,
// which keeps the state the next checks of a full run expect.
export async function r7Checks(h, args) {
  const { page, OWNER, MEMBER, signIn, signOut } = { ...args, ...h };
  await signOut(page);
  await signIn(page, OWNER);
  try {
    await r7Body(h, args);
  } finally {
    await signOut(page);
    await signIn(page, MEMBER);
  }
}

async function r7Body(h, { page, base, accountKey, accountID, memberID }) {
  const { check, api, rawPut, signIn, signOut, OWNER, MEMBER } = h;
  const failures = [];
  const step = async (label, fn) => {
    try {
      await fn();
    } catch (err) {
      failures.push(`${label}: ${err.message}`);
      console.log(`FAIL ${label}: ${err.message}`);
    }
  };
  const errorsBefore = page.errors.length;
  const shotDir = process.env.SELFCLOUD_R7_SHOTS;

  // A folder of its own with one text file; the member may write to it, so the member is a commenter there.
  const talk = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'R7 Talk' })).id;
  await api(base, 'PUT', `/api/v1/accounts/${accountID}/folders/${talk}/grants/${memberID}`, accountKey, { level: 'write' });
  await rawPut(base, `/api/v1/folders/${talk}/files?path=%2Fplan.txt`, accountKey, 'The plan.\n', 'text/plain');
  const folderURL = `${page.base}/files/${talk}`;
  const newComment = labelled('New comment');

  // ---- the owner: a comment that names the member, a markup body, a refused send, a resolve, the activity ----
  await step('the owner opens the details and sees the comments and the activity', async () => {
    await openDetails(h, page, folderURL, 'plan.txt');
    const tabs = await tabNames(page);
    check(tabs.includes('Comments') && tabs.includes('Activity'), `the drawer shows the Comments and Activity tabs (${tabs.join(', ')})`);
  });

  await step('the owner writes a comment that names the member, by typing and picking the name', async () => {
    await typeInto(page, newComment, 'Please review the plan ');
    await pickMention(page, MEMBER.name, 'Mat');
    const text = await valueOf(page, newComment);
    check(text === `Please review the plan @${MEMBER.name} `, `the picked name is added to the text as @name (${text})`);
    await pressButton(page, 'Post');
    await page.waitFor(`[...document.querySelectorAll(${q(`${details} p bdi`)})].some((b) => b.textContent.startsWith('Please review the plan'))`, 15000);
    const bodies = await commentBodies(page);
    check(bodies.some((b) => b.includes(MEMBER.name)), `the posted comment names the member (${bodies.join(' | ')})`);
    check((await valueOf(page, newComment)) === '', 'the composer is cleared once the server has taken the comment');
  });

  await step('a comment with markup is shown as the text it is, and no element is made from it', async () => {
    const hostile = '<img src=x onerror="window.__r7=1"> and <b>bold</b>';
    await typeInto(page, newComment, hostile);
    await pressButton(page, 'Post');
    await page.waitFor(`[...document.querySelectorAll(${q(`${details} p bdi`)})].some((b) => b.textContent.includes('<img src=x'))`, 15000);
    const made = await page.evaluate(`({ img: !!document.querySelector(${q(`${details} img`)}), bold: !!document.querySelector(${q(`${details} p b`)}), ran: window.__r7 === 1 })`);
    check(!made.img && !made.bold && !made.ran, `the markup makes no image, no bold element and runs no script (${JSON.stringify(made)})`);
    check((await commentBodies(page)).includes(hostile), 'the markup body is shown literally, as typed');
  });

  await step('a send the server refuses keeps the typed text, and the next send takes it', async () => {
    // The account's comment switch is turned off for a moment, so the server refuses the send with its own reason.
    const keep = 'Keep this text if the send fails';
    await typeInto(page, newComment, keep);
    await api(base, 'PATCH', `/api/v1/accounts/${accountID}/sharing-policy`, accountKey, { comments: false });
    await pressButton(page, 'Post');
    await page.waitFor(`[...document.querySelectorAll(${q(`${details} [role="alert"]`)})].some((a) => a.textContent.includes('turned off'))`, 15000);
    check((await valueOf(page, newComment)) === keep, 'the typed comment is still in the box after the refused send');
    await api(base, 'PATCH', `/api/v1/accounts/${accountID}/sharing-policy`, accountKey, { comments: true });
    await pressButton(page, 'Post');
    await page.waitFor(`[...document.querySelectorAll(${q(`${details} p bdi`)})].some((b) => b.textContent === ${q(keep)})`, 15000);
    check((await valueOf(page, newComment)) === '', 'the same text is posted on the second send, and the box is cleared');
  });

  await step('the owner resolves the thread, and the button then offers Reopen', async () => {
    await pressButton(page, 'Resolve');
    const resolved = `[...document.querySelectorAll(${q(`${details} [data-testid="comment"]`)})].some((a) => a.textContent.includes('Resolved') && [...a.querySelectorAll('button')].some((b) => b.textContent.trim() === 'Reopen' || b.getAttribute('aria-label') === 'Reopen'))`;
    await page.waitFor(resolved, 15000);
    check(await page.evaluate(resolved), 'the thread is shown as resolved, and its button offers Reopen');
  });

  await step('the owner sees the activity of the file as sentences, with You for the owner', async () => {
    await pressTab(page, 'Activity');
    await page.waitFor(`[...document.querySelectorAll('[data-testid="activity-entry"]')].some((e) => e.textContent.includes('commented on an item'))`, 15000);
    const text = await page.evaluate(`[...document.querySelectorAll('[data-testid="activity-entry"]')].map((e) => e.textContent).join(' ')`);
    check(!text.includes('comment.create') && !text.includes('audit.'), 'the activity shows sentences, not action names or catalogue keys');
    check(text.includes('You'), `the owner's own comments read as You (${text.slice(0, 90)})`);
    await pressTab(page, 'Comments');
  });

  // ---- the member: the other side of the talk ----
  await step('the member sees the thread, replies, and edits the reply; the owner comment has no Edit for the member', async () => {
    await signOut(page);
    await signIn(page, MEMBER);
    const who = await accountName(page);
    check(who.includes(MEMBER.name), `the page now signs the member in (${who.trim()})`);
    await openDetails(h, page, folderURL, 'plan.txt');
    const tabs = await tabNames(page);
    check(!tabs.includes('Who viewed'), `a member who does not manage the file has no Who viewed tab (${tabs.join(', ')})`);
    await page.waitFor(`[...document.querySelectorAll(${q(`${details} p bdi`)})].some((b) => b.textContent.startsWith('Please review the plan'))`, 15000);
    await pressButton(page, 'Reply');
    await typeInto(page, labelled('Reply to'), 'On it');
    await pressButton(page, 'Post reply');
    await page.waitFor(`[...document.querySelectorAll(${q(`${details} [data-testid="comment-reply"] p bdi`)})].some((b) => b.textContent === 'On it')`, 15000);
    check((await replyBodies(page)).includes('On it'), 'the member reply is listed under the thread');
    await pressButton(page, 'Edit', `${details} [data-testid="comment-reply"]`);
    // The field is emptied as the page's own input is, since a select-all key press does not reach the text in Chrome.
    await page.setInput('textarea[aria-label="Edit comment"]', '');
    await sleep(150);
    await typeInto(page, ariaLabelled('Edit comment'), 'On it, today');
    // The drawer has an Appearance Save too: the press is kept to this reply.
    await pressButton(page, 'Save', `${details} [data-testid="comment-reply"]`);
    await page.waitFor(`[...document.querySelectorAll(${q(`${details} [data-testid="comment-reply"] p bdi`)})].some((b) => b.textContent === 'On it, today')`, 15000);
    const edited = await page.evaluate(`[...document.querySelectorAll(${q(`${details} [data-testid="comment-reply"]`)})].some((a) => a.textContent.includes('Edited'))`);
    check(edited, 'the edited reply shows Edited');
    const ownerEditable = await page.evaluate(
      `[...document.querySelectorAll(${q(`${details} [data-testid="comment"]`)})].filter((a) => a.textContent.includes('Please review the plan')).some((a) => [...a.querySelectorAll('button')].some((b) => (b.textContent.trim() === 'Edit' || b.getAttribute('aria-label') === 'Edit') && b.closest('article') === a))`,
    );
    check(!ownerEditable, 'the owner comment offers no Edit to the member');
  });

  // ---- the owner again: the reply is there, and a manager deletes it after asking ----
  await step('the owner reads the member reply and deletes it as a manager, after a question', async () => {
    await signOut(page);
    await signIn(page, OWNER);
    await openDetails(h, page, folderURL, 'plan.txt');
    await page.waitFor(`[...document.querySelectorAll(${q(`${details} [data-testid="comment-reply"] p bdi`)})].some((b) => b.textContent === 'On it, today')`, 15000);
    check((await replyBodies(page)).includes('On it, today'), "the owner reads the member's edited reply");
    await pressButton(page, 'Delete', `${details} [data-testid="comment-reply"]`);
    await page.waitFor(`!!document.querySelector('[role="dialog"]')`, 10000);
    await pressButton(page, 'Delete comment', '[role="dialog"]');
    await page.waitFor(`![...document.querySelectorAll(${q(`${details} [data-testid="comment-reply"] p bdi`)})].some((b) => b.textContent === 'On it, today')`, 15000);
    check(!(await replyBodies(page)).includes('On it, today'), 'the reply has left the thread');
  });

  // ---- the owner's two switches, on the owner's page ----
  await step('the owner turns comments off, the details lose the tab, and the owner turns them back on', async () => {
    await h.navigateTo(page, '/manage');
    await page.waitFor(`!!document.getElementById('comments-switch')`, 15000);
    await click(page, await centre(page, `document.getElementById('comments-switch').closest('label')`));
    await page.waitFor(`document.getElementById('comments-switch').checked === false`, 10000);
    await openDetails(h, page, folderURL, 'plan.txt');
    await page.waitFor(`[...document.querySelectorAll(${q(`${details} [role="note"]`)})].some((n) => n.textContent.includes('Comments are turned off'))`, 15000);
    const tabs = await tabNames(page);
    check(!tabs.includes('Comments') && tabs.includes('Activity'), `with comments off the Comments tab is gone, and Activity stays (${tabs.join(', ')})`);
    await h.navigateTo(page, '/manage');
    await page.waitFor(`!!document.getElementById('comments-switch')`, 15000);
    await click(page, await centre(page, `document.getElementById('comments-switch').closest('label')`));
    await page.waitFor(`document.getElementById('comments-switch').checked === true`, 10000);
    check(await page.evaluate(`document.getElementById('comments-switch').checked === true`), 'the owner sees the comments switch on again');
  });

  await step('the owner turns who-viewed on, and the manager sees the list with its note', async () => {
    await h.navigateTo(page, '/manage');
    await page.waitFor(`!!document.getElementById('who-viewed-switch')`, 15000);
    await click(page, await centre(page, `document.getElementById('who-viewed-switch').closest('label')`));
    await page.waitFor(`document.getElementById('who-viewed-switch').checked === true`, 10000);
    await openDetails(h, page, folderURL, 'plan.txt');
    const tabs = await tabNames(page);
    check(tabs.includes('Who viewed'), `with the switch on, the manager sees Who viewed (${tabs.join(', ')})`);
    await pressTab(page, 'Who viewed');
    const note = `[...document.querySelectorAll(${q(`${details} [role="note"]`)})].some((n) => n.textContent.includes('A convenience, not evidence'))`;
    await page.waitFor(note, 15000);
    check(await page.evaluate(note), 'the list carries its note: a convenience, not evidence');
    await pressTab(page, 'Comments');
  });

  // ---- phone width, Hebrew, and the pictures ----
  await step('at phone width the drawer keeps its tabs and the page does not scroll sideways', async () => {
    await setPhone(page, true);
    await openDetails(h, page, folderURL, 'plan.txt');
    await pressTab(page, 'Comments');
    const overflow = await page.evaluate(`document.documentElement.scrollWidth - window.innerWidth`);
    check(overflow <= 0, `at phone width the page has no sideways scroll (overflow ${overflow}px)`);
    // The name picker is an input: on a coarse pointer it is a 44 px target, as the buttons are. The pointer is read from the
    // page, so the check says whether the emulated screen is a coarse one before it measures.
    const picker = await page.evaluate(
      `(() => { const el = document.querySelector(${q(`${details} .comments-panel input[role="combobox"]`)}); return el ? { h: el.getBoundingClientRect().height, coarse: matchMedia('(pointer: coarse)').matches } : null; })()`,
    );
    check(picker !== null && picker.coarse && picker.h >= 44, `at phone width the name picker is 44 px high on a coarse pointer (${JSON.stringify(picker)})`);
    await setPhone(page, false);
  });

  await step('in Hebrew the drawer is right to left and its tabs carry the Hebrew words', async () => {
    await openDetails(h, page, folderURL, 'plan.txt');
    await setLanguage(page, 'עברית');
    await page.waitFor(`document.documentElement.dir === 'rtl'`, 10000);
    const dir = await page.evaluate(`document.documentElement.dir`);
    const tabs = await tabNames(page);
    check(dir === 'rtl' && tabs.includes('הערות'), `Hebrew sets rtl and names the comments tab (${dir}, ${tabs.join(', ')})`);
    await setLanguage(page, 'English');
  });

  if (shotDir) {
    await step('the slice r7 pictures, desktop and phone, English and Hebrew, light and dark', async () => {
      await mkdir(shotDir, { recursive: true });
      // The theme choice is in the header at desktop width only. The file is opened in English, and the language is then
      // changed with the drawer open (the header's language choice is there at both widths).
      const desktop = async () => {
        await setPhone(page, false);
        await page.waitFor(`!!document.querySelector('[data-testid="theme"]')`, 15000);
      };
      const phone = async () => {
        await setPhone(page, true);
        await page.waitFor(`!document.querySelector('[data-testid="theme"]')`, 15000);
      };
      const hebrewTabs = async (name) => {
        await setLanguage(page, 'עברית');
        await page.waitFor(`document.documentElement.dir === 'rtl'`, 10000);
        await pressTab(page, 'הערות');
        await scrollToComments(page);
        await sleep(500);
        await shot(page, shotDir, name);
      };
      const close = async () => {
        await h.press(page, details, 'Escape');
        await page.waitFor(`!document.querySelector(${q(details)})`, 10000);
      };

      await desktop();
      await setThemeAt(page, 1, 'light');
      await openDetails(h, page, folderURL, 'plan.txt');
      await pressTab(page, 'Comments');
      await scrollToComments(page);
      await shot(page, shotDir, 'r7-desktop-en-light.png');
      await setThemeAt(page, 2, 'dark');
      await scrollToComments(page);
      await shot(page, shotDir, 'r7-desktop-en-dark.png');
      await hebrewTabs('r7-desktop-he-dark.png');
      await setThemeAt(page, 1, 'light');
      await scrollToComments(page);
      await shot(page, shotDir, 'r7-desktop-he-light.png');
      await close();
      await setLanguage(page, 'English');

      await phone();
      await openDetails(h, page, folderURL, 'plan.txt');
      await pressTab(page, 'Comments');
      await scrollToComments(page);
      await shot(page, shotDir, 'r7-phone-en-light.png');
      await hebrewTabs('r7-phone-he-light.png');
      await close();
      await setLanguage(page, 'English');

      await desktop();
      await setThemeAt(page, 2, 'dark');
      await phone();
      await openDetails(h, page, folderURL, 'plan.txt');
      await pressTab(page, 'Comments');
      await scrollToComments(page);
      await shot(page, shotDir, 'r7-phone-en-dark.png');
      await hebrewTabs('r7-phone-he-dark.png');
      await close();
      await desktop();
      await setThemeAt(page, 1, 'light');
      await setLanguage(page, 'English');
    });
  }

  await step('no console error or policy violation was logged during the r7 checks', async () => {
    // A 403 from the comments or the who-viewed route is the answer a switch gives on purpose; the browser logs it as a failed request.
    const switchAnswer = (e) => e.includes('403') && (e.includes('/comments') || e.includes('/viewers'));
    const fresh = page.errors.slice(errorsBefore).filter((e) => !switchAnswer(e));
    check(fresh.length === 0, `the browser logged no error in the r7 checks (${fresh.join(' | ') || 'none'})`);
  });

  if (failures.length > 0) throw new Error(`r7 checks failed: ${failures.join('; ')}`);
}
