// SPDX-License-Identifier: AGPL-3.0-or-later
// Converts the seven catalogues of the Svelte interface into the form the React interface reads.
// Each plural group key ends in .zero, .one, .two, .few, .many or .other in the source; the
// i18next form ends in _zero and so on. Keys, order and placeholders are kept. Run once when the
// React interface is started; after that the two catalogue sets are maintained by hand.
import { readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const LANGS = ['en', 'es', 'fr', 'pt', 'ru', 'ar', 'he'];
const PLURAL = /\.(zero|one|two|few|many|other)$/;
const src = fileURLToPath(new URL('../../web/src/i18n/', import.meta.url));
const dst = fileURLToPath(new URL('../src/i18n/', import.meta.url));

for (const lang of LANGS) {
  const raw = JSON.parse(await readFile(`${src}${lang}.json`, 'utf8'));
  const out = {};
  for (const [key, value] of Object.entries(raw)) {
    out[key.replace(PLURAL, (_, form) => `_${form}`)] = value;
  }
  await writeFile(`${dst}${lang}.json`, `${JSON.stringify(out, null, 2)}\n`);
  console.log(`${lang}: ${Object.keys(out).length} keys`);
}
