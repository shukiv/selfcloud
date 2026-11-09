// SPDX-License-Identifier: AGPL-3.0-or-later
// Pins the empty-state guard (emptyStateGuard.mjs) on small fixtures: each bypass must be flagged, and each shape that is
// the approved layout must pass. Run with: node --test scripts/emptyStateGuard.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { emptyStateProblems } from './emptyStateGuard.mjs';

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
};

// Shapes that are the approved layout and must pass.
const allowed = {
  'a panel div around EmptyState': page(`      <div className="panel">
        <EmptyState text={t("comments.empty")} />
      </div>`),
  'EmptyState with its text on one line': page('      <EmptyState role="status" text={t("starred.empty")} />'),
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
