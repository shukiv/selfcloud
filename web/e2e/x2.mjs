// SPDX-License-Identifier: AGPL-3.0-or-later

// The media checks of the second part of the player (slice x2), run inside the shared end-to-end run (run.mjs) on the
// shared page, server and accounts: a double click that plays with no press, the mini player across a real route change
// and after a sign-out, the mini bar's position slider in contrast with its bar in both themes, a saved place across a
// reload and not for another person, the subtitles beside a video (a WebVTT file and a Windows-1255 SRT file) with their
// cues kept above the bar, the phone's bar that hides while a video plays, and, when SELFCLOUD_X2_SHOTS asks for them,
// the screenshots (1 writes them into e2e-out/shots-x2, any other value is the folder). Run it alone with SELFCLOUD_E2E_ONLY=x2.
// The checks end signed in as the member, which the rest of the run expects; the interface's language and theme and the
// listing's view are put back afterwards.
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const media = fileURLToPath(new URL('./media/', import.meta.url));
const repo = fileURLToPath(new URL('../../', import.meta.url));
const shots = await shotsDir();
const PASSWORD = 'correct horse battery staple';
// The same people as the shared run's owner and member (run.mjs), who sign in with these.
const OWNER = { email: 'olivia@example.com', name: 'Olivia Owner', password: PASSWORD };
const MEMBER = { email: 'mateo@example.com', name: 'Mateo Member', password: PASSWORD };
const q = (v) => JSON.stringify(v);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// openSubs clicks the bar's Subtitles button (a programmatic click wakes the bar even while it is hidden on a phone).
// pickOption is the expression for an item in the open Subtitles menu — a track's radio, or a Timing/Size submenu item —
// found by its label; openSub clicks the named submenu's title to draw its items.
const openSubs = `document.querySelector('[data-testid="media-subs"]').click()`;
const pickOption = (name) => `[...document.querySelectorAll('.ant-dropdown-menu-item')].find((x) => x.textContent.trim() === ${q(name)})`;
const openSub = (name) => `[...document.querySelectorAll('.ant-dropdown-menu-submenu-title')].find((x) => x.textContent.trim() === ${q(name)})?.click()`;
// subPick opens the Subtitles menu, opens the named submenu (Timing or Size) and clicks the named item in it.
async function subPick(page, sub, item) {
  await page.evaluate(openSubs);
  await until(page, `!!([...document.querySelectorAll('.ant-dropdown-menu-submenu-title')].find((x) => x.textContent.trim() === ${q(sub)}))`, 5000, `the ${sub} submenu`);
  await page.evaluate(openSub(sub));
  await until(page, `!!${pickOption(item)}`, 5000, `the ${item} item`);
  await page.evaluate(`${pickOption(item)}.click()`);
}
// videoShot captures a PNG of just the video's box, so two captures of the same paused frame line up pixel for pixel.
async function videoShot(page) {
  const box = await page.evaluate(`(() => { const r = ${MEDIA}.getBoundingClientRect(); return { x: r.left, y: r.top, width: r.width, height: r.height }; })()`);
  const { data } = await page.send('Page.captureScreenshot', { format: 'png', clip: { ...box, scale: 1 } });
  return data;
}
// cueTextHeight diffs two captures of one paused frame — the cue hidden, then showing — and returns the vertical span of the
// pixels that differ: the rendered cue text's height. Chromium's getComputedStyle(video, '::cue') does not report the
// author's percentage font size (it answers the video's own 14px for every size), so the rendered pixels are the measure.
async function cueTextHeight(page, hiddenURL, showingURL) {
  return page.evaluate(`(async () => {
    const load = (src) => new Promise((res, rej) => { const i = new Image(); i.onload = () => res(i); i.onerror = () => rej(new Error('decode')); i.src = src; });
    const [ia, ib] = await Promise.all([load(${q(hiddenURL)}), load(${q(showingURL)})]);
    const w = ia.width, h = ia.height;
    const draw = (img) => { const c = document.createElement('canvas'); c.width = w; c.height = h; const x = c.getContext('2d', { willReadFrequently: true }); x.drawImage(img, 0, 0); return x.getImageData(0, 0, w, h).data; };
    const da = draw(ia), db = draw(ib);
    let min = -1, max = -1;
    for (let y = 0; y < h; y++) {
      let n = 0;
      for (let x = 0; x < w; x++) {
        const i = (y * w + x) * 4;
        if (Math.abs(da[i] - db[i]) + Math.abs(da[i + 1] - db[i + 1]) + Math.abs(da[i + 2] - db[i + 2]) > 30) n++;
      }
      if (n > 3) { if (min < 0) min = y; max = y; }
    }
    return max >= 0 ? max - min + 1 : 0;
  })()`);
}

// parseRgb reads a computed colour, "rgb(...)" or "rgba(...)", over the colour it sits on (its alpha blended in).
function blendOver(css, behind) {
  const m = /rgba?\(([^)]+)\)/.exec(css);
  if (!m) throw new Error(`not a colour: ${css}`);
  const [r, g, b, a = 1] = m[1].split(',').map((x) => parseFloat(x));
  return [0, 1, 2].map((i) => [r, g, b][i] * a + behind[i] * (1 - a));
}
// contrast is the WCAG contrast ratio of two colours given as [r, g, b] in 0-255.
function contrast(c1, c2) {
  const lum = (c) => {
    const [r, g, b] = c.map((v) => {
      const x = v / 255;
      return x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const [hi, lo] = [lum(c1), lum(c2)].sort((a, b) => b - a);
  return (hi + 0.05) / (lo + 0.05);
}

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
      // The page's state and its requests not ended, so a listing that never shows says whether its read is still out
      // (gate stfu4d: twice "Loading the folder" in the player sections).
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

// tap is one touch on a phone.
async function tap(page, { x, y }) {
  await page.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] });
  await sleep(60);
  await page.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
}

// rowPoint is the centre of a listing row's name, scrolled into view.
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

// media is the element the viewer plays.
const MEDIA = `document.querySelector('[data-testid="media-screen"] video, [data-testid="media-screen"] audio')`;

// playing waits until the file on screen has data and plays (a bounded wait).
async function playing(page, ms = 20000) {
  await until(page, `(() => { const v = ${MEDIA}; return !!v && v.readyState >= 2; })()`, ms, 'the file has data');
  await until(page, `(() => { const v = ${MEDIA}; return !!v && !v.paused && v.currentTime > 0.2; })()`, 15000, 'the file plays');
}

// viewerShown says the viewer is on screen. The data-testid sits on the antd modal root; the wrap inside it gets display:none when closed, and the dialog stays in the page.
const viewerShown = `(() => { const v = document.querySelector('[data-testid="preview"]'); if (!v) return false; const w = v.querySelector('.ant-modal-wrap') ?? v; return getComputedStyle(w).display !== 'none'; })()`;

async function closeViewer(page) {
  await page.evaluate(`document.querySelector('[data-testid="preview"] header button').click()`);
  try {
    await until(page, `!(${viewerShown})`, 10000, 'the viewer closes');
  } catch (err) {
    const info = await page.evaluate(`(() => { const v = document.querySelector('[data-testid="preview"]'); const w = v?.querySelector('.ant-modal-wrap') ?? v; return { found: !!v, wrapStyle: w?.getAttribute('style'), wrapClass: w?.className, mini: !!document.querySelector('[data-testid="media-mini"]'), url: location.pathname }; })()`);
    throw new Error(`${err.message}; ${JSON.stringify(info)}`);
  }
}

// dotOnRail measures the visible dot of a slider against where it belongs: on the rail, at the played part's end. The handle's
// box may be larger than the dot (a 44 px touch target), and nothing else in these checks sees where the dot is drawn.
const dotOnRail = (scope) => `(() => {
  const s = document.querySelector('${scope}');
  const h = s.querySelector('.ant-slider-handle');
  const rail = s.querySelector('.ant-slider-rail').getBoundingClientRect();
  const r = h.getBoundingClientRect();
  const a = getComputedStyle(h, '::after');
  const x = r.left + parseFloat(a.left) + parseFloat(a.width) / 2;
  const y = r.top + parseFloat(a.top) + parseFloat(a.height) / 2;
  const min = Number(h.getAttribute('aria-valuemin'));
  const span = (Number(h.getAttribute('aria-valuemax')) - min) || 1;
  const f = (Number(h.getAttribute('aria-valuenow')) - min) / span;
  return { dx: Math.round(x - (rail.left + rail.width * f)), dy: Math.round(y - (rail.top + rail.height / 2)) };
})()`;

async function setPhone(page, on) {
  if (on) {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 800, deviceScaleFactor: 1, mobile: true });
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
  } else {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: false });
  }
}

// signOutHere signs out from the page that is open, without a reload, so the page keeps the audio element it was playing.
async function signOutHere(page) {
  await until(page, `!!document.querySelector('[data-testid="account-menu"]')`, 15000, 'the account menu');
  await page.evaluate(`document.querySelector('[data-testid="account-menu"]').click()`);
  await until(page, `[...document.querySelectorAll('.ant-dropdown-menu-item')].some((x) => x.textContent.includes('Sign out'))`, 10000, 'Sign out');
  await page.evaluate(`[...document.querySelectorAll('.ant-dropdown-menu-item')].find((x) => x.textContent.includes('Sign out')).click()`);
  await until(page, `location.pathname === '/login' && !!document.getElementById('login-email')`, 15000, 'the sign-in page');
}

// arrowKey presses an arrow key on the focused element: ArrowLeft is virtual key 37, ArrowRight 39.
async function arrowKey(page, key) {
  const vk = key === 'ArrowLeft' ? 37 : 39;
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key, code: key, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key, code: key, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk });
}
// alphaOf reads the alpha of a computed colour: 1 for a colour with no alpha, 0 for transparent.
const alphaOf = (css) => (css.startsWith('rgba(') ? parseFloat(css.slice(0, -1).split(',').pop()) : 1);

// setPrefs sets the interface language and theme the way the page keeps them, then reloads the page to take them.
async function setPrefs(page, url, lang, theme) {
  await page.goto(url);
  await page.evaluate(`localStorage.setItem('selfcloud.lang', ${q(lang)}); localStorage.setItem('selfcloud.theme', ${q(theme)}); true`);
  await page.goto(url);
}

// shotsDir is the folder for the pictures, or null when the run does not ask for them: 1 is the run's own e2e-out/shots-x2.
async function shotsDir() {
  const v = process.env.SELFCLOUD_X2_SHOTS;
  if (!v) return null;
  const dir = v === '1' ? join(repo, 'e2e-out', 'shots-x2') : v;
  await mkdir(dir, { recursive: true });
  return dir;
}

async function shot(page, name) {
  if (!shots) return;
  const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
  await writeFile(join(shots, `${name}.png`), Buffer.from(data, 'base64'));
  console.log(`shot ${join(shots, `${name}.png`)}`);
}

// The subtitle files. The SRT is in Windows-1255 (the Hebrew code page): "שלום" is F9 EC E5 ED there.
const VTT = Buffer.from('WEBVTT\n\n00:00:01.000 --> 00:00:25.000\nHello from the WebVTT file\n');
const SRT = Buffer.from([
  ...Buffer.from('1\n00:00:01,000 --> 00:00:25,000\n'),
  0xf9, 0xec, 0xe5, 0xed, 0x0a,
  ...Buffer.from('\n'),
]);

export async function x2Checks(h, { page, base, accountKey, accountID, memberID, dir }) {
  const { api, rawPut, check } = h;
  const signIn = (who) => h.signIn(page, who);
  const signOut = () => h.signOut(page);
  // The shared run's interface settings and listing view, put back when the checks end.
  const kept = await page.evaluate(`(() => ({ lang: localStorage.getItem('selfcloud.lang'), theme: localStorage.getItem('selfcloud.theme'), files: localStorage.getItem('selfcloud.files') }))()`);
  // The fixtures: a folder of its own for the run's member (write), with audio, a video and the two subtitle files.
  const shared = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'X2 media' })).id;
  await api(base, 'PUT', `/api/v1/accounts/${accountID}/folders/${shared}/grants/${memberID}`, accountKey, { level: 'write' });
  for (const d of ['/media', '/audio']) {
    await api(base, 'POST', `/api/v1/folders/${shared}/directories`, accountKey, { path: d, parents: false });
  }
  const put = (folder, name, bytes, type) =>
    rawPut(base, `/api/v1/folders/${shared}/files?path=${encodeURIComponent(`${folder}/${name}`)}`, accountKey, bytes, type);
  const tone = await readFile(join(media, 'tone.ogg'));
  await put('/audio', 'track-one.ogg', tone, 'audio/ogg');
  await put('/audio', 'track-two.ogg', tone, 'audio/ogg');
  await put('/media', 'clip.webm', await readFile(join(media, 'clip.webm')), 'video/webm');
  await put('/media', 'clip.vtt', VTT, 'text/vtt');
  await put('/media', 'clip.srt', SRT, 'application/x-subrip');
  const children = (await api(base, 'GET', `/api/v1/folders/${shared}/children?path=%2F`, accountKey)).items;
  const dirID = (name) => children.find((n) => n.name === name)?.id;
  const audioURL = `${base}/files/${shared}/${dirID('audio')}`;
  const mediaURL = `${base}/files/${shared}/${dirID('media')}`;

  await signOut();
  try {
    await step('the owner signs in', async () => {
      await signIn(OWNER);
    });

    await step('a real double click plays the first audio file at once, with no press', async () => {
      await openFolder(page, audioURL, 'track-one.ogg');
      await dblClick(page, await rowPoint(page, 'track-one.ogg'));
      await until(page, viewerShown, 15000, 'the viewer opens');
      await playing(page);
      check(await page.evaluate(`!document.querySelector('[data-testid="media-press"]')`), 'the file plays by the double click, and no press note shows');
      await closeViewer(page);
      await until(page, `!!document.querySelector('[data-testid="media-mini"]')`, 10000, 'the mini player');
    });

    await step('the mini player keeps the same element playing across a route change', async () => {
      await page.evaluate(`window.__el = document.querySelector('audio'); true`);
      const t1 = await page.evaluate(`window.__el.currentTime`);
      await page.evaluate(`(() => { history.pushState({}, '', '/recent'); dispatchEvent(new PopStateEvent('popstate', { state: {} })); return true; })()`);
      await until(page, `location.pathname === '/recent'`, 10000, 'the recent page');
      await until(page, `!!document.querySelector('[data-testid="media-mini"]')`, 10000, 'the mini player after the change');
      check(await page.evaluate(`document.querySelector('audio') === window.__el`), 'the same audio element is still on the page after the route change');
      await sleep(1500);
      const t2 = await page.evaluate(`window.__el.currentTime`);
      check(await page.evaluate(`!window.__el.paused`) && t2 > t1 + 0.5, `the audio goes on (${t1.toFixed(2)} s, then ${t2.toFixed(2)} s), not restarted`);
      await shot(page, 'mini-desktop-en-light');
    });

    await step('a sign-out stops the audio and drops the mini player', async () => {
      await signOutHere(page);
      check(await page.evaluate(`!!window.__el && window.__el.paused === true && !document.querySelector('[data-testid="media-mini"]')`), 'after the sign-out the audio is paused and the mini player is gone');
    });

    // The mini bar sits on the sidebar blue in both themes, so its position slider must read against it: the rail and the played part at 3 to 1 or more.
    await step("the mini bar's position slider is 3 to 1 against its bar, in light and dark", async () => {
      await signIn(OWNER);
      for (const theme of ['light', 'dark']) {
        await setPrefs(page, audioURL, 'en', theme);
        await openFolder(page, audioURL, 'track-one.ogg');
        await dblClick(page, await rowPoint(page, 'track-one.ogg'));
        await playing(page);
        await closeViewer(page);
        await until(page, `!!document.querySelector('.media-mini .ant-slider-track')`, 10000, 'the mini bar slider');
        const c = await page.evaluate(`(() => ({
          bar: getComputedStyle(document.querySelector('[data-testid="media-mini"]')).backgroundColor,
          rail: getComputedStyle(document.querySelector('.media-mini .ant-slider-rail')).backgroundColor,
          fill: getComputedStyle(document.querySelector('.media-mini .ant-slider-track')).backgroundColor,
        }))()`);
        const bar = blendOver(c.bar, [0, 0, 0]);
        const rail = contrast(blendOver(c.rail, bar), bar);
        const fill = contrast(blendOver(c.fill, bar), bar);
        if (rail < 3) {
          await page.send('DOM.enable');
          await page.send('CSS.enable');
          const doc = await page.send('DOM.getDocument', { depth: -1 });
          const { nodeId } = await page.send('DOM.querySelector', { nodeId: doc.root.nodeId, selector: '.media-mini .ant-slider-rail' });
          const m = await page.send('CSS.getMatchedStylesForNode', { nodeId });
          const rules = (m.matchedCSSRules ?? []).map((x) => ({ sel: x.rule.selectorList.text.slice(0, 120), origin: x.rule.origin, bg: (x.rule.style.cssProperties ?? []).filter((p) => /background/.test(p.name)).map((p) => `${p.name}:${p.value}${p.important ? '!important' : ''}`) }));
          const inl = await page.evaluate(`(() => { const r = document.querySelector('.media-mini .ant-slider-rail'); return { style: r.getAttribute('style'), cls: r.className, computed: getComputedStyle(r).backgroundColor, parent: r.parentElement.className }; })()`);
          console.log('DIAG inline ' + JSON.stringify(inl));
          console.log('DIAG matched ' + JSON.stringify(rules.filter((r) => r.bg.length > 0)).slice(0, 1500));
        }
        check(rail >= 3, `${theme}: the slider rail (${c.rail}) is ${rail.toFixed(2)} to 1 against the bar (${c.bar})`);
        check(fill >= 3, `${theme}: the played part (${c.fill}) is ${fill.toFixed(2)} to 1 against the bar`);
        await page.evaluate(`[...document.querySelectorAll('.media-mini .media-mini-btn')].pop().click()`);
        await until(page, `!document.querySelector('[data-testid="media-mini"]')`, 10000, 'the mini bar closes');
      }
      // Signed out again, so the next step signs in as the owner from the sign-in page.
      await signOutHere(page);
    });

    await step('in Hebrew the bar runs left to right: the handle, the played part, a drag, the arrow keys and the volume', async () => {
      await signIn(OWNER);
      // ?lang=he, because the owner's account locale outranks the saved choice.
      const heAudio = audioURL + (audioURL.includes('?') ? '&' : '?') + 'lang=he';
      await openFolder(page, heAudio, 'track-one.ogg');
      await dblClick(page, await rowPoint(page, 'track-one.ogg'));
      await playing(page);
      await closeViewer(page);
      await until(page, `!!document.querySelector('.media-mini .ant-slider-handle')`, 10000, 'the mini bar slider');
      await page.evaluate(`(() => { const v = ${MEDIA}; v.currentTime = v.duration * 0.25; return true; })()`);
      await until(page, `(() => { const v = ${MEDIA}; return v.currentTime > v.duration * 0.2 && v.currentTime < v.duration * 0.3; })()`, 10000, 'the place at 25 %');
      // The handle and the played part move on a transition: the geometry is read once the played part is where 25 % puts it (fu3b,
      // a fixed 600 ms wait read it mid-transition under load).
      await until(
        page,
        `(() => { const r = document.querySelector('.media-mini .ant-slider-rail')?.getBoundingClientRect(); const t = document.querySelector('.media-mini .ant-slider-track')?.getBoundingClientRect(); return !!r && r.width > 0 && !!t && Math.abs(t.left - r.left) <= 1 && Math.abs(t.width - r.width * 0.25) <= 2; })()`,
        10000,
        'the played part at a quarter of the rail',
      );
      const geo = () => page.evaluate(`(() => {
        const b = (sel) => { const r = document.querySelector(sel).getBoundingClientRect(); return { left: r.left, width: r.width, centre: r.left + r.width / 2, mid: r.top + r.height / 2 }; };
        return { rail: b('.media-mini .ant-slider-rail'), track: b('.media-mini .ant-slider-track'), handle: b('.media-mini .ant-slider-handle') };
      })()`);
      const g = await geo();
      const middle = g.rail.left + g.rail.width / 2;
      check(g.handle.centre < middle, `Hebrew: at 25 % the handle is left of the rail's middle (centre ${g.handle.centre.toFixed(0)}, middle ${middle.toFixed(0)})`);
      check(Math.abs(g.track.left - g.rail.left) <= 1 && Math.abs(g.track.width - g.rail.width * 0.25) <= 2, `Hebrew: the played part starts at the rail's left edge (${g.track.left.toFixed(0)} against ${g.rail.left.toFixed(0)}) and is a quarter wide`);
      // A drag to the right, by a fifth of the rail, moves the place forward.
      const before = await page.evaluate(`${MEDIA}.currentTime`);
      const hx = g.handle.centre, hy = g.handle.mid, dx = g.rail.width * 0.2;
      await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: hx, y: hy });
      await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: hx, y: hy, button: 'left', buttons: 1, clickCount: 1 });
      for (let i = 1; i <= 5; i++) await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: hx + (dx * i) / 5, y: hy, button: 'left', buttons: 1 });
      await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: hx + dx, y: hy, button: 'left', buttons: 0, clickCount: 1 });
      await sleep(400);
      const dragged = await page.evaluate(`${MEDIA}.currentTime`);
      check(dragged > before + 0.5, `Hebrew: a drag to the right moves the place forward (${before.toFixed(2)} s to ${dragged.toFixed(2)} s)`);
      // The arrow keys on the focused handle: ArrowRight raises the value, ArrowLeft lowers it.
      const valueOf = (sel) => `Number(document.querySelector(${q(sel)}).getAttribute('aria-valuenow'))`;
      const mini = '.media-mini .ant-slider-handle';
      await page.evaluate(`document.querySelector(${q(mini)}).focus()`);
      const v0 = await page.evaluate(valueOf(mini));
      await arrowKey(page, 'ArrowRight');
      await sleep(300);
      const v1 = await page.evaluate(valueOf(mini));
      check(v1 > v0, `Hebrew: ArrowRight raises the bar (${v0} to ${v1})`);
      await arrowKey(page, 'ArrowLeft');
      await sleep(300);
      const v2 = await page.evaluate(valueOf(mini));
      check(v2 < v1, `Hebrew: ArrowLeft lowers the bar (${v1} to ${v2})`);
      // The viewer's volume and seek bars take the same keys.
      await page.evaluate(`[...document.querySelectorAll('.media-mini .media-mini-btn')].at(-2).click()`);
      await until(page, viewerShown, 15000, 'the viewer from the mini bar');
      for (const [name, sel] of [['the volume', '.media-volume-slider .ant-slider-handle'], ['the viewer seek bar', '.media-seek .ant-slider-handle']]) {
        await page.evaluate(`document.querySelector(${q(sel)}).focus()`);
        const a = await page.evaluate(valueOf(sel));
        await arrowKey(page, 'ArrowLeft');
        await sleep(300);
        const b = await page.evaluate(valueOf(sel));
        await arrowKey(page, 'ArrowRight');
        await sleep(300);
        const c = await page.evaluate(valueOf(sel));
        check(b < a && c > b, `Hebrew: in ${name} ArrowLeft lowers and ArrowRight raises the value (${a}, ${b}, ${c})`);
      }
      await closeViewer(page);
      await page.evaluate(`[...document.querySelectorAll('.media-mini .media-mini-btn')].pop().click()`);
      await until(page, `!document.querySelector('[data-testid="media-mini"]')`, 10000, 'the mini bar closes');
      // Back to the account's own (English) locale, so the sign-out menu item has the text signOutHere looks for.
      await page.goto(audioURL);
      await signOutHere(page);
    });

    await step('a saved place is kept across a reload, and a second person does not see it', async () => {
      await signIn(OWNER);
      await openFolder(page, mediaURL, 'clip.webm');
      await dblClick(page, await rowPoint(page, 'clip.webm'));
      await playing(page);
      await page.evaluate(`(() => { const v = ${MEDIA}; v.currentTime = 6; return true; })()`);
      await until(page, `(() => { const v = ${MEDIA}; return v.currentTime >= 6 && v.currentTime < 7.5; })()`, 10000, 'the seek to 6 s');
      await page.evaluate(`${MEDIA}.pause()`);
      await until(page, `${MEDIA}.paused`, 5000, 'the pause');
      const putSeen = () => page.sent.some((r) => r.method === 'PUT' && r.url.includes('/resume'));
      const deadline = Date.now() + 10000;
      while (!putSeen() && Date.now() < deadline) await sleep(100);
      check(putSeen(), 'the pause writes the place (a PUT to the resume route)');
      await closeViewer(page);
      await openFolder(page, mediaURL, 'clip.webm');
      await dblClick(page, await rowPoint(page, 'clip.webm'));
      await playing(page);
      const again = await page.evaluate(`${MEDIA}.currentTime`);
      check(again >= 5.5 && again < 7.5, `the reloaded file starts at the saved place (${again.toFixed(2)} s, saved 6 s)`);
      await closeViewer(page);
      await signOut();
      await signIn(MEMBER);
      await openFolder(page, mediaURL, 'clip.webm');
      await dblClick(page, await rowPoint(page, 'clip.webm'));
      await until(page, `(() => { const v = ${MEDIA}; return !!v && v.readyState >= 2; })()`, 20000, 'the member file has data');
      await sleep(800);
      const mine = await page.evaluate(`${MEDIA}.currentTime`);
      check(mine < 1, `the second person starts the same file at the beginning (${mine.toFixed(2)} s), not at the first person's place`);
      await closeViewer(page);
    });

    await step('no PUT leaves for the other person after a change of person', async () => {
      await signOut();
      await signIn(OWNER);
      await openFolder(page, mediaURL, 'clip.webm');
      await dblClick(page, await rowPoint(page, 'clip.webm'));
      await playing(page);
      await sleep(1200);
      await signOut();
      await signIn(MEMBER);
      const mark = page.sent.length;
      await sleep(2000);
      const late = page.sent.slice(mark).filter((r) => r.method === 'PUT' && r.url.includes('/resume'));
      check(late.length === 0, `no place is written after the member signs in (${late.length} writes)`);
      await signOut();
    });

    await step('a subtitle file beside a video shows its cues: WebVTT, and Windows-1255 SRT in a Hebrew interface', async () => {
      await setPrefs(page, base + '/', 'he', 'light');
      await signIn(OWNER);
      // The signed-in person's locale outranks the saved choice, so the Hebrew interface is asked for with ?lang=he.
      const heURL = mediaURL + (mediaURL.includes('?') ? '&' : '?') + 'lang=he';
      await openFolder(page, heURL, 'clip.webm');
      await dblClick(page, await rowPoint(page, 'clip.webm'));
      await playing(page);
      // The Subtitles button is drawn once the file's subtitles are read, which is asynchronous (subtitlesOf): it is waited for.
      await page.waitFor(`!!document.querySelector('[data-testid="media-subs"]')`, 15000);
      await page.evaluate(openSubs);
      await until(page, `!!${pickOption('clip.srt')}`, 5000, 'the SRT in the menu');
      await page.evaluate(`${pickOption('clip.srt')}.click()`);
      await page.evaluate(`(() => { const v = ${MEDIA}; v.pause(); v.currentTime = 2; return true; })()`);
      const hebrewCue = `(() => { const t = [...${MEDIA}.textTracks].find((x) => x.mode === 'showing'); return !!t && [...t.activeCues].some((c) => c.text.includes('שלום')); })()`;
      try {
        await until(page, hebrewCue, 10000, 'the Hebrew cue');
      } catch (err) {
        const info = await page.evaluate(`(() => { const v = ${MEDIA}; const tt = v ? [...v.textTracks].map((t) => ({ kind: t.kind, label: t.label, lang: t.language, mode: t.mode, cues: t.cues?.length ?? -1, active: t.activeCues?.length ?? -1, first: t.cues?.[0] ? String(t.cues[0].text).slice(0, 40) : null })) : null; return { tracks: tt, time: v?.currentTime, showing: tt?.find((x) => x.mode === 'showing')?.label ?? null, lang: localStorage.getItem('selfcloud.lang'), url: location.pathname }; })()`);
        throw new Error(`${err.message}; ${JSON.stringify(info)}`);
      }
      check(await page.evaluate(hebrewCue), 'the Windows-1255 SRT shows its Hebrew cue');
      // The cue sits above the bar: its bottom at 80 % of the picture, and the bar's top below that line.
      const cue = await page.evaluate(`(() => { const t = [...${MEDIA}.textTracks].find((x) => x.mode === 'showing'); const c = t?.cues?.[0]; return c ? { line: c.line, snap: c.snapToLines, align: c.lineAlign } : null; })()`);
      check(cue !== null && cue.line === 80 && cue.snap === false && cue.align === 'end', `the cue sits above the bar (line ${cue?.line}, ${cue?.align})`);
      const geo = await page.evaluate(`(() => { const v = ${MEDIA}.getBoundingClientRect(); const c = document.querySelector('[data-testid="media-controls"]').getBoundingClientRect(); return { top: v.top, height: v.height, bar: c.top }; })()`);
      check(geo.bar >= geo.top + 0.8 * geo.height, `the bar's top (${(((geo.bar - geo.top) / geo.height) * 100).toFixed(0)} % down the picture) is below the cue line (80 %)`);
      await shot(page, 'subtitles-desktop-he-light');
      await page.evaluate(openSubs);
      await until(page, `!!${pickOption('clip.vtt')}`, 5000, 'the WebVTT in the menu');
      await page.evaluate(`${pickOption('clip.vtt')}.click()`);
      const vttCue = `(() => { const t = [...${MEDIA}.textTracks].find((x) => x.mode === 'showing'); return !!t && [...t.activeCues].some((c) => c.text.includes('Hello from the WebVTT file')); })()`;
      await until(page, vttCue, 10000, 'the WebVTT cue');
      check(await page.evaluate(vttCue), 'the WebVTT file shows its cue');
      await closeViewer(page);
    });

    await step('a subtitle file from this device shows its cues and sends nothing; the timing and the size move the cues', async () => {
      // The beside-file step left the owner signed in at the folder in Hebrew: back to English there (so sign-out reads
      // "Sign out"), wait for the account menu, sign out and sign back in, so no stale media request races the sign-out.
      await setPrefs(page, mediaURL, 'en', 'light');
      await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]')`, 15000);
      await signOut();
      await signIn(OWNER);
      await openFolder(page, mediaURL, 'clip.webm');
      await dblClick(page, await rowPoint(page, 'clip.webm'));
      await playing(page);
      await page.waitFor(`!!document.querySelector('[data-testid="media-subs"]')`, 15000);
      // A file with no cues shows the note, and the note sits above the bar (the bar overlays the picture's foot, so the
      // note must not be covered by it). The file goes straight into the input; no request carries it.
      const emptyFile = join(dir, 'empty.srt');
      await writeFile(emptyFile, 'not a subtitle file');
      await page.setFiles(`[data-testid="media-subs-file"]`, [emptyFile]);
      await page.waitFor(`!!document.querySelector('[data-testid="media-subs-none"]')`, 10000);
      const noteGeo = await page.evaluate(`(() => { const n = document.querySelector('[data-testid="media-subs-none"]').getBoundingClientRect(); const c = document.querySelector('[data-testid="media-controls"]').getBoundingClientRect(); return { noteBottom: n.bottom, barTop: c.top, text: document.querySelector('[data-testid="media-subs-none"]').textContent }; })()`);
      check(noteGeo.text === 'No subtitles were found in that file', 'a file with no cues shows the note');
      check(noteGeo.noteBottom <= noteGeo.barTop + 1, `the note sits above the bar, not covered by it (note bottom ${noteGeo.noteBottom.toFixed(0)}, bar top ${noteGeo.barTop.toFixed(0)})`);
      // A WebVTT file on this device, read in the browser only: its text is never sent to the server.
      const localName = 'device.vtt';
      const localFile = join(dir, localName);
      await writeFile(localFile, 'WEBVTT\n\n00:00:01.000 --> 00:00:25.000\nFrom this device\n');
      // The Load item opens the page's picker once; a headless browser cannot show it, so the click is counted and the file
      // goes in through DOM.setFileInputFiles afterwards, which fires the real change event (as fu3b does).
      await page.evaluate(`(() => { window.__subsPicks = 0; const o = HTMLInputElement.prototype.click; HTMLInputElement.prototype.click = function () { if (this.type === 'file') { window.__subsPicks++; return; } return o.call(this); }; return true; })()`);
      const mark = page.sent.length;
      await page.evaluate(openSubs);
      await until(page, `!!${pickOption('Load from this device…')}`, 5000, 'the Load item in the menu');
      await page.evaluate(`${pickOption('Load from this device…')}.click()`);
      check(await page.evaluate(`window.__subsPicks`) === 1, 'the Load item opens the picker once');
      await page.setFiles(`[data-testid="media-subs-file"]`, [localFile]);
      await page.evaluate(`(() => { const v = ${MEDIA}; v.pause(); v.currentTime = 2; return true; })()`);
      const deviceCue = `(() => { const t = [...${MEDIA}.textTracks].find((x) => x.mode === 'showing'); return !!t && [...t.activeCues].some((c) => c.text.includes('From this device')); })()`;
      await until(page, deviceCue, 10000, 'the device cue');
      check(await page.evaluate(deviceCue), 'the file from this device shows its cue');
      check(await page.evaluate(`!document.querySelector('[data-testid="media-subs-none"]')`), 'a file with cues clears the note');
      check(page.sent.slice(mark).every((r) => !r.url.includes(localName)), 'the device file is never sent: no request names it');
      // Later 0.1 s moves the showing track's first cue by 0.1 s.
      const firstStart = `(() => { const t = [...${MEDIA}.textTracks].find((x) => x.mode === 'showing'); return t?.cues?.[0]?.startTime ?? null; })()`;
      const before = await page.evaluate(firstStart);
      await subPick(page, 'Timing', 'Later 0.1 s');
      await until(page, `(() => { const t = [...${MEDIA}.textTracks].find((x) => x.mode === 'showing'); return (t?.cues?.[0]?.startTime ?? 0) >= ${before} + 0.099; })()`, 5000, 'the cue moved later');
      const after = await page.evaluate(firstStart);
      check(Math.abs(after - before - 0.1) < 0.001, `Later 0.1 s moves the cue by 0.1 s (${before} -> ${after})`);
      // The cue size: Small, Medium and Large each set the video's class and scale the browser's cue text. The rendered
      // height is measured from the pixels (getComputedStyle does not report the ::cue percentage): the video is paused on
      // one frame, a capture with the cue hidden is the baseline, and each size's capture is diffed against it — the rows
      // that differ are the cue text. The captures stay in memory for the measurement and are written only when
      // SELFCLOUD_X2_SHOTS asks for them, into the folder it names.
      await page.evaluate(`(() => { const v = ${MEDIA}; v.pause(); v.currentTime = 2; return true; })()`);
      await sleep(300);
      await page.evaluate(`(() => { const t = [...${MEDIA}.textTracks].find((x) => x.mode === 'showing'); window.__cueTrack = t; if (t) t.mode = 'hidden'; return true; })()`);
      await sleep(150);
      const hiddenShot = await videoShot(page);
      await page.evaluate(`(() => { if (window.__cueTrack) window.__cueTrack.mode = 'showing'; return true; })()`);
      const measured = {};
      for (const [label, key] of [['Small', 's'], ['Medium', 'm'], ['Large', 'l']]) {
        await subPick(page, 'Size', label);
        await until(page, `${MEDIA}.className.includes('media-cue-${key}')`, 5000, `the media-cue-${key} class`);
        check(await page.evaluate(`${MEDIA}.className.includes('media-cue-${key}')`), `${label} sets the media-cue-${key} class on the video`);
        await sleep(250);
        const sizeShot = await videoShot(page);
        measured[key] = await cueTextHeight(page, `data:image/png;base64,${hiddenShot}`, `data:image/png;base64,${sizeShot}`);
        if (shots) {
          await writeFile(join(shots, `cue-size-${key}.png`), Buffer.from(sizeShot, 'base64'));
          console.log(`shot ${join(shots, `cue-size-${key}.png`)} (rendered cue text ${measured[key]} px tall)`);
        }
      }
      check(measured.l > measured.m && measured.m > measured.s, `the cue text grows with the size (s ${measured.s}, m ${measured.m}, l ${measured.l} px tall)`);
      // Put the timing and the size back, so the shots that follow read the defaults.
      await subPick(page, 'Timing', 'Reset');
      await subPick(page, 'Size', 'Medium');
      await closeViewer(page);
    });

    await step('on a phone the bar hides while the video plays, and a tap shows it', async () => {
      await setPhone(page, true);
      await openFolder(page, mediaURL, 'clip.webm');
      check(await page.evaluate(`matchMedia('(pointer: coarse)').matches`), 'the emulated phone has a coarse pointer');
      await tap(page, await rowPoint(page, 'clip.webm'));
      await until(page, viewerShown, 15000, 'the phone viewer');
      await playing(page);
      await until(page, `!!document.querySelector('[data-testid="media-controls"].media-controls--hidden')`, 8000, 'the bar hides');
      check(await page.evaluate(`!!document.querySelector('[data-testid="media-controls"].media-controls--hidden')`), 'the bar hides on a phone while the video plays');
      check(await page.evaluate(`(document.querySelector('[data-testid="media-queue"] [aria-current="true"] .media-state')?.textContent ?? '').includes('Playing')`), 'the current row says Playing while the video plays');
      // Subtitles are off until the person picks one, so the phone's step picks the WebVTT file from the bar's menu first.
      await page.evaluate(openSubs);
      await until(page, `!!${pickOption('clip.vtt')}`, 5000, 'the WebVTT in the phone menu');
      await page.evaluate(`${pickOption('clip.vtt')}.click()`);
      await sleep(700); // the bar fades over a short transition; the shot waits for it to finish
      await until(page, `!!([...${MEDIA}.textTracks].find((x) => x.mode === 'showing')?.cues?.[0])`, 10000, 'the cues of the first track');
      const coarse = await page.evaluate(`(() => { const t = [...${MEDIA}.textTracks].find((x) => x.mode === 'showing'); return t?.cues?.[0]?.line ?? null; })()`);
      check(coarse === 66, `on a phone the cue sits higher, above the taller bar (line ${coarse})`);
      await shot(page, 'phone-playing-en-light');
      const box = await page.evaluate(`(() => { const r = ${MEDIA}.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
      await tap(page, box);
      await until(page, `!document.querySelector('[data-testid="media-controls"].media-controls--hidden')`, 5000, 'the bar shows after a tap');
      check(await page.evaluate(`!document.querySelector('[data-testid="media-controls"].media-controls--hidden')`), 'a tap shows the bar again');
      await page.evaluate(`${MEDIA}.pause()`);
      await sleep(600); // the handle moves on a transition
      const seekDot = await page.evaluate(dotOnRail('.media-seek'));
      check(Math.abs(seekDot.dx) <= 1 && Math.abs(seekDot.dy) <= 1, `on a phone the viewer's dot sits on the bar at its position (off by ${seekDot.dx}, ${seekDot.dy} px)`);
      // antd moves the dot in its own rule while the handle has the focus, the hover or a finger on it: the dot must not jump.
      await page.evaluate(`document.querySelector('.media-seek .ant-slider-handle').focus()`);
      await sleep(400);
      const seekHeld = await page.evaluate(dotOnRail('.media-seek'));
      check(Math.abs(seekHeld.dx) <= 1 && Math.abs(seekHeld.dy) <= 1, `on a phone the viewer's dot stays on the bar while the handle is held (off by ${seekHeld.dx}, ${seekHeld.dy} px)`);
      await closeViewer(page);
      await setPhone(page, false);
    });

    await step('on a phone the mini player seek handle is a 44 px touch target', async () => {
      await setPhone(page, true);
      await openFolder(page, audioURL, 'track-one.ogg');
      await tap(page, await rowPoint(page, 'track-one.ogg'));
      await until(page, viewerShown, 15000, 'the phone viewer for audio');
      await playing(page);
      await closeViewer(page);
      await until(page, `!!document.querySelector('[data-testid="media-mini"]')`, 10000, 'the mini player on a phone');
      const w = await page.evaluate(`document.querySelector('.media-mini .ant-slider-handle').getBoundingClientRect().width`);
      check(w >= 44, `the mini player handle is ${w.toFixed(1)} px wide on a phone (44 needed)`);
      // The touch target is invisible: the dot drawn on it is at most 20 px, and the 44 px box has no fill.
      const touch = await page.evaluate(`(() => { const h = document.querySelector('.media-mini .ant-slider-handle'); return { hit: h.getBoundingClientRect().width, dot: parseFloat(getComputedStyle(h, '::after').width), bg: getComputedStyle(h).backgroundColor }; })()`);
      check(touch.dot > 0 && touch.dot <= 20 && touch.hit >= 44 && alphaOf(touch.bg) === 0, `on a phone the mini handle's dot is ${touch.dot} px, its hit area ${touch.hit.toFixed(1)} px, and the box has no fill (${touch.bg})`);
      await page.evaluate(`${MEDIA}.pause()`);
      await sleep(600); // the handle moves on a transition
      const miniDot = await page.evaluate(dotOnRail('.media-mini-seek'));
      check(Math.abs(miniDot.dx) <= 1 && Math.abs(miniDot.dy) <= 1, `on a phone the mini bar's dot sits on the bar at its position (off by ${miniDot.dx}, ${miniDot.dy} px)`);
      await page.evaluate(`document.querySelector('.media-mini-seek .ant-slider-handle').focus()`);
      await sleep(400);
      const miniHeld = await page.evaluate(dotOnRail('.media-mini-seek'));
      check(Math.abs(miniHeld.dx) <= 1 && Math.abs(miniHeld.dy) <= 1, `on a phone the mini bar's dot stays on the bar while the handle is held (off by ${miniHeld.dx}, ${miniHeld.dy} px)`);
      await shot(page, 'mini-phone-en-light');
      await page.evaluate(`document.querySelector('.media-mini [aria-label="Stop and close"]').click()`);
      await setPhone(page, false);
    });

    // The screenshot matrix (SELFCLOUD_X2_SHOTS set): mini bar and subtitles on the desktop, the phone playing and the phone's mini bar,
    // in English and Hebrew, light and dark. The URL carries ?lang= because the owner's account locale outranks the saved choice.
    if (shots) {
      await step('the screenshot matrix: desktop and phone, light and dark, English and Hebrew', async () => {
        const pickSubs = async (name) => {
          await page.evaluate(openSubs);
          await until(page, `!!${pickOption(name)}`, 5000, `the ${name} menu entry`);
          await page.evaluate(`${pickOption(name)}.click()`);
        };
        for (const [lang, theme] of [['en', 'light'], ['en', 'dark'], ['he', 'light'], ['he', 'dark']]) {
          const tag = `${lang}-${theme}`;
          const qs = (u) => u + (u.includes('?') ? '&' : '?') + `lang=${lang}`;
          await setPrefs(page, base + '/', lang, theme);
          await signOut();
          await signIn(OWNER);
          // Desktop mini bar, from audio (a video closes its session when the viewer closes).
          await openFolder(page, qs(audioURL), 'track-one.ogg');
          await dblClick(page, await rowPoint(page, 'track-one.ogg'));
          await playing(page);
          await closeViewer(page);
          await until(page, `!!document.querySelector('[data-testid="media-mini"]')`, 10000, 'the mini player');
          await shot(page, `mini-desktop-${tag}`);
          await page.evaluate(`[...document.querySelectorAll('.media-mini .media-mini-btn')].pop().click()`);
          // Desktop subtitles, on the video beside its subtitle files.
          await openFolder(page, qs(mediaURL), 'clip.webm');
          await dblClick(page, await rowPoint(page, 'clip.webm'));
          await playing(page);
          await page.evaluate(`(() => { const v = ${MEDIA}; v.pause(); v.currentTime = 2; return true; })()`);
          await pickSubs(lang === 'he' ? 'clip.srt' : 'clip.vtt');
          await sleep(400);
          await shot(page, `subtitles-desktop-${tag}`);
          await closeViewer(page);
          // Phone: the playing video with its bar hidden, and the phone's mini bar from audio.
          await setPhone(page, true);
          await openFolder(page, qs(mediaURL), 'clip.webm');
          await tap(page, await rowPoint(page, 'clip.webm'));
          await until(page, viewerShown, 15000, 'the phone viewer');
          await playing(page);
          await pickSubs(lang === 'he' ? 'clip.srt' : 'clip.vtt');
          await until(page, `!!document.querySelector('[data-testid="media-controls"].media-controls--hidden')`, 8000, 'the bar hides');
          await until(page, `!!([...${MEDIA}.textTracks].find((x) => x.mode === 'showing')?.activeCues?.length)`, 10000, 'a cue showing on the phone');
          await sleep(700); // the bar fades over a short transition; the shot waits for it to finish
          await shot(page, `phone-playing-${tag}`);
          await closeViewer(page);
          await openFolder(page, qs(audioURL), 'track-one.ogg');
          await tap(page, await rowPoint(page, 'track-one.ogg'));
          await until(page, viewerShown, 15000, 'the phone viewer for audio');
          await playing(page);
          await closeViewer(page);
          await until(page, `!!document.querySelector('[data-testid="media-mini"]')`, 10000, 'the phone mini player');
          await sleep(500); // the dot settles over antd's short transition as the bar appears
          await shot(page, `mini-phone-${tag}`);
          await page.evaluate(`[...document.querySelectorAll('.media-mini .media-mini-btn')].pop().click()`);
          await setPhone(page, false);
        }
      });
    }

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
  console.log('x2 checks passed');
}
