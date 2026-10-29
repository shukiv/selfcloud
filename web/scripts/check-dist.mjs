// SPDX-License-Identifier: AGPL-3.0-or-later
// Checks the built files against the content policy the server sends: no inline script or style, no
// inline style attributes, and no reference to another origin. Run after vite build.
import { readdir, readFile } from 'node:fs/promises';
import { join, extname, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const dist = fileURLToPath(new URL('../dist/', import.meta.url));

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
  }
}

try {
  await readFile(join(dist, 'index.html'));
} catch {
  problems.push('dist/index.html is missing');
}

if (problems.length > 0) {
  console.error(problems.join('\n'));
  process.exit(1);
}
console.log('built files pass the content policy');
