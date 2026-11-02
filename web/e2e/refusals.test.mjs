// SPDX-License-Identifier: AGPL-3.0-or-later

// Unit tests for the rule that makes a 415 expected: only a conversion of a node the s4b section refused on purpose.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { refusedOnPurpose } from './refusals.mjs';

const answer = (url, status) => ({
  source: 'network',
  url,
  text: `Failed to load resource: the server responded with a status of ${status} (Unsupported Media Type)`,
});

test('a 415 on a node the section refused on purpose is expected, for its preview and its export', () => {
  const ids = new Set(['nod_refused']);
  assert.equal(refusedOnPurpose(answer('http://127.0.0.1:1/api/v1/nodes/nod_refused/preview?format=pdf', 415), ids), true);
  assert.equal(refusedOnPurpose(answer('http://127.0.0.1:1/api/v1/nodes/nod_refused/exports/drv_1', 415), ids), true);
});

test('a 415 on any other node is not expected, even in the same run', () => {
  const ids = new Set(['nod_refused']);
  assert.equal(refusedOnPurpose(answer('http://127.0.0.1:1/api/v1/nodes/nod_other/preview?format=pdf', 415), ids), false);
  assert.equal(refusedOnPurpose(answer('http://127.0.0.1:1/api/v1/nodes/nod_other/exports/drv_1', 415), ids), false);
});

test('a status other than 415 on a refused node is not expected', () => {
  const ids = new Set(['nod_refused']);
  assert.equal(refusedOnPurpose(answer('http://127.0.0.1:1/api/v1/nodes/nod_refused/preview?format=pdf', 503), ids), false);
  assert.equal(refusedOnPurpose(answer('http://127.0.0.1:1/api/v1/nodes/nod_refused/preview?format=pdf', 404), ids), false);
});

test('with no refused nodes named, nothing is expected', () => {
  assert.equal(refusedOnPurpose(answer('http://127.0.0.1:1/api/v1/nodes/nod_x/preview', 415), new Set()), false);
});

test('a non-network entry is not expected', () => {
  const ids = new Set(['nod_refused']);
  assert.equal(
    refusedOnPurpose({ source: 'console', url: 'http://127.0.0.1:1/api/v1/nodes/nod_refused/preview', text: 'status of 415' }, ids),
    false,
  );
});
