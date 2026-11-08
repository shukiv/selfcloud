// SPDX-License-Identifier: AGPL-3.0-or-later

// Unit tests for saveFailure (diag.mjs): what a failed run keeps, and that keeping it never fails the run.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, readdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { saveFailure } from './diag.mjs';

// fakePage answers like the run's CDP page: a screenshot, an address and the errors and requests the run recorded.
function fakePage(overrides = {}) {
  return {
    errors: ["console: Uncaught TypeError: x is not a function"],
    sent: [
      { method: 'GET', url: 'http://127.0.0.1:9/api/v1/me' },
      { method: 'PATCH', url: 'http://127.0.0.1:9/api/v1/uploads/abc' },
    ],
    send: async (method) => {
      assert.equal(method, 'Page.captureScreenshot');
      return { data: Buffer.from('PNG-bytes').toString('base64') };
    },
    evaluate: async () => 'http://127.0.0.1:9/files/fld_x',
    ...overrides,
  };
}

test('a failed run keeps the picture, the address, the errors and the last requests', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'diag-test-'));
  process.env.SELFCLOUD_E2E_DIAG = dir;
  try {
    await saveFailure(fakePage(), 'run');
    const files = (await readdir(dir)).sort();
    assert.deepEqual(files, ['run-failure.png', 'run-failure.txt']);
    assert.equal((await readFile(join(dir, 'run-failure.png'))).toString(), 'PNG-bytes');
    const text = await readFile(join(dir, 'run-failure.txt'), 'utf8');
    assert.match(text, /address: http:\/\/127\.0\.0\.1:9\/files\/fld_x/);
    assert.match(text, /console: Uncaught TypeError: x is not a function/);
    assert.match(text, /PATCH \/api\/v1\/uploads\/abc/);
  } finally {
    delete process.env.SELFCLOUD_E2E_DIAG;
  }
});

test('without SELFCLOUD_E2E_DIAG nothing is written and nothing is asked of the page', async () => {
  delete process.env.SELFCLOUD_E2E_DIAG;
  let asked = false;
  await saveFailure(fakePage({ send: async () => { asked = true; return {}; } }), 'run');
  assert.equal(asked, false);
});

test('a page that cannot take the picture is reported, and the run is not failed again', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'diag-test-'));
  process.env.SELFCLOUD_E2E_DIAG = dir;
  const logged = [];
  const original = console.error;
  console.error = (msg) => logged.push(String(msg));
  try {
    await saveFailure(fakePage({ send: async () => { throw new Error('the socket closed'); } }), 'adminAccess-en');
    assert.equal(logged.length, 1);
    assert.match(logged[0], /could not keep the failure picture \(adminAccess-en\): the socket closed/);
  } finally {
    console.error = original;
    delete process.env.SELFCLOUD_E2E_DIAG;
  }
});
