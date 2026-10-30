// SPDX-License-Identifier: AGPL-3.0-or-later
// Checks the built files against the content policy the server sends and against the size budgets.
// The policy: no inline script or style, no inline style attribute, no reference to another origin.
// The nonce: the page carries the placeholder the server replaces (internal/web: cspNoncePlaceholder),
// in the meta tag and on its script and link tags. The budgets are gzip sizes, in bytes. They were
// measured on the first build (slice v1) and are fixed here; a change to them is a decision.
import { readdir, readFile } from 'node:fs/promises';
import { join, extname, relative, basename } from 'node:path';
import { gzipSync } from 'node:zlib';
import { fileURLToPath } from 'node:url';

const dist = fileURLToPath(new URL('../dist/', import.meta.url));
const PLACEHOLDER = '__SELFCLOUD_CSP_NONCE__';

// The budgets, in gzip bytes.
const BUDGET = {
  entry: 290 * 1024, // the entry chunk: every page is in it, and so is antd's core
  chunk: 120 * 1024, // any other JavaScript chunk
  language: 25 * 1024, // one language's catalogue or antd locale
};
const LANGUAGES = ['ar', 'en', 'es', 'fr', 'he', 'pt', 'ru'];

async function files(dir) {
  const out = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) out.push(...(await files(path)));
    else out.push(path);
  }
  return out;
}

const problems = [];
const inlineScript = /<script(?![^>]*\ssrc=)[^>]*>/i;
const inlineStyle = /<style[\s>]/i;
const styleAttr = /\sstyle\s*=\s*["']/i;
const externalCSS = /url\(\s*["']?(https?:|\/\/|data:)/i;
const externalImport = /@import\s/i;

let indexHTML = '';
try {
  indexHTML = await readFile(join(dist, 'index.html'), 'utf8');
} catch {
  problems.push('dist/index.html is missing');
}

if (indexHTML) {
  if (!/<meta\s+property="csp-nonce"\s+nonce="__SELFCLOUD_CSP_NONCE__"/.test(indexHTML)) {
    problems.push('index.html has no csp-nonce meta tag with the placeholder');
  }
  if (!indexHTML.includes(`nonce="${PLACEHOLDER}"`)) problems.push('index.html: no script or link carries the nonce placeholder');
}

const sizes = { entry: [], chunk: [], language: [] };
for (const path of await files(dist)) {
  const ext = extname(path);
  const name = relative(dist, path);
  if (ext === '.html') {
    const html = await readFile(path, 'utf8');
    if (inlineScript.test(html)) problems.push(`${name}: inline <script>`);
    if (inlineStyle.test(html)) problems.push(`${name}: inline <style>`);
    if (styleAttr.test(html)) problems.push(`${name}: inline style attribute`);
    if (/<link[^>]+href=["']https?:/i.test(html) || /src=["']https?:/i.test(html)) {
      problems.push(`${name}: reference to another origin`);
    }
  } else if (ext === '.css') {
    const css = await readFile(path, 'utf8');
    if (externalCSS.test(css)) problems.push(`${name}: url() to another origin or data:`);
    if (externalImport.test(css)) problems.push(`${name}: @import`);
  } else if (ext === '.js') {
    const js = await readFile(path);
    const gz = gzipSync(js, { level: 9 }).length;
    const base = basename(path);
    const stem = base.replace(/-[A-Za-z0-9_-]+\.js$/, '').replace(/\.js$/, '');
    const entry = name.startsWith('assets/index-');
    if (entry) sizes.entry.push({ name, gz });
    else if (LANGUAGES.includes(stem)) sizes.language.push({ name, gz });
    else sizes.chunk.push({ name, gz });
  }
}

for (const [kind, list] of Object.entries(sizes)) {
  for (const { name, gz } of list) {
    if (gz > BUDGET[kind]) problems.push(`${name}: ${gz} bytes gzip is over the ${kind} budget of ${BUDGET[kind]}`);
  }
}
if (sizes.entry.length !== 1) problems.push(`expected one entry chunk, found ${sizes.entry.length}`);

if (problems.length > 0) {
  console.error(problems.join('\n'));
  process.exit(1);
}
const kb = (n) => (n / 1024).toFixed(1);
console.log(
  `built files pass the content policy and the budgets (entry ${kb(sizes.entry[0]?.gz ?? 0)} KB of ${kb(BUDGET.entry)}; ` +
    `largest other chunk ${kb(Math.max(0, ...sizes.chunk.map((c) => c.gz)))} KB of ${kb(BUDGET.chunk)}; ` +
    `largest language ${kb(Math.max(0, ...sizes.language.map((c) => c.gz)))} KB of ${kb(BUDGET.language)})`,
);
