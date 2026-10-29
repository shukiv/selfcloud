// SPDX-License-Identifier: AGPL-3.0-or-later

// A minimal Chrome DevTools Protocol client for the smoke test. It uses Node's WebSocket and no
// npm packages, so the test adds no dependency (Playwright is Apache-2.0, outside the licence rule).
import { spawn } from 'node:child_process';
import { createServer } from 'node:net';
import { setTimeout as sleep } from 'node:timers/promises';

export function freePort() {
  return new Promise((resolve, reject) => {
    const srv = createServer();
    srv.unref();
    srv.on('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address();
      srv.close(() => resolve(port));
    });
  });
}

// launchBrowser starts headless Chrome with a debugging port and returns the process and the
// WebSocket URL of its first page.
export async function launchBrowser(binary, profileDir) {
  const port = await freePort();
  const proc = spawn(
    binary,
    [
      '--headless=new',
      '--no-sandbox',
      '--disable-gpu',
      '--disable-dev-shm-usage',
      '--no-first-run',
      '--no-default-browser-check',
      '--disable-extensions',
      `--remote-debugging-port=${port}`,
      `--user-data-dir=${profileDir}`,
      'about:blank',
    ],
    { stdio: 'ignore' },
  );
  const base = `http://127.0.0.1:${port}`;
  for (let i = 0; ; i++) {
    try {
      await fetch(`${base}/json/version`);
      break;
    } catch {
      if (i > 100) throw new Error('the browser did not open its debugging port');
      await sleep(100);
    }
  }
  const targets = await (await fetch(`${base}/json/list`)).json();
  const page = targets.find((t) => t.type === 'page');
  if (!page) throw new Error('the browser has no page target');
  return { proc, wsUrl: page.webSocketDebuggerUrl };
}

// Page is one tab. It records what the policy tests need: console errors, uncaught exceptions,
// security-policy log entries, and every request's URL.
export class Page {
  constructor(wsUrl) {
    this.ws = new WebSocket(wsUrl);
    this.nextId = 1;
    this.pending = new Map();
    this.listeners = new Map();
    this.errors = [];
    this.requests = [];
    this.opened = new Promise((resolve, reject) => {
      this.ws.onopen = resolve;
      this.ws.onerror = () => reject(new Error('cannot open the page socket'));
    });
    this.ws.onmessage = (ev) => this.dispatch(JSON.parse(ev.data));
  }

  dispatch(msg) {
    if (msg.id !== undefined) {
      const p = this.pending.get(msg.id);
      if (!p) return;
      this.pending.delete(msg.id);
      if (msg.error) p.reject(new Error(`${msg.error.message}`));
      else p.resolve(msg.result);
      return;
    }
    const fns = this.listeners.get(msg.method) ?? [];
    for (const fn of fns) fn(msg.params);
  }

  on(method, fn) {
    this.listeners.set(method, [...(this.listeners.get(method) ?? []), fn]);
  }

  send(method, params = {}) {
    const id = this.nextId++;
    this.ws.send(JSON.stringify({ id, method, params }));
    return new Promise((resolve, reject) => this.pending.set(id, { resolve, reject }));
  }

  async start() {
    await this.opened;
    this.on('Runtime.exceptionThrown', (p) => {
      this.errors.push(`uncaught: ${p.exceptionDetails?.exception?.description ?? p.exceptionDetails?.text}`);
    });
    this.on('Runtime.consoleAPICalled', (p) => {
      if (p.type === 'error') this.errors.push(`console: ${p.args.map((a) => a.value ?? a.description).join(' ')}`);
    });
    this.on('Log.entryAdded', (p) => {
      if (p.entry.level !== 'error') return;
      // The start-up check asks GET /api/v1/me before anyone signs in, and the server answers 401.
      // The browser logs every failed request, so that one expected answer is not counted.
      const expectedProbe =
        p.entry.source === 'network' && /\/api\/v1\/me$/.test(p.entry.url ?? '') && /status of 401/.test(p.entry.text);
      if (!expectedProbe) this.errors.push(`log(${p.entry.source}): ${p.entry.text}`);
    });
    this.on('Network.requestWillBeSent', (p) => this.requests.push(p.request.url));
    await this.send('Runtime.enable');
    await this.send('Log.enable');
    await this.send('Network.enable');
    await this.send('Page.enable');
  }

  async goto(url) {
    await this.send('Page.navigate', { url });
    await this.waitFor(`document.readyState === 'complete'`, 15000);
  }

  async evaluate(expression) {
    const res = await this.send('Runtime.evaluate', {
      expression,
      returnByValue: true,
      awaitPromise: true,
    });
    if (res.exceptionDetails) {
      throw new Error(`page script failed: ${res.exceptionDetails.exception?.description ?? res.exceptionDetails.text}`);
    }
    return res.result.value;
  }

  // waitFor polls an expression until it is truthy, and returns its value.
  async waitFor(expression, timeoutMs = 10000) {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const value = await this.evaluate(expression).catch(() => undefined);
      if (value) return value;
      if (Date.now() > deadline) throw new Error(`timed out waiting for: ${expression}`);
      await sleep(100);
    }
  }

  close() {
    this.ws.close();
  }
}
