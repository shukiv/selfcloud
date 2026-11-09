// SPDX-License-Identifier: AGPL-3.0-or-later

// The parity guard of the web client (design section 5.2). Every /api/v1 path in web/src has a route row in
// docs/cli-parity.tsv, and every exported function in web/src/lib that makes a request has an fn row, and every fn row
// names such a function. The rules are the ones that wrote the table: a function makes a request when its body calls
// fetch, or api, or a request helper of its module (a private function that calls fetch or api). A function that only
// calls an exported function is covered by that function's own row.
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

// The tests run with web/ as the working directory, as the other tests here do.
const SRC = join(process.cwd(), 'src');
const LIB = join(SRC, 'lib');
const API_FILE = join(LIB, 'api.ts');
const TABLE = join(process.cwd(), '..', 'docs', 'cli-parity.tsv');

interface Decl {
  key: string;
  name: string;
  file: string;
  exported: boolean;
  fn: boolean;
  body: string;
}

interface Import {
  file: string;
  name: string;
}

// A top-level declaration starts at column 0; a body runs to the next one.
const TOP = /^(export |function |async function |const |let |var |class |type |interface |import |enum )/;
const FN_DECL = /^(export )?(?:async )?function (\w+)/;
const CONST_DECL = /^(export )?const (\w+)\s*(?::[^=]*)?=\s*(.*)/;
const ARROW = /^(async\s*)?(\([^)]*\)|\w+)\s*(?::[^=]*)?=>/;
const FN_START = /^(async\s*)?function/;
const IMPORT = /import\s+(?:type\s+)?\{([^}]*)\}\s+from\s+["'](\.[^"']+)["']/g;

// sourceFiles lists the non-test modules under dir, sorted.
function sourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const p = join(dir, entry);
    if (statSync(p).isDirectory()) out.push(...sourceFiles(p));
    else if (/\.tsx?$/.test(entry) && !/\.test\.tsx?$/.test(entry) && !/\.d\.ts$/.test(entry)) out.push(p);
  }
  return out.sort();
}

const files = sourceFiles(LIB);
const decls: Decl[] = [];
const declsIn = new Map<string, Map<string, Decl>>();
const importsIn = new Map<string, Map<string, Import>>();
for (const f of files) {
  const src = readFileSync(f, 'utf8');
  const lines = src.split('\n').filter((l) => !/^\s*\/\//.test(l));
  const starts: number[] = [];
  lines.forEach((l, i) => {
    if (TOP.test(l)) starts.push(i);
  });
  const local = new Map<string, Decl>();
  declsIn.set(f, local);
  starts.forEach((s, k) => {
    const line = lines[s];
    const fm = FN_DECL.exec(line);
    const cm = CONST_DECL.exec(line);
    if (!fm && !cm) return;
    const name = fm ? fm[2] : cm![2];
    const end = k + 1 < starts.length ? starts[k + 1] : lines.length;
    const rest = cm ? cm[3] : '';
    const decl: Decl = {
      key: `${relative(SRC, f)}:${name}`,
      name,
      file: f,
      exported: !!(fm ? fm[1] : cm![1]),
      fn: !!fm || ARROW.test(rest) || FN_START.test(rest),
      body: lines.slice(s, end).join('\n'),
    };
    decls.push(decl);
    local.set(name, decl);
  });
  const imp = new Map<string, Import>();
  importsIn.set(f, imp);
  for (const m of src.matchAll(IMPORT)) {
    const base = resolve(dirname(f), m[2]);
    const target = [base + '.ts', join(base, 'index.ts')].find((p) => existsSync(p)) ?? base;
    for (const spec of m[1].split(',')) {
      const s = spec.trim().replace(/^type\s+/, '');
      if (!s) continue;
      const [orig, alias] = s.split(/\s+as\s+/);
      imp.set(alias || orig, { file: target, name: orig });
    }
  }
}

// resolveName is the declaration a name means in a file: a local one, or an imported one.
function resolveName(file: string, name: string): Decl | undefined {
  const local = declsIn.get(file)?.get(name);
  if (local) return local;
  const i = importsIn.get(file)?.get(name);
  return i ? declsIn.get(i.file)?.get(i.name) : undefined;
}

const apiDecl = declsIn.get(API_FILE)?.get('api');
const apiVisible = (file: string) => apiDecl !== undefined && resolveName(file, 'api') === apiDecl;
const callsName = (body: string, name: string) => new RegExp(`(?<![\\w.])${name}(?:<.+?>)?\\(`).test(body);
const directReq = (u: Decl) => u.fn && (/\bfetch\(/.test(u.body) || (apiVisible(u.file) && callsName(u.body, 'api')));
const isHelper = (u: Decl) => directReq(u) && (!u.exported || u === apiDecl);
const identsIn = (body: string) => [...new Set([...body.matchAll(/(?<![\w.])([A-Za-z_]\w*)/g)].map((m) => m[1]))];
const callsHelper = (d: Decl) =>
  identsIn(d.body).some((name) => {
    const u = resolveName(d.file, name);
    return u !== undefined && u !== d && isHelper(u) && callsName(d.body, name);
  });
// isRow: an exported function that makes a request under the rule above.
const isRow = (d: Decl) => d.exported && d.fn && (directReq(d) || callsHelper(d));

// The paths of the table and of the sources. A ${...} is a placeholder; a path is cut where a placeholder follows a
// name in the same segment, and at a query.
const cleanPath = (s: string) =>
  s
    .replace(/\$\{[^}]*\}/g, '{x}')
    .replace(/([^/])\{x\}.*$/, '$1')
    .replace(/[.,;:]+$/, '')
    .replace(/\?.*$/, '');
const literalsIn = (text: string) =>
  [...text.replace(/\$\{[^}]*\}/g, '{x}').matchAll(/\/api\/v1\/[^'"`\s?)]*/g)].map((m) => cleanPath(m[0]));
const normalize = (p: string) => p.replace(/\{[^}]*\}/g, '{}');

const table = readFileSync(TABLE, 'utf8')
  .split('\n')
  .filter((l) => l !== '')
  .slice(1)
  .map((l) => l.split('\t'));
const routePaths = new Set(
  table.filter((r) => r[0] === 'route').map((r) => normalize(r[1].slice(r[1].indexOf(' ') + 1))),
);
const fnKeys = new Set(table.filter((r) => r[0] === 'fn').map((r) => r[1]));

describe('cli parity', () => {
  it('every /api/v1 path in web/src has a route row in docs/cli-parity.tsv', () => {
    const unmatched: string[] = [];
    for (const f of sourceFiles(SRC)) {
      for (const p of literalsIn(readFileSync(f, 'utf8'))) {
        if (!routePaths.has(normalize(p))) unmatched.push(`${relative(SRC, f)}: ${p}`);
      }
    }
    expect(unmatched).toEqual([]);
  });

  it('every exported request function in web/src/lib has an fn row, and every fn row names one', () => {
    const byKey = new Map(decls.map((d) => [d.key, d]));
    const missing = decls.filter(isRow).map((d) => d.key).filter((k) => !fnKeys.has(k));
    const stale = [...fnKeys].filter((k) => {
      const d = byKey.get(k);
      return d === undefined || !isRow(d);
    });
    expect(missing).toEqual([]);
    expect(stale).toEqual([]);
  });
});
