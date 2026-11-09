// SPDX-License-Identifier: AGPL-3.0-or-later

// The parity guard of the web client (design section 5.2). Every /api/v1 path in web/src has a route row in
// docs/cli-parity.tsv, and every exported function in web/src/lib that makes a request has an fn row, and every fn row
// names such a function. The rules are the ones that wrote the table, read with the TypeScript compiler, not with lines.
//
// A unit is a top-level function, an arrow or function expression bound to a const, a member of an object-literal
// export, or a method of a class. A unit makes a request when its own code (nested functions included) calls fetch,
// api, or sendBeacon, or names XMLHttpRequest, EventSource or WebSocket. A private unit (not exported, a private or
// protected method, a member of an unexported object) that makes a request is a helper: a unit that calls a helper
// makes a request too, at any depth. An exported unit that makes a request, directly or through helpers, is a row. A
// call to an exported unit that is not api does not make the caller a row: that unit has its own row.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, posix, relative, sep } from 'node:path';
import ts from 'typescript';
import { describe, expect, it } from 'vitest';

// The tests run with web/ as the working directory, as the other tests here do.
const SRC = join(process.cwd(), 'src');
const TABLE = join(process.cwd(), '..', 'docs', 'cli-parity.tsv');
const API_KEY = 'lib/api.ts:api';
const TABLE_HEADER = 'kind\tkey\tcommand\tslice\treason';

// Sources maps a path under src, with / as the separator, to its text.
export type Sources = Record<string, string>;

// loadSources reads every non-test module under src.
export function loadSources(): Sources {
  const out: Sources = {};
  const walk = (dir: string) => {
    for (const entry of readdirSync(dir)) {
      const p = join(dir, entry);
      if (statSync(p).isDirectory()) walk(p);
      else if (/\.tsx?$/.test(entry) && !/\.test\.tsx?$/.test(entry) && !/\.d\.ts$/.test(entry)) {
        out[relative(SRC, p).split(sep).join('/')] = readFileSync(p, 'utf8');
      }
    }
  };
  walk(SRC);
  return out;
}

const hasModifier = (n: ts.Node, kind: ts.SyntaxKind) =>
  (ts.canHaveModifiers(n) ? ts.getModifiers(n) : undefined)?.some((m) => m.kind === kind) ?? false;
const isExported = (n: ts.Node) => hasModifier(n, ts.SyntaxKind.ExportKeyword);
const unwrap = (e: ts.Expression): ts.Expression =>
  ts.isParenthesizedExpression(e) || ts.isAsExpression(e) || ts.isSatisfiesExpression(e) ? unwrap(e.expression) : e;
const isFunctionLike = (e: ts.Expression): e is ts.ArrowFunction | ts.FunctionExpression =>
  ts.isArrowFunction(e) || ts.isFunctionExpression(e);

// REQUEST_GLOBALS are the browser objects that send a request when they are made or used.
const REQUEST_GLOBALS = new Set(['XMLHttpRequest', 'EventSource', 'WebSocket']);

interface Unit {
  key: string;
  file: string;
  exported: boolean;
  direct: boolean;
  calls: string[];
}

interface Target {
  file: string;
  name: string;
}

// dottedPath is the text of a name or a property chain (a, a.b, this.m, a.b.c), or undefined for any other expression.
function dottedPath(e: ts.Expression): string | undefined {
  if (ts.isIdentifier(e)) return e.text;
  if (e.kind === ts.SyntaxKind.ThisKeyword) return 'this';
  if (ts.isPropertyAccessExpression(e)) {
    const base = dottedPath(e.expression);
    return base === undefined ? undefined : `${base}.${e.name.text}`;
  }
  return undefined;
}

// memberKey is the key of the member chain rest of the declaration name in file: file:name.rest.
function memberKey(file: string, name: string, rest: string[]): string {
  return `${file}:${[name, ...rest].join('.')}`;
}

// requestRows returns the keys (file:name, file:Class.method, file:object.member) of the exported units in lib that make
// a request, sorted.
export function requestRows(sources: Sources): string[] {
  const libFiles = Object.keys(sources).filter((f) => f.startsWith('lib/')).sort();
  const topNames = new Map<string, Set<string>>();
  const importsIn = new Map<string, Map<string, Target>>();
  const trees = new Map<string, ts.SourceFile>();
  for (const f of libFiles) {
    const sf = ts.createSourceFile(f, sources[f], ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
    trees.set(f, sf);
    const names = new Set<string>();
    const imp = new Map<string, Target>();
    for (const st of sf.statements) {
      if ((ts.isFunctionDeclaration(st) || ts.isClassDeclaration(st)) && st.name) names.add(st.name.text);
      else if (ts.isVariableStatement(st)) {
        for (const d of st.declarationList.declarations) if (ts.isIdentifier(d.name)) names.add(d.name.text);
      } else if (ts.isImportDeclaration(st) && ts.isStringLiteral(st.moduleSpecifier)) {
        const spec = st.moduleSpecifier.text;
        const bindings = st.importClause?.namedBindings;
        if (!spec.startsWith('.') || !bindings || !ts.isNamedImports(bindings)) continue;
        const base = posix.normalize(posix.join(posix.dirname(f), spec));
        const target = [`${base}.ts`, `${base}/index.ts`].find((p) => sources[p] !== undefined) ?? base;
        for (const el of bindings.elements) {
          imp.set(el.name.text, { file: target, name: (el.propertyName ?? el.name).text });
        }
      }
    }
    topNames.set(f, names);
    importsIn.set(f, imp);
  }
  // resolve is the top-level declaration a name means in a file: a local one, or an imported one.
  const resolve = (file: string, name: string): Target | undefined =>
    topNames.get(file)?.has(name) ? { file, name } : importsIn.get(file)?.get(name);

  const units = new Map<string, Unit>();
  // scan records what a unit's code does: the requests it makes itself, and the units it calls.
  const scan = (u: Unit, root: ts.Node, file: string, cls?: string) => {
    const visit = (n: ts.Node): void => {
      if (ts.isIdentifier(n)) {
        if (REQUEST_GLOBALS.has(n.text)) u.direct = true;
        const isProperty = ts.isPropertyAccessExpression(n.parent) && n.parent.name === n;
        if (n.text === 'fetch' && !isProperty) u.direct = true;
      }
      // A member named fetch or sendBeacon is a request too: the browser's, or one a unit gets through its deps
      // (watchPreview calls deps.fetch, and the live deps pass the browser's fetch).
      if (ts.isPropertyAccessExpression(n) && (n.name.text === 'fetch' || n.name.text === 'sendBeacon')) u.direct = true;
      if (ts.isCallExpression(n)) {
        const callee = n.expression;
        if (ts.isIdentifier(callee)) {
          const t = resolve(file, callee.text);
          if (t) u.calls.push(`${t.file}:${t.name}`);
        } else if (ts.isPropertyAccessExpression(callee)) {
          // a dotted path (this.m, obj.m, obj.inner.m) is named by its head: this is the unit's class, a name is a
          // declaration of the file or an import. The rest of the path is the member chain.
          const path = dottedPath(callee);
          if (path !== undefined) {
            const [head, ...rest] = path.split('.');
            if (head === 'this') {
              if (cls !== undefined) u.calls.push(memberKey(file, cls, rest));
            } else {
              const t = resolve(file, head);
              if (t) u.calls.push(memberKey(t.file, t.name, rest));
            }
          }
        }
      }
      ts.forEachChild(n, visit);
    };
    visit(root);
  };
  const add = (file: string, name: string, exported: boolean, body: ts.Node, cls?: string) => {
    const key = `${file}:${name}`;
    const u: Unit = { key, file, exported, direct: false, calls: [] };
    units.set(key, u);
    scan(u, body, file, cls);
  };

  // addObject adds each method and each function member of an object literal as prefix.member, and each member of a
  // nested object literal as prefix.member.member, at any depth.
  const addObject = (file: string, prefix: string, exported: boolean, obj: ts.ObjectLiteralExpression): void => {
    for (const p of obj.properties) {
      if (ts.isMethodDeclaration(p) && ts.isIdentifier(p.name) && p.body) {
        add(file, `${prefix}.${p.name.text}`, exported, p);
      } else if (ts.isPropertyAssignment(p) && ts.isIdentifier(p.name)) {
        const init = unwrap(p.initializer);
        if (isFunctionLike(init)) add(file, `${prefix}.${p.name.text}`, exported, init);
        else if (ts.isObjectLiteralExpression(init)) addObject(file, `${prefix}.${p.name.text}`, exported, init);
      }
    }
  };

  for (const [file, sf] of trees) {
    for (const st of sf.statements) {
      if (ts.isFunctionDeclaration(st) && st.name && st.body) {
        add(file, st.name.text, isExported(st), st);
      } else if (ts.isFunctionDeclaration(st) && !st.name && st.body && hasModifier(st, ts.SyntaxKind.DefaultKeyword)) {
        // An anonymous default export is named default.
        add(file, 'default', true, st);
      } else if (ts.isExportAssignment(st) && !st.isExportEquals) {
        // export default <expression>: a function, an arrow or an object literal, named default.
        const e = unwrap(st.expression);
        if (isFunctionLike(e)) add(file, 'default', true, e);
        else if (ts.isObjectLiteralExpression(e)) addObject(file, 'default', true, e);
      } else if (ts.isClassDeclaration(st) && (st.name || hasModifier(st, ts.SyntaxKind.DefaultKeyword))) {
        const cls = st.name?.text ?? 'default';
        for (const m of st.members) {
          if (!ts.isMethodDeclaration(m) || !m.body || !ts.isIdentifier(m.name)) continue;
          const priv = ts.isPrivateIdentifier(m.name) || hasModifier(m, ts.SyntaxKind.PrivateKeyword) ||
            hasModifier(m, ts.SyntaxKind.ProtectedKeyword);
          add(file, `${cls}.${m.name.text}`, isExported(st) && !priv, m, cls);
        }
      } else if (ts.isVariableStatement(st)) {
        const exp = isExported(st);
        for (const d of st.declarationList.declarations) {
          if (!ts.isIdentifier(d.name) || !d.initializer) continue;
          const init = unwrap(d.initializer);
          if (isFunctionLike(init)) {
            add(file, d.name.text, exp, init);
          } else if (ts.isObjectLiteralExpression(init)) {
            addObject(file, d.name.text, exp, init);
          }
        }
      }
    }
  }

  // A unit makes a request when it does so itself, or calls a helper that does. A helper is a unit that is not exported,
  // or api, the one request function every call goes through.
  const requesting = new Set([...units.values()].filter((u) => u.direct).map((u) => u.key));
  const helper = (key: string) => {
    const u = units.get(key);
    return u !== undefined && (!u.exported || key === API_KEY);
  };
  for (let changed = true; changed; ) {
    changed = false;
    for (const u of units.values()) {
      if (!requesting.has(u.key) && u.calls.some((c) => requesting.has(c) && helper(c))) {
        requesting.add(u.key);
        changed = true;
      }
    }
  }
  return [...units.values()].filter((u) => u.exported && requesting.has(u.key)).map((u) => u.key).sort();
}

// readTable reads docs/cli-parity.tsv: the header, then one row per line, five tab-separated fields each, LF line ends, and
// no key twice. It returns the rows without the header, and each problem it finds.
export function readTable(text: string): { rows: string[][]; problems: string[] } {
  const problems: string[] = [];
  const lines = text.split('\n');
  if (lines[lines.length - 1] === '') lines.pop();
  const rows: string[][] = [];
  const seen = new Set<string>();
  lines.forEach((line, i) => {
    const at = `line ${i + 1}`;
    if (line.includes('\r')) {
      problems.push(`${at}: a carriage return; the table uses LF line ends`);
      return;
    }
    if (i === 0) {
      if (line !== TABLE_HEADER) problems.push(`${at}: the header is not ${JSON.stringify(TABLE_HEADER)}`);
      return;
    }
    const cells = line.split('\t');
    if (cells.length !== 5) {
      problems.push(`${at}: ${cells.length} fields, want 5`);
      return;
    }
    const key = `${cells[0]}\t${cells[1]}`;
    if (seen.has(key)) problems.push(`${at}: ${cells[0]} ${JSON.stringify(cells[1])} is repeated`);
    seen.add(key);
    rows.push(cells);
  });
  return { rows, problems };
}

// componentProblems checks the files outside lib, the components and the pages. They make no request of their own: a
// call to fetch, to XMLHttpRequest or to api, and a /api/v1 path that is not the address of an image, a link, a video or
// an audio source, is a problem. The request goes through a function of lib, which has its row.
export function componentProblems(sources: Sources): string[] {
  const problems: string[] = [];
  const REQUEST = /\bfetch\(|XMLHttpRequest|\.sendBeacon\(|new EventSource\(|new WebSocket\(/;
  const API_CALL = /(?<![\w.])api(?:<[^>]*>)?\(/;
  const PATH = /\/api\/v1\//g;
  const ADDRESS_ATTR = /(?:href|src)=\{?[`"']$/;
  for (const [f, text] of Object.entries(sources)) {
    if (f.startsWith('lib/')) continue;
    // A comment is not code: its lines are skipped, as the request rule skips them.
    const code = text
      .split('\n')
      .filter((l) => !/^\s*\/\//.test(l))
      .join('\n');
    if (REQUEST.test(code)) problems.push(`${f}: makes a request outside lib`);
    if (API_CALL.test(code)) problems.push(`${f}: calls api outside lib`);
    for (const m of code.matchAll(PATH)) {
      if (!ADDRESS_ATTR.test(code.slice(0, m.index))) problems.push(`${f}: builds an /api/v1 path outside lib`);
    }
  }
  return problems;
}

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

const table = readTable(readFileSync(TABLE, 'utf8'));
const routePaths = new Set(
  table.rows.filter((r) => r[0] === 'route').map((r) => normalize(r[1].slice(r[1].indexOf(' ') + 1))),
);
const fnKeys = new Set(table.rows.filter((r) => r[0] === 'fn').map((r) => r[1]));

describe('cli parity', () => {
  it('the table is well formed', () => {
    expect(table.problems).toEqual([]);
  });

  it('every /api/v1 path in web/src has a route row in docs/cli-parity.tsv', () => {
    const sources = loadSources();
    const unmatched: string[] = [];
    for (const [f, text] of Object.entries(sources)) {
      for (const p of literalsIn(text)) {
        if (!routePaths.has(normalize(p))) unmatched.push(`${f}: ${p}`);
      }
    }
    expect(unmatched).toEqual([]);
  });

  it('every exported request function in web/src/lib has an fn row, and every fn row names one', () => {
    const rows = requestRows(loadSources());
    const missing = rows.filter((k) => !fnKeys.has(k));
    const stale = [...fnKeys].filter((k) => !rows.includes(k));
    expect(missing).toEqual([]);
    expect(stale).toEqual([]);
  });

  it('no component or page makes a request or builds an /api/v1 path of its own', () => {
    expect(componentProblems(loadSources())).toEqual([]);
  });
});

// The holes the rule had: each case is a source the rule must read. The api helper is a stub of the real one.
const API_STUB = "export async function api(method: string, path: string) {\n  return fetch(path, { method });\n}\n";

describe('the guard sees every kind of request function', () => {
  const cases: [string, string, string[]][] = [
    [
      'a typed arrow',
      "export const f: (x: string) => Promise<unknown> = (x) => api('GET', x);",
      ['lib/a.ts:f'],
    ],
    [
      'an arrow whose parameters span lines',
      "export const g = async (\n  x: string,\n) => api('GET', x);",
      ['lib/a.ts:g'],
    ],
    [
      'a helper chain two levels deep',
      "function inner(x: string) {\n  return api('GET', x);\n}\nfunction outer(x: string) {\n  return inner(x);\n}\nexport function chain(x: string) {\n  return outer(x);\n}",
      ['lib/a.ts:chain'],
    ],
    [
      'XMLHttpRequest',
      "export function sendChunk(p: string) {\n  const xhr = new XMLHttpRequest();\n  xhr.open('PATCH', p, true);\n  xhr.send(null);\n}",
      ['lib/a.ts:sendChunk'],
    ],
    ['navigator.sendBeacon', "export function ping(p: string) {\n  return navigator.sendBeacon(p, '');\n}", ['lib/a.ts:ping']],
    ['EventSource', 'export function live(p: string) {\n  return new EventSource(p);\n}', ['lib/a.ts:live']],
    ['WebSocket', 'export function socket(p: string) {\n  return new WebSocket(p);\n}', ['lib/a.ts:socket']],
    [
      'a method of an exported class',
      'export class ProbeClient {\n  go(p: string) {\n    return fetch(p);\n  }\n  name() {\n    return "probe";\n  }\n}',
      ['lib/a.ts:ProbeClient.go'],
    ],
    [
      'a member of an object-literal export',
      "export const probeObj = {\n  go: (p: string) => fetch(p),\n  name: 'probe',\n};",
      ['lib/a.ts:probeObj.go'],
    ],
    [
      'a member of a nested object-literal export',
      "export const probeApi = {\n  files: {\n    list: (p: string) => fetch(p),\n  },\n};",
      ['lib/a.ts:probeApi.files.list'],
    ],
    [
      'a call through a nested member of a private object',
      "const inner = {\n  files: {\n    list: (p: string) => fetch(p),\n  },\n};\nexport function viaInner(p: string) {\n  return inner.files.list(p);\n}",
      ['lib/a.ts:viaInner'],
    ],
    [
      'an anonymous default export function',
      'export default function (p: string) {\n  return fetch(p);\n}',
      ['lib/a.ts:default'],
    ],
    [
      'an anonymous default export arrow',
      "export default async (p: string) => api('GET', p);",
      ['lib/a.ts:default'],
    ],
    [
      'an anonymous default export object',
      "export default {\n  go: (p: string) => fetch(p),\n};",
      ['lib/a.ts:default.go'],
    ],
    [
      'a local request function named in an export list',
      'function go(p: string) {\n  return fetch(p);\n}\nexport { go };',
      ['lib/a.ts:go'],
    ],
    [
      'a local request function renamed in an export list',
      'function go(p: string) {\n  return fetch(p);\n}\nexport { go as other };',
      ['lib/a.ts:other'],
    ],
    [
      'a local request function as the default export',
      'function go(p: string) {\n  return fetch(p);\n}\nexport default go;',
      ['lib/a.ts:default'],
    ],
    [
      'a local request function exported as default in a list',
      'function go(p: string) {\n  return fetch(p);\n}\nexport { go as default };',
      ['lib/a.ts:default'],
    ],
    [
      'a request through an injected fetch',
      'export function watch(p: string, deps: { fetch: typeof fetch }) {\n  return deps.fetch(p);\n}',
      ['lib/a.ts:watch'],
    ],
    [
      'a call to an exported function does not make the caller a row',
      "export function plain(p: string) {\n  return fetch(p);\n}\nexport function caller(p: string) {\n  return plain(p);\n}",
      ['lib/a.ts:plain'],
    ],
    [
      'a private method of an exported class is a helper',
      "export class Client {\n  private go(p: string) {\n    return fetch(p);\n  }\n  list(p: string) {\n    return this.go(p);\n  }\n}",
      ['lib/a.ts:Client.list'],
    ],
  ];
  for (const [name, src, want] of cases) {
    it(`counts ${name}`, () => {
      // A case is a file of lib that imports api, as every file that calls it does.
      const file = `import { api } from './api';\n${src}`;
      const rows = requestRows({ 'lib/api.ts': API_STUB, 'lib/a.ts': file }).filter((k) => k.startsWith('lib/a.ts'));
      expect(rows).toEqual(want);
    });
  }
});

describe('the table reader refuses a bad table', () => {
  const header = `${TABLE_HEADER}\n`;
  it('refuses a repeated key', () => {
    const { problems } = readTable(`${header}fn\tlib/x.ts:f\t-\t-\tone\nfn\tlib/x.ts:f\t-\t-\ttwo\n`);
    expect(problems.length).toBeGreaterThan(0);
  });
  it('refuses a row without five fields', () => {
    const { problems } = readTable(`${header}fn\tlib/x.ts:f\t-\t-\n`);
    expect(problems.length).toBeGreaterThan(0);
  });
  it('refuses carriage returns at the line ends', () => {
    const { problems } = readTable(`${header.replace(/\n$/, '\r\n')}fn\tlib/x.ts:f\t-\t-\tone\r\n`);
    expect(problems.length).toBeGreaterThan(0);
  });
});

describe('the component rule', () => {
  it.each([
    ['a fetch', "export default function C() {\n  fetch('/x');\n  return null;\n}"],
    ['an api call', "import { api } from '../lib/api';\nexport default function C() {\n  return api('GET', '/x');\n}"],
    ['an /api/v1 path in a string', "const u = '/api/v1/nodes/1';"],
  ])('refuses %s', (_name, src) => {
    expect(componentProblems({ 'components/C.tsx': src }).length).toBeGreaterThan(0);
  });
  it.each([
    ['an href', '<a href="/api/v1/openapi.yaml">spec</a>'],
    ['an img src', '<img src={`/api/v1/nodes/${id}/content`} alt="" />'],
    ['a comment', '// POST /api/v1/nodes/{nod}/office is the editor\nexport const x = 1;'],
  ])('allows %s', (_name, src) => {
    expect(componentProblems({ 'components/C.tsx': src })).toEqual([]);
  });
});
