// SPDX-License-Identifier: AGPL-3.0-or-later

// The drive inside a host application's frame (eb2a, decision 214). It runs inside the shared end-to-end run
// (run.mjs) in its own browser with its own profile, or alone with SELFCLOUD_E2E_ONLY=eb2a. A local server plays
// the host application on localhost while the app is on 127.0.0.1, so the frame is cross-site and its cookie is
// the partitioned one. The account's embed list names the host's origin; a frame sign-in link opens in the host
// page's iframe and the frame shows the member's files and registers no service worker. A sign-out from the
// frame's menu tells the host exactly once, from the app's origin, and the frame draws the session-ended view
// with no password field. A browser that refuses the frame's cookie draws the refused-cookie view instead and
// tells the host the frame is blocked; the cookie itself is never weakened. The embed list is reset after.
import { createServer } from 'node:http';
import { setTimeout as sleep } from 'node:timers/promises';
import { freePort } from './cdp.mjs';

const q = (v) => JSON.stringify(v);

// frameBridge reads and waits inside the app's iframe. A cross-site frame is a target of its own, reached
// through the browser's socket; a frame in the page's own process is reached through an isolated world in the
// child frame. Both answer the same expressions.
async function frameBridge(page, base) {
  const port = new URL(page.ws.url).port;
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

  let sessionId = null;
  for (let i = 0; i < 150 && !sessionId; i++) {
    const { targetInfos } = await send('Target.getTargets');
    const frame = targetInfos.find((t) => t.type === 'iframe' && t.url.startsWith(base + '/'));
    if (frame) ({ sessionId } = await send('Target.attachToTarget', { targetId: frame.targetId, flatten: true }));
    else await sleep(100);
  }
  const evaluate = async (expression) => {
    let res;
    if (sessionId) {
      res = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true }, sessionId);
    } else {
      const { frameTree } = await page.send('Page.getFrameTree');
      const child = (frameTree.childFrames ?? []).find((f) => f.frame.url.startsWith(base + '/'));
      if (!child) throw new Error('the host page has no app frame');
      const { executionContextId } = await page.send('Page.createIsolatedWorld', { frameId: child.frame.id });
      res = await page.send('Runtime.evaluate', { expression, contextId: executionContextId, returnByValue: true, awaitPromise: true });
    }
    if (res.exceptionDetails) throw new Error(`frame script failed: ${res.exceptionDetails.exception?.description ?? res.exceptionDetails.text}`);
    return res.result.value;
  };
  const waitFor = async (expression, timeoutMs = 20000) => {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const value = await evaluate(expression).catch(() => undefined);
      if (value) return value;
      if (Date.now() > deadline) throw new Error(`timed out waiting in the frame for: ${expression}`);
      await sleep(100);
    }
  };
  return { evaluate, waitFor, close: () => ws.close() };
}

export async function eb2aChecks(h, { page, base, accountID, accountKey, memberID, teamB }) {
  const { check, api } = h;
  const port = await freePort();
  const hostOrigin = `http://localhost:${port}`;
  const appOrigin = new URL(base).origin;
  const srv = createServer((req, res) => {
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    res.end(
      '<!doctype html><html><head><title>Host application</title></head><body>' +
        '<h1>Host application</h1>' +
        '<script>window.__hostMessages = [];' +
        "window.addEventListener('message', (e) => { window.__hostMessages.push({ origin: e.origin, data: e.data }); });</script>" +
        '<iframe id="drive" src="about:blank" style="width:100%;height:600px"></iframe>' +
        '</body></html>',
    );
  });
  await new Promise((resolve) => srv.listen(port, '127.0.0.1', resolve));
  let bridge = null;
  try {
    await api(base, 'PUT', `/api/v1/accounts/${accountID}/embed`, accountKey, { origins: [hostOrigin] });
    const made = await api(base, 'POST', `/api/v1/accounts/${accountID}/users/${memberID}/sign-in-links`, accountKey, {
      mode: 'frame',
      next: `/files/${teamB}`,
    });
    check(made.url.startsWith(base + '/sign-in?frame=' + accountID + '#token='), 'the account key makes a frame link for the member');

    await page.goto(`${hostOrigin}/`);
    await page.evaluate(`(() => { document.getElementById('drive').src = ${q(made.url)}; return true; })()`);
    bridge = await frameBridge(page, base);

    // The frame signs the member in and shows the files, or the browser refused the frame's cookie.
    const outcome = await bridge.waitFor(
      `!!document.querySelector('[data-testid="account-menu"]') ? 'signed-in' : ` +
        `document.body.innerText.includes("This page can't keep you signed in") ? 'blocked' : false`,
      30000,
    );
    if (outcome === 'blocked') {
      console.log('eb2a: headless Chromium refused the frame cookie; the refused-cookie checks run');
      const text = await bridge.evaluate(`document.body.innerText`);
      check(text.includes("This page can't keep you signed in"), 'the frame draws the refused-cookie view');
      check(!(await bridge.evaluate(`!!document.querySelector('input[type="password"]')`)), 'the refused-cookie view has no password field');
      const messages = await page.evaluate(`window.__hostMessages`);
      check(
        messages.length === 1 && messages[0].origin === appOrigin && JSON.stringify(messages[0].data) === JSON.stringify({ type: 'selfcloud.frame-blocked' }),
        `the host receives one frame-blocked message from the app's origin, carrying only the type (${JSON.stringify(messages)})`,
      );
      return;
    }
    check(true, 'the frame link signs the member in inside the host page');
    await bridge.waitFor(`location.pathname === ${q('/files/' + teamB)}`, 15000);
    // The listing loads after the address moves; wait for the file, and say what the frame shows if it never comes.
    await bridge.waitFor(`document.body.innerText.includes('readme.txt')`, 15000).catch(() => undefined);
    const listing = await bridge.evaluate(`document.body.innerText`);
    check(listing.includes('readme.txt'), `the frame shows the member's files (${listing.slice(0, 160).replace(/\s+/g, ' ')})`);

    const registrations = await bridge.evaluate(`navigator.serviceWorker.getRegistrations().then((r) => r.length)`);
    check(registrations === 0, `the frame registers no service worker (${registrations} registrations)`);

    // Sign out from the frame's menu: the host is told once, and the frame draws the session-ended view.
    await bridge.evaluate(`document.querySelector('[data-testid="account-menu"]').click()`);
    await bridge.waitFor(`[...document.querySelectorAll('.ant-dropdown-menu-item')].some((x) => x.textContent.trim() === 'Sign out')`, 10000);
    await bridge.evaluate(`[...document.querySelectorAll('.ant-dropdown-menu-item')].find((x) => x.textContent.trim() === 'Sign out').click()`);
    await bridge.waitFor(`document.body.innerText.includes('Your session ended')`, 15000);
    check(true, 'the frame draws the session-ended view after sign-out');
    check(!(await bridge.evaluate(`!!document.querySelector('input[type="password"]')`)), 'the session-ended view has no password field');
    check(!(await bridge.evaluate(`document.body.innerText.includes('Forgot password')`)), 'the session-ended view offers no forgot-password way');
    const framePath = await bridge.evaluate(`location.pathname`);
    check(framePath !== '/login', `the frame never goes to the sign-in page (it is at ${framePath})`);
    const messages = await page.evaluate(`window.__hostMessages`);
    check(messages.length === 1, `the host receives exactly one message (${messages.length})`);
    check(messages[0] && messages[0].origin === appOrigin, `the message's origin is the app's origin (${messages[0] && messages[0].origin})`);
    check(
      messages[0] && JSON.stringify(messages[0].data) === JSON.stringify({ type: 'selfcloud.session-ended' }),
      `the message carries only the type (${JSON.stringify(messages[0] && messages[0].data)})`,
    );
  } finally {
    bridge?.close();
    await api(base, 'PUT', `/api/v1/accounts/${accountID}/embed`, accountKey, { origins: [] }).catch(() => undefined);
    await new Promise((resolve) => srv.close(resolve));
  }
}
