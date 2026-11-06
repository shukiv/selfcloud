// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice l6: New documents (decision 91). In a team folder, the sidebar's New -> Document makes a blank Word file in the folder
// shown and opens it in the office editor in the same tab. The fake Collabora (convertFake.mjs) receives CheckFileInfo for the
// new file under the name "Untitled document.docx", and Back returns to the folder, which lists the file. The section makes its
// own folder inside Team A, so the default name is the one the server gives on a first document there. The folder is removed
// in a finally, and the language and the viewport are put back.
import { setLanguage, setTheme, sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);

// pickMenuItem clicks the open menu's entry with this text.
async function pickMenuItem(page, text) {
  await page.waitFor(`[...document.querySelectorAll('[role="menuitem"]')].some((x) => x.textContent.trim() === ${q(text)})`, 10000);
  await page.evaluate(`[...document.querySelectorAll('[role="menuitem"]')].find((x) => x.textContent.trim() === ${q(text)}).click()`);
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
    await page.evaluate(`document.querySelector('[data-testid="new-button"]').click()`);
    await pickMenuItem(page, 'Document');
    await page.waitFor(`location.pathname.startsWith('/edit/')`, 15000);
    newID = (await page.evaluate(`location.pathname`)).replace('/edit/', '');
    const created = await api(base, 'GET', `/api/v1/nodes/${newID}`, accountKey);
    check(created.name === docName && created.kind === 'file', `New -> Document made "${docName}" in the folder (${created.name})`);
    check(created.folder_id === teamA, 'the new file is in Team A');
    const fromEditor = await page.evaluate(`new URLSearchParams(location.search).get('from')`);
    check(fromEditor === `/files/${teamA}/${folderID}`, `the editor's Back goes to the folder the file was made in (${fromEditor})`);

    // The editor page loads the frame, and the fake receives CheckFileInfo for the new file under its name.
    const before = fake.editor.length;
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

    // Back returns to the folder, and the folder lists the new file.
    await page.waitFor(`!!document.querySelector('button[aria-label="Back"]')`, 15000);
    await page.evaluate(`[...document.querySelectorAll('button')].find((b) => b.getAttribute('aria-label') === 'Back').click()`);
    await page.waitFor(`location.pathname === ${q(`/files/${teamA}/${folderID}`)}`, 15000);
    await rowAppears(page, docName);
    check(true, 'Back returns to the folder, which lists "Untitled document.docx"');
  } finally {
    await setLanguage(page, 'English').catch(() => {});
    await size(1280, 800).catch(() => {});
    if (themeWas) await setTheme(page, themeWas === 'dark' ? 'Dark' : 'Light').catch(() => {});
    if (folderID) await api(base, 'DELETE', `/api/v1/nodes/${folderID}?permanent=true`, accountKey).catch(() => {});
  }
}
