// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice nt2 (decision 108, the template gallery): a phone (375 px, a finger) signs in as an editor of Team A, makes a folder
// there, and opens the gallery for that folder at /new/{fld}/{nod}?kind=document. The sections are Blank, then Letters, Resumes
// and Reports, in that order; a reload of the address shows the gallery again; a built-in's picture loads (its ID has a colon,
// so the path segment is encoded); two tiles sit across at 375 px with no sideways scroll; a tap on a built-in makes a file in
// the folder and opens it in this tab, in the fake editor, and Close is not needed (the folder is removed in a finally).
import { join } from 'node:path';
import { launchBrowser, Page } from './cdp.mjs';

const q = (v) => JSON.stringify(v);

// tap is one touch on the screen: a finger down and up. The browser makes the click from it.
async function tap(page, { x, y }) {
  await page.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] });
  await new Promise((r) => setTimeout(r, 60));
  await page.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
}

// nt2GalleryChecks runs the section. It needs the run's helpers: the API, the sign-in, navigation, the start-up list of expected
// errors, and the browser binary; the member is an editor of Team A.
export async function nt2GalleryChecks(h, { base, accountKey, teamA, dir, browserBinary, MEMBER, EXPECTED, signIn, navigateTo }) {
  const { check, api } = h;
  const suffix = Date.now().toString(36);
  let folderID = null;
  const launch = await launchBrowser(browserBinary, join(dir, 'nt2gallery-profile'), { finger: true });
  const page = new Page(launch.wsUrl);
  page.base = base;
  try {
    await page.start(EXPECTED);
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
    await page.send('Emulation.setDeviceMetricsOverride', { width: 375, height: 812, deviceScaleFactor: 1, mobile: true });
    await signIn(page, MEMBER);

    // A fresh folder in Team A: the gallery offers its templates, and the file made from one lands in it.
    const made = await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `/NT2 gallery ${suffix}`, parents: false });
    folderID = made.id;
    const from = `/files/${teamA}/${folderID}`;
    await navigateTo(page, `/new/${teamA}/${folderID}?kind=document`);
    await page.waitFor(`document.querySelectorAll('[data-section]').length > 1`, 20000);

    const sections = await page.evaluate(`[...document.querySelectorAll('[data-section]')].map((e) => e.getAttribute('data-section'))`);
    check(
      JSON.stringify(sections) === JSON.stringify(['blank', 'letters', 'resumes', 'reports']),
      `the document gallery shows Blank, then Letters, Resumes and Reports (${sections.join(', ')})`,
    );

    // A reload of the gallery's address shows it again (the server answers every /new/ path with the app).
    await page.evaluate(`location.reload(), true`);
    await page.waitFor(`document.querySelectorAll('[data-section]').length > 1`, 20000);
    check(true, 'a reload of the gallery address shows the gallery again');

    // A built-in's picture loads from its encoded path (builtin:letter has a colon in its ID).
    // The picture is lazy: it loads once its tile is near the screen, so the tile is scrolled into view, as a person would scroll.
    await page.evaluate(`document.querySelector('[data-template="builtin:letter"]')?.scrollIntoView({ block: 'center' })`);
    await page.waitFor(`(() => { const i = document.querySelector('[data-template="builtin:letter"] img'); return !!i && i.complete && i.naturalWidth > 0; })()`, 20000);
    check(true, 'the built-in builtin:letter shows its picture at 375 px');

    // Two tiles sit across at a phone's width, and nothing scrolls sideways.
    const across = await page.evaluate(`(() => { const t = [...document.querySelectorAll('[data-section="letters"] [data-template]')]; return t.length >= 2 && Math.round(t[0].getBoundingClientRect().top) === Math.round(t[1].getBoundingClientRect().top) && Math.round(t[0].getBoundingClientRect().left) !== Math.round(t[1].getBoundingClientRect().left); })()`);
    check(across, 'two tiles sit across in a row at 375 px');
    check(await page.evaluate(`document.documentElement.scrollWidth <= window.innerWidth`), 'the gallery has no sideways scroll at 375 px');

    // A finger's tap on a built-in makes the file in the folder, and the same tab opens it in the editor, with the folder as the
    // place to return to (the phone's way, decision 102).
    const at = await page.evaluate(
      `(() => { const r = document.querySelector(${q('[data-template="builtin:letter"]')}).getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`,
    );
    await tap(page, at);
    // The tab first shows the wait page (/edit/wait), then replaces itself with the editor's address, which carries from.
    await page.waitFor(`location.pathname.startsWith('/edit/') && location.pathname !== '/edit/wait'`, 30000);
    const nodeID = await page.evaluate(`location.pathname.split('/')[2]`);
    check(true, 'a tap on a built-in opens the made file in this tab, at its editor address');
    const back = await page.evaluate(`new URLSearchParams(location.search).get('from')`);
    check(back === from, `the editor's place to return to is the folder the gallery was opened for (${back})`);
    await page.waitFor(`!!document.querySelector('[data-testid="office-editor"]')`, 20000);
    check(true, 'the made file opens in the editor');

    // The file is in the folder, as a Word document made from the template.
    const node = await api(base, 'GET', `/api/v1/nodes/${nodeID}`, accountKey);
    check(node.parent_id === folderID, 'the made file is in the folder the gallery was opened for');
    check(/\.docx$/i.test(node.name), `the made file is a Word document (${node.name})`);
  } finally {
    page.close();
    launch.proc.kill();
    if (folderID) await api(base, 'DELETE', `/api/v1/nodes/${folderID}?permanent=true`, accountKey).catch(() => {});
  }
}
