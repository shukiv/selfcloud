// SPDX-License-Identifier: AGPL-3.0-or-later

// The slice p5 checks: the parts of slice p the browser checks do not reach, through the API. The account key makes a
// restricted label field and sets it on a new file in Team A. A delegated token for the member (write on Team A, acting as
// the member) must neither read the value nor set it. Then the key moves the file into Team B, and the value must survive
// the move. This runs last, after every browser check, so the moved file is seen by no page.
const q = (v) => JSON.stringify(v);

export async function p5Checks(h, { base, accountKey, memberID, accountID, teamA, teamB }) {
  await h.rawPut(base, `/api/v1/folders/${teamA}/files?path=%2Fp5-note.txt`, accountKey, 'the note\n', 'text/plain');
  const note = (await h.api(base, 'GET', `/api/v1/folders/${teamA}/children?path=%2F&limit=500`, accountKey)).items.find(
    (n) => n.name === 'p5-note.txt',
  );
  const field = await h.api(base, 'POST', `/api/v1/accounts/${accountID}/label-fields`, accountKey, {
    name: 'Lead',
    type: 'text',
    restricted: true,
  });
  await h.api(base, 'PUT', `/api/v1/nodes/${note.id}/labels/${field.id}`, accountKey, { value: 'managers only' });

  const minted = await h.api(base, 'POST', `/api/v1/accounts/${accountID}/tokens`, accountKey, {
    user: memberID,
    folders: [teamA],
    permissions: 'write',
  });
  const memberToken = minted.token;
  const memberLabels = await h.api(base, 'GET', `/api/v1/nodes/${note.id}/labels`, memberToken);
  h.check(
    !memberLabels.items.some((it) => it.name === 'Lead'),
    'a member token does not see a restricted label on a file it edits',
  );
  const setAttempt = await fetch(`${base}/api/v1/nodes/${note.id}/labels/${field.id}`, {
    method: 'PUT',
    headers: { Authorization: `Bearer ${memberToken}`, 'Content-Type': 'application/json' },
    body: q({ value: 'changed by the member' }),
  });
  const refusal = setAttempt.status === 403 ? await setAttempt.json() : null;
  h.check(
    setAttempt.status === 403 && refusal?.error?.details?.reason === 'restricted_label',
    `a member token cannot set a restricted label (status ${setAttempt.status})`,
  );

  // The key moves the file to a directory in Team B. A move keeps the values, so the key still reads the restricted one.
  await h.api(base, 'POST', `/api/v1/folders/${teamB}/directories`, accountKey, { path: '/p5-archive', parents: false });
  const moved = await h.api(base, 'POST', `/api/v1/nodes/${note.id}/move`, accountKey, {
    folder: teamB,
    path: '/p5-archive',
  });
  h.check(moved.folder_id === teamB, 'the key moves a file from Team A into Team B');
  const after = await h.api(base, 'GET', `/api/v1/nodes/${note.id}/labels`, accountKey);
  h.check(
    after.items.some((it) => it.name === 'Lead' && it.value === 'managers only'),
    'the restricted value survives the move between team spaces',
  );
}
