// SPDX-License-Identifier: AGPL-3.0-or-later
// The share dialog's end-to-end checks for the React interface: sharingChecks and publicLinksDeleteAllChecks.
// Each check description is the one web/e2e/run.mjs gives, word for word, so the two PASS lists match for this part.
// The helpers take the page and the run's own values through ctx, so this file does not depend on run.mjs.
// ctx: { base, dir, accountID, accountKey, memberID, teamB, sink, check, OWNER, MEMBER, waitForMail, fld }
// page.base must be the server's address, as the webui run sets it for signIn.

import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const q = (v) => JSON.stringify(v);
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
  await page.waitFor(
    `(() => { const b = [...document.querySelectorAll('button')].find((x) => x.textContent.trim() === ${q(text)} && !x.disabled); if (!b) return false; b.click(); return true; })()`,
    10000,
  );
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
  return page.evaluate(`[...document.querySelectorAll('button')].map((b) => b.textContent.trim())`);
}

async function navigateTo(page, path) {
  await page.evaluate(
    `(() => { const a = document.createElement('a'); a.href = ${q(path)}; document.body.append(a); a.click(); a.remove(); return true; })()`,
  );
}

// signOut and signIn use the account menu the React shell draws (data-testid), as the webui e2e suite does.
async function signOut(page) {
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

async function dialogSubmit(page) {
  await clickOn(page, `[role="dialog"] form button[type="submit"]`);
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
  await clickOn(page, `.teams a[href="/files/${teamC}"]`);
  await page.waitFor(`location.pathname === '/files/${teamC}'`);
  await rowAppears(page, 'Board');
  await selectRow(page, 'Board');
  await clickButton(page, 'Share');
  await page.waitFor(`!!document.getElementById('share-who')`);
  await pickPerson(page, 'mateo', MEMBER.name);
  await setCheckbox(page, 'share-notify', false);
  await dialogSubmit(page);
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
  await page.waitFor(`!document.querySelector('.teams a[href="/files/${teamB}"]')`);
  check(true, 'the owner hides a team space, and it leaves the list');
  await clickOn(page, `.teams + label input[type="checkbox"]`);
  await page.waitFor(`!!document.querySelector('.teams a[href="/files/${teamB}"]')`);
  await clickOn(page, `button[aria-label="Options for Team B"]`);
  await clickMenuItem(page, 'Show');
  // Wait until both lists have been read again: the space is listed once, and no list still marks it hidden.
  await page.waitFor(`document.querySelectorAll('.teams a[href="/files/${teamB}"]').length === 1 && !document.querySelector('.teams .count')`);
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
  await selectRow(page, 'notes.txt');
  const viewerSel = await buttonTexts(page);
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
  await dialogSubmit(page);
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
    `(() => { const b = [...(document.querySelector('.requests .request')?.querySelectorAll('button') ?? [])].find((x) => x.textContent.trim() === 'Approve'); if (!b) return false; b.click(); return true; })()`,
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
  await clickOn(page, `.teams a[href="/files/${teamB}"]`);
  await page.waitFor(`location.pathname === '/files/${teamB}'`);
  await rowAppears(page, 'readme.txt');
  await selectRow(page, 'readme.txt');
  const noDl = await buttonTexts(page);
  check(!noDl.includes('Download'), 'a viewer of a no-download space sees no Download button');
}

// publicLinksDeleteAllChecks: the account has an anyone link and a drop link (made here through the account key) and an
// organisation link. The owner opens Manage > Links, sees the two public links and the organisation link, and uses
// "Delete all public links", confirming the count. Both public links stop working; the organisation link stays.
// Not run in this build: it opens Manage > Links, which is part v4. Kept so the check can move there unchanged.
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
  await page.waitFor(`[...document.querySelectorAll('[role="status"]')].some((x) => x.textContent.includes(${q(`${before} public link`)}))`, 15000);
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
