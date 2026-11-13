// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice oe1 (decision 102): a phone opens an office file in the same tab. A finger's browser (coarse pointer, no
// hover) at 390 px signs in as an editor, makes a folder in Team A with a blank document in it, and taps the document's row.
// The same tab goes to the editor's address, full page (no app shell), with the file's name in the bar; Close goes back to the
// folder the file was opened from. The folder is removed in a finally.
import { join } from 'node:path';
import { launchBrowser, Page } from './cdp.mjs';
import { sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);

// tap is one touch on the screen: a finger down and up. The browser makes the click from it.
async function tap(page, { x, y }) {
  await page.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] });
  await sleep(60);
  await page.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
}

// oe1Checks runs the section. It needs the run's helpers: the API, the sign-in, navigation, the start-up list of expected
// errors, and the browser binary; the member is an editor of Team A.
export async function oe1Checks(h, { base, accountKey, teamA, dir, browserBinary, MEMBER, EXPECTED, signIn, navigateTo }) {
  const { check, api, rowAppears } = h;
  const suffix = Date.now().toString(36);
  const folderName = `OE1 phone ${suffix}`;
  const docName = 'Untitled document.docx';
  let folderID = null;
  const launch = await launchBrowser(browserBinary, join(dir, 'oe1-phone-profile'), { finger: true });
  const page = new Page(launch.wsUrl);
  page.base = base;
  try {
    await page.start(EXPECTED);
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
    await page.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
    await signIn(page, MEMBER);

    // A fresh folder in Team A, with a blank document made in it by the server.
    const made = await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `/${folderName}`, parents: false });
    folderID = made.id;
    const doc = await api(base, 'POST', `/api/v1/nodes/${folderID}/documents`, accountKey, { type: 'docx' });
    const from = `/files/${teamA}/${folderID}`;
    await navigateTo(page, from);
    await rowAppears(page, doc.name);

    // A finger's tap on the row opens the file in this tab, at the editor's address, with the folder as the place to return to.
    const at = await page.evaluate(
      `(() => { const r = document.querySelector(${q(`[role="row"][aria-label="${doc.name}"]`)}).getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`,
    );
    await tap(page, at);
    await page.waitFor(`location.pathname === ${q(`/edit/${doc.id}`)}`, 15000);
    check(true, 'a finger opens an office file in the same tab, at its editor address');
    const back = await page.evaluate(`new URLSearchParams(location.search).get('from')`);
    check(back === from, `the editor's place to return to is the folder the file was opened from (${back})`);

    // Full page: no app shell, the bar shows the file's name, and nothing scrolls sideways at 390 px.
    await page.waitFor(`!!document.querySelector('[data-testid="office-editor"]')?.innerText.includes(${q(docName.replace(/\.docx$/, ''))})`, 15000);
    check(await page.evaluate(`!document.querySelector('.ant-layout')`), 'the phone editor page is drawn without the app shell');
    check(await page.evaluate(`document.documentElement.scrollWidth <= window.innerWidth`), 'the phone editor page has no sideways scroll at 390 px');

    // Close goes back to the folder, in the same tab.
    await page.evaluate(`[...document.querySelectorAll('button')].find((b) => b.getAttribute('aria-label') === 'Close').click()`);
    await page.waitFor(`location.pathname === ${q(from)}`, 15000);
    check(true, 'Close goes back to the folder the file was opened from, in the same tab');
  } finally {
    page.close();
    launch.proc.kill();
    if (folderID) await api(base, 'DELETE', `/api/v1/nodes/${folderID}?permanent=true`, accountKey).catch(() => {});
  }
}
