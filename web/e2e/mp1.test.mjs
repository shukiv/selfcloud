// SPDX-License-Identifier: AGPL-3.0-or-later

// Unit tests for the media page's resume-save wait (mp1.mjs). A fake page stands in for the browser's record of
// requests, so the test needs no Chrome: it names what was sent and what is still in flight.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { waitResumeSaved } from './mp1.mjs';

const RESUME = 'http://127.0.0.1:9/api/v1/nodes/nod_a/resume';
const put = () => ({ method: 'PUT', url: RESUME });

test('a resume request an earlier section left in flight does not hold the wait', async () => {
  const mark = Date.now();
  const page = { sent: [put()], inflight: new Map([['old', { url: RESUME, at: mark - 60000, status: 204 }]]) };
  page.sent.push(put());
  await waitResumeSaved(page, 1, mark, 300);
});

test('a resume PUT sent after the mark that never ends still times out', async () => {
  const mark = Date.now();
  const page = { sent: [put()], inflight: new Map() };
  page.sent.push(put());
  page.inflight.set('new', { url: RESUME, at: mark + 1, status: 204 });
  await assert.rejects(waitResumeSaved(page, 1, mark, 300), /the resume place was not saved/);
});

test('the wait holds until the new PUT leaves the flight record', async () => {
  const mark = Date.now();
  const page = { sent: [put()], inflight: new Map() };
  page.sent.push(put());
  page.inflight.set('new', { url: RESUME, at: Date.now(), status: 0 });
  setTimeout(() => page.inflight.delete('new'), 60);
  await waitResumeSaved(page, 1, mark, 2000);
  assert.equal(page.inflight.size, 0);
});

test('no new PUT after the pause times out', async () => {
  const mark = Date.now();
  const page = { sent: [put()], inflight: new Map() };
  await assert.rejects(waitResumeSaved(page, 1, mark, 300), /the resume place was not saved/);
});
