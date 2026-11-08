// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice l6: New documents (decision 91). In a team folder, the sidebar's New -> Document makes a blank Word file in the folder
// shown and opens it in the office editor in the same tab. The fake Collabora (convertFake.mjs) receives CheckFileInfo for the
// new file under the name "Untitled document.docx", and Back returns to the folder, which lists the file. The section makes its
// own folder inside Team A, so the default name is the one the server gives on a first document there. The folder is removed
// in a finally, and the language and the viewport are put back.
import { setLanguage, setTheme, sleep } from './media.mjs';
import { openedTab } from './cdp.mjs';

const q = (v) => JSON.stringify(v);

// pickMenuItem clicks the entry with this text in a dropdown that is open now (antd marks a closed one ant-dropdown-hidden).
async function pickMenuItem(page, text) {
  const visible = `[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) [role="menuitem"]')].filter((x) => x.textContent.trim() === ${q(text)})`;
  await page.waitFor(`${visible}.length > 0`, 10000);
  // With a user gesture, as a person's click is: the new document's tab is a pop-up, which the browser blocks without one.
  await page.send('Runtime.evaluate', { expression: `${visible}[0].click()`, userGesture: true, returnByValue: true, awaitPromise: true });
}

// l6Checks runs the section. opts: the member's page (an editor on Team A), the base address, the account key, the team folder
// (A), and the fake converter whose record of the editor is read.
export async function l6Checks(h, { page, base, accountKey, teamA, fake }) {
  const { check, api, navigateTo, rowAppears } = h;
  const suffix = Date.now().toString(36);
  const folderName = `L6 New ${suffix}`;
  const docName = 'Untitled document.docx';
  let folderID = null;
  let newID = null;
  const themeWas = await page.evaluate(`document.documentElement.dataset.theme`);
  const size = (w, hgt) => page.send('Emulation.setDeviceMetricsOverride', { width: w, height: hgt, deviceScaleFactor: 1, mobile: false });
  try {
    await size(1280, 800);
    await setLanguage(page, 'English');
    await setTheme(page, 'Light');

    // A fresh folder in Team A, so the first blank document there is named by the default alone.
    const made = await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `/${folderName}`, parents: false });
    folderID = made.id;
    await navigateTo(page, `/files/${teamA}/${folderID}`);
    await page.waitFor(`location.pathname === ${q(`/files/${teamA}/${folderID}`)}`, 15000);

    // The sidebar's New menu holds the three documents after the uploads. Document opens the new file in the editor.
    await page.waitFor(`!!document.querySelector('[data-testid="new-button"]') && !document.querySelector('[data-testid="new-button"]').disabled`, 15000);
    // The fake's record is read from here on: the editor may call CheckFileInfo as soon as the file opens.
    const before = fake.editor.length;
    // A dropdown left open by an earlier step would be the one the pick reads: close every open dropdown first.
    await page.evaluate(`document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))`);
    await page.waitFor(`document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden)').length === 0`, 10000);
    await page.evaluate(`document.querySelector('[data-testid="new-button"]').click()`);
    // The new document opens in its own tab (decision 102, round 2 R5): the listing stays here; the file's editor is there.
    const listingPage = page;
    await pickMenuItem(page, 'Document');
    page = await openedTab(listingPage, '/edit/wait');
    await page.waitFor(`location.pathname.startsWith('/edit/') && location.pathname !== '/edit/wait'`, 15000);
    newID = (await page.evaluate(`location.pathname`)).replace('/edit/', '');
    const created = await api(base, 'GET', `/api/v1/nodes/${newID}`, accountKey);
    check(created.name === docName && created.kind === 'file', `New -> Document made "${docName}" in the folder (${created.name})`);
    check(created.folder_id === teamA, `the new file is in Team A (its folder ${created.folder_id}, Team A ${teamA}; the directory ${folderID} is in ${made.folder_id}; the file's parent ${created.parent_id})`);
    const fromEditor = await page.evaluate(`new URLSearchParams(location.search).get('from')`);
    check(fromEditor === `/files/${teamA}/${folderID}`, `the editor's Back goes to the folder the file was made in (${fromEditor})`);

    // The editor page loads the frame, and the fake receives CheckFileInfo for the new file under its name.
    await page.waitFor(`!!document.querySelector('iframe[name="selfcloud-office-editor"]')`, 15000);
    await page.waitFor(`document.querySelector('[data-testid="office-editor"]')?.innerText.includes(${q(docName.replace(/\.docx$/, ''))})`, 15000);
    check(true, 'the editor page shows "Untitled document" in its top bar');
    let seen = null;
    for (let i = 0; i < 150 && !seen; i++) {
      seen = fake.editor.slice(before).find((e) => e.name === docName) ?? null;
      if (!seen) await sleep(100);
    }
    check(seen !== null && seen.checkStatus === 200 && seen.userCanWrite === true,
      `the fake editor received CheckFileInfo for "${docName}" and may write (${JSON.stringify(seen)})`);

    // Close ends the editor's tab, and the listing shows the folder, with the new file in it.
    await page.waitFor(`!!document.querySelector('button[aria-label="Close"]')`, 15000);
    await page.evaluate(`[...document.querySelectorAll('button')].find((b) => b.getAttribute('aria-label') === 'Close').click()`);
    page = listingPage;
    await page.waitFor(`location.pathname === ${q(`/files/${teamA}/${folderID}`)}`, 15000);
    await rowAppears(page, docName);
    check(true, 'Back returns to the folder, which lists "Untitled document.docx"');

    // The page's own New (FilesPage's entry, not the sidebar's) makes the second file in the same folder; the name takes the
    // first free number. Back lists both files.
    await page.evaluate(`document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))`);
    await page.waitFor(`document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden)').length === 0`, 10000);
    // FilesPage's own New is shown where the sidebar does not hold the New button: a narrow window, as an icon labelled New.
    await size(400, 800);
    const pageNew = `[...document.querySelectorAll('button[aria-label]')].find((b) => b.getAttribute('aria-label') === 'New' && !b.closest('.shell-sider'))`;
    await page.waitFor(`!!${pageNew}`, 15000);
    await page.evaluate(`${pageNew}.click()`);
    const listingPage2 = page;
    await pickMenuItem(page, 'Document');
    page = await openedTab(listingPage2, '/edit/wait');
    await page.waitFor(`location.pathname.startsWith('/edit/') && location.pathname !== '/edit/wait'`, 15000);
    const second = (await page.evaluate(`location.pathname`)).replace('/edit/', '');
    const made2 = await api(base, 'GET', `/api/v1/nodes/${second}`, accountKey);
    check(made2.name === 'Untitled document (1).docx' && made2.folder_id === teamA, `the page's own New makes "${made2.name}" in Team A (${made2.folder_id})`);
    await page.waitFor(`!!document.querySelector('button[aria-label="Close"]')`, 15000);
    await page.evaluate(`[...document.querySelectorAll('button')].find((b) => b.getAttribute('aria-label') === 'Close').click()`);
    page = listingPage2;
    await page.waitFor(`location.pathname === ${q(`/files/${teamA}/${folderID}`)}`, 15000);
    await rowAppears(page, docName);
    await rowAppears(page, made2.name);
    check(true, 'the page\'s own New: Back lists both "Untitled document.docx" and "Untitled document (1).docx"');
  } finally {
    await setLanguage(page, 'English').catch(() => {});
    await size(1280, 800).catch(() => {});
    if (themeWas) await setTheme(page, themeWas === 'dark' ? 'Dark' : 'Light').catch(() => {});
    if (folderID) await api(base, 'DELETE', `/api/v1/nodes/${folderID}?permanent=true`, accountKey).catch(() => {});
  }
}
