// SPDX-License-Identifier: AGPL-3.0-or-later
// Checks that every literal translation key in the source exists in the English catalogue.
// It reads t('key'), tp('key' ...) and the object form key: 'key' that the state modules return for
// a sentence to be translated later. A plural key needs its _other form. Keys built at run time
// (template strings, concatenation) are not checked; the catalogue tests cover those families.
import { readdir, readFile } from 'node:fs/promises';
import { join, relative, extname } from 'node:path';
import { fileURLToPath } from 'node:url';

const src = fileURLToPath(new URL('../src/', import.meta.url));
const en = JSON.parse(await readFile(join(src, 'i18n', 'en.json'), 'utf8'));

async function files(dir) {
  const out = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) out.push(...(await files(path)));
    else out.push(path);
  }
  return out;
}

const calls = /\btp?\(\s*'([^']+)'/g;
// A translation key always has a dot (nav.files); other key: properties (sort keys, for one) do not.
const objects = /\bkey:\s*'([^']+\.[^']+)'/g;

const problems = [];
let checked = 0;
for (const path of await files(src)) {
  if (!['.ts', '.tsx'].includes(extname(path))) continue;
  const text = await readFile(path, 'utf8');
  const name = relative(src, path);
  const isPlural = (m) => /\btp\(/.test(m);
  for (const m of text.matchAll(calls)) {
    checked++;
    const key = m[1];
    const ok = isPlural(m[0]) ? `${key}_other` in en : key in en;
    if (!ok) problems.push(`${name}: no English text for ${key}`);
  }
  for (const m of text.matchAll(objects)) {
    checked++;
    if (!(m[1] in en) && !(`${m[1]}_other` in en)) problems.push(`${name}: no English text for ${m[1]}`);
  }
}

if (problems.length > 0) {
  console.error([...new Set(problems)].join('\n'));
  process.exit(1);
}
console.log(`key checks passed (${checked} literal keys)`);
