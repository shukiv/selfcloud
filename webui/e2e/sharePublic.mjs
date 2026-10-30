// SPDX-License-Identifier: AGPL-3.0-or-later
// The anyone-link, drop-link and link checks of the React build (sharing interface, part v3). The check texts are the
// same as in web/e2e/run.mjs, word for word, so the two PASS lists agree for this part. The helpers of run.mjs are
// passed in as h (h.check, h.api, h.signIn, h.signOut, h.navigateTo, h.setInput, h.OWNER, h.MEMBER, and the rest).
// Selectors follow the React DOM: antd alerts (.ant-alert), the share dialog's ids and names, and the visitor page's
// classes (.rows, .crumbs, .public-preview, .dropzone). The Manage > Overview step and Manage > Links are not in this build (part v4).

const q = (v) => JSON.stringify(v);

// linkChecks: a share link, end to end. The owner makes an organisation link on a directory in a team space that
// allows links, through the share dialog, and copies its URL once. Signed out, the browser opens the URL: the page
// strips the secret from the address and sends the visitor to sign-in, which returns it to the link page. The member
// is then in the directory, lists it under Shared with me, and the owner removes the link with Restricted. The
// same URL is then refused, and the member loses the directory.
export async function linkChecks(page, { base, accountID, accountKey }, h) {
  const fld = (await h.api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'Link Hall' })).id;
  await h.api(base, 'PATCH', `/api/v1/folders/${fld}/settings`, accountKey, { allow_links: true });
  const plans = await h.api(base, 'POST', `/api/v1/folders/${fld}/directories`, accountKey, { path: '/Plans', parents: false });
  await h.rawPut(base, `/api/v1/folders/${fld}/files?path=%2FPlans%2Fbrief.txt`, accountKey, 'the brief\n');
  // An image, so the visitor's file view has a preview: a real one-pixel PNG, so the browser draws it without an error.
  const pixel = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==', 'base64');
  await h.rawPut(base, `/api/v1/folders/${fld}/files?path=%2FPlans%2Fplan.png`, accountKey, pixel, 'image/png');

  // The owner shares Plans through an organisation link, and copies the URL the dialog shows once.
  if (await page.evaluate(`!!document.querySelector('.topbar')`)) await h.signOut(page);
  else await page.goto(base + '/login');
  await h.signIn(page, h.OWNER);
  await page.waitFor(`!!document.querySelector('.teams a[href="/files/${fld}"]')`);
  await h.clickOn(page, `.teams a[href="/files/${fld}"]`);
  await page.waitFor(`location.pathname === '/files/${fld}'`);
  await h.rowAppears(page, 'Plans');
  await h.selectRow(page, 'Plans');
  await h.clickButton(page, 'Share');
  await page.waitFor(`!!document.getElementById('share-general')`);
  await h.clickOn(page, `input[name="share-general-access"][value="organisation"]`);
  await h.clickButton(page, 'Create link');
  await page.waitFor(`!!document.querySelector('.made input')`);
  const url = await page.evaluate(`document.querySelector('.made input').value`);
  h.check(url.startsWith(base + '/s#') && url.slice(base.length + 3).length === 43, 'the owner makes an organisation link, and the dialog shows its URL once');
  await h.press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  // Signed out, the URL leaves the secret out of the address and sends the visitor to sign in.
  await h.signOut(page);
  await page.goto(url);
  await page.waitFor(`location.pathname === '/login' && !!document.getElementById('login-email')`);
  const stripped = await page.evaluate(`location.hash === '' && location.pathname === '/login'`);
  h.check(stripped, 'the link page strips the secret from the address, and sends a signed-out visitor to sign in');
  await h.signInHere(page, h.MEMBER);
  // The item's own address opens its folder with the item selected.
  await page.waitFor(`location.pathname.endsWith('/${plans.id}') && !location.hash`);
  h.check(true, 'a member opens the link, and lands on the item it names, with no secret in the address');
  const kept = await page.evaluate(`sessionStorage.getItem('selfcloud.share.secret')`);
  h.check(kept === null, 'the secret is gone from session storage once used');
  await h.clickNav(page, 'Shared with me');
  await page.waitFor(`location.pathname === '/shared' && !!document.querySelector('.shared a[href="/n/${plans.id}"]')`);
  h.check(true, 'the item the link opened is listed under Shared with me');

  // The owner removes the link with Restricted. The member loses the item at once, and the URL no longer opens it.
  await h.signOut(page);
  await h.signIn(page, h.OWNER);
  await page.waitFor(`!!document.querySelector('.teams a[href="/files/${fld}"]')`);
  await h.clickOn(page, `.teams a[href="/files/${fld}"]`);
  await page.waitFor(`location.pathname === '/files/${fld}'`);
  await h.rowAppears(page, 'Plans');
  await h.selectRow(page, 'Plans');
  await h.clickButton(page, 'Share');
  await page.waitFor(`!!document.querySelector('input[name="share-general-access"][value="restricted"]')`);
  await h.clickOn(page, `input[name="share-general-access"][value="restricted"]`);
  await page.waitFor(`document.querySelector('input[name="share-general-access"][value="restricted"]').checked`);
  await h.press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);
  h.check(true, 'the owner removes the link with Restricted');

  await h.signOut(page);
  await h.signIn(page, h.MEMBER);
  await h.clickNav(page, 'Shared with me');
  await page.waitFor(`location.pathname === '/shared' && !document.querySelector('.shared a[href="/n/${plans.id}"]')`);
  h.check(true, 'the member loses the item when the link is removed');
  await page.goto(url);
  await page.waitFor(`!!document.querySelector('.ant-alert') && !!document.querySelector('.ant-alert').textContent.includes("doesn't work")`, 15000);
  h.check(true, 'the removed link shows the one message a dead link gets');
  await anyoneLinkChecks(page, { base, accountID, accountKey, fld, plans }, h);
  await dropLinkChecks(page, { base, accountID, accountKey, fld, plans }, h);
  // Not run in this build: publicLinksDeleteAllChecks (shareDialog.mjs) drives Manage > Links, which is part v4.
}

// anyoneLinkChecks: the owner turns on links that anyone can open, and makes one on Plans with a password. A
// signed-out visitor opens it, is asked for the password, is refused a wrong one, then sees the folder, previews
// a file, and cannot reach an item outside the link. The owner deletes the link, and the visitor gets the one message.
async function anyoneLinkChecks(page, { base, accountID, accountKey, fld, plans }, h) {
  const PASSWORD = 'open sesame 2026';
  const outside = (await h.api(base, 'POST', `/api/v1/folders/${fld}/directories`, accountKey, { path: '/Other', parents: false })).id;

  // Not ported: the Manage > Overview switch belongs to the React Manage page (part v4). The account's switch is set
  // through the API here, so the rest of the check runs; the check that the switch is clicked is not in this build.
  await h.api(base, 'PATCH', `/api/v1/accounts/${accountID}/sharing-policy`, accountKey, { public_links: true });
  await h.signOut(page);
  await h.signIn(page, h.OWNER);

  // The owner makes a link on Plans that anyone can open, with a password, and copies the URL the dialog shows once.
  await h.navigateTo(page, `/files/${fld}`);
  await h.rowAppears(page, 'Plans');
  await h.selectRow(page, 'Plans');
  await h.clickButton(page, 'Share');
  await page.waitFor(`!!document.getElementById('share-general')`);
  await page.waitFor(`!!document.querySelector('input[name="share-general-access"][value="anyone"]')`);
  await h.clickOn(page, `input[name="share-general-access"][value="anyone"]`);
  await page.waitFor(`!!document.getElementById('share-anyone-password')`);
  await h.setInput(page, 'share-anyone-password', PASSWORD);
  await h.clickButton(page, 'Create link');
  await page.waitFor(`!!document.querySelector('.made input')`);
  const url = await page.evaluate(`document.querySelector('.made input').value`);
  h.check(url.startsWith(base + '/s#') && url.slice(base.length + 3).length === 43, 'the owner makes a link that anyone can open, with a password');
  await h.press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  // A signed-out visitor is asked for the password; a wrong one is refused with the message; the right one opens it.
  await h.signOut(page);
  await page.goto(url);
  await page.waitFor(`!!document.getElementById('share-password')`);
  h.check(true, 'a signed-out visitor is asked for the password of a link that anyone can open');
  await h.setInput(page, 'share-password', 'not the password');
  await h.clickButton(page, 'Open');
  await page.waitFor(`!!document.querySelector('.ant-alert-error') && !!document.querySelector('.ant-alert-error').textContent.includes('not right')`);
  h.check(true, 'a wrong password is refused with its message');
  await h.setInput(page, 'share-password', PASSWORD);
  await h.clickButton(page, 'Open');
  await page.waitFor(`!!document.querySelector('.rows') && [...document.querySelectorAll('h1')].some((h) => h.textContent === 'Plans')`, 15000);
  const listed = await page.evaluate(`document.querySelector('.rows').textContent`);
  h.check(listed.includes('brief.txt') && listed.includes('plan.png'), 'the visitor sees the folder, with no sign-in');
  const kept = await page.evaluate(`location.hash.length > 1`);
  h.check(kept, 'the secret stays in the address, so a reload works');
  const stored = await page.evaluate(`sessionStorage.getItem('selfcloud.share.secret')`);
  h.check(stored === null, 'nothing about the visit is left in session storage');

  // The visitor previews the picture: the preview is a signed link, and it has a Download button.
  await page.evaluate(`[...document.querySelectorAll('.rows button')].find((b) => b.textContent === 'plan.png').click(), true`);
  await page.waitFor(`!!document.querySelector('img.public-preview')`, 15000);
  const src = await page.evaluate(`document.querySelector('img.public-preview').getAttribute('src')`);
  h.check(src.includes('/api/v1/links/'), 'the file opens with a preview from a signed link');
  h.check(await page.evaluate(`!!document.querySelector('a.btn[download]')`), 'the file has a Download button');
  // The path stops at the link's root: no item above it is named.
  const crumbs = await page.evaluate(`[...document.querySelectorAll('.crumbs button')].map((b) => b.textContent)`);
  h.check(JSON.stringify(crumbs) === JSON.stringify(['Plans']), `the path stops at the link's root (${crumbs.join(' / ')})`);

  // The visitor's credential cannot reach an item outside the link. The check runs on the server, from the harness.
  const secret = url.slice(url.indexOf('#') + 1);
  const opened = await h.api(base, 'POST', '/api/v1/share/open', '', { secret, password: PASSWORD });
  const inside = await fetch(base + `/api/v1/nodes/${plans.id}`, { headers: { Authorization: `Bearer ${opened.token}` } });
  const beside = await fetch(base + `/api/v1/nodes/${outside}`, { headers: { Authorization: `Bearer ${opened.token}` } });
  h.check(inside.status === 200 && beside.status === 404, `a visitor reaches the link's folder, and not the item beside it (${inside.status}, ${beside.status})`);

  // The owner deletes the link. A signed-in member who opens it gets the one dead-link message.
  await h.signOut(page);
  await h.signIn(page, h.OWNER);
  await h.navigateTo(page, `/files/${fld}`);
  await h.rowAppears(page, 'Plans');
  await h.selectRow(page, 'Plans');
  await h.clickButton(page, 'Share');
  await page.waitFor(`!!document.querySelector('input[name="share-general-access"][value="restricted"]')`);
  await h.clickOn(page, `input[name="share-general-access"][value="restricted"]`);
  await page.waitFor(`document.querySelector('input[name="share-general-access"][value="restricted"]').checked`);
  await h.press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);
  await h.signOut(page);
  await h.signIn(page, h.MEMBER);
  await page.goto(url);
  await page.waitFor(`!!document.querySelector('.ant-alert') && !!document.querySelector('.ant-alert').textContent.includes("doesn't work")`, 15000);
  h.check(true, 'a deleted link that anyone can open shows the one dead-link message');
}

// dropLinkChecks: the owner asks for files in Plans (Request files, with a name), a signed-out visitor sends two
// files into it, one of them named like a file already there, and sees no listing of the folder. The owner sees both
// files and turns the link off. The visitor who opens the dead link is signed in, as the anyone-link checks above
// do: a signed-out visitor to a dead link is sent to sign in, by design.
async function dropLinkChecks(page, { base, accountID, accountKey, fld, plans }, h) {
  await h.rawPut(base, `/api/v1/folders/${fld}/files?path=%2FPlans%2Freport.txt`, accountKey, 'old report\n');

  await h.signOut(page);
  await h.signIn(page, h.OWNER);
  await h.navigateTo(page, `/files/${fld}`);
  await h.rowAppears(page, 'Plans');
  await h.selectRow(page, 'Plans');
  await h.clickButton(page, 'Share');
  await page.waitFor(`!!document.getElementById('share-request-files')`);
  // Turning Request files on makes the link; the name request is then saved on it.
  await page.evaluate(`(() => { const box = document.querySelector('section.request button[role="switch"]'); box.click(); return true; })()`);
  await page.waitFor(`!!document.querySelector('section.request .made input')`, 15000);
  const url = await page.evaluate(`document.querySelector('section.request .made input').value`);
  h.check(url.startsWith(base + '/s#') && url.slice(base.length + 3).length === 43, 'the owner turns on Request files and gets a link');
  await page.waitFor(`(() => { const s = [...document.querySelectorAll('section.request span')].find((x) => x.textContent.includes('Ask for name')); const sw = s?.closest('label, div')?.querySelector('button[role="switch"]'); if (!sw) return false; sw.click(); return true; })()`);
  await h.clickButton(page, 'Save');
  await page.waitFor(`document.querySelector('section.request .hint[role="status"]')?.textContent.includes('Saved')`, 15000);
  await h.press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  // A signed-out visitor opens the link: it asks for a name and an email, and offers no listing of the folder.
  await h.signOut(page);
  await page.goto(url);
  await page.waitFor(`!!document.getElementById('drop-name')`, 15000);
  const nameLabel = await page.evaluate(`document.querySelector('label[for="drop-name"]').textContent`);
  h.check(nameLabel === 'Your name', 'a signed-out visitor sees the name field as required');
  await h.setInput(page, 'drop-name', 'Vera Visitor');
  await h.setInput(page, 'drop-email', 'vera@example.com');
  for (const [name, body] of [['report.txt', 'new report\n'], ['brief.txt', 'the brief\n']]) {
    await page.evaluate(`(() => {
      const dt = new DataTransfer();
      dt.items.add(new File([${q(body)}], ${q(name)}, { type: 'text/plain' }));
      const input = document.querySelector('.dropzone input[type="file"]');
      input.files = dt.files;
      input.dispatchEvent(new Event('change', { bubbles: true }));
      return true;
    })()`);
  }
  await page.waitFor(
    `[...document.querySelectorAll('.rows li .muted')].filter((x) => x.textContent === 'Sent').length === 2`,
    30000,
  );
  const visit = await page.evaluate(`[...document.querySelectorAll('.rows li .name')].map((x) => x.textContent)`);
  h.check(JSON.stringify(visit) === JSON.stringify(['report.txt', 'brief.txt']), `the visitor sees only the two files it sent (${visit.join(', ')})`);
  const listed = await page.evaluate(`!!document.querySelector('.crumbs') || [...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'Plans')`);
  h.check(listed === false, 'the visitor sees no listing of the folder');

  // The owner sees both files, and the one whose name was taken arrived renamed.
  await h.signOut(page);
  await h.signIn(page, h.OWNER);
  await h.navigateTo(page, `/files/${fld}`);
  await h.rowAppears(page, 'Plans');
  await h.openRow(page, 'Plans');
  await h.rowAppears(page, 'brief.txt');
  await h.rowAppears(page, 'report (1).txt');
  h.check(true, 'the owner sees both files; the clashing name arrived as report (1).txt');

  // The owner turns Request files off: the link stops working at once.
  await h.navigateTo(page, `/files/${fld}`);
  await h.rowAppears(page, 'Plans');
  await h.selectRow(page, 'Plans');
  await h.clickButton(page, 'Share');
  await page.waitFor(`!!document.getElementById('share-request-files')`);
  await h.clickOn(page, `section.request button[role="switch"]`);
  await page.waitFor(`!document.getElementById('drop-expires')`, 15000);
  await h.press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  await h.signOut(page);
  await h.signIn(page, h.MEMBER);
  await page.goto(url);
  await page.waitFor(`!!document.querySelector('.ant-alert') && !!document.querySelector('.ant-alert').textContent.includes("doesn't work")`, 15000);
  h.check(true, 'a deleted drop link shows the one dead-link message');
}

