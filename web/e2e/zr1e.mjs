// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice zr1: a maker's zip link is refused once the owner demotes the maker. The member, a contributor on a folder whose
// no-download rule is on, makes the zip in the browser and sees it ready. The owner then makes the member a viewer, and
// "Download again" checks the zip first: the dialog shows the direct download's refusal, and no second zip is saved.
import { mkdir, readdir } from 'node:fs/promises';
import { join } from 'node:path';

const q = (v) => JSON.stringify(v);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export async function zr1eChecks(h, { page, base, accountKey, accountID, memberID, dir }) {
  const { check, api, rawPut, clickButton, rowAppears, selectRow, signIn, MEMBER } = h;
  // A space the member reads. The folder in it is covered by the no-download rule, and the member is a contributor on it.
  const space = (await api(base, 'POST', `/api/v1/accounts/${accountID}/folders`, accountKey, { name: 'ZR1 Space' })).id;
  await api(base, 'PUT', `/api/v1/accounts/${accountID}/folders/${space}/grants/${memberID}`, accountKey, { level: 'read' });
  await api(base, 'POST', `/api/v1/folders/${space}/directories`, accountKey, { path: '/zr1-covered', parents: false });
  await rawPut(base, `/api/v1/folders/${space}/files?path=%2Fzr1-covered%2Fnotes.txt`, accountKey, 'zr1 notes\n');
  const top = (await api(base, 'GET', `/api/v1/folders/${space}/children?path=%2F`, accountKey)).items.find((n) => n.name === 'zr1-covered');
  if (!top) throw new Error('the covered folder was not made');
  await api(base, 'PATCH', `/api/v1/nodes/${top.id}`, accountKey, { no_download: true });
  await api(base, 'PUT', `/api/v1/nodes/${top.id}/permissions/${memberID}`, accountKey, { role: 'contributor', notify: false });

  // The shared page is signed in as a member already, and the sign-in page is only shown to a visitor: sign out first.
  await h.signOut(page);
  await signIn(page, MEMBER);
  const downloads = join(dir, 'zr1e-downloads');
  await mkdir(downloads, { recursive: true });
  await page.send('Page.setDownloadBehavior', { behavior: 'allow', downloadPath: downloads });
  await page.goto(`${page.base}/files/${space}`);
  await rowAppears(page, 'zr1-covered');
  await selectRow(page, 'zr1-covered');
  await clickButton(page, 'Download as zip');
  await page.waitFor(
    `!!document.querySelector('.ant-modal [role="status"]') && document.body.innerText.includes(${q('The zip is ready')})`,
    30000,
  );
  // The browser saves the zip a moment after the dialog says it is ready; a partial download has another name.
  const zipCount = async () => (await readdir(downloads)).filter((f) => f.endsWith('.zip')).length;
  for (let i = 0; i < 150 && (await zipCount()) === 0; i++) await sleep(100);
  check((await zipCount()) === 1, 'the contributor\'s zip is made and saved');

  // The owner makes the member a viewer. The dialog is still open: the link is what the person clicks next.
  await api(base, 'PUT', `/api/v1/nodes/${top.id}/permissions/${memberID}`, accountKey, { role: 'viewer', notify: false });
  await clickButton(page, 'Download again');
  const refusal = 'Downloads are off for this file.';
  await page.waitFor(
    `[...document.querySelectorAll('.ant-modal [role="alert"]')].some((a) => a.innerText.includes(${q(refusal)}))`,
    15000,
  );
  check(true, 'a demoted maker\'s zip link shows the no-download refusal in the dialog');
  // The refusal is the zip's own 403, which the browser logs; the run counts it as expected, not as a console error.
  check(page.expectedAnswers.get('zip refused')?.length === 1, `the server refused the zip once, and the browser logged it (${page.expectedAnswers.get('zip refused')?.length ?? 0})`);
  await sleep(500);
  const after = (await readdir(downloads)).filter((f) => f.endsWith('.zip')).length;
  check(after === 1, `the refused link starts no download (${after} zip)`);
  await page.evaluate(`document.querySelector('.ant-modal .ant-modal-close')?.click(), true`);
}
