// SPDX-License-Identifier: AGPL-3.0-or-later
// The empty-state guard (decision 111). check-source.mjs runs it on every shipped .ts and .tsx file, and emptyStateGuard.test.mjs
// pins its behaviour on fixtures. An empty state is antd's Empty through EmptyState. Each string literal equal to a key in
// EMPTY_KEYS must sit inside an EmptyState element (its text prop, or its children, which are the action slot), or be held in a
// variable whose binding is read by an EmptyState's text prop in the same function. The EmptyState must be the default import
// from a module named EmptyState, with no other binding of that name in the file. The check reads the TypeScript syntax tree,
// so line breaks, multi-line tags and wrappers do not hide a key. The limits are written in decision 111.
import ts from 'typescript';

export const EMPTY_KEYS = [
  'starred.empty', 'recent.empty', 'shared.empty', 'trash.empty.none', 'files.empty', 'files.emptyMouse', 'files.emptyTouch',
  'requests.empty', 'people.empty', 'groups.empty', 'spaces.empty', 'audit.empty', 'links.empty', 'labels.empty',
  'tenants.empty', 'tenants.noMatch', 'search.empty', 'search.pageEmpty', 'notifications.empty', 'share.open.empty',
  'storage.largestEmpty', 'storage.peopleEmpty', 'storage.spacesEmpty', 'manage.overview.noActivity', 'tenant.users.empty',
  'keys.empty', 'files.media.queueEmpty', 'files.media.addNone', 'files.playlists.none', 'files.playlists.empty',
  'files.pick.empty', 'files.pick.noSpaces', 'files.versions.none', 'comments.empty', 'activity.empty', 'viewers.empty',
  'people.detail.noActivity', 'people.detail.noSpaces', 'people.detail.noGroups', 'groups.noMembers', 'share.none',
  'tenant.reset.none',
];

// isScanned says whether the guard reads a file: shipped TypeScript, never the tests.
export function isScanned(name) {
  return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name);
}

// A key literal that names a dictionary entry (the property name in { "starred.empty": "..." }) is not drawn, so it is not checked.
const isKeyLiteral = (node) =>
  (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) &&
  EMPTY_KEYS.includes(node.text) &&
  !(ts.isPropertyAssignment(node.parent) && node.parent.name === node);

// The default import of EmptyState from a module named EmptyState (components/EmptyState, or ../EmptyState), or null.
function importedEmptyState(sf) {
  for (const st of sf.statements) {
    const clause = ts.isImportDeclaration(st) ? st.importClause : undefined;
    if (
      clause && clause.name?.text === 'EmptyState' && !clause.namedBindings &&
      ts.isStringLiteral(st.moduleSpecifier) && /(^|\/)EmptyState$/.test(st.moduleSpecifier.text)
    ) {
      return clause;
    }
  }
  return null;
}

// shadowed says whether any other binding in the file is also named EmptyState, so a tag may not be the import.
function shadowed(sf, clause) {
  let found = false;
  const visit = (n) => {
    if (n === clause) return;
    const named = (n.name && ts.isIdentifier(n.name) && n.name.text === 'EmptyState') || (ts.isImportClause(n) && n.name?.text === 'EmptyState');
    if (named && (ts.isVariableDeclaration(n) || ts.isParameter(n) || ts.isFunctionDeclaration(n) || ts.isClassDeclaration(n) ||
      ts.isBindingElement(n) || ts.isImportSpecifier(n) || ts.isImportClause(n))) {
      found = true;
    }
    ts.forEachChild(n, visit);
  };
  visit(sf);
  return found;
}

// hasEmptyStateTag says whether the file writes an EmptyState element at all.
function hasEmptyStateTag(sf) {
  let found = false;
  const visit = (n) => {
    if ((ts.isJsxOpeningElement(n) || ts.isJsxSelfClosingElement(n)) && ts.isIdentifier(n.tagName) && n.tagName.text === 'EmptyState') found = true;
    ts.forEachChild(n, visit);
  };
  visit(sf);
  return found;
}

// emptyStateProblems returns the problems in one file's text. file is its path under src, for the messages.
export function emptyStateProblems(file, text) {
  const kind = file.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS;
  const sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, kind);
  const clause = importedEmptyState(sf);
  const bound = clause !== null && !shadowed(sf, clause);
  const problems = [];
  if (!bound && hasEmptyStateTag(sf)) {
    problems.push(`${file}: EmptyState is not the default import from components/EmptyState (decision 111)`);
  }
  // Only the imported component counts as an EmptyState. With no valid import, nothing does, so every key in the file is reported.
  const isTag = (node) =>
    bound && (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) && ts.isIdentifier(node.tagName) && node.tagName.text === 'EmptyState';
  const isText = (node) =>
    ts.isJsxAttribute(node) && ts.isIdentifier(node.name) && node.name.text === 'text' && ts.isJsxAttributes(node.parent) && isTag(node.parent.parent);

  // scopeOf is the nearest function, or the file, that a node sits in: where a const or a parameter is bound.
  const scopeOf = (node) => {
    for (let p = node.parent; p; p = p.parent) if (ts.isFunctionLike(p) || ts.isSourceFile(p)) return p;
    return null;
  };
  // declarations maps a name to the variable declarations that bind it, with the scope each one is in.
  const declarations = new Map();
  const collectDecls = (n) => {
    if (ts.isVariableDeclaration(n) && ts.isIdentifier(n.name)) {
      const list = declarations.get(n.name.text) ?? [];
      list.push({ decl: n, scope: scopeOf(n) });
      declarations.set(n.name.text, list);
    }
    ts.forEachChild(n, collectDecls);
  };
  collectDecls(sf);
  // bindingOf resolves an identifier the way a reader would: the nearest enclosing scope that declares its name.
  const bindingOf = (ident) => {
    const scopes = [];
    for (let p = ident.parent; p; p = p.parent) if (ts.isFunctionLike(p) || ts.isSourceFile(p)) scopes.push(p);
    for (const scope of scopes) {
      const hits = (declarations.get(ident.text) ?? []).filter((d) => d.scope === scope).map((d) => d.decl);
      if (hits.length > 0) return hits;
    }
    return [];
  };
  // heldByEmptyState collects the declarations whose names are read by an EmptyState's text prop. A property name (copy.empty)
  // is not a read of a variable called empty.
  const held = new Set();
  const collectHeld = (n) => {
    if (isText(n) && n.initializer) {
      const readRoots = (x) => {
        if (ts.isIdentifier(x)) {
          const p = x.parent;
          const isPropertyName = (ts.isPropertyAccessExpression(p) && p.name === x) || (ts.isPropertyAssignment(p) && p.name === x);
          if (!isPropertyName) for (const d of bindingOf(x)) held.add(d);
        }
        ts.forEachChild(x, readRoots);
      };
      readRoots(n.initializer);
    }
    ts.forEachChild(n, collectHeld);
  };
  collectHeld(sf);

  // judge says where one key literal is drawn: inside an EmptyState (ok), or held by a declaration (holder), or neither.
  const judge = (literal) => {
    let child = literal;
    for (let p = literal.parent; p && !ts.isSourceFile(p); child = p, p = p.parent) {
      if (isText(p)) return { ok: true, holder: null };
      if (ts.isJsxElement(p) && child !== p.openingElement && isTag(p.openingElement)) return { ok: true, holder: null };
      if (ts.isFunctionLike(p)) return { ok: false, holder: null };
      if (ts.isVariableDeclaration(p)) return { ok: false, holder: ts.isIdentifier(p.name) ? p : null };
    }
    return { ok: false, holder: null };
  };

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
