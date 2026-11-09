// SPDX-License-Identifier: AGPL-3.0-or-later
// The empty-state guard (decision 111). check-source.mjs runs it on every shipped .tsx file, and emptyStateGuard.test.mjs pins
// its behaviour on fixtures. An empty state is antd's Empty through EmptyState: each string literal equal to a key in EMPTY_KEYS
// must sit inside an EmptyState element, in its text prop or its children, or be held in a variable that is passed to an
// EmptyState's text prop. Anything else (a bare paragraph or box, a Typography.Paragraph, a function call, a key that is only
// rendered) fails. The check reads the TypeScript syntax tree, so line breaks, multi-line tags and wrappers do not hide a key.
// Outcome messages (preview, playlist stops, the share status) are not in the list. No place is exempt by file: FilesPage's
// heading and hint are inside the EmptyState description, like every other empty state.
import ts from 'typescript';

export const EMPTY_KEYS = [
  'starred.empty', 'recent.empty', 'shared.empty', 'trash.empty.none', 'files.empty', 'files.emptyMouse', 'files.emptyTouch',
  'requests.empty', 'people.empty', 'groups.empty', 'spaces.empty', 'audit.empty', 'links.empty', 'labels.empty',
  'tenants.empty', 'tenants.noMatch', 'search.empty', 'search.pageEmpty', 'notifications.empty', 'share.open.empty',
  'storage.largestEmpty', 'storage.peopleEmpty', 'storage.spacesEmpty', 'manage.overview.noActivity', 'tenant.users.empty',
  'keys.empty', 'files.media.queueEmpty', 'files.media.addNone', 'files.playlists.none', 'files.playlists.empty',
  'files.pick.empty', 'files.pick.noSpaces', 'files.versions.none', 'comments.empty', 'activity.empty', 'viewers.empty',
  'people.detail.noActivity', 'people.detail.noSpaces', 'people.detail.noGroups', 'groups.noMembers', 'share.none',
];

const isKeyLiteral = (node) => (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) && EMPTY_KEYS.includes(node.text);

// An EmptyState element, opening or self-closing.
const isEmptyStateTag = (node) =>
  (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) && ts.isIdentifier(node.tagName) && node.tagName.text === 'EmptyState';

// The text prop of an EmptyState: its value is the empty state's description.
const isEmptyStateText = (node) =>
  ts.isJsxAttribute(node) && ts.isIdentifier(node.name) && node.name.text === 'text' && ts.isJsxAttributes(node.parent) && isEmptyStateTag(node.parent.parent);

// judge says where one key literal is drawn: inside an EmptyState (ok), or held by the variable named holder (a declaration
// reached before any function boundary), or neither.
function judge(literal) {
  let child = literal;
  for (let p = literal.parent; p && !ts.isSourceFile(p); child = p, p = p.parent) {
    if (isEmptyStateText(p)) return { ok: true, holder: null };
    if (ts.isJsxElement(p) && child !== p.openingElement && isEmptyStateTag(p.openingElement)) return { ok: true, holder: null };
    if (ts.isFunctionLike(p)) return { ok: false, holder: null };
    if (ts.isVariableDeclaration(p)) return { ok: false, holder: ts.isIdentifier(p.name) ? p.name.text : null };
  }
  return { ok: false, holder: null };
}

// heldByEmptyState collects every identifier that appears in an EmptyState's text prop, so a key held in a variable can be
// passed there (TenantsPage picks its words into emptyText, then text={emptyText}).
function heldByEmptyState(sf) {
  const names = new Set();
  const collect = (node) => {
    if (ts.isIdentifier(node)) names.add(node.text);
    ts.forEachChild(node, collect);
  };
  const visit = (node) => {
    if (isEmptyStateText(node) && node.initializer) collect(node.initializer);
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return names;
}

// emptyStateProblems returns the problems in one file's text. file is its path under src, for the messages.
export function emptyStateProblems(file, text) {
  const sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const held = heldByEmptyState(sf);
  const problems = [];
  const visit = (node) => {
    if (isKeyLiteral(node)) {
      const { ok, holder } = judge(node);
      if (!ok && !(holder !== null && held.has(holder))) {
        const at = sf.getLineAndCharacterOfPosition(node.getStart(sf));
        problems.push(`${file}:${at.line + 1}: the empty state ${node.text} is not drawn through EmptyState (decision 111)`);
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return problems;
}
