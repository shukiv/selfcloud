// SPDX-License-Identifier: AGPL-3.0-or-later
// Fails when the source breaks the design rules or the strict content policy: physical left and
// right in layout, raw HTML injection, element style attributes, inline script or style, the antd
// options that need an inline style, icon imports from the wrong package, and reading error
// messages (the interface shows codes, never server text). Run by npm run check.
import { readdir, readFile } from 'node:fs/promises';
import { join, extname, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';

const root = fileURLToPath(new URL('../src/', import.meta.url));

const rules = [
  // Physical properties, in CSS and in style objects. Logical ones: insetInlineStart, marginInlineEnd.
  [/(margin|padding|border)-(left|right)\b|(^|[^\w-])(left|right)\s*:|text-align:\s*(left|right)\b/, 'physical left/right in CSS; use a logical property'],
  [/\b(margin|padding|border)(Left|Right)\b|\btextAlign\s*:\s*['"](left|right)['"]/, 'physical left/right in a style object; use a logical property'],
  [/<style[\s>]|<style\s*$/, 'JSX <style> element; put the rule in a stylesheet'],
  [/dangerouslySetInnerHTML|\binnerHTML\b|\bouterHTML\b|\binsertAdjacentHTML\b|\bdocument\.write\b/, 'raw HTML injection is not allowed'],
  [/setAttribute\(\s*['"](style|on[a-z]*)['"]/, 'setting a style or event attribute is not allowed'],
  [/setAttributeNS\([^)]*['"](style|on[a-z]*)['"]/, 'setting a style or event attribute is not allowed'],
  [/\bautoSize\b|\bWatermark\b/, 'antd autoSize and Watermark write style attributes, which the policy blocks'],
  [/from\s+['"]@ant-design\/icons(\/[^'"]*)?['"]/, 'import icons from lucide-react, not @ant-design/icons'],
  [/\b(err|error|e)\.message\b/, 'read an error code, never the server text'],
];

// The antd statics (message, notification, Modal.confirm and the rest) render outside the app's
// ConfigProvider, so they get no nonce and no theme. The app uses App.useApp() for them.
const antdStatics = 'the antd static API renders outside the theme and the nonce; use App.useApp()';
const staticModals = new Set(['confirm', 'info', 'success', 'error', 'warning']);

// astProblems finds what a line rule cannot: a .message read anywhere in a catch clause (whatever the
// variable is called) or in a .catch callback, and the antd statics.
function astProblems(name, text) {
  const kind = name.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS;
  const sf = ts.createSourceFile(name, text, ts.ScriptTarget.Latest, true, kind);
  const at = (node) => sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1;
  const out = [];
  const staticNames = new Set();
  for (const st of sf.statements) {
    if (!ts.isImportDeclaration(st) || !st.importClause) continue;
    const spec = st.moduleSpecifier.text;
    if (spec !== 'antd' || !st.importClause.namedBindings || !ts.isNamedImports(st.importClause.namedBindings)) continue;
    for (const el of st.importClause.namedBindings.elements) {
      if (el.name.text === 'message' || el.name.text === 'notification') {
        staticNames.add(el.name.text);
        out.push([at(st), antdStatics]);
      }
    }
  }
  const visit = (node, inCatch) => {
    if (ts.isCatchClause(node)) inCatch = true;
    if (ts.isPropertyAccessExpression(node)) {
      if (node.name.text === 'message' && inCatch) out.push([at(node), 'read an error code, never the server text']);
      if (ts.isIdentifier(node.expression) && node.expression.text === 'Modal' && staticModals.has(node.name.text)) {
        out.push([at(node), antdStatics]);
      }
      if (ts.isIdentifier(node.expression) && staticNames.has(node.expression.text)) out.push([at(node), antdStatics]);
    }
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === 'catch') {
      for (const arg of node.arguments) {
        if ((ts.isArrowFunction(arg) || ts.isFunctionExpression(arg)) && arg.body) visit(arg.body, true);
      }
    }
    ts.forEachChild(node, (child) => visit(child, inCatch));
  };
  visit(sf, false);
  return out;
}

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
  if (!['.ts', '.tsx', '.css'].includes(ext)) continue;
  const name = relative(root, path);
  // Tests may name the forms they refuse; the rules below apply to the code that ships.
  const testing = /\.test\.tsx?$/.test(name);
  const lines = (await readFile(path, 'utf8')).split('\n');
  if (!testing && ext !== '.css') {
    for (const [line, why] of astProblems(name, lines.join('\n'))) problems.push(`${name}:${line}: ${why}`);
  }
  lines.forEach((line, i) => {
    for (const [re, why] of rules) {
      if (re.test(line)) problems.push(`${name}:${i + 1}: ${why}`);
    }
    if (!testing && /javascript:/i.test(line)) problems.push(`${name}:${i + 1}: javascript: URL`);
  });
}

if (problems.length > 0) {
  console.error(problems.join('\n'));
  process.exit(1);
}
console.log('source checks passed');
