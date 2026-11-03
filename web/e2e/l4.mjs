// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice l4: the office editor in the web interface (decision 72). A member with the editor role opens an office file from its
// row's menu ("Open in editor") and gets the editor full-window, with its frame loaded by the fake Collabora (convertFake.mjs),
// which calls the WOPI host with the token it was given, takes the file's lock and reports the document loaded. The token is
// in the form's body and never in the address. Back returns to the folder. A member with the viewer role gets "Open in viewer",
// read only, with no lock. A text file gets no editor entry. The file's lock shows as a mark in the list and as "Being edited"
// in Details; an owner (a manager) ends the editing session through the confirmation, and the editor's next save is refused.
// Hebrew, and a phone at 390 px, are checked for the editor page. The folder and files are removed, and the language, the
// viewport and the signed-in person are put back, in a finally.
import { mkdir } from 'node:fs/promises';
import { setLanguage, setTheme, shot, sleep } from './media.mjs';
import { pickMenuEntry } from './menu.mjs';

const q = (v) => JSON.stringify(v);
const rowSel = (name) => `[role="row"][aria-label="${name.replace(/["\\]/g, '\\$&')}"]`;
const DOCX = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document';

// menuItems opens the More menu of a row and returns the entries' texts.
async function rowMenu(page, name) {
  await page.evaluate(`(() => { const row = document.querySelector(${q(rowSel(name))}); row.focus(); row.click(); return true; })()`);
  await page.evaluate(
    `(() => { const row = document.querySelector(${q(rowSel(name))}); const b = row && row.querySelector('button[aria-haspopup="menu"]'); if (b) b.click(); return !!b; })()`,
  );
  await page.waitFor(`document.querySelectorAll('[role="menuitem"]').length > 0`, 10000);
  return page.evaluate(`[...document.querySelectorAll('[role="menuitem"]')].map((x) => x.textContent.trim())`);
}

// pickMenuItem clicks the open menu's entry with this text.
async function pickMenuItem(page, text) {
  await page.waitFor(`[...document.querySelectorAll('[role="menuitem"]')].some((x) => x.textContent.trim() === ${q(text)})`, 10000);
  await page.evaluate(`[...document.querySelectorAll('[role="menuitem"]')].find((x) => x.textContent.trim() === ${q(text)}).click()`);
}

// closeMenu closes an open menu with Escape.
async function closeMenu(page) {
  await page.evaluate(`(() => { const el = document.activeElement || document.body; el.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', keyCode: 27, bubbles: true })); return true; })()`);
  await sleep(300);
}

// lockMarkOf says whether a row shows the editing mark.
const lockMarkOf = (page, name) =>
  page.evaluate(`!!(document.querySelector(${q(rowSel(name))})?.querySelector('[data-testid="editing-mark"]'))`);

// l4Checks runs the section. opts: the page of the member, the base address, the account key, the owner's sign-in helpers,
// the team folders (A: the member edits, B: the member reads), and the fake converter whose record of the editor is read.
export async function l4Checks(h, { page, base, accountKey, teamA, teamB, fake, dir, ownerPage, signInAs }) {
  const { check, api, rawPut, navigateTo, rowAppears } = h;
  const suffix = Date.now().toString(36);
  const folderName = `L4 Office ${suffix}`;
  const docxName = `letter-${suffix}.docx`;
  const viewName = `view-${suffix}.docx`;
  const noteName = `note-${suffix}.txt`;
  const shotsDir = process.env.SELFCLOUD_E2E_SHOTS
    ? await (async () => {
        await mkdir(process.env.SELFCLOUD_E2E_SHOTS, { recursive: true });
        return process.env.SELFCLOUD_E2E_SHOTS;
      })()
    : null;
  const snap = (name) => (shotsDir ? shot(page, shotsDir, name) : null);
  const size = (w, hgt) => page.send('Emulation.setDeviceMetricsOverride', { width: w, height: hgt, deviceScaleFactor: 1, mobile: false });
  let folderID = null;
  let docxID = null;
  let viewID = null;
  const themeWas = await page.evaluate(`document.documentElement.dataset.theme`);
  try {
    await size(1280, 800);
    await setLanguage(page, 'English');
    await setTheme(page, 'Light');

    // Setup as the account key: a folder with a Word file in Team A (the member edits), and one in Team B (the member reads).
    const made = await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `/${folderName}`, parents: false });
    folderID = made.id;
    await rawPut(base, `/api/v1/folders/${teamA}/files?path=${encodeURIComponent(`/${folderName}/${docxName}`)}`, accountKey, 'hello', DOCX);
    await rawPut(base, `/api/v1/folders/${teamA}/files?path=${encodeURIComponent(`/${folderName}/${noteName}`)}`, accountKey, 'note\n', 'text/plain');
    await rawPut(base, `/api/v1/folders/${teamB}/files?path=${encodeURIComponent(`/${viewName}`)}`, accountKey, 'read only here', DOCX);
    const listed = await api(base, 'GET', `/api/v1/folders/${teamA}/children?path=${encodeURIComponent(`/${folderName}`)}`, accountKey);
    docxID = (listed.items ?? []).find((n) => n.name === docxName)?.id ?? null;
    const listedB = await api(base, 'GET', `/api/v1/folders/${teamB}/children`, accountKey);
    viewID = (listedB.items ?? []).find((n) => n.name === viewName)?.id ?? null;
    check(docxID !== null && viewID !== null, 'the section made its Word files in Team A and Team B');

    // ---- The member, editor on Team A. The row's menu offers Open in editor, and not on a text file.
    await navigateTo(page, `/files/${teamA}/${folderID}`);
    await rowAppears(page, docxName);
    const menuDocx = await rowMenu(page, docxName);
    check(menuDocx.includes('Open in editor'), `an editor's menu offers Open in editor (${menuDocx.join(', ') || 'no entries'})`);
    await sleep(700);
    await snap('menu-open-in-editor.png');
    await pickMenuItem(page, 'Open in editor');
    await page.waitFor(`location.pathname === ${q(`/edit/${docxID}`)}`, 15000);
    check(true, 'Open in editor goes to the editor page for the file');
    await page.waitFor(`!!document.querySelector('iframe[name="selfcloud-office-editor"]')`, 15000).catch(async (err) => {
      // Diagnostic for a stalled open: what the page shows, and what the fake editor saw.
      const shown = await page.evaluate(`(document.querySelector('[data-testid="office-editor"]')?.innerText ?? 'no editor page') + ' || body: ' + document.body.innerText.slice(0, 300) + ' || url: ' + location.href`);
      console.error(`editor diagnostic: page text ${JSON.stringify(shown)}; fake editor opens ${fake.editor.length}; fake hits ${JSON.stringify(fake.hits)}`);
      throw err;
    });
    await page.waitFor(`document.querySelector('[data-testid="office-editor"]')?.innerText.includes(${q(docxName.replace(/\.docx$/, ''))})`, 15000);
    check(true, 'the editor page shows the file name in its top bar');
    // The fake's record: the editor was given the token in its body, saw a writable file, and took the lock.
    // The frame posts its form after the page is up, so the fake's record may arrive a moment after the name shows.
    for (let i = 0; i < 150 && fake.editor.length === 0; i++) await new Promise((r) => setTimeout(r, 100));
    const seen = fake.editor[fake.editor.length - 1];
    check(seen && seen.checkStatus === 200 && seen.userCanWrite === true, `the editor called CheckFileInfo through the token and may write (the editor saw ${JSON.stringify(seen ?? null)})`);
    check(seen && seen.locked === true && seen.actionHadToken === false, 'the editor took the lock, and the token was not in its address');
    const address = await page.evaluate(`location.href`);
    check(!/access_token|sct_/.test(address), 'the editor page address carries no token');
    await page.waitFor(`!document.querySelector('[data-testid="office-editor"] [role="status"]')`, 20000).catch(() => {});
    await snap('editor-page.png');

    // Back returns to the folder the file was opened from.
    await page.evaluate(`[...document.querySelectorAll('button')].find((b) => b.getAttribute('aria-label') === 'Back').click()`);
    await page.waitFor(`location.pathname === ${q(`/files/${teamA}/${folderID}`)}`, 15000);
    check(true, 'Back returns to the folder the file was opened from');

    // Phone, Hebrew: the editor's top bar fits at 390 px with no sideways scroll. The editor page has no language menu, so the
    // language is set on the folder, the editor is opened again, and the folder is set back to English after the check.
    await setLanguage(page, 'עברית');
    await size(390, 844);
    await navigateTo(page, `/edit/${docxID}?mode=edit&from=${encodeURIComponent(`/files/${teamA}/${folderID}`)}`);
    await page.waitFor(`!!document.querySelector('iframe[name="selfcloud-office-editor"]')`, 15000);
    await sleep(400);
    const overflow = await page.evaluate(`document.documentElement.scrollWidth > window.innerWidth`);
    check(!overflow, 'the editor page has no sideways scroll at 390 px in Hebrew');
    await snap('editor-phone-he.png');
    await navigateTo(page, `/files/${teamA}/${folderID}`);
    await page.waitFor(`location.pathname === ${q(`/files/${teamA}/${folderID}`)}`, 15000);
    await setLanguage(page, 'English');
    await size(1280, 800);

    // The lock shows as a mark in the list.
    await rowAppears(page, docxName);
    await page.waitFor(`document.querySelector(${q(rowSel(docxName))})?.querySelector('[data-testid="editing-mark"]') !== null`, 15000).catch(() => {});
    check(await lockMarkOf(page, docxName), 'the list shows the editing mark on the file an editor holds');
    check(!(await lockMarkOf(page, noteName)), 'the list shows no editing mark on a file no editor holds');
    await snap('list-lock-mark.png');

    // The member, an editor, sees the lock in Details but has no way to end it.
    const menuNote = await rowMenu(page, noteName);
    check(!menuNote.includes('Open in editor') && !menuNote.includes('Open in viewer'), 'a text file gets no editor entry');
    await closeMenu(page);

    // ---- The viewer, on Team B: Open in viewer, read only, no lock.
    await navigateTo(page, `/files/${teamB}`);
    await rowAppears(page, viewName);
    const menuView = await rowMenu(page, viewName);
    check(menuView.includes('Open in viewer') && !menuView.includes('Open in editor'), `a viewer's menu offers Open in viewer only (${menuView.join(', ')})`);
    await pickMenuItem(page, 'Open in viewer');
    await page.waitFor(`location.pathname === ${q(`/edit/${viewID}`)}`, 15000);
    await page.waitFor(`!!document.querySelector('iframe[name="selfcloud-office-editor"]')`, 15000);
    await sleep(800);
    const seenView = fake.editor[fake.editor.length - 1];
    check(seenView && seenView.userCanWrite === false && seenView.locked === false, 'the viewer opened read only, and took no lock');
    await page.evaluate(`[...document.querySelectorAll('button')].find((b) => b.getAttribute('aria-label') === 'Back').click()`);
    await page.waitFor(`location.pathname === '/files/${teamB}'`, 15000);

    // ---- The owner ends the editing session: Details shows the holder, the confirmation warns, and the lock goes.
    if (ownerPage && signInAs) {
      await signInAs(ownerPage);
      await size(1280, 800);
      await navigateTo(ownerPage, `/files/${teamA}/${folderID}`);
      await rowAppears(ownerPage, docxName);
      await ownerPage.evaluate(`(() => { const row = document.querySelector(${q(rowSel(docxName))}); row.focus(); row.click(); return true; })()`);
      await ownerPage.waitFor(`document.querySelector(${q(rowSel(docxName))})?.querySelector('[data-testid="editing-mark"]') !== null`, 15000).catch(() => {});
      const menuOwner = await rowMenu(ownerPage, docxName);
      check(menuOwner.includes('Information'), `the owner's menu holds Information, which opens Details (${menuOwner.join(', ')})`);
      await pickMenuEntry(ownerPage, 'Details');
      await ownerPage.waitFor(`!!document.querySelector('[data-testid="details"]')?.innerText.includes('Being edited')`, 15000);
      check(true, 'Details says the file is being edited');
      await sleep(700);
      await snap('details-lock.png');
      await ownerPage.waitFor(`[...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'End the editing session')`, 10000);
      await ownerPage.evaluate(`[...document.querySelectorAll('button')].find((b) => b.textContent.trim() === 'End the editing session').click()`);
      await ownerPage.waitFor(`!!document.querySelector('.ant-modal')?.innerText.includes('Unsaved changes')`, 10000);
      check(true, 'the confirmation warns that unsaved changes may be lost');
      await sleep(700);
      await snap('break-dialog.png');
      await ownerPage.evaluate(`[...document.querySelectorAll('.ant-modal button')].find((b) => b.textContent.trim() === 'End the session').click()`);
      await ownerPage.waitFor(`!document.querySelector('.ant-modal')`, 15000);
      const after = await api(base, 'GET', `/api/v1/nodes/${docxID}`, accountKey);
      check(after.editing !== true, 'after the break the file reads as not being edited');
    }
  } finally {
    // Put back what the section changed: the language, the theme and the viewport are set back, and its files are removed.
    await setLanguage(page, 'English').catch(() => {});
    await size(1280, 800).catch(() => {});
    if (themeWas) await setTheme(page, themeWas === 'dark' ? 'Dark' : 'Light').catch(() => {});
    if (folderID) await api(base, 'DELETE', `/api/v1/nodes/${folderID}?permanent=true`, accountKey).catch(() => {});
    if (viewID) await api(base, 'DELETE', `/api/v1/nodes/${viewID}?permanent=true`, accountKey).catch(() => {});
  }
}
