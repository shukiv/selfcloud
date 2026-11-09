// SPDX-License-Identifier: AGPL-3.0-or-later
// Pins the empty-state guard (emptyStateGuard.mjs) on small fixtures: each bypass must be flagged, and each shape that is
// the approved layout must pass. Run with: node --test scripts/emptyStateGuard.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { emptyStateProblems, isScanned } from './emptyStateGuard.mjs';

// The file set the guard reads: shipped .ts and .tsx files, never the tests.
test('the guard scans shipped .ts and .tsx files, not tests or styles', () => {
  assert.equal(isScanned('lib/copy.ts'), true);
  assert.equal(isScanned('pages/A.tsx'), true);
  assert.equal(isScanned('pages/A.test.tsx'), false);
  assert.equal(isScanned('lib/copy.test.ts'), false);
  assert.equal(isScanned('index.css'), false);
});

// page wraps a fixture's JSX in a component that imports EmptyState, as a real page does, so the bypasses are the only faults.
const page = (body, pre = '') => `import EmptyState from "../components/EmptyState";
import { useT } from "../lib/i18n";

export default function Page() {
  const { t, common } = useT();
${pre}  return (
    <main>
${body}
    </main>
  );
}
`;

// Bypasses the guard must flag.
const bypass = {
  'a key split across lines': page(`      <p role="status">{t(
        "starred.empty",
      )}</p>`),
  'a key held in a variable and rendered in a paragraph': page('      <p role="status">{msg}</p>', '  const msg = t("starred.empty");\n'),
  'Typography.Paragraph': page('      <Typography.Paragraph>{t("starred.empty")}</Typography.Paragraph>'),
  'a multi-line paragraph opening tag': page(`      <p
        role="status"
      >{t("starred.empty")}</p>`),
  'common() instead of t()': page('      <p>{common("share.none")}</p>'),
  'the FilesPage blocker: a bare paragraph with files.empty next to the drop box': (
    // The file name is FilesPage's own, where the old guard exempted the key for the whole file.
    { name: 'pages/FilesPage.tsx', src: page('      <p>{t("files.empty")}</p>') }
  ),
  // Review 2 of em1: a name is not a binding. The text prop reads copy.empty, which is not the local empty.
  'a variable with the same name as a property passed to EmptyState': page(
    '      <p role="status">{empty}</p>\n      <EmptyState text={copy.empty} />',
    '  const empty = t("starred.empty");\n  const copy = { empty: "plain" };\n',
  ),
  // The same name in another function is another binding.
  'a variable of the same name in another function': {
    name: 'pages/Fixture.tsx',
    src: `import EmptyState from "../components/EmptyState";
import { useT } from "../lib/i18n";

export function held() {
  const { t } = useT();
  const empty = t("starred.empty");
  return empty;
}

export default function Page() {
  const empty = "plain";
  return (
    <main>
      <EmptyState text={empty} />
    </main>
  );
}
`,
  },
  // Decision 114: a Listing's empty prop counts only for the imported Listing; a local one is not it.
  'an empty prop of a Listing that is not the imported component': page(
    '      <Listing items={[]} empty={{ title: t("keys.empty") }} />',
    '  const Listing = (p) => <p>{p.empty.title}</p>;\n',
  ),
  // A key in another prop of the imported Listing is not drawn through EmptyState.
  'a key in another prop of the imported Listing': {
    name: 'pages/Fixture.tsx',
    src: `import Listing from "../components/files/Listing";
import { useT } from "../lib/i18n";

export default function Page() {
  const { t } = useT();
  return <Listing items={[]} label={t("keys.empty")} empty={{ title: "x" }} />;
}
`,
  },
  // A helper in a .ts file hands the key out, and a page renders it. The key must not leave a .ts file at all.
  'a key returned by a .ts helper': { name: 'lib/copy.ts', src: 'export const copy = () => t("starred.empty");\n' },
  // A local component named EmptyState is not the imported one.
  'a local EmptyState that is not the imported component': {
    name: 'pages/Fixture.tsx',
    src: `import { useT } from "../lib/i18n";

function EmptyState(p) {
  return <p>{p.text}</p>;
}

export default function Page() {
  const { t } = useT();
  return (
    <main>
      <EmptyState text={t("starred.empty")} />
    </main>
  );
}
`,
  },
};

// Shapes that are the approved layout and must pass.
const allowed = {
  'a panel div around EmptyState': page(`      <div className="panel">
        <EmptyState text={t("comments.empty")} />
      </div>`),
  'EmptyState with its text on one line': page('      <EmptyState role="status" text={t("starred.empty")} />'),
  'a key held in a variable that the same function passes to EmptyState': page(
    '      <EmptyState text={empty} />',
    '  const empty = t("starred.empty");\n',
  ),
  'a key held in an object property that the same function passes to EmptyState': page(
    '      <EmptyState text={copy.empty} />',
    '  const copy = { empty: t("starred.empty") };\n',
  ),
  // Decision 114: a preset list's empty table is drawn by Listing through EmptyState, from its empty prop's words.
  'a key in the empty prop of the imported Listing, a ternary too': {
    name: 'pages/Fixture.tsx',
    src: `import Listing from "../components/files/Listing";
import { useT } from "../lib/i18n";

export default function Page({ q }) {
  const { t } = useT();
  return (
    <Listing
      items={[]}
      empty={{ title: q ? t("tenants.noMatch") : t("tenants.empty"), action: { label: "New", onClick: () => undefined } }}
    />
  );
}
`,
  },
  'the read-only empty folder in FilesPage': {
    name: 'pages/FilesPage.tsx',
    src: page('      <EmptyState role="status" className="empty-folder-drop" text={t("files.empty")} />'),
  },
};

for (const [what, value] of Object.entries(bypass)) {
  const name = value.name ?? 'pages/Fixture.tsx';
  const src = value.src ?? value;
  test(`the guard flags ${what}`, () => {
    assert.ok(emptyStateProblems(name, src).length > 0, `not flagged: ${what}`);
  });
}

for (const [what, value] of Object.entries(allowed)) {
  const name = value.name ?? 'pages/Fixture.tsx';
  const src = value.src ?? value;
  test(`the guard passes ${what}`, () => {
    assert.deepEqual(emptyStateProblems(name, src), []);
  });
}
