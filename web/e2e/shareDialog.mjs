// SPDX-License-Identifier: AGPL-3.0-or-later
// The share dialog's end-to-end checks for the React interface: sharingChecks and publicLinksDeleteAllChecks.
// Each check description is the one the Svelte suite gave, word for word, so the PASS lists match for this part.
// The helpers take the page and the run's own values through ctx, so this file does not depend on run.mjs.
// ctx: { base, dir, accountID, accountKey, memberID, teamB, sink, check, OWNER, MEMBER, waitForMail, fld }
// page.base must be the server's address, as run.mjs sets it for signIn.

import { readFileSync } from 'node:fs';
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { pickMenuEntry } from './menu.mjs';
import { click, setLanguage, setPhone, setThemeByPlace, sleep } from './media.mjs';

const he = JSON.parse(readFileSync(new URL('../src/i18n/he.json', import.meta.url), 'utf8'));
const en = JSON.parse(readFileSync(new URL('../src/i18n/en.json', import.meta.url), 'utf8'));

const q = (v) => JSON.stringify(v);
// The sidebar's folder tree (decision 62): each space is a row with a link to the space.
const TREE = '[data-testid="folder-tree"]';
const liveText = `(document.querySelector('[role="dialog"] .live')?.textContent ?? '')`;

async function api(base, method, path, key, body) {
  const headers = {};
  if (key) headers.Authorization = `Bearer ${key}`;
  if (body) headers['Content-Type'] = 'application/json';
  const res = await fetch(base + path, { method, headers, body: body ? JSON.stringify(body) : undefined });
  const text = await res.text();
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${text}`);
  return text ? JSON.parse(text) : null;
}

async function rawPut(base, path, key, body, type = 'text/plain') {
  const res = await fetch(base + path, { method: 'POST', headers: { Authorization: `Bearer ${key}`, 'Content-Type': type }, body });
  if (!res.ok) throw new Error(`POST ${path}: ${res.status} ${await res.text()}`);
}

async function clickOn(page, selector, timeout = 10000) {
  await page.waitFor(`(() => { const e = document.querySelector(${q(selector)}); if (!e) return false; e.click(); return true; })()`, timeout);
}

async function clickButton(page, text) {
  // The selection panel's icons carry their names as labels: one that is there is pressed directly.
  const panelButton = `[...document.querySelectorAll('[data-testid="selection-panel"] button')].find((x) => x.getAttribute('aria-label') === ${q(text)} && !x.disabled)`;
  if (await page.evaluate(`!!${panelButton}`)) {
    await page.evaluate(`${panelButton}.click()`);
    return;
  }
  // Some toolbar actions live in the New and More menus: those are chosen from the menu that holds them. Any other button
  // is waited for on the page, as before (a dialog's, or a row's).
  const NEW_MENU = ['New folder', 'Upload files', 'Upload folder'];
  const MENU_ONLY = [...NEW_MENU, 'Details', 'Versions', 'Add shortcut', 'Cut', 'Copy', 'Paste', 'Move to…', 'Copy to…', 'Download as zip'];
  if (!MENU_ONLY.includes(text)) {
    await page.waitFor(
      `(() => { const b = [...document.querySelectorAll('button')].find((x) => x.textContent.trim() === ${q(text)} && !x.disabled); if (!b) return false; b.click(); return true; })()`,
      10000,
    );
    return;
  }
  const holder = NEW_MENU.includes(text) ? 'New' : 'More';
  // The panel's More is an icon with its name as the label; the toolbar's New and More are words.
  const toolbarButton = `[...document.querySelectorAll('[data-testid="browser-toolbar"] button, [data-testid="selection-panel"] button, [data-testid="new-button"]')].find((x) => x.textContent.trim() === ${q(holder)} || x.getAttribute('aria-label') === ${q(holder)})`;
  await page.waitFor(`!!${toolbarButton}`, 10000);
  await page.evaluate(`${toolbarButton}.click()`);
  // The entry is picked by its label; one in a submenu (decision 70) opens its submenu first.
  await pickMenuEntry(page, text);
}

const rowSel = (name) => `[role="row"][aria-label="${name.replace(/["\\]/g, '\\$&')}"]`;

async function rowAppears(page, name, timeout = 15000) {
  await page.waitFor(`!!document.querySelector(${q(rowSel(name))})`, timeout);
}

async function selectRow(page, name) {
  await rowAppears(page, name);
  await page.evaluate(`(() => { const el = document.querySelector(${q(rowSel(name))}); el.focus(); el.click(); return true; })()`);
}

async function press(page, selector, key) {
  // keyCode is set too: the dialog's Escape handler reads it.
  const keyCode = key === 'Escape' ? 27 : 0;
  await page.evaluate(`(() => { const el = document.querySelector(${q(selector)}); el.focus(); el.dispatchEvent(new KeyboardEvent('keydown', { key: ${q(key)}, keyCode: ${keyCode}, bubbles: true })); return true; })()`);
}

async function buttonTexts(page) {
  // An icon button has no text: its name (aria-label) is read too (decision 68), so a check for a missing button still reads it.
  return page.evaluate(`[...document.querySelectorAll('button')].flatMap((b) => [b.textContent.trim(), b.getAttribute('aria-label')]).filter(Boolean)`);
}

async function navigateTo(page, path) {
  await page.evaluate(
    `(() => { const a = document.createElement('a'); a.href = ${q(path)}; document.body.append(a); a.click(); a.remove(); return true; })()`,
  );
}

// signOut and signIn use the account menu the React shell draws (data-testid), as the e2e suite does.
async function signOut(page) {
  // A signed-out visitor on a public page has no account menu: the shell is opened first, through a link click
  // (as run.mjs does). A visitor who is not signed in has nothing to sign out of.
  if (!(await page.evaluate(`!!document.querySelector('[data-testid="account-menu"]')`))) {
    if (await page.evaluate(`location.pathname === '/login'`)) return;
    await navigateTo(page, '/');
    await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]') || location.pathname === '/login'`, 15000);
    if (!(await page.evaluate(`!!document.querySelector('[data-testid="account-menu"]')`))) return;
  }
  await clickOn(page, '[data-testid="account-menu"]');
  await page.waitFor(
    `(() => { const b = [...document.querySelectorAll('.ant-dropdown-menu-item')].find((x) => x.textContent.trim() === 'Sign out'); if (!b) return false; b.click(); return true; })()`,
    10000,
  );
  await page.waitFor(`location.pathname === '/login' && !!document.getElementById('login-email')`, 15000);
}

async function signIn(page, who) {
  await page.goto(page.base + '/login');
  await page.waitFor(`!!document.getElementById('login-email')`);
  await page.setInput('#login-email', who.email);
  await page.setInput('#login-password', who.password);
  await clickOn(page, 'form button[type="submit"]');
  await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]')`, 15000);
}

// setInput types a value into the field with this ID, through the native setter React reads.
async function setInput(page, id, value) {
  await page.setInput(`#${id}`, value);
}

// setCheckbox sets a checkbox by its ID.
async function setCheckbox(page, id, on) {
  await page.evaluate(`(() => { const el = document.getElementById(${q(id)}); if (el.checked !== ${on}) el.click(); return true; })()`);
}

// pickAntdOption opens the antd select with this ID and clicks the option with this text.
async function pickAntdOption(page, id, text) {
  await page.waitFor(`!!document.getElementById(${q(id)})`);
  await page.evaluate(
    `(() => { const sel = document.getElementById(${q(id)}).closest('.ant-select'); sel.querySelector('.ant-select-selector').dispatchEvent(new MouseEvent('mousedown', { bubbles: true })); return true; })()`,
  );
  await page.waitFor(
    `(() => { const o = [...document.querySelectorAll('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option')].find((x) => x.textContent.trim() === ${q(text)}); if (!o) return false; o.click(); return true; })()`,
    10000,
  );
}

// peopleSubmit presses the submit button of the share dialog's people form only. General access sits above that form and
// draws a form of its own (the "Create link" button) when a link is chosen, so the dialog's first submit button is not
// always the people form's.
async function peopleSubmit(page) {
  await clickOn(page, `[role="dialog"] section[aria-labelledby="share-add"] form button[type="submit"]`);
}

// pickPerson types a query into the people combobox and chooses the option with the name.
async function pickPerson(page, query, name, inputId = 'share-who') {
  await setInput(page, inputId, query);
  await page.waitFor(`[...document.querySelectorAll('[role="listbox"] [role="option"]')].some((o) => o.textContent.includes(${q(name)}))`, 10000);
  await page.evaluate(
    `(() => { const o = [...document.querySelectorAll('[role="listbox"] [role="option"]')].find((x) => x.textContent.includes(${q(name)})); o.click(); return true; })()`,
  );
  await page.waitFor(`document.getElementById(${q(inputId)}).value === ${q(name)}`);
}

// peopleRect reads the people field's rectangle, or null while the field is not drawn.
const peopleRect = `(() => { const f = document.getElementById('share-who'); if (!f) return null; const r = f.getBoundingClientRect(); return { x: r.left, y: r.top, width: r.width, height: r.height }; })()`;

// waitStillPeople waits until the people field's rectangle is the same in two reads about 150 ms apart, and returns its
// centre. General access shows a one-line loading status first and then a taller radio group, which moves the field down,
// so a point measured during the move can be outside the field by the time the mouse gets there.
async function waitStillPeople(page) {
  const deadline = Date.now() + 10000;
  let last = await page.evaluate(peopleRect);
  for (;;) {
    await sleep(150);
    const now = await page.evaluate(peopleRect);
    if (!now) throw new Error('the people field left the share dialog');
    if (now.x === last.x && now.y === last.y && now.width === last.width && now.height === last.height) {
      return { x: now.x + now.width / 2, y: now.y + now.height / 2 };
    }
    last = now;
    if (Date.now() > deadline) throw new Error(`the people field kept moving (${JSON.stringify(now)})`);
  }
}

// focusPeopleField focuses the share dialog's people field with a real mouse click, after blurring it (the dialog puts the
// cursor there when it opens; a script's focus() does not fire the focus event in a page without the window's focus). It
// waits until the list shows the named person, and returns the status line's text and the attempt that showed it (1 when the
// first click did). A miss is logged and clicked again, up to three attempts, unless the click moved General access off
// restricted, which means it landed outside the field. On failure it throws with the status, the focus, and the names the
// list shows, so a run's log says what the page did.
async function focusPeopleField(page, name) {
  await page.evaluate(`(() => { document.getElementById('share-who').blur(); return true; })()`);
  // The dialog animates in, so the first click waits until its animations have ended (the same settled test as d3's helper).
  await page.waitFor(`(() => { const d = document.querySelector('[role="dialog"]'); return !!d && d.getAnimations({ subtree: true }).every((a) => a.playState !== 'running'); })()`, 10000);
  // General access reads the item's links after the dialog opens. Until the answer, it shows a one-line loading status;
  // then it draws its taller radio group and the people field moves down. The field is measured only after that.
  await page.waitFor(
    `(() => {
      const d = document.querySelector('[role="dialog"]');
      if (!d || !d.querySelector('input[name="share-general-access"]')) return false;
      const loading = [...d.querySelectorAll('[role="status"]')].some((s) => s.textContent === ${q(en['share.loading'])} || s.textContent === ${q(he['share.loading'])});
      return !loading;
    })()`,
    10000,
  );
  const stillRestricted = `document.querySelector('[role="dialog"] input[name="share-general-access"][value="restricted"]')?.checked === true`;
  let attempt = 0;
  for (;;) {
    attempt += 1;
    // The field is measured again right before every click, once its rectangle has stopped moving.
    const at = await waitStillPeople(page);
    await click(page, at);
    try {
      await page.waitFor(`[...document.querySelectorAll('[role="listbox"] [role="option"]')].some((o) => o.textContent.includes(${q(name)}))`, 4000);
      break;
    } catch (e) {
      // A click that moved General access off restricted landed outside the field; clicking again would only move it again.
      if (!(await page.evaluate(stillRestricted))) {
        const dbg = await page.evaluate(`(() => { const c = document.getElementById('share-who').closest('.combo'); return JSON.stringify({ focused: document.activeElement && document.activeElement.id, expanded: document.getElementById('share-who').getAttribute('aria-expanded'), status: c.querySelector('[role="status"]').textContent, listed: [...document.querySelectorAll('[role="listbox"] [role="option"]')].map((o) => o.textContent.trim()) }); })()`);
        throw new Error(`focusPeopleField: the click landed outside the people field, so General access is no longer restricted. DEBUG ${dbg}`);
      }
      if (attempt < 3) {
        // A miss is logged with its attempt number, so a run that needed a second click shows it.
        console.log(`focusPeopleField: attempt ${attempt} did not show ${name} in the list; trying again`);
        await page.evaluate(`(() => { document.getElementById('share-who').blur(); return true; })()`);
        await sleep(300);
        continue;
      }
      const dbg = await page.evaluate(`(() => { const c = document.getElementById('share-who').closest('.combo'); return JSON.stringify({ focused: document.activeElement && document.activeElement.id, expanded: document.getElementById('share-who').getAttribute('aria-expanded'), status: c.querySelector('[role="status"]').textContent, listed: [...document.querySelectorAll('[role="listbox"] [role="option"]')].map((o) => o.textContent.trim()) }); })()`);
      throw new Error(`${e.message} DEBUG ${dbg}`);
    }
  }
  const counted = await page.evaluate(`document.getElementById('share-who').closest('.combo').querySelector('[role="status"]').textContent`);
  return { counted, attempt };
}

// peopleFocusChecks is slice fu10's check (decision 96): focusing the share dialog's people field, before anything is typed,
// opens the account's people. It runs in four looks, English light and dark, Hebrew light and dark, and then English at a
// 390 pixel phone, and in each one the list has the member, the status line counts the list, and a click picks the member.
// Run on its own with SELFCLOUD_E2E_ONLY=fu10, it signs the owner in and makes its own folder.
export async function peopleFocusChecks(page, ctx) {
  const { base, accountID, accountKey, MEMBER, OWNER, check } = ctx;
  const fld = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'Focus Room' })).id;
  await api(base, 'POST', `/api/v1/folders/${fld}/directories`, accountKey, { path: '/Shared', parents: false });
  await signOut(page);
  await signIn(page, OWNER);
  const enCounted = /people and groups|person or group/;
  const heCounted = /אנשים וקבוצות|אנשים או קבוצות|איש קשר או קבוצה/;
  const looks = [
    { name: 'English light', lang: 'English', share: 'Share', theme: 'light', phone: false, counted: enCounted },
    { name: 'English dark', lang: 'English', share: 'Share', theme: 'dark', phone: false, counted: enCounted },
    { name: 'Hebrew light', lang: 'עברית', share: he['share.open'], theme: 'light', phone: false, counted: heCounted },
    { name: 'Hebrew dark', lang: 'עברית', share: he['share.open'], theme: 'dark', phone: false, counted: heCounted },
    { name: 'English phone', lang: 'English', share: 'Share', theme: 'light', phone: true, counted: enCounted },
  ];
  for (const look of looks) {
    await setLanguage(page, look.lang);
    await setThemeByPlace(page, look.theme === 'light' ? 1 : 2, look.theme);
    await setPhone(page, look.phone);
    await navigateTo(page, `/files/${fld}`);
    await rowAppears(page, 'Shared');
    await selectRow(page, 'Shared');
    await clickButton(page, look.share);
    await page.waitFor(`!!document.getElementById('share-who')`);
    const { counted, attempt } = await focusPeopleField(page, MEMBER.name);
    check(look.counted.test(counted), `${look.name}: focusing the people field lists the account's people, and the status line counts them (${counted}; attempt ${attempt})`);
    const inside = await page.evaluate(
      `(() => { const r = document.querySelector('[role="listbox"]').getBoundingClientRect(); const d = document.querySelector('[role="dialog"]').getBoundingClientRect(); return r.left >= d.left - 1 && r.right <= d.right + 1 && r.left >= 0 && r.right <= window.innerWidth; })()`,
    );
    check(inside, `${look.name}: the list stays inside the dialog and on the screen`);
    // Enter right after the focus picks nothing (decision 96). A real Enter key goes to the field: the list has
    // no option active, so nobody is added, the field is still empty and shows its usual hint, and the dialog stays open.
    // An Enter in a form's text field may submit the form; the check reads the field's error state too.
    await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13 });
    await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13 });
    await sleep(400);
    const afterEnter = await page.evaluate(
      `(() => ({ value: document.getElementById('share-who').value, hint: document.getElementById('share-who-hint').textContent, invalid: document.getElementById('share-who').getAttribute('aria-invalid'), dialog: !!document.querySelector('[role="dialog"]') }))()`,
    );
    const usualHint = (look.lang === 'עברית' ? he : en)['share.whoHint'];
    check(
      afterEnter.value === '' && afterEnter.hint === usualHint && afterEnter.invalid !== 'true' && afterEnter.dialog,
      `${look.name}: Enter right after the focus adds no one, and the field keeps its usual message (${JSON.stringify(afterEnter)})`,
    );
    await page.evaluate(
      `(() => { const o = [...document.querySelectorAll('[role="listbox"] [role="option"]')].find((x) => x.textContent.includes(${q(MEMBER.name)})); o.click(); return true; })()`,
    );
    await page.waitFor(`document.getElementById('share-who').value === ${q(MEMBER.name)}`);
    check(true, `${look.name}: a click on the list picks the member`);
    await press(page, '[role="dialog"]', 'Escape');
    await page.waitFor(`!document.querySelector('[role="dialog"]')`);
    if (look.phone) await setPhone(page, false);
  }
}

async function clickMenuItem(page, text) {
  await page.waitFor(
    `(() => { const b = [...document.querySelectorAll('[role="menu"] [role="menuitem"]')].find((x) => x.textContent.trim().startsWith(${q(text)})); if (!b) return false; b.click(); return true; })()`,
    10000,
  );
}

// sharingChecks is the sharing interface's end-to-end path. The owner shares a folder with the member as a viewer
// (no mail), the member sees it and cannot change it, the owner raises it to editor, shares with a group with an
// expiry, the member uploads, asks for access to a file it cannot reach, the owner approves, the owner hides and
// shows a space, and turns on no-download for viewers.
export async function sharingChecks(page, ctx) {
  const { base, dir, accountID, accountKey, memberID, teamB, check, OWNER, MEMBER, waitForMail } = ctx;
  // Set up through the API: a team space the member has no role on, with a directory and a file in it.
  const teamC = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'Board Room' })).id;
  await api(base, 'POST', `/api/v1/folders/${teamC}/directories`, accountKey, { path: '/Board', parents: false });
  await rawPut(base, `/api/v1/folders/${teamC}/files?path=%2FBoard%2Fnotes.txt`, accountKey, 'notes for the board\n');
  await rawPut(base, `/api/v1/folders/${teamC}/files?path=%2Fsecret.txt`, accountKey, 'the file behind the request\n');
  const group = await api(base, 'POST', `/api/v1/accounts/${accountID}/groups`, accountKey, { name: 'Field team' });
  await api(base, 'PUT', `/api/v1/accounts/${accountID}/groups/${group.id}/members/${memberID}`, accountKey);
  const top = (await api(base, 'GET', `/api/v1/folders/${teamC}/children?path=%2F`, accountKey)).items;
  const board = top.find((n) => n.name === 'Board').id;
  const secret = top.find((n) => n.name === 'secret.txt').id;
  const mate = join(dir, 'mate.txt');
  await writeFile(mate, 'from the member, into the shared folder\n');

  // The owner shares Board with the member as a viewer, without mail. Then hides Team B and shows it again.
  await signOut(page);
  await signIn(page, OWNER);
  await clickOn(page, `${TREE} a[href="/files/${teamC}"]`);
  await page.waitFor(`location.pathname === '/files/${teamC}'`);
  await rowAppears(page, 'Board');
  await selectRow(page, 'Board');
  await clickButton(page, 'Share');
  await page.waitFor(`!!document.getElementById('share-who')`);
  // Decision 96: focusing the field, before anything is typed, lists the account's people, and the status line counts them.
  // A real click focuses the field (focusPeopleField); the status line is read after the list shows the member.
  const { counted, attempt } = await focusPeopleField(page, MEMBER.name);
  check(/^\d+ (people and groups|person or group)$/.test(counted), `focusing the people field lists the account's people before anything is typed (status: "${counted}"; attempt ${attempt})`);
  // The owner picks the member from the list with a click, and the member is added.
  await page.evaluate(
    `(() => { const o = [...document.querySelectorAll('[role="listbox"] [role="option"]')].find((x) => x.textContent.includes(${q(MEMBER.name)})); o.click(); return true; })()`,
  );
  await page.waitFor(`document.getElementById('share-who').value === ${q(MEMBER.name)}`);
  await setCheckbox(page, 'share-notify', false);
  await peopleSubmit(page);
  await page.waitFor(`${liveText}.includes('Shared with ${MEMBER.name} as Can view')`);
  check(true, 'the owner shares a folder with the member as a viewer, without mail');
  check(
    await page.evaluate(`document.querySelector('[role="dialog"]').textContent.includes('${MEMBER.name}')`),
    'the share dialog lists the member among the people with access',
  );
  await press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  await clickOn(page, `button[aria-label="Options for Team B"]`);
  await clickMenuItem(page, 'Hide');
  await page.waitFor(`!document.querySelector('${TREE} a[href="/files/${teamB}"]')`);
  check(true, 'the owner hides a team space, and it leaves the list');
  await clickOn(page, `.side-check input[type="checkbox"]`);
  await page.waitFor(`!!document.querySelector('${TREE} a[href="/files/${teamB}"]')`);
  await clickOn(page, `button[aria-label="Options for Team B"]`);
  await clickMenuItem(page, 'Show');
  // Wait until both lists have been read again: the space is in the tree once, and its row no longer carries the Hidden tag.
  await page.waitFor(
    `document.querySelectorAll('${TREE} a[href="/files/${teamB}"]').length === 1 && ![...document.querySelectorAll('${TREE} .tree-row')].some((r) => !!r.querySelector('a[href="/files/${teamB}"]') && r.textContent.includes('Hidden'))`,
  );
  check(true, 'the hidden space comes back when shown again');

  // The member sees the folder in Shared with me, opens it, and has no upload, rename or delete.
  await signOut(page);
  await signIn(page, MEMBER);
  await clickMenuItem(page, 'Shared with me');
  await page.waitFor(`location.pathname === '/shared' && !!document.querySelector('.shared a[href="/n/${board}"]')`);
  check(true, 'Shared with me lists the folder the owner shared');
  await clickOn(page, `.shared a[href="/n/${board}"]`);
  await page.waitFor(`location.pathname === '/files/${teamC}/${board}'`);
  await rowAppears(page, 'notes.txt');
  const viewerButtons = await buttonTexts(page);
  check(!viewerButtons.includes('Upload files') && !viewerButtons.includes('New folder'), 'a viewer of the shared folder cannot upload or make folders');
  // The sidebar's New is on the folder shown: a viewer's entries are disabled, with the reason (decision 66).
  await page.evaluate(`document.querySelector('.shell-sider [data-testid="new-button"]').click()`);
  await page.waitFor(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].some((i) => i.textContent.trim() === 'New folder')`, 10000);
  const viewerMenu = await page.evaluate(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].map((i) => ({ text: i.textContent.trim(), disabled: i.classList.contains('ant-dropdown-menu-item-disabled') }))`);
  check(['New folder', 'Upload files', 'Upload folder'].every((t) => viewerMenu.some((i) => i.text === t && i.disabled)), `a viewer's New entries are disabled in the sidebar (${JSON.stringify(viewerMenu)})`);
  await press(page, 'body', 'Escape');
  await page.waitFor(`![...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden)')].length`, 10000);
  await selectRow(page, 'notes.txt');
  // The selection panel's icons have no text: their names (aria-label) are read too.
  const viewerSel = [...(await buttonTexts(page)), ...(await page.evaluate(`[...document.querySelectorAll('[data-testid="selection-panel"] button')].map((b) => b.getAttribute('aria-label'))`))];
  check(!viewerSel.includes('Rename') && !viewerSel.includes('Delete'), 'a viewer cannot rename or delete');
  check(viewerSel.includes('Download'), 'a viewer can download, since the space does not forbid it');

  // The owner raises the member to editor, shares with the group with an expiry, and sees both in the list.
  await signOut(page);
  await signIn(page, OWNER);
  await navigateTo(page, `/files/${teamC}`);
  await rowAppears(page, 'Board');
  await selectRow(page, 'Board');
  await clickButton(page, 'Share');
  await page.waitFor(`!!document.getElementById('share-who')`);
  // The people list loads after the dialog opens; the member's row is there once the list is.
  await page.waitFor(`!!document.getElementById(${q(`role-${memberID}`)})`);
  await pickAntdOption(page, `role-${memberID}`, 'Can edit');
  await page.waitFor(`${liveText}.includes('is now Can edit')`);
  check(true, 'the owner raises the member to editor');
  await pickPerson(page, 'field', 'Field team');
  const thirty = new Date(Date.now() + 30 * 24 * 3600 * 1000).toISOString().slice(0, 10);
  await setInput(page, 'share-expires', thirty);
  await peopleSubmit(page);
  await page.waitFor(`${liveText}.includes('Shared with Field team as Can view')`);
  check(!(await page.evaluate(liveText)).includes('No email was sent'), 'the reply carries no "no email" notice, since the mail went out');
  check((await waitForMail(ctx.sink, MEMBER.email)).to.includes(MEMBER.email), 'the member the group reaches is mailed the share');
  check(await page.evaluate(`document.querySelector('[role="dialog"]').textContent.includes('Ends ')`), 'the group share shows its expiry');
  await press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  // The member, now an editor, uploads into the folder.
  await signOut(page);
  await signIn(page, MEMBER);
  await navigateTo(page, `/files/${teamC}/${board}`);
  await rowAppears(page, 'notes.txt');
  await page.setFiles('[role="toolbar"] input[type="file"]:not([webkitdirectory])', [mate]);
  await rowAppears(page, 'mate.txt');
  check(true, 'an editor of the shared folder can upload into it');

  // The member asks for editor on a file it cannot reach. The form says the same whatever the answer is.
  await navigateTo(page, `/n/${secret}`);
  await page.waitFor(`!!document.getElementById('request-role')`);
  await pickAntdOption(page, 'request-role', 'Can edit');
  await setInput(page, 'request-message', 'Need it for the review');
  await clickButton(page, 'Ask for access');
  await page.waitFor(`!!document.querySelector('.request .sent')`);
  check(
    (await page.evaluate(`document.querySelector('.request .sent').textContent`)) === 'If this item exists, the people who manage it have been asked.',
    'the request form gives the one confirmation',
  );

  // The owner sees the request with its count, approves it, and the member gets the file.
  await signOut(page);
  await signIn(page, OWNER);
  await page.waitFor(`/1 waiting/.test([...document.querySelectorAll('[role="menuitem"]')].find((x) => x.textContent.includes('Requests'))?.textContent ?? '')`);
  check(true, 'the owner sees a requests entry with a count');
  await clickMenuItem(page, 'Requests');
  await page.waitFor(`location.pathname === '/requests' && !!document.querySelector('.requests .request')`);
  await page.waitFor(`document.querySelector('.requests .request').textContent.includes('secret.txt')`);
  await page.waitFor(
    `(() => { const b = [...(document.querySelector('.requests .request')?.querySelectorAll('button') ?? [])].find((x) => x.textContent.trim() === 'Approve' || (x.getAttribute('aria-label') ?? '').startsWith('Approve:')); if (!b) return false; b.click(); return true; })()`,
  );
  await page.waitFor(`document.querySelector('.ant-alert-success') && !!document.querySelector('.ant-alert-success').textContent.includes('Access given to ${MEMBER.name}')`);
  check(true, 'the owner approves the request');

  // The owner turns on no-download for viewers in Team B.
  await clickOn(page, `button[aria-label="Options for Team B"]`);
  await clickMenuItem(page, 'Space settings');
  await page.waitFor(`!!document.querySelector('[role="dialog"]')`);
  await page.waitFor(
    `(() => { const l = [...document.querySelectorAll('[role="dialog"] label.switch')].find((x) => x.textContent.includes("can't download")); if (!l) return false; l.querySelector('button, input').click(); return true; })()`,
  );
  await page.waitFor(`(document.querySelector('[role="dialog"] .live')?.textContent ?? '') === 'Saved.'`);
  check(true, 'the owner turns on no-download for viewers in Team B');
  await press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  // The member opens the file it was approved for; and as a viewer of Team B it sees no Download button.
  await signOut(page);
  await signIn(page, MEMBER);
  await navigateTo(page, `/n/${secret}`);
  await page.waitFor(`document.body.textContent.includes('secret.txt')`, 15000);
  check(true, 'the approved member opens the file it asked for');
  await clickOn(page, `${TREE} a[href="/files/${teamB}"]`);
  await page.waitFor(`location.pathname === '/files/${teamB}'`);
  await rowAppears(page, 'readme.txt');
  await selectRow(page, 'readme.txt');
  const noDl = await buttonTexts(page);
  check(!noDl.includes('Download'), 'a viewer of a no-download space sees no Download button');
}

// publicLinksDeleteAllChecks: the account has an anyone link and a drop link (made here through the account key) and an
// organisation link. The owner opens Manage > Links, sees the two public links and the organisation link, and uses
// "Delete all public links", confirming the count. Both public links stop working; the organisation link stays.
export async function publicLinksDeleteAllChecks(page, ctx) {
  const { base, accountID, accountKey, fld, check, OWNER } = ctx;
  const dir = async (path) => (await api(base, 'POST', `/api/v1/folders/${fld}/directories`, accountKey, { path, parents: false })).id;
  const [pub, inbox, shared] = [await dir('/Public'), await dir('/Inbox'), await dir('/Shared')];
  const anyone = await api(base, 'POST', `/api/v1/nodes/${pub}/share-links`, accountKey, { audience: 'anyone', role: 'viewer' });
  const drop = await api(base, 'POST', `/api/v1/nodes/${inbox}/share-links`, accountKey, { upload_only: true, title: 'Send us files' });
  const org = await api(base, 'POST', `/api/v1/nodes/${shared}/share-links`, accountKey, { audience: 'organisation', role: 'viewer' });
  const secrets = [anyone.url, drop.url].map((u) => u.slice(u.indexOf('#') + 1));

  await signOut(page);
  await signIn(page, OWNER);
  await navigateTo(page, '/manage/links');
  await page.waitFor(`location.pathname === '/manage/links' && [...document.querySelectorAll('.item-name')].some((x) => x.textContent === 'Shared')`, 15000);
  const names = await page.evaluate(`[...document.querySelectorAll('.item-name')].map((x) => x.textContent)`);
  check(['Public', 'Inbox', 'Shared'].every((n) => names.includes(n)), `Manage > Links lists the anyone link, the drop link and the organisation link (${names.join(', ')})`);

  const before = (await api(base, 'GET', `/api/v1/accounts/${accountID}/sharing-policy`, accountKey)).anyone_links;
  await clickButton(page, 'Delete all public links');
  await page.waitFor(`!!document.querySelector('[role="dialog"]') && !!document.querySelector('[role="dialog"]').textContent.includes(${q(`${before} link`)})`, 15000);
  check(true, `the confirmation names the count of public links (${before})`);
  await page.evaluate(`(() => { const b = [...document.querySelectorAll('[role="dialog"] button')].find((x) => x.textContent.trim() === 'Delete all public links' && !x.disabled); if (!b) return false; b.click(); return true; })()`);
  await page.waitFor(`[...document.querySelectorAll('[role="status"], .ant-message-notice')].some((x) => x.textContent.includes(${q(`${before} public link`)}))`, 15000);
  await page.waitFor(
    `![...document.querySelectorAll('.item-name')].some((x) => x.textContent === 'Public' || x.textContent === 'Inbox') && [...document.querySelectorAll('.item-name')].some((x) => x.textContent === 'Shared')`,
    15000,
  );
  check(true, 'Delete all public links removes the public links from the list and keeps the organisation link');

  const policy = await api(base, 'GET', `/api/v1/accounts/${accountID}/sharing-policy`, accountKey);
  check(policy.anyone_links === 0, `no link that anyone can open is left (anyone_links ${policy.anyone_links})`);
  for (const secret of secrets) {
    const res = await fetch(base + '/api/v1/share/open', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ secret }) });
    check(res.status === 404, `a deleted public link no longer opens (${res.status})`);
  }
  const kept = await api(base, 'GET', `/api/v1/nodes/${shared}/share-links`, accountKey);
  check(kept.items.some((l) => l.audience === 'organisation' && l.id === org.link.id), 'the organisation link stays');
}
