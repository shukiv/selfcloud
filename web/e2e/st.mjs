// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice st (decision 79): the Storage page as mock-up A, in its own browser or inside the shared run (run.mjs), or alone with
// SELFCLOUD_E2E_ONLY=st. The member's My files gets a song, a clip, a picture, a document, a file with an old version and a
// file in the trash. The page is then read in light with a limit, in dark with no limit, in Hebrew, and at 390 px. Checked: the
// meter and its text alternative, the "Free up space" cards, "Go to Trash" landing on the trash, each filter chip showing its
// kind (the server filters, so the list is the largest of that kind), and no sideways scroll. With SELFCLOUD_E2E_SHOTS set to a
// folder, the pictures are saved there. The limit the section sets is taken off again, and the seeded files are removed.
import { setLanguage, setPhone, setTheme, setThemeByPlace, shot, sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);

// The member's own answer, read in the browser so its session is the one that answers.
const meOf = (page) => page.evaluate(`fetch('/api/v1/me?storage=breakdown', { credentials: 'same-origin' }).then((r) => r.json())`);

// put writes one file into the member's My files as the account key, with its media type, and returns nothing.
async function put(h, base, accountKey, pf, name, body, type) {
  await h.rawPut(base, `/api/v1/folders/${pf}/files?path=${encodeURIComponent(`/${name}`)}`, accountKey, body, type);
}

// idOf finds a file of the member's My files by its name, from the page's own largest-files list.
async function idOf(page, name) {
  const items = await page.evaluate(`fetch('/api/v1/me/storage/largest?limit=100', { credentials: 'same-origin' }).then((r) => r.json()).then((j) => j.items)`);
  return items.find((i) => i.name === name)?.id ?? null;
}

// namesShown reads the names in the largest-files table, as the page draws them.
const namesShown = (page) =>
  page.evaluate(`[...document.querySelectorAll('.storage-table tbody th[scope="row"] bdi')].map((b) => b.textContent.trim())`);

// chipClick presses the filter chip with this label, and waits until the chip is pressed and the list is read again (the
// list's skeleton is gone once the server has answered).
async function chipClick(page, label) {
  await page.waitFor(
    `(() => { const b = [...document.querySelectorAll('[role="group"] button')].find((x) => x.textContent.trim() === ${q(label)}); if (!b) return false; b.click(); return true; })()`,
    10000,
  );
  await page.waitFor(`document.querySelector('[role="group"] button[aria-pressed="true"]')?.textContent.trim() === ${q(label)}`, 10000);
  await sleep(300);
  await page.waitFor(`!document.querySelector('.ant-skeleton')`, 10000);
  await sleep(300);
}

// meterAlt is the text alternative of the bar, which says the whole breakdown.
const meterAlt = (page) => page.evaluate(`document.querySelector('.storage-bar')?.getAttribute('aria-label') ?? null`);

// setLimit sets the member's own limit, in bytes; null follows the account default (no limit here).
async function setLimit(h, base, accountKey, accountID, memberID, bytes) {
  await h.api(base, 'PATCH', `/api/v1/accounts/${accountID}/users/${memberID}`, accountKey, { quota_bytes: bytes });
}

export async function stChecks(h, { page, base, accountKey, accountID, memberID, MEMBER, dir, shotsDir = process.env.SELFCLOUD_E2E_SHOTS || null }) {
  const { check, api, navigateTo } = h;
  const suffix = Date.now().toString(36);
  const names = {
    song: `st-song-${suffix}.mp3`,
    clip: `st-clip-${suffix}.webm`,
    cover: `st-cover-${suffix}.png`,
    report: `st-report-${suffix}.pdf`,
    notes: `st-notes-${suffix}.txt`,
    old: `st-old-${suffix}.txt`,
  };
  const snap = (name) => (shotsDir ? shot(page, shotsDir, name) : null);
  let pf = null;
  let trashed = null;
  try {
    pf = (await meOf(page)).personal_folder_id;
    check(typeof pf === 'string' && pf !== '', 'the member has a My files folder to read');

    // The files: four kinds, a file with an old version, and a file in the trash.
    await put(h, base, accountKey, pf, names.song, 'a'.repeat(3000), 'audio/mpeg');
    await put(h, base, accountKey, pf, names.clip, 'v'.repeat(2000), 'video/webm');
    await put(h, base, accountKey, pf, names.cover, 'p'.repeat(1000), 'image/png');
    await put(h, base, accountKey, pf, names.report, 'd'.repeat(1500), 'application/pdf');
    await put(h, base, accountKey, pf, names.notes, 'n'.repeat(600), 'text/plain');
    const notesID = await idOf(page, names.notes);
    const notes = await api(base, 'GET', `/api/v1/nodes/${notesID}`, accountKey);
    const res = await fetch(`${base}/api/v1/nodes/${notesID}/content`, {
      method: 'PUT',
      headers: { Authorization: `Bearer ${accountKey}`, 'If-Match': notes.etag, 'Content-Type': 'text/plain' },
      body: 'm'.repeat(200),
    });
    check(res.ok, `a new version of the notes file is written (${res.status})`);
    await put(h, base, accountKey, pf, names.old, 'o'.repeat(400), 'text/plain');
    trashed = await idOf(page, names.old);
    await api(base, 'DELETE', `/api/v1/nodes/${trashed}`, accountKey);

    // Light, with a limit of 1 GB.
    await setLimit(h, base, accountKey, accountID, memberID, 1_000_000_000);
    await navigateTo(page, '/storage');
    await page.waitFor(`!!document.querySelector('.storage-bar')`, 15000);
    const alt = await meterAlt(page);
    check(alt !== null && /of 1 GB used/.test(alt) && /old versions/.test(alt) && /in the trash/.test(alt) && /free/.test(alt),
      `the meter's text alternative names the limit, the old versions, the trash and the free part (${alt})`);
    const cards = await page.evaluate(`[...document.querySelectorAll('.storage-free-card')].map((c) => c.textContent)`);
    check(cards.some((t) => /They count until the trash is emptied/.test(t)) && cards.some((t) => /earlier versions/.test(t)),
      `the Free up space cards show the trash and the old versions (${cards.length} cards)`);
    await snap('st-light-limit.png');

    // Go to Trash lands on the trash.
    await page.evaluate(`[...document.querySelectorAll('.storage-free-card a')].find((a) => a.textContent.trim().length > 0).click(), true`);
    await page.waitFor(`location.pathname === '/trash'`, 15000);
    check(true, 'Go to Trash lands on the trash');
    await navigateTo(page, '/storage');
    await page.waitFor(`!!document.querySelector('[role="group"]')`, 15000);

    // Each chip shows its kind, read by the server.
    await chipClick(page, 'Audio');
    let shown = await namesShown(page);
    check(shown.includes(names.song) && !shown.includes(names.clip) && !shown.includes(names.cover), `the Audio chip shows the audio only (${shown.join(', ')})`);
    await chipClick(page, 'Video');
    shown = await namesShown(page);
    check(shown.includes(names.clip) && !shown.includes(names.song), `the Video chip shows the video only (${shown.join(', ')})`);
    await chipClick(page, 'Pictures');
    shown = await namesShown(page);
    check(shown.includes(names.cover) && !shown.includes(names.report), `the Pictures chip shows the picture only (${shown.join(', ')})`);
    await chipClick(page, 'Documents');
    shown = await namesShown(page);
    check(shown.includes(names.report) && shown.includes(names.notes) && !shown.includes(names.song),
      `the Documents chip shows the document and the text file (${shown.join(', ')})`);
    await chipClick(page, 'In the trash');
    shown = await namesShown(page);
    check(shown.includes(names.old) && !shown.includes(names.song), `the In the trash chip shows the trashed file only (${shown.join(', ')})`);
    await chipClick(page, 'All files');
    shown = await namesShown(page);
    check(shown.includes(names.song) && shown.includes(names.clip), 'All files shows every kind again');
    const sideways = await page.evaluate(`document.documentElement.scrollWidth - document.documentElement.clientWidth`);
    check(sideways <= 0, `the Storage page has no sideways scroll at 1280 px (overflow ${sideways}px)`);

    // Dark, with no limit: no free part, and the bar says no limit. A limit of 0 is no limit (null would follow the account's
    // default, which this account may set). The page is left and opened again, so it reads the limit anew.
    await setLimit(h, base, accountKey, accountID, memberID, 0);
    await navigateTo(page, '/recent');
    await page.waitFor(`location.pathname === '/recent' && !document.querySelector('.storage-bar')`, 15000);
    await navigateTo(page, '/storage');
    await page.waitFor(`location.pathname === '/storage'`, 15000);
    await page.waitFor(`!!document.querySelector('.storage-bar')`, 15000);
    await sleep(500);
    await setTheme(page, 'Dark');
    await page.waitFor(`document.documentElement.dataset.theme === 'dark'`, 10000);
    const darkAlt = await meterAlt(page);
    check(darkAlt !== null && /no limit/.test(darkAlt) && !/ free/.test(darkAlt), `dark with no limit: the bar says so and has no free part (${darkAlt})`);
    await snap('st-dark-nolimit.png');

    // Hebrew, right to left, in dark and then in light.
    await setLanguage(page, 'עברית');
    await page.waitFor(`document.documentElement.dir === 'rtl'`, 10000);
    const heAlt = await meterAlt(page);
    check(heAlt !== null, 'in Hebrew the meter still has its text alternative');
    await snap('st-he-dark.png');
    await setThemeByPlace(page, 1, 'light');
    await page.waitFor(`document.documentElement.dataset.theme === 'light'`, 10000);
    await snap('st-he-light.png');

    // Phone width, in Hebrew and light: the cards one under the other, and no sideways scroll.
    await setPhone(page, true);
    await page.waitFor(`!!document.querySelector('.storage-bar')`, 15000);
    const phone = await page.evaluate(`(() => ({ overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth, width: document.documentElement.clientWidth }))()`);
    check(phone.overflow <= 0 && phone.width <= 390, `at 390 px the Storage page has no sideways scroll (overflow ${phone.overflow}px, width ${phone.width}px)`);
    // The table's wrapper hides overflow, so the page can pass the scroll check while its columns are cut off: the row
    // actions must sit inside the screen.
    const reach = await page.evaluate(`(() => { const b = [...document.querySelectorAll('.storage-table tbody button')]; return b.length ? Math.max(...b.map((x) => x.getBoundingClientRect().right)) : -1; })()`);
    check(reach > 0 && reach <= phone.width, `at 390 px the row actions are on the screen (rightmost action at ${Math.round(reach)} px of ${phone.width} px)`);
    await snap('st-phone.png');
    await setPhone(page, false);
  } finally {
    // Put the limit and the language back, and remove the seeded files for good where the server allows it.
    await setLimit(h, base, accountKey, accountID, memberID, null).catch(() => undefined);
    await setLanguage(page, 'English').catch(() => undefined);
    await setTheme(page, 'Light').catch(() => undefined);
    await setPhone(page, false).catch(() => undefined);
    for (const name of Object.values(names)) {
      const id = await idOf(page, name).catch(() => null);
      if (id) await api(base, 'DELETE', `/api/v1/nodes/${id}`, accountKey).catch(() => undefined);
    }
  }
}
