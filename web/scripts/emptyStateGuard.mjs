// SPDX-License-Identifier: AGPL-3.0-or-later
// The empty-state guard (decision 111). check-source.mjs runs it on every shipped .tsx file, and emptyStateGuard.test.mjs pins
// its behaviour on fixtures. An empty state is antd's Empty through EmptyState: each key in EMPTY_KEYS may be drawn only
// through EmptyState, so a bare paragraph or box with the words, or a file that draws one without importing EmptyState, fails.
// Outcome messages (preview, playlist stops, the share status) are not in the list.

export const EMPTY_KEYS = [
  'starred.empty', 'recent.empty', 'shared.empty', 'trash.empty.none', 'files.empty', 'files.emptyMouse', 'files.emptyTouch',
  'requests.empty', 'people.empty', 'groups.empty', 'spaces.empty', 'audit.empty', 'links.empty', 'labels.empty',
  'tenants.empty', 'tenants.noMatch', 'search.empty', 'search.pageEmpty', 'notifications.empty', 'share.open.empty',
  'storage.largestEmpty', 'storage.peopleEmpty', 'storage.spacesEmpty', 'manage.overview.noActivity', 'tenant.users.empty',
  'keys.empty', 'files.media.queueEmpty', 'files.media.addNone', 'files.playlists.none', 'files.playlists.empty',
  'files.pick.empty', 'files.pick.noSpaces', 'files.versions.none', 'comments.empty', 'activity.empty', 'viewers.empty',
  'people.detail.noActivity', 'people.detail.noSpaces', 'people.detail.noGroups', 'groups.noMembers', 'share.none',
];
const EMPTY_DESCRIPTION_KEYS = new Map([['pages/FilesPage.tsx', ['files.empty', 'files.emptyMouse', 'files.emptyTouch']]]);

// emptyStateProblems returns the problems in one file's text. file is its path under src, for the messages.
export function emptyStateProblems(file, text) {
  const problems = [];
  const lines = text.split('\n');
  const importsEmptyState = /import\s+EmptyState\s+from\s+/.test(text);
  const described = EMPTY_DESCRIPTION_KEYS.get(file) ?? [];
  lines.forEach((line, i) => {
    const key = EMPTY_KEYS.find((k) => line.includes(`t("${k}")`) || line.includes(`t('${k}')`));
    if (key === undefined || described.includes(key)) return;
    const bare = /<(p|div|span)\b/.test(line) || (i > 0 && /<(p|div|span)\b[^>]*>\s*$/.test(lines[i - 1]));
    if (bare) problems.push(`${file}:${i + 1}: the empty state ${key} is drawn as a bare paragraph or box; use EmptyState (decision 111)`);
    else if (!importsEmptyState) problems.push(`${file}:${i + 1}: the empty state ${key} is drawn outside EmptyState (decision 111)`);
  });
  return problems;
}
