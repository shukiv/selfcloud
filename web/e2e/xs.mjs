// SPDX-License-Identifier: AGPL-3.0-or-later
// Sharing with people in other accounts on the same server (xs, decisions 187-189), end to end. The owner of one
// account turns on the switch in Manage > Overview and shares a directory with an address of another account through
// the share dialog. The reader of that address proves it by a mailed link, sees the item under Shared with me with its
// sharer and account, opens it at /x and downloads a file through one deferred link. Removing the grant, and turning
// the owner's switch off, each take the item away; both bring it back. The helpers of run.mjs arrive as h; ctx carries
// { page, base, accountID, accountKey, adminKey, sink, waitForMail, dir }.

import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';

const q = (v) => JSON.stringify(v);

// mailBody decodes the quoted-printable body of a mail the sink kept, as owner.mjs and sec2.mjs read it.
const mailBody = (m) => m.data.replace(/=\r\n/g, '').replace(/=([0-9A-F]{2})/g, (_, hex) => String.fromCharCode(parseInt(hex, 16)));

const rowSel = (name) => `[role="row"][aria-label="${name.replace(/["\\]/g, '\\$&')}"]`;

// clickRowText is one real click on a visitor's listing row, by its name. The share page's folder view is the node listing
// (decision 184): the row is a tr[data-id] named by its aria-label, and the click lands on its name cell, not the actions cell.
async function clickRowText(page, name) {
  const p = await page.evaluate(`(() => { const row = [...document.querySelectorAll('.public-browse .files-list tr[data-id]')].find((r) => r.getAttribute('aria-label') === ${q(name)}); if (!row) return null; row.scrollIntoView({ block: 'center' }); const cell = [...row.querySelectorAll('td')].find((td) => td.textContent.includes(${q(name)})); const r = (cell ?? row).getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
  if (!p) throw new Error(`no row ${name}`);
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: p.x, y: p.y });
  await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: p.x, y: p.y, button: 'left', buttons: 1, clickCount: 1 });
  await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: p.x, y: p.y, button: 'left', buttons: 0, clickCount: 1 });
}

// outsideOption finds the combobox option that offers a share with an address of another account.
const outsideOption = (email) =>
  `[...document.querySelectorAll('[role="listbox"] [role="option"]')].find((o) => o.textContent.includes(${q('Share with ' + email)}))`;

export async function xsChecks(h, ctx) {
  const { page, base, accountID, accountKey, adminKey, sink, waitForMail, dir } = ctx;
  const check = h.check;
  const READER = { email: 'rita@example.com', name: 'Rita Reader', password: 'correct horse battery staple' };
  const accountName = (await h.api(base, 'GET', '/api/v1/accounts', adminKey)).items.find((a) => a.id === accountID)?.name ?? '';

  // Another account, its key, and a reader whose address is verified but not yet proved. Its switch is on.
  const otherID = (await h.api(base, 'POST', '/api/v1/accounts', adminKey, { name: 'XS Other Co' })).id;
  const otherKey = (await h.api(base, 'POST', `/api/v1/accounts/${otherID}/keys`, adminKey, { name: 'xs' })).key;
  const reader = await h.api(base, 'POST', `/api/v1/accounts/${otherID}/users`, otherKey, {
    email: READER.email,
    display_name: READER.name,
    role: 'member',
    verification: 'link',
  });
  await h.api(base, 'POST', '/api/v1/auth/verify', null, { token: reader.verification.token, password: READER.password });
  await h.api(base, 'PATCH', `/api/v1/accounts/${otherID}/sharing-policy`, otherKey, { external_sharing: true });

  // The owner's account: a team space with a directory D holding one text file.
  const hall = (await h.api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'XS Hall' })).id;
  const dID = (await h.api(base, 'POST', `/api/v1/folders/${hall}/directories`, accountKey, { path: '/D', parents: false })).id;
  await h.rawPut(base, `/api/v1/folders/${hall}/files?path=%2FD%2Fnote.txt`, accountKey, 'a note from the other account\n');
  const noteID = (await h.api(base, 'GET', `/api/v1/folders/${hall}/children?path=%2FD`, accountKey)).items.find((n) => n.name === 'note.txt').id;

  // The owner turns on the account's switch in Manage > Overview, and signs in again so the session reads it.
  await h.signOut(page);
  await h.signIn(page, h.OWNER);
  await h.navigateTo(page, '/manage');
  await page.waitFor(`(() => { const el = document.getElementById('external-sharing-switch'); if (!el) return false; el.click(); return true; })()`);
  await page.waitFor(`document.getElementById('external-sharing-switch').checked`);
  await h.signOut(page);
  await h.signIn(page, h.OWNER);
  check(true, 'the owner turns on sharing with people in other accounts in Manage > Overview');

  // The owner shares D with the reader's address through the share dialog.
  await h.navigateTo(page, `/files/${hall}`);
  await h.rowAppears(page, 'D');
  await h.selectRow(page, 'D');
  await h.clickButton(page, 'Share');
  await page.waitFor(`!!document.getElementById('share-who')`);

  // A person of the owner's own account is found by name and offered as a person, not as a share with another account.
  await h.setInput(page, 'share-who', h.MEMBER.name.split(' ')[0]);
  await page.waitFor(`[...document.querySelectorAll('[role="listbox"] [role="option"]')].some((o) => o.textContent.includes(${q(h.MEMBER.name)}))`, 10000);
  const ownOffered = await page.evaluate(`!!${outsideOption(h.MEMBER.email)}`);
  check(!ownOffered, "a person of the owner's own account is offered as a person, not as a share with another account");

  // The reader's address, which the directory does not know, is offered as a person of another account.
  await h.setInput(page, 'share-who', READER.email);
  await page.waitFor(`!!${outsideOption(READER.email)}`, 10000);
  const offer = await page.evaluate(`${outsideOption(READER.email)}.textContent`);
  check(offer.includes('Someone outside your account') && offer.includes('Other account'), 'an unknown address is offered as a share with a person of another account');
  await page.evaluate(`${outsideOption(READER.email)}.click(), true`);
  await page.waitFor(`document.getElementById('share-who').value === ${q(READER.email)}`);
  await h.clickOn(page, `[role="dialog"] section[aria-labelledby="share-add"] form button[type="submit"]`);
  await page.waitFor(`(document.querySelector('[role="dialog"] .live')?.textContent ?? '').includes(${q(`Shared with ${READER.email} as Can view.`)})`);
  check(true, "the owner shares the directory with the reader's address as a viewer");
  await page.waitFor(`!!document.getElementById('share-external-grants')`);
  const grant = await page.evaluate(`document.getElementById('share-external-grants').closest('section').textContent`);
  check(grant.includes(READER.email) && grant.includes('Other account') && grant.includes('Can view'), 'the share dialog lists the outside grant with its tag and role');
  await h.press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  // The reader proves the address by the mailed link, then sees the directory under Shared with me.
  await h.signOut(page);
  await h.signIn(page, READER);
  await h.navigateTo(page, '/shared');
  await page.waitFor(`!!document.getElementById('shared-external-title')`);
  await page.waitFor(`[...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'Send me a link')`);
  check(true, 'an unproved address is asked to prove itself before the outside items appear');
  await h.clickButton(page, 'Send me a link');
  await page.waitFor(`document.body.textContent.includes(${q(`We sent a link to ${READER.email}`)})`);
  const mail = await waitForMail(sink, READER.email);
  const token = /prove#token=([A-Za-z0-9_-]+)/.exec(mailBody(mail))?.[1] ?? '';
  check(token !== '', 'the proof mail carries a link with a token');
  await page.goto(`${base}/prove#token=${token}`);
  await page.waitFor(`!!document.querySelector('.ant-alert-success') && document.querySelector('.ant-alert-success').textContent.includes('Your email address is proved.')`, 15000);
  check(true, "the mailed link proves the reader's address");
  await h.clickButton(page, 'Shared with me');
  await page.waitFor(`location.pathname === '/shared'`);

  // The directory is listed with its sharer and account, and opens at /x.
  await page.waitFor(`!!document.querySelector(${q(rowSel('D'))})`, 15000);
  const byline = await page.evaluate(`document.querySelector(${q(rowSel('D'))}).textContent`);
  check(byline.includes(h.OWNER.name) && byline.includes(accountName), `the outside item names its sharer and account (${byline.trim()})`);

  // The item page reads through the session and defers a file's link to the click that needs it.
  const downloads = join(dir, 'xs-downloads');
  await mkdir(downloads, { recursive: true });
  await page.send('Page.setDownloadBehavior', { behavior: 'allow', downloadPath: downloads });
  const linkAnswers = [];
  const thumbs = [];
  const onResponse = (p) => {
    const u = p.response.url;
    if (u.includes(`/api/v1/nodes/${noteID}/links`)) linkAnswers.push(p.response.status);
    if (/\/api\/v1\/nodes\/[^/]+\/thumbnail/.test(u)) thumbs.push(u);
  };
  page.on('Network.responseReceived', onResponse);
  try {
    await page.evaluate(`document.querySelector(${q(rowSel('D'))}).click(), true`);
    await page.waitFor(`location.pathname === '/x/${dID}'`, 15000);
    await page.waitFor(`[...document.querySelectorAll('.public-browse .files-list tr[data-id]')].some((r) => r.getAttribute('aria-label') === 'note.txt')`, 15000);
    check(true, 'the outside directory opens at /x with its file');
    const xByline = await page.evaluate(`document.body.textContent`);
    check(xByline.includes('Shared by') && xByline.includes(h.OWNER.name) && xByline.includes(accountName), 'the /x page names who shared the item and from which account');

    // Opening the file asks for no link yet: the link is deferred to the download click.
    await clickRowText(page, 'note.txt');
    await page.waitFor(`[...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'Download')`, 15000);
    check(linkAnswers.length === 0 && thumbs.length === 0, `opening the directory and the file asks for no link and no thumbnail (${linkAnswers.length} links, ${thumbs.length} thumbnails)`);

    // The download asks for exactly one attachment link, answered 201.
    await h.clickButton(page, 'Download');
    const until = Date.now() + 15000;
    while (linkAnswers.length === 0 && Date.now() < until) await new Promise((r) => setTimeout(r, 50));
    check(linkAnswers.length === 1 && linkAnswers[0] === 201, `the download asks for exactly one attachment link, answered 201 (${JSON.stringify(linkAnswers)})`);

    // Phone width: neither the item page nor Shared with me scrolls sideways.
    await page.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
    const itemOverflow = await page.evaluate(`document.documentElement.scrollWidth - document.documentElement.clientWidth`);
    check(itemOverflow <= 0, `the /x page does not scroll sideways at phone width (overflow ${itemOverflow}px)`);
    await h.navigateTo(page, '/shared');
    await page.waitFor(`location.pathname === '/shared' && !!document.getElementById('shared-external-title')`, 15000);
    const sharedOverflow = await page.evaluate(`document.documentElement.scrollWidth - document.documentElement.clientWidth`);
    check(sharedOverflow <= 0, `Shared with me does not scroll sideways at phone width (overflow ${sharedOverflow}px)`);
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
  } finally {
    page.off('Network.responseReceived', onResponse);
  }

  // The owner removes the grant through the dialog; the reader loses the item.
  await h.signOut(page);
  await h.signIn(page, h.OWNER);
  await h.navigateTo(page, `/files/${hall}`);
  await h.rowAppears(page, 'D');
  await h.selectRow(page, 'D');
  await h.clickButton(page, 'Share');
  await page.waitFor(`!!document.getElementById('share-external-grants')`);
  await h.clickOn(page, `button[aria-label="Remove: ${READER.email}"]`);
  await page.waitFor(`(document.querySelector('[role="dialog"] .live')?.textContent ?? '').includes(${q(`${READER.email} no longer has access.`)})`);
  check(true, 'the owner removes the outside grant');
  await page.waitFor(`!document.getElementById('share-external-grants')`);
  await h.press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[role="dialog"]')`);

  await h.signOut(page);
  await h.signIn(page, READER);
  await h.navigateTo(page, '/shared');
  await page.waitFor(`!!document.getElementById('shared-external-title') && !document.querySelector(${q(rowSel('D'))})`, 15000);
  check(true, "the removed grant leaves the reader's outside list empty");
  await page.goto(`${base}/x/${dID}`);
  await page.waitFor(`document.body.textContent.includes('or it is no longer available')`, 15000);
  check(true, 'the removed grant makes the item gone at /x');

  // A new grant brings the item back.
  await h.api(base, 'POST', `/api/v1/nodes/${dID}/external-permissions`, accountKey, { email: READER.email, role: 'viewer' });
  await h.navigateTo(page, '/shared');
  await page.waitFor(`!!document.querySelector(${q(rowSel('D'))})`, 15000);
  check(true, "a new grant brings the item back to the reader's list");

  // The owner's switch off takes the item away; on brings it back. A full load reads the list again: the reader is
  // already on Shared with me, and a same-route move would keep the answer it cached.
  await h.api(base, 'PATCH', `/api/v1/accounts/${accountID}/sharing-policy`, accountKey, { external_sharing: false });
  await page.goto(`${base}/shared`);
  await page.waitFor(`!!document.getElementById('shared-external-title') && !document.querySelector(${q(rowSel('D'))})`, 15000);
  check(true, "the owner's switch off empties the reader's outside list");
  await page.goto(`${base}/x/${dID}`);
  await page.waitFor(`document.body.textContent.includes('or it is no longer available')`, 15000);
  check(true, "the owner's switch off makes the item gone at /x");
  await h.api(base, 'PATCH', `/api/v1/accounts/${accountID}/sharing-policy`, accountKey, { external_sharing: true });
  await page.goto(`${base}/shared`);
  await page.waitFor(`!!document.querySelector(${q(rowSel('D'))})`, 15000);
  check(true, "the owner's switch on brings the item back");

  // The gone views' 404s are counted, not errors; nothing else may have been logged.
  check(page.errors.length === 0, `the xs page has no console errors or policy violations (${page.errors.join(' | ') || 'none'})`);

  // Leave the shared page signed in as the member, as the rest of the run expects.
  await h.signOut(page);
  await h.signIn(page, h.MEMBER);
}