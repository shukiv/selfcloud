// SPDX-License-Identifier: AGPL-3.0-or-later

// The parity guard of the web client (design section 5.2). Every /api/v1 path in web/src has a route row in
// docs/cli-parity.tsv, and every exported function in web/src/lib that makes a request has an fn row, and every fn row
// names such a function. The rules are the ones that wrote the table: a function makes a request when its body calls
// fetch, or api, or a request helper of its module (a private function that calls fetch or api). A function that only
// calls an exported function is covered by that function's own row.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, posix, relative, sep } from 'node:path';
import { describe, expect, it } from 'vitest';

// The tests run with web/ as the working directory, as the other tests here do.
const SRC = join(process.cwd(), 'src');
const TABLE = join(process.cwd(), '..', 'docs', 'cli-parity.tsv');
const API_FILE = 'lib/api.ts';

// Sources maps a path under src, with / as the separator, to its text.
export type Sources = Record<string, string>;

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

// requestRows returns the keys (file:name) of the exported functions in lib that make a request, sorted.
export function requestRows(sources: Sources): string[] {
  const libFiles = Object.keys(sources).filter((f) => f.startsWith('lib/')).sort();
  const decls: Decl[] = [];
  const declsIn = new Map<string, Map<string, Decl>>();
  const importsIn = new Map<string, Map<string, Import>>();
  for (const f of libFiles) {
    const src = sources[f];
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
        key: `${f}:${name}`,
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
      const base = posix.normalize(posix.join(posix.dirname(f), m[2]));
      const target = [`${base}.ts`, `${base}/index.ts`].find((p) => sources[p] !== undefined) ?? base;
      for (const spec of m[1].split(',')) {
        const s = spec.trim().replace(/^type\s+/, '');
        if (!s) continue;
        const [orig, alias] = s.split(/\s+as\s+/);
        imp.set(alias || orig, { file: target, name: orig });
      }
    }
  }

  // resolveName is the declaration a name means in a file: a local one, or an imported one.
  const resolveName = (file: string, name: string): Decl | undefined => {
    const local = declsIn.get(file)?.get(name);
    if (local) return local;
    const i = importsIn.get(file)?.get(name);
    return i ? declsIn.get(i.file)?.get(i.name) : undefined;
  };
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
  const isRow = (d: Decl) => d.exported && d.fn && (directReq(d) || callsHelper(d));
  return decls.filter(isRow).map((d) => d.key).sort();
}

// readTable splits docs/cli-parity.tsv into its rows, without the header, and reports each problem it finds.
export function readTable(text: string): { rows: string[][]; problems: string[] } {
  const rows = text
    .split('\n')
    .filter((l) => l !== '')
    .slice(1)
    .map((l) => l.split('\t'));
  return { rows, problems: [] };
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

const { rows: table } = readTable(readFileSync(TABLE, 'utf8'));
const routePaths = new Set(
  table.filter((r) => r[0] === 'route').map((r) => normalize(r[1].slice(r[1].indexOf(' ') + 1))),
);
const fnKeys = new Set(table.filter((r) => r[0] === 'fn').map((r) => r[1]));

describe('cli parity', () => {
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
  ];
  for (const [name, src, want] of cases) {
    it(`counts ${name}`, () => {
      const rows = requestRows({ 'lib/api.ts': API_STUB, 'lib/a.ts': src }).filter((k) => k.startsWith('lib/a.ts'));
      expect(rows).toEqual(want);
    });
  }
});

describe('the table reader refuses a bad table', () => {
  const header = 'kind\tkey\tcommand\tslice\treason\n';
  it('refuses a repeated key', () => {
    const { problems } = readTable(
      `${header}fn\tlib/x.ts:f\t-\t-\tone\nfn\tlib/x.ts:f\t-\t-\ttwo\n`,
    );
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
