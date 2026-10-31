// SPDX-License-Identifier: AGPL-3.0-or-later

// The slice p3 checks: shortcuts, folder colours and item descriptions, in the browser. p3Setup makes the data through
// the API as the account key; the owner's checks set a colour and make a shortcut with the dialogs; the member's checks
// see the colour and the description as text, open a reachable shortcut, and meet an unavailable one. When SHOTS_DIR is
// set, the checks also write screenshots there (light, dark, phone and Hebrew); the gate leaves it unset.
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const q = (v) => JSON.stringify(v);

// p3Setup makes two directories and a file in the team spaces, a space the member cannot reach, and a shortcut from the
// member's notes to that space's file, all through the API.
export async function p3Setup(h, base, accountKey, { accountID, teamA, teamB }) {
  const children = async (folder) => (await h.api(base, 'GET', `/api/v1/folders/${folder}/children?path=%2F&limit=500`, accountKey)).items;
  await h.api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: '/p3-projects', parents: false });
  await h.api(base, 'POST', `/api/v1/folders/${teamB}/directories`, accountKey, { path: '/p3-notes', parents: false });
  await h.rawPut(base, `/api/v1/folders/${teamA}/files?path=%2Fp3-report.txt`, accountKey, 'the report\n', 'text/plain');
  const vault = await h.api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'P3 Vault' });
  await h.rawPut(base, `/api/v1/folders/${vault.id}/files?path=%2Fp3-vault.txt`, accountKey, 'the vault\n', 'text/plain');
  const notes = (await children(teamB)).find((n) => n.name === 'p3-notes');
  const projects = (await children(teamA)).find((n) => n.name === 'p3-projects');
  const vaultFile = (await children(vault.id)).find((n) => n.name === 'p3-vault.txt');
  await h.api(base, 'PATCH', `/api/v1/nodes/${notes.id}`, accountKey, { color: 'slate', description: 'Line one\n<b>not bold</b>' });
  await h.api(base, 'POST', `/api/v1/nodes/${notes.id}/shortcuts`, accountKey, { target: vaultFile.id, name: 'Vault link' });
  return { notesID: notes.id, projectsID: projects.id, vaultFileID: vaultFile.id };
}

// shot writes a PNG of the page when SHOTS_DIR is set, and does nothing otherwise.
async function shot(page, name) {
  const dir = process.env.SHOTS_DIR;
  if (!dir) return;
  // Let the drawer, dialog or menu finish its entrance; a frame taken during it shows a half-open panel.
  await new Promise((r) => setTimeout(r, 800));
  const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
  await writeFile(join(dir, name), Buffer.from(data, 'base64'));
}

// clickText clicks the first element of the selector whose text is the given text.
async function clickText(h, page, selector, text) {
  await page.waitFor(`[...document.querySelectorAll(${q(selector)})].some((e) => e.textContent.trim() === ${q(text)})`);
  await page.evaluate(`(() => { [...document.querySelectorAll(${q(selector)})].find((e) => e.textContent.trim() === ${q(text)}).click(); return true; })()`);
}

// clickRadio picks a colour: the radio of the colour's name, in the open drawer.
async function clickRadio(page, name) {
  await page.waitFor(`[...document.querySelectorAll('[role="radiogroup"] label')].some((l) => l.textContent.trim() === ${q(name)})`);
  await page.evaluate(`(() => { [...document.querySelectorAll('[role="radiogroup"] label')].find((l) => l.textContent.trim() === ${q(name)}).click(); return true; })()`);
}

// p3OwnerChecks, as the owner: colour and description set in the drawer, then a shortcut made with the dialogs.
export async function p3OwnerChecks(page, h, { teamA, projectsID }) {
  await h.navigateTo(page, `/files/${teamA}`);
  await h.rowAppears(page, 'p3-projects');
  await h.selectRow(page, 'p3-projects');
  await h.clickButton(page, 'Details');
  await page.waitFor(`!!document.querySelector('[data-testid="details"] [role="radiogroup"]')`);
  await clickRadio(page, 'Indigo');
  await h.clickButton(page, 'Save');
  try {
    await page.waitFor(`[...document.querySelectorAll('[role="status"]')].some((s) => s.textContent.trim() === 'Saved.')`, 15000);
  } catch (err) {
    // Say what the drawer shows, so a failed save is read from the message and not from a guess.
    const shown = await page.evaluate(`(document.querySelector('[data-testid="details"]')?.textContent ?? 'no drawer').slice(0, 400)`);
    throw new Error(`the colour was not saved; the drawer shows: ${shown}`, { cause: err });
  }
  await h.press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`!document.querySelector('[data-testid="details"]')`);
  await page.waitFor(`document.querySelector('[role="row"][aria-label="p3-projects"] svg.icon')?.style.color === 'var(--colour-indigo)'`, 15000);
  // The inline style names the variable; the computed colour shows the variable resolved, and differs from a plain folder's.
  const painted = await page.evaluate(`(() => { const c = (name) => getComputedStyle(document.querySelector('[role="row"][aria-label="' + name + '"] svg.icon')).color; return [c('p3-projects'), c('Reports')]; })()`);
  h.check(/^rgb\(/.test(painted[0]) && painted[0] !== painted[1], `the coloured folder's icon is painted in its colour (${painted[0]}, a plain folder ${painted[1]})`);
  h.check(true, 'the owner sets a folder colour in the details drawer, and its icon takes it');

  await h.selectRow(page, 'p3-report.txt');
  await h.clickButton(page, 'Add shortcut');
  await page.waitFor(`!!document.querySelector('[data-testid="pick"]')`);
  // Open Team A in the tree, then choose p3-projects in it.
  await page.evaluate(`(() => { const n = [...document.querySelectorAll('.ant-tree-treenode')].find((x) => x.textContent.includes('Team A')); n.querySelector('.ant-tree-switcher').click(); return true; })()`);
  await page.waitFor(`[...document.querySelectorAll('.ant-tree-treenode')].some((x) => x.textContent.includes('p3-projects'))`, 15000);
  await page.evaluate(`(() => { const n = [...document.querySelectorAll('.ant-tree-treenode')].find((x) => x.textContent.includes('p3-projects')); n.querySelector('.ant-tree-node-content-wrapper').click(); return true; })()`);
  await h.clickButton(page, 'Next');
  await page.waitFor(`!!document.getElementById('shortcut-name')`);
  h.check(await page.evaluate(`document.getElementById('shortcut-name').value === 'p3-report.txt'`), 'the shortcut dialog prefills the name with the target\'s');
  await shot(page, 'owner-add-shortcut.png');
  await page.setInput('#shortcut-name', 'Report link.txt');
  await h.clickButton(page, 'Create shortcut');
  await page.waitFor(`[...document.querySelectorAll('[role="status"]')].some((s) => s.textContent.includes('Shortcut added: Report link.txt'))`, 15000);
  h.check(true, 'the owner names the shortcut in the dialog, and it is created with that name');

  await h.navigateTo(page, `/files/${teamA}/${projectsID}`);
  await h.rowAppears(page, 'Report link.txt');
  h.check(await page.evaluate(`document.querySelector('[role="row"][aria-label="Report link.txt"]').textContent.includes('Shortcut')`), 'the new shortcut is listed in its folder, marked as a shortcut');
}

// p3MemberChecks, as the member: the colour and description as text, an unavailable shortcut, and a reachable one.
export async function p3MemberChecks(page, h, { teamA, teamB, notesID, projectsID, vaultFileID }) {
  await h.navigateTo(page, `/files/${teamB}`);
  await h.rowAppears(page, 'p3-notes');
  await h.selectRow(page, 'p3-notes');
  await h.clickButton(page, 'Details');
  await page.waitFor(`!!document.querySelector('[data-testid="description"]')`);
  const text = await page.evaluate(`document.querySelector('[data-testid="description"]').textContent`);
  const literal = await page.evaluate(`!document.querySelector('[data-testid="description"] b')`);
  h.check(text === 'Line one\n<b>not bold</b>' && literal, 'the description keeps its line break and shows <b> as text');
  h.check(await page.evaluate(`document.querySelector('[data-testid="details"]').textContent.includes('Slate')`), 'the colour is shown by name to a reader');
  h.check(await page.evaluate(`!document.querySelector('[data-testid="details"] [role="radiogroup"]') && !document.querySelector('[data-testid="details"] textarea')`), 'a reader gets no colour or description control');
  await shot(page, 'light-details.png');
  await h.press(page, '[data-testid="details"]', 'Escape');
  await page.waitFor(`!document.querySelector('.ant-drawer-open')`);

  await h.navigateTo(page, `/files/${teamB}/${notesID}`);
  await h.rowAppears(page, 'Vault link');
  h.check(await page.evaluate(`document.querySelector('[role="row"][aria-label="Vault link"]').textContent.includes('Unavailable')`), 'an unavailable shortcut is marked Unavailable');
  const pageText = await page.evaluate(`document.body.textContent + ' ' + location.href`);
  h.check(!pageText.includes(vaultFileID), "the unavailable shortcut shows no ID of its target");
  await page.evaluate(`document.querySelector('[role="row"][aria-label="Vault link"]').dispatchEvent(new MouseEvent('dblclick', { bubbles: true })), true`);
  await page.waitFor(`[...document.querySelectorAll('[role="dialog"]')].some((d) => d.textContent.includes('Ask for access'))`);
  h.check(true, 'opening an unavailable shortcut offers a request for access, and stays on the page');
  h.check(!(await page.evaluate(`location.href`)).includes(vaultFileID), 'opening it puts no target ID in the address');
  await h.press(page, '[role="dialog"]', 'Escape');
  await page.waitFor(`![...document.querySelectorAll('[role="dialog"]')].some((d) => d.textContent.includes('Ask for access'))`);

  await h.navigateTo(page, `/files/${teamA}/${projectsID}`);
  await h.rowAppears(page, 'Report link.txt');
  await page.evaluate(`document.querySelector('[role="row"][aria-label="Report link.txt"]').dispatchEvent(new MouseEvent('dblclick', { bubbles: true })), true`);
  await page.waitFor(`document.body.textContent.includes('the report')`, 15000);
  h.check(true, 'a reachable shortcut opens its target');
  // Back leaves the target's address for the listing before it, and the preview the target opened goes with it.
  await page.evaluate(`history.back(), true`);
  await page.waitFor(`!document.querySelector('[role="dialog"]')`, 10000);
  h.check(true, 'Back closes the preview a shortcut opened, and the listing shows again');

  // Dark, then phone, then Hebrew: the same listing each time.
  await h.navigateTo(page, `/files/${teamB}/${notesID}`);
  await h.rowAppears(page, 'Vault link');
  await clickText(h, page, '[data-testid="theme"] .ant-segmented-item-label', 'Dark');
  await page.waitFor(`document.documentElement.dataset.theme === 'dark'`);
  await shot(page, 'dark-listing.png');
  await clickText(h, page, '[data-testid="theme"] .ant-segmented-item-label', 'Light');
  await page.waitFor(`document.documentElement.dataset.theme === 'light'`);
  await page.send('Emulation.setDeviceMetricsOverride', { width: 375, height: 812, deviceScaleFactor: 1, mobile: true });
  await h.rowAppears(page, 'Vault link');
  // The listing switches to its narrow layout after the width changes, a render later. Wait for the page to settle; a
  // real overflow never settles, and the wait then fails.
  await page.waitFor(`document.documentElement.scrollWidth <= document.documentElement.clientWidth`, 8000);
  h.check(true, 'the shortcuts listing has no horizontal scroll at phone width');
  await shot(page, 'phone-listing.png');
  await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
  await h.clickOn(page, '[data-testid="language"]');
  await clickText(h, page, '.ant-dropdown-menu-item', 'עברית');
  await page.waitFor(`document.documentElement.dir === 'rtl' && !!document.querySelector('[role="row"][aria-label="Vault link"]')`, 15000);
  await shot(page, 'hebrew-listing.png');
  h.check(true, 'Hebrew lays the shortcuts listing out right to left');
  await h.clickOn(page, '[data-testid="language"]');
  await clickText(h, page, '.ant-dropdown-menu-item', 'English');
  await page.waitFor(`document.documentElement.dir === 'ltr'`);
}
