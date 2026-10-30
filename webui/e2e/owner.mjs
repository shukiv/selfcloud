// SPDX-License-Identifier: AGPL-3.0-or-later
// The owner, member and installation admin checks of the React build (parts v4). Their check texts are the Svelte
// build's (web/e2e/run.mjs), word for word, so the two PASS lists agree. The helpers of run.mjs come in as h, with the
// mail sink and waitForMail. Selectors follow the React DOM: antd alerts (.ant-alert), the Modal of Recent's Clear
// confirmation (.ant-modal), and the ids the pages keep from Svelte.

const q = (v) => JSON.stringify(v);
const rowSel = (name) => `[role="row"][aria-label="${name.replace(/["\\]/g, '\\$&')}"]`;

// dialogSubmit presses the submit button of the open dialog's form.
async function dialogSubmit(h, page) {
  await h.clickOn(page, `[role="dialog"] form button[type="submit"]`);
}

// pickPerson types a query into the people combobox and chooses the option with the name.
async function pickPerson(h, page, query, name, inputId) {
  await h.setInput(page, inputId, query);
  await page.waitFor(`[...document.querySelectorAll('[role="listbox"] [role="option"]')].some((o) => o.textContent.includes(${q(name)}))`, 10000);
  await page.evaluate(`(() => { const o = [...document.querySelectorAll('[role="listbox"] [role="option"]')].find((x) => x.textContent.includes(${q(name)})); o.click(); return true; })()`);
  await page.waitFor(`document.getElementById(${q(inputId)}).value === ${q(name)}`);
}

// mailBody decodes the quoted-printable body of a mail the sink kept.
function mailBody(m) {
  return m.data.replace(/=\r\n/g, '').replace(/=([0-9A-F]{2})/g, (_, hex) => String.fromCharCode(parseInt(hex, 16)));
}

// clickInRow clicks the button of the first row (list item or table row) that holds the text and the button's label. A
// button carries a visually hidden suffix (the name it acts on), so the label is matched as a prefix.
async function clickInRow(page, text, label, timeout = 10000) {
  await page.waitFor(
    `(() => { const row = [...document.querySelectorAll('li, tr, [role="row"]')].find((r) => r.textContent.includes(${q(text)}) && [...r.querySelectorAll('button')].some((b) => b.textContent.trim().startsWith(${q(label)}))); if (!row) return false; [...row.querySelectorAll('button')].find((b) => b.textContent.trim().startsWith(${q(label)})).click(); return true; })()`,
    timeout,
  );
}

// asks counts the sign-in prompts the switch checks saw: each link for another user is asked about once.
let asks = 0;
export function switchPrompts() {
  return asks;
}

// Home, Starred and Recent, as the member: a star, an open, a clear; the owner then sees what was shared with it.
export async function homeChecks(page, h, { base, accountKey, memberID, ownerID, teamA }) {
  const note = 'home-note.txt';
  const shared = 'home-shared.txt';
  await h.rawPut(base, `/api/v1/folders/${teamA}/files?path=%2F${note}`, accountKey, 'a note for home\n');
  const list = await h.api(base, 'GET', '/api/v1/folders', accountKey);
  const mine = (list.items ?? []).find((f) => f.kind === 'personal' && f.owner_user_id === memberID);
  if (!mine) throw new Error('the member has no personal folder in the listing');
  await h.rawPut(base, `/api/v1/folders/${mine.id}/files?path=%2F${shared}`, accountKey, 'shared with the owner\n');
  const sharedID = await rootChild(h, base, mine.id, accountKey, shared);
  await h.api(base, 'PUT', `/api/v1/nodes/${sharedID}/permissions/${ownerID}`, accountKey, { role: 'viewer', notify: false });
  const noteID = await rootChild(h, base, teamA, accountKey, note);

  // Star the note from its listing: the star button is pressed. The note came in through the API after the page
  // had listed Team A, so the page is loaded again, as a person reloads it.
  await page.goto(base + `/files/${teamA}`);
  await h.rowAppears(page, note);
  await page.waitFor(`(() => { const b = document.querySelector(${q(rowSel(note) + ' button[aria-pressed]')}); if (!b) return false; b.click(); return true; })()`);
  await page.waitFor(`document.querySelector(${q(rowSel(note) + ' button[aria-pressed]')}).getAttribute('aria-pressed') === 'true'`);
  h.check(true, 'a member stars a file from the listing, and the star button is pressed');

  // Starred lists it, and so does Home.
  await h.clickNav(page, 'Starred');
  await page.waitFor(`location.pathname === '/starred' && !!document.querySelector('.items a[href="/n/${noteID}"]')`);
  h.check(true, 'the starred file is listed in Starred');
  await h.clickNav(page, 'Home');
  await page.waitFor(`location.pathname === '/' && !!document.querySelector('[aria-labelledby="home-starred"] a[href="/n/${noteID}"]')`, 15000);
  h.check(true, 'the starred file shows on Home');

  // Opening the file (through its address) records it in Recent. The server writes views in batches every two seconds.
  await h.navigateTo(page, `/n/${noteID}`);
  await page.waitFor(`location.pathname.startsWith('/files/') || location.pathname === '/n/${noteID}'`, 15000);
  await new Promise((resolve) => setTimeout(resolve, 2500));
  await h.clickNav(page, 'Recent');
  await page.waitFor(`location.pathname === '/recent' && !!document.querySelector('.items a[href="/n/${noteID}"]')`, 15000);
  h.check(true, 'the opened file is listed in Recent');

  // Clear Recent after a confirmation: the list empties, and the file itself is still there.
  await h.clickButton(page, 'Clear all');
  await page.waitFor(`!!document.querySelector('.ant-modal .ant-modal-footer .ant-btn-dangerous')`);
  await h.clickOn(page, `.ant-modal .ant-modal-footer .ant-btn-dangerous`);
  await page.waitFor(`!document.querySelector('.items a[href="/n/${noteID}"]') && !!document.querySelector('.empty')`, 15000);
  h.check(true, 'Recent is cleared after the confirmation, and the file is kept');
  await h.navigateTo(page, `/files/${teamA}`);
  await h.rowAppears(page, note);
  h.check(true, 'the note is still in its folder after Recent is cleared');
  return { sharedID, shared };
}

// rootChild finds the item of a folder's root with this name, as the account key sees it, and returns its ID.
async function rootChild(h, base, folder, key, name) {
  const page = await h.api(base, 'GET', `/api/v1/folders/${folder}/children?path=%2F`, key);
  const found = (page.items ?? []).find((n) => n.name === name);
  if (!found) throw new Error(`${name} is not in the folder ${folder}`);
  return found.id;
}

// homeOwnerChecks: the owner's Home shows the item the member shared with it, in Recently shared.
export async function homeOwnerChecks(page, h, { sharedID }) {
  await h.navigateTo(page, '/');
  await page.waitFor(`location.pathname === '/' && !!document.querySelector('[aria-labelledby="home-shared"] a[href="/n/${sharedID}"]')`, 15000);
  h.check(true, 'the owner sees a recently shared item on Home');
}

// ownerChecks is the owner's management pages, end to end. The owner invites a person by email; the run takes the
// link from the mail the sink received and verifies the account with it. The owner then makes a group with that
// person in it, and a team space.
export async function ownerChecks(page, h, { base, sink, waitForMail, INVITEE }) {
  await h.navigateTo(page, '/manage/people');
  await page.waitFor(`location.pathname === '/manage/people' && !!document.querySelector('main')`);
  await h.clickButton(page, 'Invite person');
  await page.waitFor(`!!document.getElementById('invite-email')`);
  await h.setInput(page, 'invite-email', INVITEE.email);
  await h.setInput(page, 'invite-name', INVITEE.name);
  await dialogSubmit(h, page);
  const mail = await waitForMail(sink, INVITEE.email);
  const token = /#token=([A-Za-z0-9_-]+)/.exec(mailBody(mail))?.[1];
  if (!token) throw new Error('the invitation mail has no link');
  await h.api(base, 'POST', '/api/v1/auth/verify', null, { token, password: INVITEE.password });
  h.check(true, 'the owner invites a person by email, and the link in the mail verifies the account');

  await h.navigateTo(page, '/manage/groups');
  await h.clickButton(page, 'New group');
  await page.waitFor(`!!document.getElementById('group-new-name')`);
  await h.setInput(page, 'group-new-name', 'Ops crew');
  await dialogSubmit(h, page);
  await page.waitFor(`document.querySelector('main').textContent.includes('Ops crew')`);
  await clickInRow(page, 'Ops crew', 'Members');
  await page.waitFor(`!!document.getElementById('group-add')`);
  await pickPerson(h, page, 'dana', INVITEE.name, 'group-add');
  await h.clickButton(page, 'Add to group');
  await page.waitFor(`document.querySelector('main').textContent.includes(${q(INVITEE.name)})`);
  h.check(true, 'the owner creates a group and adds the invited person to it');

  await h.navigateTo(page, '/manage/spaces');
  await h.clickButton(page, 'New team space');
  await page.waitFor(`!!document.getElementById('space-new-name')`);
  await h.setInput(page, 'space-new-name', 'Lab');
  await dialogSubmit(h, page);
  await page.waitFor(`document.querySelector('main').textContent.includes('Lab')`);
  h.check(true, 'the owner creates a team space');
}

// p1Setup gives the member a 1 MB file in My files, through the account key, before the owner sets its limit.
export async function p1Setup(h, base, accountKey, memberID, randomBytes) {
  const list = await h.api(base, 'GET', '/api/v1/folders', accountKey);
  const mine = (list.items ?? []).find((f) => f.kind === 'personal' && f.owner_user_id === memberID);
  if (!mine) throw new Error('the member has no personal folder in the listing');
  await h.rawPut(base, `/api/v1/folders/${mine.id}/files?path=%2Fp1-ledger.bin`, accountKey, randomBytes(1_000_000), 'application/octet-stream');
}

// p1OwnerChecks: the owner sets the member's own limit in the person dialog, to 0.01 GB (10 MB). Then Manage > Storage
// shows the member's total, and no file name from its My files.
export async function p1OwnerChecks(page, h, { MEMBER }) {
  await h.navigateTo(page, '/manage/people');
  await page.waitFor(`location.pathname === '/manage/people' && document.querySelector('main').textContent.includes(${q(MEMBER.name)})`);
  await clickInRow(page, MEMBER.name, 'Details');
  await page.waitFor(`!!document.getElementById('person-limit')`);
  await h.clickButton(page, 'Change limit');
  await page.waitFor(`!!document.querySelector('[role="dialog"] input[aria-label="Amount"]')`);
  await page.setInput('[role="dialog"] input[aria-label="Amount"]', '0.01');
  await h.clickButton(page, 'Save limit');
  await page.waitFor(`(document.getElementById('person-limit')?.textContent ?? '').includes('10 MB')`);
  h.check(true, "the owner sets the member's limit to 10 MB in the person dialog");
  await h.press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  await h.navigateTo(page, '/manage/storage');
  await page.waitFor(
    `document.querySelector('h1')?.textContent.trim() === 'Storage' && (document.getElementById('people-storage')?.textContent ?? '').includes(${q(MEMBER.name)})`,
    15000,
  );
  const row = await page.evaluate(`(() => { const t = document.getElementById('people-storage'); const r = [...t.querySelectorAll('tr, li')].find((x) => x.textContent.includes(${q(MEMBER.name)})); return r ? r.textContent : ''; })()`);
  h.check(row.includes('1 MB') && row.includes('10 MB'), `Manage > Storage shows the member's total and limit (${row.replace(/\s+/g, ' ').trim()})`);
  const content = await page.evaluate(`document.querySelector('main').textContent`);
  h.check(!content.includes('p1-ledger.bin'), "Manage > Storage shows no file name from the member's My files");
}

// p1MemberChecks: the member, over its limit, uploads a 9.5 MB file to My files. The server refuses the creation,
// and the interface says so. Then the member's /storage page shows the meter and the largest file it holds.
export async function p1MemberChecks(page, h, { dir, writeFile, randomBytes, join }) {
  const over = join(dir, 'p1-over.bin');
  await writeFile(over, randomBytes(9_500_000));
  await h.clickNav(page, 'My files');
  await page.waitFor(`/^\\/files\\/fld_/.test(location.pathname) && !!document.querySelector('[role="toolbar"] input[type="file"]:not([webkitdirectory])')`);
  await page.setFiles('[role="toolbar"] input[type="file"]:not([webkitdirectory])', [over]);
  await page.waitFor(`[...document.querySelectorAll('[role="alert"], .ant-alert-error')].some((e) => e.textContent.toLowerCase().includes('storage limit'))`, 20000);
  const alert = await page.evaluate(`[...document.querySelectorAll('[role="alert"], .ant-alert-error')].map((e) => e.textContent.trim()).find((t) => t.toLowerCase().includes('storage limit'))`);
  h.check(!!alert, `the member's upload over its limit is refused, with the message (${alert})`);
  h.check(!(await page.evaluate(`!!document.querySelector(${q(rowSel('p1-over.bin'))})`)), 'the refused file is not listed in My files');

  await h.clickNav(page, 'Storage');
  await page.waitFor(`location.pathname === '/storage' && !!document.querySelector('main [role="meter"]')`);
  const max = await page.evaluate(`document.querySelector('main [role="meter"]').getAttribute('aria-valuemax')`);
  h.check(max === '10000000', `the storage page shows the meter against the 10 MB limit (aria-valuemax ${max})`);
  await page.waitFor(`[...document.querySelectorAll('.storage-table th')].some((th) => th.textContent.includes('p1-ledger.bin'))`, 15000);
  h.check(true, "the storage page lists the member's largest file");
}

// adminChecks is the installation admin's tenant pages, end to end: a new tenant whose first owner gets a link shown
// once, and an API key for it shown once. The key then works for the API, and is gone from the page once its dialog closes.
export async function adminChecks(page, h, { base, adminKey, TENANT }) {
  await page.waitFor(`location.pathname === '/admin/tenants' && [...document.querySelectorAll('span')].some((s) => s.textContent.trim() === 'Admin')`);
  h.check(true, 'an admin lands on the tenants list, under the Admin badge');

  await h.clickButton(page, 'New tenant');
  await page.waitFor(`!!document.getElementById('new-tenant-name')`);
  await h.setInput(page, 'new-tenant-name', TENANT.name);
  await h.setInput(page, 'new-tenant-owner-email', TENANT.owner.email);
  await h.setInput(page, 'new-tenant-owner-name', TENANT.owner.name);
  await h.setInput(page, 'new-tenant-limit', '5');
  await h.clickOn(page, `input[name="how"][value="link"]`);
  await dialogSubmit(h, page);
  await page.waitFor(`!!document.getElementById('tenant-link-value') || !!document.querySelector('[role="dialog"] .ant-alert-error')`);
  const createdError = await page.evaluate(`document.querySelector('[role="dialog"] .ant-alert-error')?.textContent ?? ''`);
  if (createdError) throw new Error(`the tenant was not created: ${createdError}`);
  const link = await page.evaluate(`document.getElementById('tenant-link-value').value`);
  h.check(link.includes('/verify#token='), 'a new tenant shows its first owner a link');
  await h.clickButton(page, 'I have copied it');
  await page.waitFor(`!document.getElementById('tenant-link-value')`);
  h.check(!(await page.evaluate(`document.body.textContent.includes('#token=')`)), 'the link is not on the page once its dialog closes');

  const tenantID = (await h.api(base, 'GET', '/api/v1/accounts', adminKey)).items.find((a) => a.name === TENANT.name)?.id;
  if (!tenantID) throw new Error('the new tenant is not listed');
  await h.navigateTo(page, `/admin/tenants/${tenantID}?tab=keys`);
  await page.waitFor(`location.pathname === '/admin/tenants/${tenantID}'`);
  await h.clickButton(page, 'New key');
  await page.waitFor(`!!document.getElementById('new-key-name')`);
  await h.setInput(page, 'new-key-name', 'CI');
  await dialogSubmit(h, page);
  await page.waitFor(`!!document.getElementById('new-key-once-value')`);
  const secret = await page.evaluate(`document.getElementById('new-key-once-value').value`);
  h.check(secret.length >= 20, 'the new API key is shown in its dialog');
  await h.clickButton(page, 'I have copied it');
  await page.waitFor(`!document.getElementById('new-key-once-value')`);
  h.check(!(await page.evaluate(`document.body.innerHTML.includes(${q(secret)})`)), 'the key is not on the page once its dialog closes');
  await h.api(base, 'GET', `/api/v1/accounts/${tenantID}/usage`, secret);
  h.check(true, 'the key the dialog showed works for the API');
}

// memberNotFound is a member opening the owner's pages: the not-found view, and no Manage link.
export async function memberNotFound(page, h) {
  await h.navigateTo(page, '/manage');
  await page.waitFor(`document.querySelector('h1')?.textContent === 'Page not found'`);
  const manageLink = await page.evaluate(`[...document.querySelectorAll('[role="menuitem"]')].some((i) => i.textContent.trim() === 'Overview')`);
  h.check(!manageLink, 'a member who opens /manage sees the not-found view, and no Manage link');
}

// signInLinkChecks: the account's key makes a sign-in link for the member with a path to open. The browser opens it and
// lands on that path, signed in, with the token gone from the address bar. The same link a second time shows the
// expired message and no shell.
export async function signInLinkChecks(page, h, { base, accountID, accountKey, memberID, teamA, MEMBER }) {
  await h.signOut(page);
  const made = await h.api(base, 'POST', `/api/v1/accounts/${accountID}/users/${memberID}/sign-in-links`, accountKey, { next: `/files/${teamA}` });
  h.check(made.url.startsWith(base + '/sign-in#token='), 'the account key makes a sign-in link for the member');
  await page.goto(made.url);
  await page.waitFor(`location.pathname === '/files/${teamA}' && !location.hash && !!document.querySelector('[data-testid="account-menu"]')`, 15000);
  const who = await page.evaluate(`document.querySelector('[data-testid="account-menu"]').textContent`);
  h.check(who.includes(MEMBER.name), `the browser opens the link, lands on the requested path, signed in (${who.trim()})`);

  await h.signOut(page);
  await page.goto(made.url);
  await page.waitFor(`document.body.textContent.includes('already used')`, 15000);
  h.check(!(await page.evaluate(`!!document.querySelector('[data-testid="account-menu"]')`)), 'the same link a second time shows the expired message, and no shell');
}

// switchChecks: a browser signed in as the member that opens a link for the owner is asked first. Stay keeps the member
// signed in and the link unused. A new link, then Switch, signs the browser in as the owner.
export async function switchChecks(page, h, { base, accountID, accountKey, OWNER, MEMBER, signIn }) {
  const ownerID = (await h.api(base, 'GET', `/api/v1/accounts/${accountID}/users`, accountKey)).items.find((u) => u.email === OWNER.email)?.id;
  h.check(!!ownerID, 'the account lists the owner, so a link can be made for the owner');

  await page.goto(base + '/login');
  await signIn(page, MEMBER);
  const first = await h.api(base, 'POST', `/api/v1/accounts/${accountID}/users/${ownerID}/sign-in-links`, accountKey, { next: '/files' });
  await page.goto(first.url);
  await page.waitFor(`!!document.querySelector('[role="dialog"]')`, 15000);
  const asked = await page.evaluate(`document.querySelector('[role="dialog"]').textContent`);
  asks += 1;
  h.check(asked.includes(MEMBER.name) && asked.includes(OWNER.name), `a browser signed in as the member is asked before a link signs it in as the owner (${asked.trim().slice(0, 90)}...)`);
  await h.clickButton(page, `Stay signed in as ${MEMBER.name}`);
  await page.waitFor(`location.pathname.startsWith('/files') && !!document.querySelector('[data-testid="account-menu"]')`, 15000);
  const stayed = await page.evaluate(`document.querySelector('[data-testid="account-menu"]').textContent`);
  h.check(stayed.includes(MEMBER.name), `Stay keeps the member signed in (${stayed.trim()})`);

  const second = await h.api(base, 'POST', `/api/v1/accounts/${accountID}/users/${ownerID}/sign-in-links`, accountKey, { next: '/files' });
  await page.goto(second.url);
  await page.waitFor(`!!document.querySelector('[role="dialog"]')`, 15000);
  asks += 1;
  await h.clickButton(page, `Switch to ${OWNER.name}`);
  await page.waitFor(`location.pathname.startsWith('/files') && !!document.querySelector('[data-testid="account-menu"]')`, 15000);
  const switched = await page.evaluate(`document.querySelector('[data-testid="account-menu"]').textContent`);
  h.check(switched.includes(OWNER.name), `Switch signs the browser in as the owner (${switched.trim()})`);
}
