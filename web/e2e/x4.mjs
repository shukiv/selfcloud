// SPDX-License-Identifier: AGPL-3.0-or-later

// The saved-playlist checks of the player (slice x4, decision 58), run inside the shared end-to-end run (run.mjs) on the
// shared page and server, or alone with SELFCLOUD_E2E_ONLY=x4. A queue is saved as a playlist; the list shows it; it opens in
// the queue; a file that is trashed shows as unavailable, and the list still plays the rest; a playlist is renamed, its entry
// removed, and the playlist deleted after a confirmation. With SELFCLOUD_X4_SHOTS set to a folder, the screenshots of desktop
// and phone, light and dark, English and Hebrew are written there, each taken once the transitions have settled.
import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const media = fileURLToPath(new URL('./media/', import.meta.url));
const shots = process.env.SELFCLOUD_X4_SHOTS;
const PASSWORD = 'correct horse battery staple';
const MEMBER = { email: 'mateo@example.com', name: 'Mateo Member', password: PASSWORD };
const q = (v) => JSON.stringify(v);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// The antd transitions run for 0.2 s; a shot waits this long after the last change of the page.
const SETTLE_MS = 600;

// until polls a page expression with a bound, and returns when it is true.
async function until(page, expr, ms = 15000, what = expr) {
  const deadline = Date.now() + ms;
  for (;;) {
    if (await page.evaluate(`!!(${expr})`).catch(() => false)) return;
    if (Date.now() > deadline) throw new Error(`timed out (${ms} ms) waiting for: ${what}`);
    await sleep(100);
  }
}

// clickButton clicks the first button whose text (or aria-label) is the given one, inside a root selector.
async function clickButton(page, root, label) {
  const ok = await page.evaluate(`(() => {
    const r = document.querySelector(${q(root)}) ?? document;
    const b = [...r.querySelectorAll('button')].find((x) => (x.getAttribute('aria-label') ?? x.textContent.trim()) === ${q(label)});
    if (!b || b.disabled) return false;
    b.click();
    return true;
  })()`);
  if (!ok) throw new Error(`no enabled button "${label}" in ${root}`);
}

// setInput types a value into a field the way React reads it: through the native setter, then an input event.
async function setInput(page, root, value) {
  await page.evaluate(`(() => {
    const el = document.querySelector(${q(`${root} input`)});
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(el, ${q(value)});
    el.dispatchEvent(new Event('input', { bubbles: true }));
    return true;
  })()`);
}

// openPlayer opens the player on the first file of the listing: the queue panel, with the saved playlists under it, is part
// of the player.
async function openPlayer(page, name = 'one.ogg') {
  await until(page, `!!document.querySelector('[role="row"][aria-label=${q(name)}]')`, 15000, `row ${name}`);
  await page.evaluate(`(() => { document.querySelector('[role="row"][aria-label=${q(name)}]').dispatchEvent(new MouseEvent('dblclick', { bubbles: true })); return true; })()`);
  await until(page, `!!document.querySelector('[data-testid="media-queue"]')`, 15000, 'the queue');
  await until(page, `!!document.querySelector('[data-testid="media-playlists"]')`, 15000, 'the saved playlists');
  // The playlist is a drawer, closed by default (decision 63): its rows are measured here, so it is opened.
  await page.evaluate(`(() => { const t = document.querySelector('.media-playlist-toggle'); if (t && t.getAttribute('aria-expanded') !== 'true') t.click(); })()`);
  await until(page, `document.querySelector('[data-media-drawer]')?.dataset.open === 'true'`, 5000, 'the playlist drawer');
  // The drawer slides in over 200 ms: positions read before it has settled are stale.
  await until(page, `document.querySelector('[data-media-drawer]').getAnimations().length === 0`, 3000, 'the drawer has settled');
}

// apiIn calls the signed-in member's own API, as the page does, and returns its JSON.
async function apiIn(page, method, path, body) {
  return page.evaluate(`(async () => {
    // A write carries the session's CSRF token, read from the signed-in person's own record, as the page sends it.
    const me = await fetch('/api/v1/me', { credentials: 'same-origin', headers: { Accept: 'application/json' } }).then((x) => x.json());
    const headers = { Accept: 'application/json', 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrf_token ?? '' };
    const r = await fetch(${q(path)}, { method: ${q(method)}, credentials: 'same-origin', headers, body: ${body === undefined ? 'undefined' : q(JSON.stringify(body))} });
    return { status: r.status, body: r.status === 204 ? null : await r.json().catch(() => null) };
  })()`);
}

// setPhone sets the phone's size and touch, or puts the desktop back.
async function setPhone(page, on) {
  if (on) {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 800, deviceScaleFactor: 1, mobile: true });
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
  } else {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: false });
  }
}

// setPrefs sets the interface language and theme the way the page keeps them, then reloads the page to take them.
async function setPrefs(page, url, lang, theme) {
  await page.goto(url);
  await page.evaluate(`localStorage.setItem('selfcloud.lang', ${q(lang)}); localStorage.setItem('selfcloud.theme', ${q(theme)}); true`);
  await page.goto(url);
}

// shot writes a screenshot once the page has settled.
async function shot(page, name) {
  if (!shots) return;
  await sleep(SETTLE_MS);
  const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
  await writeFile(join(shots, `${name}.png`), Buffer.from(data, 'base64'));
  console.log(`shot ${join(shots, `${name}.png`)}`);
}

// touchSizes measures the playlist buttons: each must be at least 44 px in both directions on a coarse pointer, and it
// must stay that size under the pointer and when focused, where antd moves things.
async function touchSizes(page, check, what) {
  const sizes = await page.evaluate(`(() => [...document.querySelectorAll('.pl-touch')].map((b) => {
    const r = b.getBoundingClientRect();
    return { w: Math.round(r.width), h: Math.round(r.height), label: b.getAttribute('aria-label') ?? b.textContent.trim() };
  }))()`);
  const small = sizes.filter((s) => s.w < 44 || s.h < 44);
  check(sizes.length > 0 && small.length === 0, `${what}: every playlist button is 44 px on a touch screen (${q(small)})`);
  // Hover and focus: the first button under the pointer and the first focused one keep their size.
  const first = await page.evaluate(`(() => { const b = document.querySelector('.pl-touch'); const r = b.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: first.x, y: first.y });
  await sleep(300);
  const hover = await page.evaluate(`(() => { const r = document.querySelector('.pl-touch').getBoundingClientRect(); return { w: Math.round(r.width), h: Math.round(r.height) }; })()`);
  check(hover.w >= 44 && hover.h >= 44, `${what}: a playlist button under the pointer stays 44 px (${q(hover)})`);
  await page.evaluate(`document.querySelector('.pl-touch').focus(); true`);
  await sleep(300);
  const focus = await page.evaluate(`(() => { const r = document.activeElement.getBoundingClientRect(); return { w: Math.round(r.width), h: Math.round(r.height) }; })()`);
  check(focus.w >= 44 && focus.h >= 44, `${what}: a focused playlist button stays 44 px (${q(focus)})`);
}

// rowGaps checks the buttons of the first saved playlist row sit apart: any two side by side (or on one line) have at least
// GAP_MIN px between them, as the other button rows of the panel do (review of slice x4: Play, Edit, rename and Delete touched).
const GAP_MIN = 6;
async function rowGaps(page, check, what) {
  const gaps = await page.evaluate(`(() => {
    const row = document.querySelector('[data-testid="playlist-row"]');
    const btns = [...row.querySelectorAll('.media-row-buttons .ant-btn')].map((b) => b.getBoundingClientRect());
    const out = [];
    for (let i = 1; i < btns.length; i++) {
      const a = btns[i - 1], b = btns[i];
      if (Math.abs(a.top - b.top) > 4) continue; // wrapped to another line
      out.push(Math.round(Math.min(Math.abs(b.left - a.right), Math.abs(a.left - b.right))));
    }
    return { count: btns.length, gaps: out };
  })()`);
  check(gaps.count >= 4 && gaps.gaps.length > 0 && Math.min(...gaps.gaps) >= GAP_MIN, `${what}: the playlist's buttons have at least ${GAP_MIN} px between them (${q(gaps)})`);
}

export async function x4Checks(h, { page, base, accountKey, accountID, memberID }) {
  const { api, rawPut, check } = h;
  const step = async (name, fn) => {
    console.log(`-- ${name}`);
    await fn();
  };
  // The shared run's interface settings and listing view, put back when the checks end.
  const kept = await page.evaluate(`(() => ({ lang: localStorage.getItem('selfcloud.lang'), theme: localStorage.getItem('selfcloud.theme'), files: localStorage.getItem('selfcloud.files') }))()`);

  // The fixtures: a folder of the run's own with three audio files, and the member can write to it.
  const shared = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'X4 playlists' })).id;
  await api(base, 'PUT', `/api/v1/accounts/${accountID}/folders/${shared}/grants/${memberID}`, accountKey, { level: 'write' });
  await api(base, 'POST', `/api/v1/folders/${shared}/directories`, accountKey, { path: '/audio', parents: false });
  const put = (name, bytes, type) =>
    rawPut(base, `/api/v1/folders/${shared}/files?path=${encodeURIComponent(`/audio/${name}`)}`, accountKey, bytes, type);
  const tone = await readFile(join(media, 'tone.ogg'));
  await put('one.ogg', tone, 'audio/ogg');
  await put('two.ogg', tone, 'audio/ogg');
  await put('three.ogg', tone, 'audio/ogg');
  const children = (await api(base, 'GET', `/api/v1/folders/${shared}/children?path=%2F`, accountKey)).items;
  const audioURL = `${base}/files/${shared}/${children.find((n) => n.name === 'audio')?.id}`;
  const fileID = async (name) => {
    const kids = (await api(base, 'GET', `/api/v1/folders/${shared}/children?path=%2Faudio`, accountKey)).items;
    return kids.find((n) => n.name === name)?.id;
  };

  await h.signOut(page);
  try {
    await step('the member signs in', async () => {
      await h.signIn(page, MEMBER);
    });

    await step('a queue saves as a playlist and the list shows it', async () => {
      await page.goto(audioURL);
      await until(page, `!!document.querySelector('[data-testid="media-add"], button[data-view]')`, 15000, 'the listing');
      await page.evaluate(`(() => { const row = document.querySelector('[role="row"][aria-label="one.ogg"]'); row?.dispatchEvent(new MouseEvent('dblclick', { bubbles: true })); return true; })()`);
      await until(page, `!!document.querySelector('[data-testid="media-queue"]')`, 15000, 'the queue');
      await clickButton(page, '[data-testid="media-playlists"]', 'Save queue').catch(() => clickButton(page, '[data-testid="media-queue"]', 'Save queue'));
      await until(page, `!!document.querySelector('.ant-modal input')`, 10000, 'the save dialog');
      await setInput(page, '.ant-modal', 'Evening mix');
      await clickButton(page, '.ant-modal', 'Save');
      await until(page, `!!document.querySelector('[data-testid="playlist-row"]')`, 10000, 'the saved row');
      const list = await apiIn(page, 'GET', '/api/v1/playlists');
      check(list.status === 200 && list.body.items.length === 1 && list.body.items[0].entry_count === 3, `the list holds one playlist of three files (${q(list)})`);
    });

    await step('a trashed file shows as unavailable, and the rest of the playlist plays', async () => {
      const gone = await fileID('two.ogg');
      await api(base, 'DELETE', `/api/v1/nodes/${gone}`, accountKey);
      await page.goto(audioURL);
      await openPlayer(page);
      await until(page, `!!document.querySelector('[data-testid="playlist-row"]')`, 15000, 'the saved row');
      await clickButton(page, '[data-testid="playlist-row"]', 'Play: Evening mix');
      await until(page, `!!document.querySelector('[data-testid="media-unavailable"]')`, 15000, 'the unavailable row in the queue');
      const off = await page.evaluate(`document.querySelector('[data-testid="media-unavailable"]').textContent`);
      check(/Unavailable/.test(off), `the trashed file reads as unavailable with no name (${q(off)})`);
    });

    await step('the playlist is renamed, an entry removed, and the playlist deleted after a confirmation', async () => {
      await clickButton(page, '[data-testid="playlist-row"]', 'Rename: Evening mix');
      await until(page, `!!document.querySelector('.ant-modal input')`, 10000, 'the rename dialog');
      await setInput(page, '.ant-modal', 'Morning mix');
      await clickButton(page, '.ant-modal', 'Save');
      await until(page, `document.querySelector('[data-testid="playlist-row"]')?.textContent.includes('Morning mix') === true`, 10000, 'the new name');
      await clickButton(page, '[data-testid="playlist-row"]', 'Edit: Morning mix');
      await until(page, `!!document.querySelector('[data-testid="playlist-editor"]')`, 10000, 'the editor');
      const listed = (await apiIn(page, 'GET', '/api/v1/playlists')).body.items[0];
      const before = (await apiIn(page, 'GET', `/api/v1/playlists/${listed.id}`)).body;
      await clickButton(page, '[data-testid="playlist-editor"]', 'Remove: one.ogg');
      await until(page, `document.querySelectorAll('[data-testid="playlist-entry"]').length === ${before.entries.length - 1}`, 10000, 'one fewer entry');
      await clickButton(page, '[data-testid="playlist-row"]', 'Delete: Morning mix');
      await until(page, `!!document.querySelector('.ant-popover button.ant-btn-dangerous, .ant-popconfirm button.ant-btn-dangerous, .ant-popover .ant-btn-primary')`, 10000, 'the confirmation');
      await page.evaluate(`(() => { const b = document.querySelector('.ant-popover button.ant-btn-dangerous, .ant-popconfirm button.ant-btn-dangerous, .ant-popover .ant-btn-primary'); b.click(); return true; })()`);
      await until(page, `!document.querySelector('[data-testid="playlist-row"]')`, 10000, 'the playlist is gone');
      const list = await apiIn(page, 'GET', '/api/v1/playlists');
      check(list.body.items.length === 0, `the playlist is deleted (${q(list.body)})`);
    });

    await step('the saved list reads in the page language and on both themes and widths', async () => {
      // A playlist to show: saved again, so the screenshots have a list with unavailable and reachable rows.
      await apiIn(page, 'POST', '/api/v1/playlists', { name: 'Evening mix' });
      const made = await apiIn(page, 'GET', '/api/v1/playlists');
      const pls = made.body.items[0].id;
      const three = await fileID('one.ogg');
      await apiIn(page, 'PUT', `/api/v1/playlists/${pls}/entries`, { nodes: [three] });
      const matrix = [
        ['en', 'light', 'desktop'],
        ['en', 'dark', 'phone'],
        ['he', 'light', 'phone'],
        ['he', 'dark', 'desktop'],
      ];
      for (const [lang, theme, width] of matrix) {
        // ?lang= comes first in the page's order (the signed-in person's own language is second), so the shot is in this language.
        const url = `${audioURL}?lang=${lang}`;
        await setPrefs(page, url, lang, theme);
        await setPhone(page, width === 'phone');
        await page.goto(url);
        await openPlayer(page);
        await until(page, `!!document.querySelector('[data-testid="playlist-row"]')`, 15000, 'the list');
        const shownLang = await page.evaluate(`document.documentElement.lang + ' ' + document.documentElement.dir`);
        check(shownLang === `${lang} ${lang === 'he' ? 'rtl' : 'ltr'}`, `the page is shown in ${lang} for the saved list (${theme} ${width}): ${shownLang}`);
        await rowGaps(page, check, `${lang} ${theme} ${width}`);
        // One file reads with the singular (review of slice x4: "1 files" and a plural in Hebrew that was wrong).
        const countText = await page.evaluate(`document.querySelector('[data-testid="playlist-row"] .media-row-artist')?.textContent ?? ''`);
        const wantCount = lang === 'he' ? '1 קובץ' : '1 file';
        check(countText === wantCount, `${lang} ${theme} ${width}: one file reads "${wantCount}" (${q(countText)})`);
        if (width === 'phone') await touchSizes(page, check, `${lang} ${theme} phone`);
        await shot(page, `playlists-${lang}-${theme}-${width}`);
        await setPhone(page, false);
      }
    });
  } finally {
    // The last shot leaves the page in Hebrew; the sign-out looks for its English name, and the checks after these
    // expect English, so the page is put back first.
    await page.goto(`${page.base}/files?lang=en`).catch(() => undefined);
    await page.waitFor(`document.documentElement.lang === 'en'`, 10000).catch(() => undefined);
    await h.signOut(page).catch(() => undefined);
    if (kept) {
      await page.evaluate(`(() => { ${kept.lang === null ? "localStorage.removeItem('selfcloud.lang');" : `localStorage.setItem('selfcloud.lang', ${q(kept.lang)});`} ${kept.theme === null ? "localStorage.removeItem('selfcloud.theme');" : `localStorage.setItem('selfcloud.theme', ${q(kept.theme)});`} return true; })()`).catch(() => undefined);
    }
  }
}
