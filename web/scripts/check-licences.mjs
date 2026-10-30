// SPDX-License-Identifier: AGPL-3.0-or-later
// Enforces the dependency licence rule (README, "Dependency licences"):
//  - code that ships to the browser (the direct production dependencies and everything they pull in)
//    must be MIT, ISC, BSD or OFL;
//  - build-time packages may also be Apache-2.0, MPL-2.0, MIT-0, CC0-1.0 or BlueOak-1.0.0.
// Each licence is read from the package's own package.json in node_modules, and every package in the
// lock file must be installed for the check to see it.
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const webDir = fileURLToPath(new URL('../', import.meta.url));
const nm = join(webDir, 'node_modules');
const SHIPPED = new Set(['MIT', 'ISC', 'BSD-2-Clause', 'BSD-3-Clause', 'OFL-1.1']);
// Build-time packages may also use these. MIT-0 and CC0-1.0 are more permissive than MIT, and BlueOak-1.0.0
// is a permissive licence; they reach the tree only through the test runner's DOM emulation (jsdom), never the
// browser bundle, which the shipped check above still covers.
const BUILD = new Set([...SHIPPED, 'Apache-2.0', 'MPL-2.0', 'MIT-0', 'CC0-1.0', 'BlueOak-1.0.0']);

const readJSON = async (p) => JSON.parse(await readFile(p, 'utf8'));
const pkgJSON = (name) => readJSON(join(nm, name, 'package.json'));

// shipped walks the direct production dependencies, then their production dependencies, and so on.
// Optional and development dependencies do not reach the browser.
async function shipped(pkg) {
  const seen = new Set();
  const queue = Object.keys(pkg.dependencies ?? {});
  while (queue.length) {
    const name = queue.shift();
    if (seen.has(name)) continue;
    seen.add(name);
    let pj;
    try {
      pj = await pkgJSON(name);
    } catch {
      continue; // not installed for this platform
    }
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

const pkg = await readJSON(join(webDir, 'package.json'));
const lock = await readJSON(join(webDir, 'package-lock.json'));
const shippedSet = await shipped(pkg);
const problems = [];
const counts = new Map();
for (const key of Object.keys(lock.packages)) {
  if (!key) continue;
  const name = key.replace(/^.*node_modules\//, '');
  let pj;
  try {
    pj = await pkgJSON(name);
  } catch {
    // Platform-specific optional packages are not installed on every machine; anything else must be.
    if (!lock.packages[key].optional) problems.push(`${name}: in the lock file but not installed; run npm ci`);
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

if (!shippedSet.has('@ant-design/colors')) {
  problems.push('@ant-design/colors is not reached from the direct dependencies');
}

if (problems.length > 0) {
  console.error(problems.join('\n'));
  process.exit(1);
}
const summary = [...counts].sort((a, b) => b[1] - a[1]).map(([l, n]) => `${l} ${n}`).join(', ');
console.log(`licence check passed (${shippedSet.size} shipped packages): ${summary}`);
