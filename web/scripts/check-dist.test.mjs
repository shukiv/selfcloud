// SPDX-License-Identifier: AGPL-3.0-or-later
// Runs check-dist.mjs on small fixture folders, not the real build. Run it with: node --test scripts/check-dist.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const checker = fileURLToPath(new URL('./check-dist.mjs', import.meta.url));

// fixture writes a dist folder with the given index.html and its assets (name -> text), and returns its path.
function fixture(indexHTML, assets = {}) {
  const dir = mkdtempSync(join(tmpdir(), 'check-dist-'));
  mkdirSync(join(dir, 'assets'));
  writeFileSync(join(dir, 'index.html'), indexHTML);
  for (const [name, text] of Object.entries(assets)) writeFileSync(join(dir, 'assets', name), text);
  return dir;
}

function check(dir) {
  return spawnSync(process.execPath, [checker], {
    env: { ...process.env, SELFCLOUD_CHECK_DIST: dir },
    encoding: 'utf8',
  });
}

const NONCE_META = '<meta property="csp-nonce" nonce="__SELFCLOUD_CSP_NONCE__">';

// tl1: the antd Table stays out of the first load. A file the page names at once that holds the Table's marker fails.
test('a Table marker in a file the first load names fails: the Table must stay in a lazy chunk', () => {
  const html = `<!doctype html><html><head>${NONCE_META}<script type="module" nonce="__SELFCLOUD_CSP_NONCE__" src="/assets/index-a.js"></script></head><body></body></html>`;
  const dir = fixture(html, { 'index-a.js': 'const cls = "ant-table-selection-column";' });
  const r = check(dir);
  assert.equal(r.status, 1, r.stdout + r.stderr);
  assert.match(r.stderr, /\/assets\/index-a\.js is named by index\.html and holds the antd Table code/);
});

test('an index.html that names no module script and no modulepreload fails: the first load is empty', () => {
  const dir = fixture(`<!doctype html><html><head>${NONCE_META}</head><body></body></html>`);
  const r = check(dir);
  assert.equal(r.status, 1, r.stdout + r.stderr);
  assert.match(r.stderr, /first load is empty/);
});

test('CodeMirror in a first-load file fails: it must load with the first text file shown', () => {
  const dir = fixture(
    `<!doctype html><html><head>${NONCE_META}</head><body>` +
      '<script type="module" nonce="__SELFCLOUD_CSP_NONCE__" src="/assets/index-AAAAAAAA.js"></script></body></html>',
    { 'index-AAAAAAAA.js': 'const t={".cm-scroller":{display:"flex"}};' },
  );
  const r = check(dir);
  assert.equal(r.status, 1, r.stdout + r.stderr);
  assert.match(r.stderr, /CodeMirror is in the first load \(\/assets\/index-AAAAAAAA\.js\)/);
});

test('a missing group marker says it may have changed with the minifier or React', () => {
  // One entry chunk and no vendor chunk: React's markers are in no chunk named vendor, so the guard fails.
  const dir = fixture(
    `<!doctype html><html><head>${NONCE_META}</head><body>` +
      '<script type="module" nonce="__SELFCLOUD_CSP_NONCE__" src="/assets/index-AAAAAAAA.js"></script></body></html>',
    { 'index-AAAAAAAA.js': 'console.log(1);' },
  );
  const r = check(dir);
  assert.equal(r.status, 1, r.stdout + r.stderr);
  assert.match(r.stderr, /may have changed with the minifier or React/);
});

// sc1: the precompressed variants (.gz and .br beside each asset) are not checked as files of their own. Each one here carries
// the marker of a group, so a checker that read it as a JavaScript file would find React's marker in several files and fail.
test('the precompressed variants beside the build change nothing: the same pass, the same first-load line', () => {
  const vendor = 'const PureComponent=1;const f="__reactFiber$";';
  const parts = {
    'index-AAAAAAAA.js': 'const a=1;',
    'vendor-BBBBBBBB.js': vendor,
    'csp-CCCCCCCC.js': 'const s=document.querySelector(\'meta[property="csp-nonce"]\');',
    'antd-shared-DDDDDDDD.js': 'const t="rc-tooltip";',
  };
  const html =
    `<!doctype html><html><head>${NONCE_META}` +
    '<link rel="modulepreload" nonce="__SELFCLOUD_CSP_NONCE__" href="/assets/vendor-BBBBBBBB.js">' +
    '<link rel="modulepreload" nonce="__SELFCLOUD_CSP_NONCE__" href="/assets/csp-CCCCCCCC.js">' +
    '<link rel="modulepreload" nonce="__SELFCLOUD_CSP_NONCE__" href="/assets/antd-shared-DDDDDDDD.js">' +
    '<script type="module" nonce="__SELFCLOUD_CSP_NONCE__" src="/assets/index-AAAAAAAA.js"></script></head><body></body></html>';
  const plain = fixture(html, parts);
  const before = check(plain);
  assert.equal(before.status, 0, before.stdout + before.stderr);

  const variants = {};
  for (const [name, text] of Object.entries(parts)) {
    variants[`${name}.gz`] = text;
    variants[`${name}.br`] = text;
  }
  const withVariants = fixture(html, { ...parts, ...variants });
  const after = check(withVariants);
  assert.equal(after.status, 0, after.stdout + after.stderr);
  assert.equal(after.stdout, before.stdout, 'the variants changed the checker\'s figures');
});
