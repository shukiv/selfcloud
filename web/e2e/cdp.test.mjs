// SPDX-License-Identifier: AGPL-3.0-or-later

// Unit tests for the page's dialog, crash and timeout handling (cdp.mjs). A fake socket stands in for the browser, so the
// test needs no Chrome: the test emits the browser's events and answers the calls itself.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Page, watchTargetCrashes } from './cdp.mjs';

// FakeSocket answers each call with an empty result, except the evaluations the test sets an answer for. A call is not answered
// while silent is set, which is how a test holds a call open.
class FakeSocket {
  constructor(url) {
    this.url = url;
    this.sent = [];
    this.answers = new Map();
    this.silent = false;
    setTimeout(() => this.onopen?.(), 0);
  }

  send(text) {
    const msg = JSON.parse(text);
    this.sent.push(msg);
    if (this.silent) return;
    const expression = msg.params?.expression;
    if (msg.method === 'Runtime.evaluate' && this.answers.has(expression)) {
      const value = this.answers.get(expression);
      queueMicrotask(() => this.reply(msg.id, { result: { type: typeof value, value } }));
      return;
    }
    queueMicrotask(() => this.reply(msg.id, {}));
  }

  reply(id, result) {
    this.onmessage({ data: JSON.stringify({ id, result }) });
  }

  emit(method, params) {
    this.onmessage({ data: JSON.stringify({ method, params }) });
  }

  close() {
    this.onclose?.();
  }
}

// withFakeSocket runs fn with the fake in place of the browser's WebSocket, and puts the real one back afterwards.
async function withFakeSocket(fn) {
  const real = globalThis.WebSocket;
  globalThis.WebSocket = FakeSocket;
  try {
    return await fn();
  } finally {
    globalThis.WebSocket = real;
  }
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 10));

test('a JavaScript dialog is recorded by its type and message, and dismissed', async () => {
  await withFakeSocket(async () => {
    const page = new Page('ws://127.0.0.1:1/devtools/page/one');
    await page.start();
    assert.ok(page.ws.sent.some((m) => m.method === 'Page.enable'), 'Page.enable is sent, so the dialog events flow');
    page.ws.emit('Page.javascriptDialogOpening', { type: 'beforeunload', message: '', url: 'http://127.0.0.1/files' });
    await tick();
    assert.deepEqual(page.errors, ['dialog(beforeunload): (no message) at http://127.0.0.1/files']);
    const dismissed = page.ws.sent.filter((m) => m.method === 'Page.handleJavaScriptDialog');
    assert.equal(dismissed.length, 1);
    assert.deepEqual(dismissed[0].params, { accept: false }, 'the dialog is dismissed, so the page stays where it is');
    page.close();
  });
});

test('a wait that meets a dialog fails with the dialog and the page address on record', async () => {
  await withFakeSocket(async () => {
    const page = new Page('ws://127.0.0.1:1/devtools/page/two');
    await page.start();
    page.ws.answers.set('never', false);
    page.ws.answers.set('location.pathname', '/files');
    page.ws.emit('Page.javascriptDialogOpening', { type: 'alert', message: 'Leave the page?', url: 'http://127.0.0.1/files' });
    await tick();
    await assert.rejects(page.waitFor('never', 300), (err) => {
      assert.match(err.message, /^timed out waiting for: never \(the page is at \/files; open tabs: /);
      assert.match(err.message, /page errors: dialog\(alert\): Leave the page\? at http:\/\/127\.0\.0\.1\/files/);
      return true;
    });
    page.close();
  });
});

test('a call waiting on a socket that closes fails at once, with the socket named', async () => {
  await withFakeSocket(async () => {
    const page = new Page('ws://127.0.0.1:1/devtools/page/three');
    await page.start();
    page.ws.silent = true;
    const answer = page.send('Runtime.evaluate', { expression: '1' }, 0);
    page.close();
    await assert.rejects(answer, /the page socket closed/);
  });
});

test('a crashed target is recorded with the browser\'s reason, its exit code and its address', async () => {
  await withFakeSocket(async () => {
    const watch = watchTargetCrashes('ws://127.0.0.1:1/devtools/browser/one');
    await tick();
    assert.deepEqual(
      watch.socket.sent.map((m) => [m.method, m.params]),
      [['Target.setDiscoverTargets', { discover: true }]],
    );
    watch.socket.emit('Target.targetCreated', { targetInfo: { targetId: 'T1', type: 'page', url: 'http://127.0.0.1:9/files' } });
    watch.socket.emit('Target.targetInfoChanged', { targetInfo: { targetId: 'T1', type: 'page', url: 'http://127.0.0.1:9/files/x' } });
    watch.socket.emit('Target.targetCrashed', { targetId: 'T1', status: 'killed', errorCode: 9 });
    watch.socket.emit('Target.targetCrashed', { targetId: 'T2', status: 'oom', errorCode: 0 });
    await tick();
    assert.deepEqual(watch.crashes, [
      'page T1 at http://127.0.0.1:9/files/x crashed: killed (code 9)',
      'target T2 crashed: oom (code 0)',
    ]);
    watch.close();
  });
});

test('a renderer crash is recorded on the page', async () => {
  await withFakeSocket(async () => {
    const page = new Page('ws://127.0.0.1:1/devtools/page/four');
    await page.start();
    page.ws.emit('Inspector.targetCrashed', {});
    await tick();
    assert.deepEqual(page.errors, ['the page renderer crashed']);
    page.close();
  });
});
