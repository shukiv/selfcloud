// SPDX-License-Identifier: AGPL-3.0-or-later
// Enforces the dependency licence rule (README, "Dependency licences"):
//  - code that ships to the browser (production packages, and the packages the browser runtime of
//    Svelte imports, which go into dist/) must be MIT, ISC, BSD or OFL;
//  - build-time packages may also be Apache-2.0 or MPL-2.0.
// Each licence is read from the package's own package.json in node_modules.
import { readFile, readdir } from 'node:fs/promises';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const webDir = fileURLToPath(new URL('../', import.meta.url));
const nm = join(webDir, 'node_modules');
const SHIPPED = new Set(['MIT', 'ISC', 'BSD-2-Clause', 'BSD-3-Clause', 'OFL-1.1']);
const BUILD = new Set([...SHIPPED, 'Apache-2.0', 'MPL-2.0']);

const readJSON = async (p) => JSON.parse(await readFile(p, 'utf8'));
const pkgJSON = (name) => readJSON(join(nm, name, 'package.json'));

// bareImports returns the package names a file imports (relative and '#' imports are internal).
function bareImports(source) {
  const out = new Set();
  for (const m of source.matchAll(/(?:from|import)\s*\(?\s*['"]([^'"]+)['"]/g)) {
    const spec = m[1];
    if (spec.startsWith('.') || spec.startsWith('#') || spec.startsWith('/')) continue;
    const parts = spec.split('/');
    out.add(spec.startsWith('@') ? parts.slice(0, 2).join('/') : parts[0]);
  }
  return out;
}

async function jsFiles(dir) {
  const out = [];
  for (const e of await readdir(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) out.push(...(await jsFiles(p)));
    else if (e.name.endsWith('.js')) out.push(p);
  }
  return out;
}

// The browser runtime of Svelte: its client entry and the internal client and shared modules.
async function svelteRuntimeImports() {
  const svelteSrc = join(nm, 'svelte', 'src');
  const files = [join(svelteSrc, 'index-client.js'), ...(await jsFiles(join(svelteSrc, 'internal', 'client'))),
    ...(await jsFiles(join(svelteSrc, 'internal', 'shared')))];
  const names = new Set();
  for (const f of files) for (const n of bareImports(await readFile(f, 'utf8'))) names.add(n);
  return names;
}

// shipped returns the packages whose code is in the browser bundle: the runtime imports of Svelte, the
// fonts, and every production package in the lock, each with its own dependencies (transitively).
async function shipped(lock) {
  const roots = new Set(['svelte', '@fontsource-variable/inter', '@fontsource-variable/noto-sans-hebrew',
    '@fontsource-variable/noto-sans-arabic', ...(await svelteRuntimeImports())]);
  for (const [key, entry] of Object.entries(lock.packages)) {
    if (key && !entry.dev && !entry.optional) roots.add(key.replace(/^.*node_modules\//, ''));
  }
  const seen = new Set();
  const queue = [...roots];
  while (queue.length) {
    const name = queue.shift();
    if (seen.has(name)) continue;
    seen.add(name);
    let pj;
    try {
      pj = await pkgJSON(name);
    } catch {
      continue; // not installed here (for example a platform-specific build)
    }
    // Svelte's package.json lists its compiler's dependencies too; only its runtime imports ship, so it
    // is not walked through its dependencies.
    if (name === 'svelte') continue;
    for (const dep of Object.keys(pj.dependencies ?? {})) queue.push(dep);
  }
  return seen;
}

function licenceOf(pj) {
  const l = pj.license;
  if (typeof l === 'string') return l.replace(/\s*OR\s*/g, ' OR ').trim();
  if (l && typeof l.type === 'string') return l.type;
  return 'UNKNOWN';
}

const lock = await readJSON(join(webDir, 'package-lock.json'));
const shippedSet = await shipped(lock);
const problems = [];
const counts = new Map();
for (const key of Object.keys(lock.packages)) {
  if (!key) continue;
  const name = key.replace(/^.*node_modules\//, '');
  let pj;
  try {
    pj = await pkgJSON(name);
  } catch {
    continue;
  }
  const licence = licenceOf(pj);
  counts.set(licence, (counts.get(licence) ?? 0) + 1);
  const isShipped = shippedSet.has(name);
  const allowed = isShipped ? SHIPPED : BUILD;
  if (!allowed.has(licence)) {
    problems.push(`${name}@${pj.version}: ${licence} is not allowed for ${isShipped ? 'shipped code' : 'build-time code'}`);
  }
}

if (problems.length > 0) {
  console.error(problems.join('\n'));
  process.exit(1);
}
const summary = [...counts].sort((a, b) => b[1] - a[1]).map(([l, n]) => `${l} ${n}`).join(', ');
console.log(`licence check passed (${shippedSet.size} shipped packages): ${summary}`);
