// SPDX-License-Identifier: AGPL-3.0-or-later

// The Media page checks (slice mp1, decision 186), run inside the shared end-to-end run (run.mjs) on the shared page,
// server and accounts: the sidebar's Media entry right after Starred, the page's empty state with its way back to My
// files, a file played from My files that goes on in the mini player and then on the Media page — the same element,
// marked, never made again — with the queue table below it (mp1b: a click on row 2 plays it in the page's one player)
// and no mini player beside it, the mini player back on My files, the page's Stop and close that ends the session, and
// the page fitting a phone with no sideways scroll. Run it
// alone with SELFCLOUD_E2E_ONLY=mp1. The checks end signed in as the member, which the rest of the run expects; the
// interface's language and theme and the listing's view are put back afterwards.
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const media = fileURLToPath(new URL('./media/', import.meta.url));
const PASSWORD = 'correct horse battery staple';
// The same people as the shared run's owner and member (run.mjs), who sign in with these.
const OWNER = { email: 'olivia@example.com', name: 'Olivia Owner', password: PASSWORD };
const MEMBER = { email: 'mateo@example.com', name: 'Mateo Member', password: PASSWORD };
const q = (v) => JSON.stringify(v);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function step(name, fn) {
  console.log(`-- ${name}`);
  await fn();
}

// until polls a page expression with a bound, and returns when it is true.
async function until(page, expr, ms = 15000, what = expr) {
  const deadline = Date.now() + ms;
  for (;;) {
    if (await page.evaluate(`!!(${expr})`).catch(() => false)) return;
    if (Date.now() > deadline) {
      const st = await page.evaluate(`JSON.stringify({ path: location.pathname, text: document.body.innerText.slice(0, 300) })`).catch((e) => `state-unavailable ${e}`);
      const unended = [...(page.inflight?.values() ?? [])].map((r) => `${r.url.startsWith(page.base) ? r.url.slice(page.base.length) : r.url.slice(0, 80)} status ${r.status} for ${Date.now() - r.at} ms`).join(' ; ') || 'none';
      throw new Error(`timed out (${ms} ms) waiting for: ${what} | DIAG ${st} | requests not ended: ${unended}`);
    }
    await sleep(100);
  }
}

// dblClick sends a real mouse double click at a point, the way Chrome sends it for a person.
async function dblClick(page, { x, y }) {
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
  for (const clickCount of [1, 2]) {
    await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', buttons: 1, clickCount });
    await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', buttons: 0, clickCount });
    await sleep(100);
  }
}

async function rowPoint(page, name) {
  const p = await page.evaluate(`(() => {
    const row = document.querySelector(${q(`[role="row"][aria-label=${JSON.stringify(name)}]`)});
    if (!row) return null;
    row.scrollIntoView({ block: 'center' });
    const r = (row.querySelector('bdi') ?? row).getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  })()`);
  if (!p) throw new Error(`no row named ${name}`);
  return p;
}

// openFolder shows a folder's listing in List view, with nothing selected.
async function openFolder(page, url, firstName) {
  await page.goto(url);
  await page.evaluate(`localStorage.setItem('selfcloud.files', JSON.stringify({ view: 'list', sort: { key: 'name', dir: 'asc' } }))`);
  await page.goto(url);
  await until(page, `!!document.querySelector('button[data-view]')`, 15000, 'the view toggle');
  await page.evaluate(`(() => { const b = document.querySelector('button[data-view]'); if (b.dataset.view === 'list') b.click(); })()`);
  await until(page, `!!document.querySelector('[role="row"][aria-label=${JSON.stringify(firstName)}]')`, 15000, `row ${firstName}`);
}

// The element the session plays, wherever its box is: the viewer's screen, the page's stage or the hidden viewer.
const MEDIA = `document.querySelector('[data-testid="media-screen"] video, [data-testid="media-screen"] audio')`;

async function playing(page, ms = 20000) {
  await until(page, `(() => { const v = ${MEDIA}; return !!v && v.readyState >= 2; })()`, ms, 'the file has data');
  await until(page, `(() => { const v = ${MEDIA}; return !!v && !v.paused && v.currentTime > 0.2; })()`, 15000, 'the file plays');
}

// viewerShown says the viewer's dialog is on screen (its test id is only borne while it is the dialog).
const viewerShown = `(() => { const v = document.querySelector('[data-testid="preview"]'); return !!v && getComputedStyle(v).display !== 'none'; })()`;

async function closeViewer(page) {
  await page.evaluate(`document.querySelector('[data-testid="preview"] header button').click()`);
  await until(page, `!(${viewerShown})`, 10000, 'the viewer closes');
}

// setPhone is the phone these checks measure: 390 by 844, a touch screen.
async function setPhone(page, on) {
  if (on) {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
  } else {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: false });
  }
}

// stopOnPage presses the page's Stop and close, the X of the session's header on the page's stage.
async function stopOnPage(page) {
  await page.evaluate(`[...document.querySelectorAll('[data-testid="media-page-stage"] button')].find((b) => b.getAttribute('aria-label') === 'Stop and close')?.click()`);
}

// clickTab presses one of the Media page's tabs by its label.
async function clickTab(page, label) {
  await page.evaluate(`[...document.querySelectorAll('[role="tab"]')].find((t) => t.textContent.trim() === ${q(label)})?.click()`);
}

// clickRow presses a library row by its name, the way a person's single click plays it. The row is found inside its tab's
// region: a tab already opened keeps its rows mounted (hidden), so a bare name search could find another tab's row.
const rowSel = (region, name) => `[role="region"][aria-label=${JSON.stringify(region)}] [role="row"][aria-label=${JSON.stringify(name)}]`;
async function clickRow(page, region, name) {
  await page.evaluate(`document.querySelector(${q(rowSel(region, name))})?.click()`);
}

// waitListed waits until the search lists the file under its type: the search indexes a file by a job after the upload.
// It asks every 2 s, well under the search's sixty a minute per caller, so a slow index on a loaded machine cannot spend
// the account key's minute and refuse the later sections' searches. A 429 is not a failure: it waits 10 s and asks again,
// within the deadline.
async function waitListed(api, base, accountKey, type, name) {
  const deadline = Date.now() + 45000;
  for (;;) {
    if (Date.now() > deadline) throw new Error(`${name} was not listed in the ${type} search in 45 seconds`);
    let found;
    try {
      found = await api(base, 'GET', `/api/v1/search?type=${type}&kind=file&limit=50`, accountKey);
    } catch (e) {
      if (!/: 429\b/.test(e.message)) throw e;
      await sleep(10000);
      continue;
    }
    if (found.items.some((n) => n.name === name)) return;
    await sleep(2000);
  }
}

// waitResumeSaved waits until a resume place written after the pause has left for the server and the answer came back.
async function waitResumeSaved(page, before) {
  const isResume = (r) => r.method === 'PUT' && r.url.includes('/resume');
  const deadline = Date.now() + 15000;
  for (;;) {
    const sent = page.sent.filter(isResume).length;
    const pending = [...(page.inflight?.values() ?? [])].some((r) => r.url.includes('/resume'));
    if (sent > before && !pending) return;
    if (Date.now() > deadline) throw new Error('the resume place was not saved in 15 seconds');
    await sleep(100);
  }
}

export async function mp1Checks(h, { page, base, accountKey, accountID }) {
  const { api, rawPut, check } = h;
  const signIn = (who) => h.signIn(page, who);
  const signOut = () => h.signOut(page);
  // The shared run's interface settings and listing view, put back when the checks end.
  const kept = await page.evaluate(`(() => ({ lang: localStorage.getItem('selfcloud.lang'), theme: localStorage.getItem('selfcloud.theme'), files: localStorage.getItem('selfcloud.files') }))()`);
  // The fixtures: a folder of the owner's own with two audio files, one of them played and marked.
  const folder = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'MP1 media' })).id;
  const tone = await readFile(join(media, 'tone.ogg'));
  for (const name of ['track-one.ogg', 'track-two.ogg']) {
    await rawPut(base, `/api/v1/folders/${folder}/files?path=${encodeURIComponent(`/${name}`)}`, accountKey, tone, 'audio/ogg');
  }
  // A tagged song and a video, for the library tabs (mp4): the Music tab reads the tags, the Videos tab lists the video.
  const tagged = await readFile(join(media, 'tag24.mp3'));
  await rawPut(base, `/api/v1/folders/${folder}/files?path=${encodeURIComponent('/tag24.mp3')}`, accountKey, tagged, 'audio/mpeg');
  const clip = await readFile(join(media, 'clip.webm'));
  await rawPut(base, `/api/v1/folders/${folder}/files?path=${encodeURIComponent('/clip.webm')}`, accountKey, clip, 'video/webm');
  const folderURL = `${base}/files/${folder}`;

  await signOut();
  try {
    await signIn(OWNER);
    // The words these checks read are the English ones; the theme is the light one.
    await page.goto(base + '/');
    await page.evaluate(`localStorage.setItem('selfcloud.lang', 'en'); localStorage.setItem('selfcloud.theme', 'light'); true`);
    await page.goto(base + '/');

    await step('Media sits right after Starred in the sidebar', async () => {
      await until(page, `!!document.querySelector('[data-testid="account-menu"]')`, 15000, 'the shell');
      const texts = await page.evaluate(`[...document.querySelectorAll('[role="menu"] [role="menuitem"]')].map((x) => x.textContent.trim())`);
      const i = texts.indexOf('Starred');
      check(i >= 0 && texts[i + 1] === 'Media', `Media is right after Starred (${texts.join(', ')})`);
    });

    await step('the Media page with no session offers the way back to My files', async () => {
      await h.clickNav(page, 'Media');
      await until(page, `location.pathname === '/media'`, 10000, 'the Media page');
      await until(page, `document.body.innerText.includes('Nothing is playing')`, 15000, 'the empty state');
      check(await page.evaluate(`document.body.innerText.includes('Play a video or a song from your files and it shows here.')`), 'the empty state says what to do');
      await page.evaluate(`[...document.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Go to My files').click()`);
      // My files opens at /files and settles on the root folder's own address.
      await until(page, `location.pathname === '/files' || location.pathname.startsWith('/files/')`, 10000, 'My files');
    });

    await step('a file played from the folder goes on in the mini player, marked', async () => {
      await openFolder(page, folderURL, 'track-one.ogg');
      await dblClick(page, await rowPoint(page, 'track-one.ogg'));
      await until(page, viewerShown, 15000, 'the viewer opens');
      await playing(page);
      await closeViewer(page);
      await until(page, `!!document.querySelector('[data-testid="media-mini"]')`, 10000, 'the mini player');
      await page.evaluate(`(() => { const el = document.querySelector('audio'); el.__mp1 = 1; window.__mp1el = el; return true; })()`);
    });

    await step('the Media page hosts the same element, with the queue below and no mini player', async () => {
      await h.clickNav(page, 'Media');
      await until(page, `location.pathname === '/media'`, 10000, 'the Media page');
      await until(page, `!!document.querySelector('[data-testid="media-page-stage"] audio')`, 15000, 'the player on the page stage');
      check(await page.evaluate(`document.querySelector('[data-testid="media-page-stage"] audio') === window.__mp1el && window.__mp1el.__mp1 === 1`), 'the same marked element is on the page stage');
      // The table is lazy (mp1b), so it arrives a tick after the page's host is given.
      await until(page, `!!document.querySelector('[data-testid="media-page-queue"] [data-testid="media-queue-table"]')`, 15000, 'the queue table in the page host');
      check(await page.evaluate(`!!document.querySelector('[data-testid="media-page-queue"] [data-testid="media-queue-table"]')`), 'the queue table is in the page queue host');
      check(await page.evaluate(`!document.querySelector('[data-testid="media-mini"]') && !document.querySelector('[data-testid="preview"]')`), 'no mini player and no dialog beside the page');
      check(await page.evaluate(`!window.__mp1el.paused`), 'the file plays on');
      const t1 = await page.evaluate(`window.__mp1el.currentTime`);
      await sleep(1200);
      const t2 = await page.evaluate(`window.__mp1el.currentTime`);
      check(t2 > t1 + 0.5, `the place goes on (${t1.toFixed(2)} s, then ${t2.toFixed(2)} s), not restarted`);
    });

    await step('the page queue is a table, and a click on row 2 plays it in the page player', async () => {
      // The folder holds four playable files (mp4 adds a tagged song and a video for the library tabs), so the queue
      // lists four rows; the two tracks are found by name, not by their place in the list.
      const qrow = (name) => `[data-testid="media-page-queue"] [data-testid="media-qrow"][aria-label=${JSON.stringify(name)}]`;
      await until(page, `document.querySelectorAll('[data-testid="media-page-queue"] [data-testid="media-qrow"]').length === 4`, 15000, 'the four queue rows');
      check(await page.evaluate(`!!document.querySelector('[data-testid="media-page-queue"] [data-testid="media-queue-table"]') && !document.querySelector('[data-testid="media-page-queue"] [data-testid="media-row"]')`), 'the queue is the table, with no drawer rows');
      check(await page.evaluate(`(() => { const on = document.querySelector(${q(qrow('track-one.ogg'))}); const off = document.querySelector(${q(qrow('track-two.ogg'))}); return !!on && !!off && on.classList.contains('media-qrow--on') && !off.classList.contains('media-qrow--on'); })()`), 'the playing row is marked');
      // A click on track-two's name plays it through the session's one player, on the page's stage.
      const p = await page.evaluate(`(() => {
        const row = document.querySelector(${q(qrow('track-two.ogg'))});
        row.scrollIntoView({ block: 'center' });
        const r = (row.querySelector('bdi') ?? row).getBoundingClientRect();
        return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
      })()`);
      await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: p.x, y: p.y, button: 'left', buttons: 1, clickCount: 1 });
      await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: p.x, y: p.y, button: 'left', buttons: 0, clickCount: 1 });
      await until(page, `(() => { const v = ${MEDIA}; return !!v && v.getAttribute('aria-label') === 'track-two.ogg' && v.readyState >= 2; })()`, 20000, 'row 2 has data');
      await playing(page);
      check(await page.evaluate(`document.querySelectorAll('audio').length === 1 && document.querySelector('[data-testid="media-page-stage"] audio') === document.querySelector('audio')`), 'one player, in the page stage, plays row 2');
      check(await page.evaluate(`(() => { const on = document.querySelector(${q(qrow('track-two.ogg'))}); const off = document.querySelector(${q(qrow('track-one.ogg'))}); return !!on && !!off && on.classList.contains('media-qrow--on') && !off.classList.contains('media-qrow--on'); })()`), 'the mark moves to row 2');
      // The element is made once for the file that plays (decision 186): carry the identity mark to the session's current
      // element, so the checks that follow judge the same one.
      await page.evaluate(`(() => { const el = document.querySelector('audio'); el.__mp1 = 1; window.__mp1el = el; return true; })()`);
    });

    await step('My files brings the mini player back with the same element', async () => {
      await h.clickNav(page, 'My files');
      await until(page, `location.pathname === '/files' || location.pathname.startsWith('/files/')`, 10000, 'My files');
      await until(page, `!!document.querySelector('[data-testid="media-mini"]')`, 10000, 'the mini player');
      check(await page.evaluate(`document.querySelector('audio') === window.__mp1el && window.__mp1el.__mp1 === 1 && !window.__mp1el.paused`), 'the same marked element plays on in the mini player');
    });

    await step("the page's Stop and close ends the session and shows the empty state", async () => {
      await h.clickNav(page, 'Media');
      await until(page, `!!document.querySelector('[data-testid="media-page-stage"] audio')`, 15000, 'the player on the page');
      await stopOnPage(page);
      await until(page, `!document.querySelector('audio') && document.body.innerText.includes('Nothing is playing')`, 10000, 'the session ends and the empty state shows');
      check(await page.evaluate(`!document.querySelector('[data-testid="media-mini"]')`), 'no mini player is left');
    });

    // The library tabs (mp4): the search indexes the uploads by a job, so wait for them to be listed before opening a tab.
    await waitListed(api, base, accountKey, 'audio', 'tag24.mp3');
    await waitListed(api, base, accountKey, 'video', 'clip.webm');

    await step('the Music tab lists the songs with their tags', async () => {
      await h.clickNav(page, 'Media');
      await until(page, `location.pathname === '/media'`, 10000, 'the Media page');
      await clickTab(page, 'Music');
      await until(page, `location.search.includes('tab=music')`, 10000, 'the Music tab');
      await until(page, `!!document.querySelector(${q(rowSel('Music', 'tag24.mp3'))})`, 15000, 'the tagged song row');
      const taggedText = await page.evaluate(`document.querySelector(${q(rowSel('Music', 'tag24.mp3'))}).textContent`);
      check(taggedText.includes('Artist One'), `the Music tab shows the artist (${taggedText})`);
      check(taggedText.includes('שלום עולם'), 'the Music tab shows the tag title');
      check(taggedText.includes('Album X'), 'the Music tab shows the album');
      check(await page.evaluate(`!!document.querySelector(${q(rowSel('Music', 'track-one.ogg'))})`), 'the untagged song is listed by its name');
    });

    await step('clicking a song plays it in the page’s one player, on Now playing', async () => {
      await clickRow(page, 'Music', 'track-one.ogg');
      await until(page, `!!document.querySelector('[data-testid="media-page-stage"] audio')`, 15000, 'the player on the page stage');
      await until(page, `!location.search.includes('tab=')`, 10000, 'back to Now playing');
      check(await page.evaluate(`[...document.querySelectorAll('[role="tab"]')].find((t) => t.textContent.trim() === 'Now playing')?.getAttribute('aria-selected') === 'true'`), 'Now playing is the active tab');
      await page.evaluate(`(() => { const el = document.querySelector('[data-testid="media-page-stage"] audio'); el.__mp4 = 1; window.__mp4el = el; return true; })()`);
      await playing(page);
      check(await page.evaluate(`document.querySelectorAll('audio').length === 1`), 'one audio element on the page');
      check(await page.evaluate(`!document.querySelector('[data-testid="media-mini"]') && !document.querySelector('[data-testid="preview"]')`), 'no mini player and no dialog beside the page');
    });

    await step('after a few seconds and a pause, Recently played lists the place stopped at', async () => {
      await until(page, `window.__mp4el.currentTime > 5`, 15000, 'a few seconds played');
      const before = page.sent.filter((r) => r.method === 'PUT' && r.url.includes('/resume')).length;
      await page.evaluate(`window.__mp4el.pause()`);
      await waitResumeSaved(page, before);
      await clickTab(page, 'Recently played');
      await until(page, `location.search.includes('tab=recent')`, 10000, 'the Recently played tab');
      await until(page, `!!document.querySelector(${q(rowSel('Recently played', 'track-one.ogg'))})`, 15000, 'the played row');
      const prog = await page.evaluate(`document.querySelector(${q(rowSel('Recently played', 'track-one.ogg'))}).textContent`);
      check(/0:0[5-9] of 0:08/.test(prog), `Recently played shows the place stopped at (${prog})`);
      check(await page.evaluate(`document.querySelector('[data-testid="media-page-stage"] audio') === window.__mp4el && window.__mp4el.paused`), 'the same paused element stays on the hidden stage');
      check(await page.evaluate(`!!document.querySelector('[data-testid="media-page-queue"] [data-testid="media-queue-table"]')`), 'the queue table stays in its host across the tab change');
    });

    await step('the Videos tab lists the uploaded video', async () => {
      await clickTab(page, 'Videos');
      await until(page, `location.search.includes('tab=videos')`, 10000, 'the Videos tab');
      // Wait for the tab's read to settle: a row, or the empty state inside the table's frame.
      await until(page, `(() => { const r = document.querySelector('[role="region"][aria-label="Videos"]'); return !!r && (!!r.querySelector('[role="row"]') || !!r.querySelector('.empty-title')); })()`, 15000, 'the Videos tab settles');
      const hasVideo = await page.evaluate(`!!document.querySelector(${q(rowSel('Videos', 'clip.webm'))})`);
      // waitListed already proved the search lists the video, so a missing row here is a bug, not a reason to skip.
      check(hasVideo, 'the Videos tab lists the uploaded video');
      const vidText = await page.evaluate(`document.querySelector(${q(rowSel('Videos', 'clip.webm'))}).textContent`);
      check(vidText.includes('MP1 media'), `the Videos tab shows the folder (${vidText})`);
      // End the session so the phone check below starts from the empty state, as it expects.
      await stopOnPage(page);
      await until(page, `!document.querySelector('audio')`, 10000, 'the session ends');
    });

    await step('the Media page fits a phone with no sideways scroll', async () => {
      await openFolder(page, folderURL, 'track-one.ogg');
      await dblClick(page, await rowPoint(page, 'track-one.ogg'));
      await until(page, viewerShown, 15000, 'the viewer opens');
      await playing(page);
      await closeViewer(page);
      await h.clickNav(page, 'Media');
      await until(page, `!!document.querySelector('[data-testid="media-page-stage"] audio')`, 15000, 'the player on the page');
      await setPhone(page, true);
      await sleep(400);
      const over = await page.evaluate(`Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) - window.innerWidth`);
      check(over <= 0, `no horizontal overflow at 390x844 (overflow ${over}px)`);
      await stopOnPage(page);
      await until(page, `!document.querySelector('audio')`, 10000, 'the session ends');
    });
  } finally {
    await setPhone(page, false);
    const restore = [['selfcloud.lang', kept.lang], ['selfcloud.theme', kept.theme], ['selfcloud.files', kept.files]];
    await page.evaluate(`(() => { for (const [k, v] of ${q(restore)}) { if (v === null) localStorage.removeItem(k); else localStorage.setItem(k, v); } return true; })()`);
  }
  // The phone size was just left: a fresh load of the shell lets the account menu open at the desktop size.
  await page.goto(base + '/');
  await until(page, `!!document.querySelector('[data-testid="account-menu"]') || location.pathname === '/login'`, 15000, 'the shell after the phone size');
  await signOut();
  await signIn(MEMBER);
  console.log('mp1 checks passed');
}
