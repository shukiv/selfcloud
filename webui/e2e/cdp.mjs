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
// security-policy log entries, and every request's URL. Every violation of the content policy,
// enforced or report-only, is recorded as an error, so a run with one fails.
export class Page {
  constructor(wsUrl) {
    this.ws = new WebSocket(wsUrl);
    this.nextId = 1;
    this.pending = new Map();
    this.listeners = new Map();
    this.errors = [];
    this.requests = [];
    // sent records each request's method and URL, so a test can count the pieces of an upload.
    this.sent = [];
    // expectedAnswers maps a name the run chose to the URLs of the failed requests it caused on
    // purpose (for example a 401 for a wrong password). The browser logs each failed request; the
    // run counts these and checks that they came from the step it ran.
    this.expectedAnswers = new Map();
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

  // start takes the answers the run expects: each is { name, matches(entry) }. A failed request that
  // matches is counted under name in expectedAnswers and is not reported as an error.
  async start(expected = []) {
    await this.opened;
    this.on('Runtime.exceptionThrown', (p) => {
      this.errors.push(`uncaught: ${p.exceptionDetails?.exception?.description ?? p.exceptionDetails?.text}`);
    });
    this.on('Runtime.consoleAPICalled', (p) => {
      if (p.type === 'error') this.errors.push(`console: ${p.args.map((a) => a.value ?? a.description).join(' ')}`);
    });
    this.on('Log.entryAdded', (p) => {
      if (p.entry.level !== 'error') return;
      const hit = expected.find(({ matches }) => matches(p.entry));
      if (hit) {
        const list = this.expectedAnswers.get(hit.name) ?? [];
        this.expectedAnswers.set(hit.name, [...list, p.entry.url]);
        return;
      }
      // The last requests before the error show what the page was doing when it happened.
      const before = this.sent.slice(-4).map((r) => `${r.method} ${r.url.replace(/^https?:\/\/[^/]+/, '')}`).join(' ; ');
      this.errors.push(`log(${p.entry.source}): ${p.entry.text} ${p.entry.url ?? ''} [after: ${before}]`);
    });
    this.on('Network.requestWillBeSent', (p) => {
      this.requests.push(p.request.url);
      this.sent.push({ method: p.request.method, url: p.request.url });
    });
    await this.send('Runtime.enable');
    await this.send('Log.enable');
    await this.send('Network.enable');
    await this.send('Page.enable');
    await this.send('DOM.enable');
    // Every page this tab loads reports its content-policy violations as console errors, which the
    // run then counts. Both the enforced and the report-only policy are covered: a report-only
    // violation is still a gap to close before the flip.
    await this.send('Page.addScriptToEvaluateOnNewDocument', {
      source:
        "document.addEventListener('securitypolicyviolation', (e) => console.error(" +
        "'content policy violation (' + (e.disposition || 'enforce') + '): ' + e.violatedDirective + ' ' + e.blockedURI + ' at ' + e.sourceFile + ':' + e.lineNumber + ':' + e.columnNumber));",
    });
  }

  // setFiles puts files into the file input the selector names, as a person would pick them.
  async setFiles(selector, files) {
    // The input appears once the page has drawn its toolbar, so the lookup waits for it.
    let nodeId = 0;
    for (let i = 0; i < 100 && !nodeId; i++) {
      const { root } = await this.send('DOM.getDocument', { depth: -1 });
      ({ nodeId } = await this.send('DOM.querySelector', { nodeId: root.nodeId, selector }));
      if (!nodeId) await new Promise((r) => setTimeout(r, 100));
    }
    if (!nodeId) throw new Error(`no file input for ${selector}`);
    await this.send('DOM.setFileInputFiles', { nodeId, files });
  }

  // setInput types a value into a React-controlled input. React ignores a plain `el.value =`
  // assignment, so the value goes in through the native setter, and an input event follows.
  async setInput(selector, value) {
    await this.evaluate(`(() => {
      const el = document.querySelector(${JSON.stringify(selector)});
      if (!el) throw new Error('no element for ' + ${JSON.stringify(selector)});
      const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
      Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, ${JSON.stringify(value)});
      el.dispatchEvent(new Event('input', { bubbles: true }));
    })()`);
  }

  // pickOption opens the antd Select that the selector names and clicks the option with the text.
  async pickOption(selector, text) {
    await this.evaluate(`(() => {
      const root = document.querySelector(${JSON.stringify(selector)});
      if (!root) throw new Error('no select for ' + ${JSON.stringify(selector)});
      root.querySelector('.ant-select-selector')?.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    })()`);
    await this.waitFor(
      `[...document.querySelectorAll('.ant-select-item-option')].some((o) => o.textContent.trim() === ${JSON.stringify(text)})`,
    );
    await this.evaluate(`(() => {
      const option = [...document.querySelectorAll('.ant-select-item-option')].find((o) => o.textContent.trim() === ${JSON.stringify(text)});
      option.click();
    })()`);
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
