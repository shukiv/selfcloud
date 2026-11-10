// SPDX-License-Identifier: AGPL-3.0-or-later
// Runs precompress.mjs on small fixture folders, not the real build. Run it with: node --test scripts/precompress.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { brotliDecompressSync, gunzipSync } from 'node:zlib';
import { randomBytes } from 'node:crypto';
import { fileURLToPath } from 'node:url';

const script = fileURLToPath(new URL('./precompress.mjs', import.meta.url));
const packageJSON = fileURLToPath(new URL('../package.json', import.meta.url));

// text is a compressible payload of about the given size.
function text(size) {
  const line = 'console.log("selfcloud", 1);\n';
  return Buffer.from(line.repeat(Math.ceil(size / line.length)).slice(0, size));
}

// fixture writes a dist folder: files maps a path under dist (for example assets/a.js) to its bytes. It returns the folder.
function fixture(files) {
  const dir = mkdtempSync(join(tmpdir(), 'precompress-'));
  for (const [name, bytes] of Object.entries(files)) {
    const path = join(dir, name);
    mkdirSync(join(path, '..'), { recursive: true });
    writeFileSync(path, bytes);
  }
  return dir;
}

function run(dir) {
  return spawnSync(process.execPath, [script], {
    env: { ...process.env, SELFCLOUD_PRECOMPRESS_DIST: dir },
    encoding: 'utf8',
  });
}

// list returns every file under the folder, by its path from the folder, sorted.
function list(dir, sub = '') {
  const out = [];
  for (const entry of readdirSync(join(dir, sub), { withFileTypes: true })) {
    const rel = sub ? `${sub}/${entry.name}` : entry.name;
    if (entry.isDirectory()) out.push(...list(dir, rel));
    else out.push(rel);
  }
  return out.sort();
}

test('a compressible file of each kept type gets a gzip and a brotli variant that decode to the file', () => {
  const kinds = ['js', 'css', 'svg', 'json', 'txt', 'map', 'ttf', 'otf'];
  const files = {};
  for (const k of kinds) files[`assets/app-${k}.${k}`] = text(4096);
  const dir = fixture(files);
  const r = run(dir);
  assert.equal(r.status, 0, r.stdout + r.stderr);
  for (const k of kinds) {
    const name = `assets/app-${k}.${k}`;
    const original = files[name];
    const gz = readFileSync(join(dir, `${name}.gz`));
    const br = readFileSync(join(dir, `${name}.br`));
    assert.ok(gz.length < original.length, `${name}.gz is not smaller`);
    assert.ok(br.length < original.length, `${name}.br is not smaller`);
    assert.deepEqual(gunzipSync(gz), original, `${name}.gz does not decode to the file`);
    assert.deepEqual(brotliDecompressSync(br), original, `${name}.br does not decode to the file`);
  }
});

test('types that do not compress are skipped: woff2, woff, and images other than SVG', () => {
  const kinds = ['woff2', 'woff', 'png', 'jpg', 'gif', 'webp'];
  const files = {};
  for (const k of kinds) files[`assets/font-${k}.${k}`] = text(4096);
  const dir = fixture(files);
  assert.equal(run(dir).status, 0);
  assert.deepEqual(list(dir), Object.keys(files).sort(), 'a variant was written for a type that is skipped');
});

test('a file under 1 KB gets no variant, and a file of 1 KB does', () => {
  const dir = fixture({ 'assets/small.js': text(1023), 'assets/edge.js': text(1024) });
  assert.equal(run(dir).status, 0);
  assert.deepEqual(list(dir), ['assets/edge.js', 'assets/edge.js.br', 'assets/edge.js.gz', 'assets/small.js']);
});

test('a variant that is not smaller than its file is not written', () => {
  const dir = fixture({ 'assets/noise.js': randomBytes(4096) });
  assert.equal(run(dir).status, 0);
  assert.deepEqual(list(dir), ['assets/noise.js'], 'a variant larger than its file was written');
});

test('files outside assets/ are never touched: the page, the offline page, the worker and the icons', () => {
  const outside = {
    'index.html': text(4096),
    'offline.html': text(4096),
    'sw.js': text(4096),
    'favicon.svg': text(4096),
    'robots.txt': text(4096),
  };
  const dir = fixture({ ...outside, 'assets/app.js': text(4096) });
  assert.equal(run(dir).status, 0);
  for (const name of Object.keys(outside)) {
    assert.deepEqual(readFileSync(join(dir, name)), outside[name], `${name} changed`);
  }
  assert.deepEqual(
    list(dir).filter((n) => !n.startsWith('assets/')),
    Object.keys(outside).sort(),
    'a variant was written outside assets/',
  );
});

test('a source named .gz fails the build, names it, and writes nothing', () => {
  const dir = fixture({ 'assets/legacy.js.gz': text(4096) });
  const r = run(dir);
  assert.equal(r.status, 1, r.stdout + r.stderr);
  assert.match(r.stderr, /legacy\.js\.gz/);
  assert.deepEqual(list(dir), ['assets/legacy.js.gz'], 'a file was written beside a source named .gz');
});

test('a source named .br fails the build too', () => {
  const dir = fixture({ 'assets/app.js.br': text(4096) });
  const r = run(dir);
  assert.equal(r.status, 1, r.stdout + r.stderr);
  assert.deepEqual(list(dir), ['assets/app.js.br'], 'a file was written beside a source named .br');
});

test('a nested folder under assets/ is compressed at every depth', () => {
  const dir = fixture({ 'assets/chunks/deep/x.js': text(4096) });
  assert.equal(run(dir).status, 0);
  const gz = readFileSync(join(dir, 'assets/chunks/deep/x.js.gz'));
  const br = readFileSync(join(dir, 'assets/chunks/deep/x.js.br'));
  assert.deepEqual(gunzipSync(gz), text(4096));
  assert.deepEqual(brotliDecompressSync(br), text(4096));
});

test('two fresh runs over the same files give byte-equal variants', () => {
  const files = { 'assets/a.js': text(4096), 'assets/b/c.css': text(3000) };
  const first = fixture(files);
  const second = fixture(files);
  assert.equal(run(first).status, 0);
  assert.equal(run(second).status, 0);
  for (const name of ['assets/a.js.gz', 'assets/a.js.br', 'assets/b/c.css.gz', 'assets/b/c.css.br']) {
    assert.deepEqual(readFileSync(join(first, name)), readFileSync(join(second, name)), `${name} differs between runs`);
  }
});

test('a second run on its own output is refused, and writes nothing', () => {
  const original = text(4096);
  const dir = fixture({ 'assets/app.js': original });
  assert.equal(run(dir).status, 0);
  const first = list(dir);
  const gzFirst = readFileSync(join(dir, 'assets/app.js.gz'));
  const again = run(dir);
  assert.equal(again.status, 1, 'a second run over its own variants was not refused');
  assert.deepEqual(list(dir), first, 'a second run wrote other files');
  assert.deepEqual(readFileSync(join(dir, 'assets/app.js.gz')), gzFirst, 'a second run rewrote the gzip variant');
  assert.deepEqual(readFileSync(join(dir, 'assets/app.js')), original);
});

test('a folder with no assets/ fails: the build has no files to compress', () => {
  const dir = fixture({ 'index.html': text(4096) });
  const r = run(dir);
  assert.equal(r.status, 1, r.stdout + r.stderr);
  assert.match(r.stderr, /assets/);
});

test('postbuild runs precompress after check-dist', () => {
  const postbuild = JSON.parse(readFileSync(packageJSON, 'utf8')).scripts.postbuild;
  const check = postbuild.indexOf('check-dist.mjs');
  const pre = postbuild.indexOf('precompress.mjs');
  assert.ok(check >= 0 && pre > check, `postbuild is ${postbuild}`);
});
