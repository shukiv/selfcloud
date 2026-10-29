// SPDX-License-Identifier: AGPL-3.0-or-later
// Fails when the source breaks the rules of the design system and of the strict content policy:
// physical left and right in layout, raw HTML injection, and inline style attributes (which the
// policy blocks). Run by npm run check.
import { readdir, readFile } from 'node:fs/promises';
import { join, extname, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../src/', import.meta.url));

// Physical properties and values. The leading group keeps "copyright:" and "data-left:" from matching.
const physical = /(margin|padding|border)-(left|right)\b|(^|[^\w-])(left|right)\s*:|text-align:\s*(left|right)\b/;
const rawHTML = /\{@html\b|\binnerHTML\b|\bouterHTML\b|\binsertAdjacentHTML\b|\bdocument\.write\b/;
const inlineStyle = /(^|\s)style\s*=\s*["{]/;

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
for (const path of await files(root)) {
  const ext = extname(path);
  if (!['.svelte', '.ts', '.css'].includes(ext)) continue;
  const name = relative(root, path);
  const lines = (await readFile(path, 'utf8')).split('\n');
  lines.forEach((line, i) => {
    const where = `${name}:${i + 1}`;
    if (physical.test(line)) problems.push(`${where}: physical left/right; use a logical property`);
    if (rawHTML.test(line)) problems.push(`${where}: raw HTML injection is not allowed`);
    if (ext === '.svelte' && inlineStyle.test(line)) {
      problems.push(`${where}: inline style attribute; the content policy blocks it`);
    }
  });
}

if (problems.length > 0) {
  console.error(problems.join('\n'));
  process.exit(1);
}
console.log('source checks passed');
