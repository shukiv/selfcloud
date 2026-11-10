// SPDX-License-Identifier: AGPL-3.0-or-later
// Writes the precompressed variants of the built assets (decision 131). For each file under dist/assets/ of a type that
// compresses, it writes a gzip (level 9) and a brotli (quality 11) beside it, named <file>.gz and <file>.br. The server sends
// the variant a browser asks for (internal/web). Only Node's zlib is used: no dependency.
//
// Only assets/ is compressed. Those files are hashed and immutable. index.html is built for each response with its CSP nonce,
// and offline.html, the worker, the manifest and the icons are answered as they are. A variant is written only when it is
// smaller than its file. Vite empties dist/ before each build, so a variant from an earlier build cannot be left behind.
import { readdir, readFile, writeFile } from 'node:fs/promises';
import { join, extname, relative } from 'node:path';
import { brotliCompressSync, constants, gzipSync } from 'node:zlib';
import { fileURLToPath } from 'node:url';

// SELFCLOUD_PRECOMPRESS_DIST names another built folder; precompress.test.mjs uses it with fixtures. The build leaves it unset.
const dist = process.env.SELFCLOUD_PRECOMPRESS_DIST ?? fileURLToPath(new URL('../dist/', import.meta.url));
const assets = join(dist, 'assets');

// The types that compress. Woff and woff2 are compressed already, and images other than SVG gain nothing from it.
const KINDS = new Set(['.js', '.css', '.svg', '.json', '.map', '.txt', '.ttf', '.otf']);
// A smaller file gains too little to be worth a second file on the server.
const FLOOR = 1024;

async function files(dir) {
  const out = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) out.push(...(await files(path)));
    else out.push(path);
  }
  return out;
}

let list;
try {
  list = await files(assets);
} catch {
  console.error(`${assets} is missing: the build has no assets to compress`);
  process.exit(1);
}

// A source named .gz or .br would be indistinguishable from a variant, so it stops the build before anything is written. Vite
// empties dist/ before each build, so a file with either name there is a source, not a variant of this run.
const clashes = list.filter((path) => /\.(gz|br)$/.test(path));
if (clashes.length > 0) {
  const names = clashes.map((path) => relative(dist, path)).join(', ');
  console.error(`${names}: a file under assets/ is named .gz or .br; those names are the build's own variants, so rename the source`);
  process.exit(1);
}

let written = 0;
const sizes = { original: 0, gzip: 0, brotli: 0 };
for (const path of list.sort()) {
  if (!KINDS.has(extname(path))) continue;
  const bytes = await readFile(path);
  if (bytes.length < FLOOR) continue;
  sizes.original += bytes.length;

  const gz = gzipSync(bytes, { level: 9 });
  if (gz.length < bytes.length) {
    await writeFile(`${path}.gz`, gz);
    written++;
    sizes.gzip += gz.length;
  }
  const br = brotliCompressSync(bytes, {
    params: { [constants.BROTLI_PARAM_QUALITY]: 11, [constants.BROTLI_PARAM_SIZE_HINT]: bytes.length },
  });
  if (br.length < bytes.length) {
    await writeFile(`${path}.br`, br);
    written++;
    sizes.brotli += br.length;
  }
}

const kb = (n) => (n / 1024).toFixed(1);
console.log(
  `precompressed the assets: ${written} variants; the originals are ${kb(sizes.original)} KB, ` +
    `the gzip variants ${kb(sizes.gzip)} KB, the brotli variants ${kb(sizes.brotli)} KB`,
);
