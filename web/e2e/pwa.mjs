// SPDX-License-Identifier: AGPL-3.0-or-later

// The slice pwa1 checks (decision 65): the installable app, in a real browser on 127.0.0.1, a secure context. The manifest
// and its icons are fetched and valid; the worker is registered and controls the page after a reload; no answer of the API,
// the office endpoints, the share pages or the brand files is in any cache; sign-out leaves no app file; with the network
// cut, a navigation shows the offline page; a new version waits and is offered; the install entries follow the browser's
// offer, and the iPhone instructions appear on an iPhone's user agent; the app icons and the favicon are drawn from the
// installation's icon file (decision 119). With SELFCLOUD_PWA1_SHOTS set to a folder, the run also saves the pictures for the
// slice and the icons as PNG files.
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { click, setLanguage, setPhone, shot, sleep } from './media.mjs';
import { pngPixel } from './icon.mjs';

const q = (v) => JSON.stringify(v);

// centre gives the middle of the element a JavaScript expression finds, scrolled into view, or null.
async function centre(page, expr) {
  return page.evaluate(
    `(() => { const el = ${expr}; if (!el) return null; el.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' }); const r = el.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`,
  );
}

// clickMenuItem presses the item of the open menu with the given text, as a person does.
async function clickMenuItem(page, text) {
  const done = await page.evaluate(
    `(() => { const b = [...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].find((x) => x.textContent.trim() === ${q(text)}); if (!b) return false; b.click(); return true; })()`,
  );
  if (!done) throw new Error(`no menu item "${text}"`);
  await sleep(250);
}

// openAccountMenu opens the account menu, as a person does.
async function openAccountMenu(page) {
  // The menu is opened by a click on its button. The click is sent to the button itself, because an offer can lie over it at
  // the position a person would press.
  const opened = `!!document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu')`;
  const pressed = await page.evaluate(`(() => { const b = document.querySelector('[data-testid="account-menu"]'); if (!b) return false; b.click(); return true; })()`);
  if (!pressed) throw new Error('no account menu');
  await page.waitFor(opened, 15000);
  await sleep(250);
}

// menuTexts are the items the account menu shows now.
const menuTexts = (page) =>
  page.evaluate(`[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item')].map((b) => b.textContent.trim())`);

// caches lists the URL of every entry of every cache this origin keeps.
const CACHE_URLS = `(async () => { const out = []; for (const k of await caches.keys()) { const c = await caches.open(k); for (const r of await c.keys()) out.push(r.url); } return out; })()`;
const CACHE_NAMES = `caches.keys()`;

// dimensions reads the width and height from a PNG's header.
function dimensions(bytes) {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  return { width: view.getUint32(16), height: view.getUint32(20), signature: bytes.slice(1, 4).toString('latin1') };
}

// workerNetwork opens a browser-level session, attaches to the worker's target and cuts or restores its network. The page's own
// offline mode does not reach the worker's requests, so the worker's target is emulated. The session stays open while the
// emulation is on: closing it removes the emulation. It returns a function that restores the network and closes the session.
async function workerNetwork(wsUrl, offline) {
  const port = new URL(wsUrl).port;
  const version = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json();
  const ws = new WebSocket(version.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true });
    ws.addEventListener('error', () => reject(new Error('no browser session')), { once: true });
  });
  let nextId = 1;
  const pending = new Map();
  ws.addEventListener('message', (m) => {
    const msg = JSON.parse(m.data);
    const p = pending.get(msg.id);
    if (!p) return;
    pending.delete(msg.id);
    if (msg.error) p.reject(new Error(msg.error.message));
    else p.resolve(msg.result);
  });
  const send = (method, params = {}, sessionId) =>
    new Promise((resolve, reject) => {
      const id = nextId++;
      pending.set(id, { resolve, reject });
      ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
    });
  const { targetInfos } = await send('Target.getTargets');
  const worker = targetInfos.find((t) => t.type === 'service_worker' && t.url.endsWith('/sw.js'));
  if (!worker) throw new Error('no service worker target for /sw.js');
  const { sessionId } = await send('Target.attachToTarget', { targetId: worker.targetId, flatten: true });
  await send('Network.enable', {}, sessionId);
  const set = (cut) => send('Network.emulateNetworkConditions', { offline: cut, latency: 0, downloadThroughput: -1, uploadThroughput: -1 }, sessionId);
  await set(offline);
  return {
    restore: async () => {
      await set(false).catch(() => undefined);
      ws.close();
    },
  };
}

// closeUpdatePrompt closes the update offer with its close button, if it is on screen.
async function closeUpdatePrompt(page) {
  await page.evaluate(`document.querySelector('[data-testid="update-prompt"] button[aria-label="Close"]')?.click()`);
  await sleep(300);
}

// setAgent makes the page report the given user agent on its next load.
async function setAgent(page, userAgent) {
  await page.send('Emulation.setUserAgentOverride', { userAgent });
}

export async function pwaChecks(h, { page, base, brand, dir, check, wsUrl }) {
  const shotDir = process.env.SELFCLOUD_PWA1_SHOTS;
  if (shotDir) await mkdir(shotDir, { recursive: true });
  const desktopAgent = await page.evaluate('navigator.userAgent');

  // The manifest: the installation's name and accent, the file list at the start, standalone, with three icons.
  const manifest = JSON.parse(await page.evaluate(`fetch('/manifest.webmanifest').then((r) => r.text())`));
  check(manifest.name === brand.name, `the manifest names the installation (${manifest.name})`);
  check(manifest.start_url === '/files' && manifest.display === 'standalone' && manifest.scope === '/' && manifest.id === '/', 'the manifest starts on /files, standalone, in scope /');
  check(manifest.theme_color === brand.accentUpper, `the manifest's theme colour is the accent (${manifest.theme_color})`);
  check(Array.isArray(manifest.icons) && manifest.icons.length === 3, 'the manifest lists the 192, 512 and maskable 512 icons');

  // Each icon loads as a PNG of its declared size. The apple-touch-icon is checked on its own.
  const iconPaths = [...manifest.icons.map((i) => ({ src: i.src, size: Number(i.sizes.split('x')[0]) })), { src: '/brand/apple-touch-icon.png', size: 180 }];
  for (const icon of iconPaths) {
    const response = await fetch(new URL(icon.src, base));
    const bytes = Buffer.from(await response.arrayBuffer());
    const { width, height, signature } = dimensions(bytes);
    check(response.ok && response.headers.get('content-type') === 'image/png' && signature === 'PNG' && width === icon.size && height === icon.size,
      `${icon.src} is a ${icon.size} by ${icon.size} PNG`);
    if (shotDir) await writeFile(join(shotDir, `icon-${icon.src.split('/').pop()}`), bytes);
  }

  // The page's head names the manifest, the apple icon and the two theme colours.
  const head = await page.evaluate(`({
    manifest: document.querySelector('link[rel="manifest"]')?.getAttribute('href'),
    apple: document.querySelector('link[rel="apple-touch-icon"]')?.getAttribute('href'),
    themes: [...document.querySelectorAll('meta[name="theme-color"]')].map((m) => m.getAttribute('media')),
  })`);
  check(head.manifest === '/manifest.webmanifest' && head.apple === '/brand/apple-touch-icon.png', 'the page links the manifest and the apple icon');
  check(head.themes.length === 2 && head.themes.some((m) => m.includes('light')) && head.themes.some((m) => m.includes('dark')), 'the page has a theme colour for light and for dark');

  // The app icons and the favicon are the installation's own picture (decision 119): the e2e brand's icon file, a green edge with a
  // red square in the middle (icon.mjs). Chromium draws each one on a canvas: the centre is red and a corner is green. The favicon
  // is the check that its wrapper's data image renders under its own policy; a refusal shows here as a failed draw.
  const drawn = (src, size) => `(async () => {
    const img = new Image();
    img.src = ${JSON.stringify(src)};
    await new Promise((ok, no) => { img.onload = ok; img.onerror = () => no(new Error('not drawn')); });
    const canvas = document.createElement('canvas');
    canvas.width = ${size};
    canvas.height = ${size};
    const ctx = canvas.getContext('2d');
    ctx.drawImage(img, 0, 0, ${size}, ${size});
    const at = (x, y) => Array.from(ctx.getImageData(x, y, 1, 1).data.slice(0, 3));
    return { centre: at(${size / 2}, ${size / 2}), corner: at(1, 1) };
  })()`;
  const isRed = (c) => c[0] > 180 && c[1] < 80 && c[2] < 80;
  const isGreen = (c) => c[1] > 120 && c[0] < 80 && c[2] < 80;
  for (const [src, size] of [['/brand/icon-512.png', 512], ['/brand/icon-512-maskable.png', 512], ['/favicon.svg', 64]]) {
    const got = await page.evaluate(drawn(src, size));
    check(isRed(got.centre) && isGreen(got.corner), `${src} is drawn from the installation's icon file: red in the middle, green at the corner (${got.centre}, ${got.corner})`);
  }

  // The worker registers after the page loads, and controls the page after a reload.
  await page.waitFor(`navigator.serviceWorker.getRegistration().then((r) => !!(r && r.active))`, 20000);
  await page.goto(`${base}/files`);
  await page.waitFor(`!!navigator.serviceWorker.controller`, 20000);
  check(true, 'the worker is registered and controls the page after a reload');

  // Nothing of the API, the office endpoints, the share pages or the brand files is in any cache; the app's own files are.
  await page.waitFor(`location.pathname === '/files'`, 15000);
  await sleep(500);
  const urls = await page.evaluate(CACHE_URLS);
  const path = (u) => new URL(u).pathname;
  const kept = urls.map(path);
  // The page's scripts may come from the browser's memory cache, which never reaches the worker. The check therefore asks for
  // the app's own files with fetch(), which does go through the worker, and then reads the caches.
  await page.evaluate(`Promise.all(performance.getEntriesByType('resource').map((e) => e.name).filter((n) => new URL(n).pathname.startsWith('/assets/')).slice(0, 4).map((n) => fetch(n).then((r) => r.arrayBuffer())))`);
  await sleep(500);
  const cached = (await page.evaluate(CACHE_URLS)).map((u) => new URL(u).pathname);
  check(cached.some((p) => p.startsWith('/assets/')), `the app's own hashed files are cached (${cached.filter((p) => p.startsWith('/assets/')).length})`);
  const forbidden = kept.filter((p) => p.startsWith('/api/') || p.startsWith('/wopi/') || p === '/s' || p.startsWith('/s/') || p.startsWith('/brand/'));
  check(forbidden.length === 0, `no API answer, office endpoint, share page or brand file is cached (${forbidden.join(' ') || 'none'})`);
  check(urls.every((u) => !new URL(u).search), 'no cached answer carries a query string (no signed link)');

  // Sign-out leaves no app file on this computer; the caches that remain hold only the signed-out marker.
  await h.signOut(page);
  const after = await page.evaluate(CACHE_URLS);
  // The state cache keeps two records, the signed-out marker and the last build's identifier: neither is an app file.
  const appLeft = after.filter((u) => !['/signed-out', '/build'].includes(new URL(u).pathname));
  check(appLeft.length === 0, `sign-out leaves no app file (${appLeft.length} left)`);
  const names = await page.evaluate(CACHE_NAMES);
  check(names.every((n) => n === 'selfcloud-state' || !n.startsWith('selfcloud-')), `sign-out removes every app cache (${names.join(' ') || 'none'})`);

  // Back in: the caches fill again from the app's own files, and the page loads.
  await h.signIn(page, { email: brand.member.email, name: brand.member.name, password: brand.member.password });
  await page.goto(`${base}/files`);
  await page.waitFor(`!!navigator.serviceWorker.controller`, 20000);
  await sleep(500);

  // With the network cut, a navigation shows the offline page, styled and with its words. The browser's offline mode does
  // not reach the worker's own requests, so the one probe address is failed with the browser's interception, which the
  // worker's request to the same address meets too.
  await page.send('Network.emulateNetworkConditions', { offline: true, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
  const cut = await workerNetwork(wsUrl, true);
  await page.goto(`${base}/files/offline-probe`).catch(() => undefined);
  const offline = await page.waitFor(`document.getElementById('title')?.textContent === 'You are offline'`, 15000).then(
    () => true,
    () => false,
  );
  await cut.restore();
  const seen = await page.evaluate(`({ url: location.href, title: document.title, text: document.body.innerText.slice(0, 160), controlled: !!navigator.serviceWorker.controller })`);
  if (!offline) console.log(`offline probe saw ${JSON.stringify(seen)}`);
  check(offline, 'with the network cut, a navigation shows the offline page');
  if (offline) {
    const styled = await page.evaluate(`getComputedStyle(document.querySelector('.offline')).borderRadius !== '0px'`);
    check(styled, 'the offline page is styled (its own stylesheet came from the cache)');
    if (shotDir) {
      await shot(page, shotDir, 'pwa1-offline-desktop-light.png');
    }
  }
  await page.send('Network.emulateNetworkConditions', { offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
  await page.goto(`${base}/files`);
  await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]') || location.pathname === '/login'`, 15000);

  // A new version: a worker with another script address waits behind the one in control. The page offers it and, when it
  // is idle, reloads once the person chooses Reload. The offer sits at the end edge of the page's bottom, so it must not cover
  // the sidebar in either direction (decision 65).
  await page.evaluate(`navigator.serviceWorker.register('/sw.js?v=2').then(() => true)`);
  await page.waitFor(`navigator.serviceWorker.getRegistration().then((r) => !!(r && r.waiting))`, 20000);
  await page.waitFor(`!!document.querySelector('[data-testid="update-prompt"]')`, 15000);
  // The page has finished loading: its sidebar is drawn and its skeletons are gone (a picture taken earlier showed them).
  await page.waitFor(`!!document.querySelector('.shell-sider') && document.querySelectorAll('.ant-skeleton-active').length === 0`, 20000).catch(() => undefined);
  await sleep(600);
  check(true, 'a new version that waits is offered on the page');
  const measure = () =>
    page.evaluate(`(() => {
      const r = (el) => { const b = el.getBoundingClientRect(); return { left: b.left, right: b.right, top: b.top, bottom: b.bottom }; };
      const prompt = document.querySelector('[data-testid="update-prompt"]');
      const sider = document.querySelector('.shell-sider');
      if (!prompt || !sider) return null;
      return { dir: document.documentElement.dir, prompt: r(prompt), sider: r(sider), width: innerWidth, height: innerHeight };
    })()`);
  const overlap = (a, b) => a.left < b.right && b.left < a.right && a.top < b.bottom && b.top < a.bottom;
  const english = await measure();
  check(english && !overlap(english.prompt, english.sider), `in English the offer does not cover the sidebar (${JSON.stringify(english)})`);
  check(english && english.prompt.right <= english.width && english.prompt.bottom <= english.height, 'the offer is on screen in English');
  if (shotDir) await shot(page, shotDir, 'pwa1-update-prompt.png');
  await setLanguage(page, 'עברית');
  await page.waitFor(`document.documentElement.dir === 'rtl' && !!document.querySelector('[data-testid="update-prompt"]')`, 10000);
  await sleep(400);
  const hebrew = await measure();
  check(hebrew && !overlap(hebrew.prompt, hebrew.sider), `in Hebrew the offer does not cover the sidebar (${JSON.stringify(hebrew)})`);
  if (shotDir) await shot(page, shotDir, 'pwa1-update-prompt-he.png');
  await setLanguage(page, 'English');
  await page.waitFor(`document.documentElement.dir === 'ltr' && !!document.querySelector('[data-testid="update-prompt"]')`, 10000);
  // A marker on this page tells the reload apart: it is gone once the page has loaded again.
  await page.evaluate(`window.__beforeReload = true`);
  await page.evaluate(`[...document.querySelectorAll('[data-testid="update-prompt"] button')].find((b) => b.textContent.trim() === 'Reload').click()`);
  await page.waitFor(`typeof window.__beforeReload === 'undefined' && navigator.serviceWorker.controller !== null && !!document.querySelector('[data-testid="account-menu"]')`, 20000);
  check(true, 'Reload takes the new version over and reloads the page');
  // The page registers /sw.js again after the test's /sw.js?v=2 registration, so the offer comes back once. The test closes it,
  // as a person would; the offer is not the subject of the install checks below.
  await sleep(1500);
  await closeUpdatePrompt(page);

  // The install entry: shown only when the browser offers the install, and in the account menu.
  await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]')`, 20000);
  await page.evaluate(`(() => { const e = new Event('beforeinstallprompt', { cancelable: true }); e.prompt = () => Promise.resolve({ outcome: 'dismissed' }); window.dispatchEvent(e); })()`);
  await sleep(200);
  await openAccountMenu(page);
  let items = await menuTexts(page);
  check(items.includes('Install app'), `the account menu offers Install when the browser offers it (${items.join(', ')})`);
  if (shotDir) {
    await shot(page, shotDir, 'pwa1-account-menu-install-desktop-en.png');
  }
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await sleep(300);
  await setLanguage(page, 'עברית');
  await page.waitFor(`document.documentElement.dir === 'rtl'`, 10000);
  await openAccountMenu(page);
  if (shotDir) await shot(page, shotDir, 'pwa1-account-menu-install-desktop-he.png');
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await sleep(300);
  await setLanguage(page, 'English');

  // An iPhone: no install prompt, so the menu offers the instructions, and the dialog names the steps at 390 pixels.
  const iphone = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1';
  await setAgent(page, iphone);
  await setPhone(page, true);
  await page.goto(`${base}/files`);
  await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]')`, 20000);
  // /files moves on to the member's own folder once the account is read (FilesPage.tsx, the redirect with replace). The move
  // changes the address, and a change of address closes the sidebar's dialogs (Shell.tsx). The menu is opened after the move,
  // or the instructions opened by the item below are closed by it (CI run 42429830700, attempt 1).
  await page.waitFor(`/^\\/files\\/fld_/.test(location.pathname)`, 20000);
  await openAccountMenu(page);
  items = await menuTexts(page);
  check(items.includes('Add to Home Screen'), `an iPhone is offered the Add to Home Screen steps (${items.join(', ')})`);
  if (shotDir) await shot(page, shotDir, 'pwa1-account-menu-iphone-en.png');
  await clickMenuItem(page, 'Add to Home Screen');
  await page.waitFor(`!!document.querySelector('.ant-modal-content') && document.querySelector('.ant-modal-title')?.textContent === 'Add to Home Screen'`, 10000);
  const steps = await page.evaluate(`[...document.querySelectorAll('.ant-modal ol li')].map((li) => li.textContent.trim())`);
  check(steps.length === 3 && steps[0].includes('Share'), 'the iPhone instructions name the three steps');
  // The update offer may come back over the top bar; it is closed so the picture shows the instructions.
  await closeUpdatePrompt(page);
  await sleep(400);
  if (shotDir) await shot(page, shotDir, 'pwa1-iphone-dialog-390-en.png');
  // The dialog is not closed by a click outside it (maskClosable is false).
  await click(page, { x: 5, y: 5 });
  await sleep(300);
  check(await page.evaluate(`!!document.querySelector('.ant-modal-content')`), 'a click outside the instructions does not close them');
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await page.waitFor(`!document.querySelector('.ant-modal-wrap:not([style*="display: none"])')`, 10000).catch(() => undefined);
  await setLanguage(page, 'עברית');
  await openAccountMenu(page);
  await clickMenuItem(page, 'הוספה למסך הבית');
  await page.waitFor(`!!document.querySelector('.ant-modal-content')`, 10000);
  await closeUpdatePrompt(page);
  await sleep(400);
  if (shotDir) await shot(page, shotDir, 'pwa1-iphone-dialog-390-he.png');
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await setLanguage(page, 'English');
  await setPhone(page, false);
  await setAgent(page, desktopAgent);

  // The offline page itself, in both themes and at the phone width. Its files are its own, so it needs no nonce.
  for (const [scheme, theme] of [['light', 'light'], ['dark', 'dark']]) {
    await page.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: scheme }] });
    await page.goto(`${base}/offline.html`);
    await page.waitFor(`document.getElementById('title')?.textContent === 'You are offline'`, 10000);
    if (shotDir) await shot(page, shotDir, `pwa1-offline-desktop-${theme}.png`);
    await setPhone(page, true);
    await sleep(200);
    if (shotDir) await shot(page, shotDir, `pwa1-offline-390-${theme}.png`);
    await setPhone(page, false);
  }
  await page.send('Emulation.setEmulatedMedia', { features: [] });
  await page.goto(`${base}/files`);
  await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]') || location.pathname === '/login'`, 15000);

  // The favicon opened as a page of its own, so that its own response policy applies (decision 119). A tab loads the favicon as an
  // image, and the image is drawn under the page's policy, so the canvas check above does not prove the wrapper's own policy. This
  // one navigates to /favicon.svg and reads the middle of a screenshot: without the data image the picture is not drawn, and the
  // middle of the screen is white.
  await page.goto(`${base}/favicon.svg`);
  await sleep(300);
  const view = await page.evaluate(`({ width: innerWidth, height: innerHeight })`);
  const { data: shotData } = await page.send('Page.captureScreenshot', { format: 'png' });
  const middle = pngPixel(Buffer.from(shotData, 'base64'), Math.floor(view.width / 2), Math.floor(view.height / 2));
  check(isRed(middle), `/favicon.svg opened as a page draws its picture under its own policy: the middle of the screen is ${middle}`);
  await page.goto(`${base}/files`);
  await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]') || location.pathname === '/login'`, 15000);
  check(true, 'the install entries, the iPhone steps, the update offer and the offline page are checked');
}
