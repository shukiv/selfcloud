// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice ce2b-2 (decision 130): the code editor's conflict dialog and its four choices, keep as a copy past a taken name, the unsaved
// guard, the JSON lint's marker and count, and the long-line rule. It runs in the shared run (run.mjs), signed in as the member, or
// alone with SELFCLOUD_E2E_ONLY=ce2b-2. The member's My files gets the files the section needs; they are removed after. Checked: a
// save refused with 412 opens the dialog with its title, its message and its four choices, and keeps the draft; Keep my draft closes
// it and the next save is refused again; Keep as a copy skips a taken name and saves the draft as the next free one, and leaves the
// original; Overwrite saves the draft over the newer change; Close with unsaved text asks first (Stay keeps the page, Save and leave
// saves and goes on); a JSON file shows its marker and a count; a long line turns wrapping off. The 412 answers the section causes on
// purpose are named to the run (ce2b2Stale, ce2b2Taken); no other console error and no content-policy violation over the section.
const q = (v) => JSON.stringify(v);
const EDITOR_PAGE = '[data-testid="code-editor-page"]';
const EDITOR = `${EDITOR_PAGE} .cm-editor`;
const BAR_STATE = `${EDITOR_PAGE} header [role="status"]`;
// The dialog words, as the English catalogue has them (lib/code/text.ts).
const TITLE = 'This file changed';
const MESSAGE = 'This file changed since you opened it. Reload it to see the saved version.';
const KEEP = 'Keep my draft (saving will ask again)';
const COPY = 'Keep as a copy';
const OVERWRITE = 'Overwrite theirs with mine (theirs stays in history)';
const RELOAD = 'Reload theirs';

// spaTo moves the app to another address inside the page, as the router does (see ce2b1.mjs for why a link click is not used).
const spaTo = (page, path) =>
  page.evaluate(`(() => { history.pushState({}, '', ${q(path)}); window.dispatchEvent(new PopStateEvent('popstate', { state: {} })); return true; })()`);

// leaveEditor moves from the editor page to another address and waits until the editor has left the page.
async function leaveEditor(page, path) {
  await spaTo(page, path);
  await page.waitFor(`location.pathname === ${q(path)} && !document.querySelector(${q(EDITOR_PAGE)})`, 20000);
}

// ctrlS presses Ctrl+S in the focused editor.
async function ctrlS(page) {
  const key = { key: 's', code: 'KeyS', windowsVirtualKeyCode: 83, nativeVirtualKeyCode: 83, modifiers: 2 };
  await page.send('Input.dispatchKeyEvent', { type: 'keyDown', ...key });
  await page.send('Input.dispatchKeyEvent', { type: 'keyUp', ...key });
}

// editorText is the text the editor page shows, line breaks as one "\n" each.
const editorText = (page) =>
  page.evaluate(`(() => { const c = document.querySelector(${q(`${EDITOR} .cm-content`)}); return c ? [...c.querySelectorAll('.cm-line')].map((l) => l.textContent).join('\\n') : null; })()`);

// barState is the bar's save state text, or null.
const barState = (page) =>
  page.evaluate(`(() => { const s = document.querySelector(${q(BAR_STATE)}); return s ? s.textContent.trim() : null; })()`);

// dialogText is the text of the open dialog, or null when none is open.
const dialogText = (page) =>
  page.evaluate(`(() => { const m = document.querySelector('.ant-modal'); return m ? m.textContent : null; })()`);

// clickIn clicks the button of a scope (a selector) that has this label, and says whether it found one.
const clickIn = (page, scope, label) =>
  page.evaluate(`(() => { const b = [...document.querySelectorAll(${q(scope)})].find((x) => x.textContent.trim() === ${q(label)}); if (!b) return false; b.click(); return true; })()`);

// openEditor opens the editor page for a node, as an editor's address does, and waits until the file's text is drawn.
async function openEditor(page, folder, id, name) {
  await leaveEditor(page, `/files/${folder}`);
  await spaTo(page, `/edit/${id}?mode=edit`);
  await page.waitFor(`!!document.querySelector(${q(`${EDITOR} .cm-content[aria-label=${q(name)}]`)})`, 20000);
  await page.waitFor(`document.querySelectorAll(${q(`${EDITOR} .cm-line`)}).length > 0`, 20000);
}

// contentBytes reads a file's bytes as the account key, for the bytes a save wrote.
async function contentBytes(base, id, key) {
  const res = await fetch(`${base}/api/v1/nodes/${id}/content`, { headers: { Authorization: `Bearer ${key}` } });
  if (!res.ok) throw new Error(`GET content: ${res.status}`);
  return Buffer.from(await res.arrayBuffer());
}

// putIfMatch writes a file as the account key, with an If-Match, as another client would.
async function putIfMatch(base, id, key, etag, body, type) {
  const res = await fetch(`${base}/api/v1/nodes/${id}/content`, {
    method: 'PUT',
    headers: { Authorization: `Bearer ${key}`, 'Content-Type': type, 'If-Match': etag },
    body,
  });
  if (!res.ok) throw new Error(`PUT content: ${res.status} ${await res.text()}`);
}

// ce2b2Checks runs the section. stale collects the node IDs a 412 is expected for, taken the encoded copy paths whose 412 is expected.
export async function ce2b2Checks(h, { page, base, accountKey, stale, taken }) {
  const { check, api, rawPut } = h;
  const before = page.errors.length;
  const suffix = Date.now().toString(36);
  const stem = `ce2b2-conflict-${suffix}`;
  const names = {
    conflict: `${stem}.json`,
    takenCopy: `${stem} (2).json`,
    copy: `${stem} (3).json`,
    lint: `ce2b2-lint-${suffix}.json`,
    long: `ce2b2-long-${suffix}.json`,
  };
  const enc = encodeURIComponent;
  let pf = null;
  try {
    const me = await page.evaluate(`fetch('/api/v1/me', { credentials: 'same-origin' }).then((r) => r.json())`);
    check(typeof me.personal_folder_id === 'string', `the editor's checks run as the member (${me.user?.email})`);
    pf = me.personal_folder_id;
    await rawPut(base, `/api/v1/folders/${pf}/files?path=${enc(`/${names.conflict}`)}`, accountKey, '{\n  "a": 1\n}\n', 'application/json');
    // The name a copy finds taken: "(2)" is there already, so the copy takes "(3)".
    await rawPut(base, `/api/v1/folders/${pf}/files?path=${enc(`/${names.takenCopy}`)}`, accountKey, 'taken\n', 'text/plain');
    await rawPut(base, `/api/v1/folders/${pf}/files?path=${enc(`/${names.lint}`)}`, accountKey, '{\n  "a": 1,\n}\n', 'application/json');
    await rawPut(base, `/api/v1/folders/${pf}/files?path=${enc(`/${names.long}`)}`, accountKey, `{"a":"${'x'.repeat(2500)}"}\n`, 'application/json');
    const listed = await api(base, 'GET', `/api/v1/folders/${pf}/children`, accountKey);
    const idOf = (name) => listed.items.find((n) => n.name === name)?.id ?? null;
    const conflictID = idOf(names.conflict);
    const lintID = idOf(names.lint);
    const longID = idOf(names.long);
    check(conflictID && lintID && longID, 'the editor section files exist in My files');
    const etagOf = async (id) => (await api(base, 'GET', `/api/v1/nodes/${id}`, accountKey)).etag;

    // ---- A save another client's change made stale opens the dialog, with its title, its message and its four choices.
    await openEditor(page, pf, conflictID, names.conflict);
    await page.send('Input.insertText', { text: 'Z' });
    await putIfMatch(base, conflictID, accountKey, await etagOf(conflictID), 'changed by someone else\n', 'application/json');
    stale.add(conflictID);
    await ctrlS(page);
    await page.waitFor(`!!document.querySelector('.ant-modal')`, 15000);
    const shown = (await dialogText(page)) ?? '';
    const choices = await page.evaluate(`[...document.querySelectorAll('.ant-modal-footer button')].map((b) => b.textContent.trim())`);
    check(shown.includes(TITLE) && shown.includes(MESSAGE), `a save against a changed file opens the conflict dialog (${JSON.stringify(shown)})`);
    check(JSON.stringify(choices) === JSON.stringify([RELOAD, COPY, OVERWRITE, KEEP]), `the dialog has its four choices in order (${JSON.stringify(choices)})`);
    check(((await editorText(page)) ?? '').startsWith('Z'), 'the dialog keeps the draft on the page');

    // ---- Keep my draft closes the dialog with no request; the next save is refused and asks again.
    await clickIn(page, '.ant-modal-footer button', KEEP);
    await page.waitFor(`!document.querySelector('.ant-modal')`, 10000);
    await ctrlS(page);
    // The dialog opening again is the check: the next save was refused and asked.
    await page.waitFor(`!!document.querySelector('.ant-modal')`, 15000);
    // ---- Keep as a copy: "(2)" is taken, so the draft is saved as "(3)" beside the file, and the original is not changed.
    await clickIn(page, '.ant-modal-footer button', COPY);
    taken.add(enc(`/${names.takenCopy}`));
    await page.waitFor(`document.body.textContent.includes(${q(`Saved as ${names.copy}`)})`, 20000);
    const after = await api(base, 'GET', `/api/v1/folders/${pf}/children`, accountKey);
    const copyID = after.items.find((n) => n.name === names.copy)?.id ?? null;
    check(copyID !== null, `Keep as a copy saves the draft as the next free name (${names.copy})`);
    const copyText = copyID ? (await contentBytes(base, copyID, accountKey)).toString('utf8') : '';
    check(copyText === 'Z{\n  "a": 1\n}\n', `the copy holds the draft (${JSON.stringify(copyText)})`);
    const original = (await contentBytes(base, conflictID, accountKey)).toString('utf8');
    check(original === 'changed by someone else\n', `the original is not changed by a copy (${JSON.stringify(original)})`);
    await page.waitFor(`!document.querySelector('.ant-modal')`, 10000);

    // ---- Overwrite saves the draft over a second change, with the ETag the check read.
    await putIfMatch(base, conflictID, accountKey, await etagOf(conflictID), 'changed again\n', 'application/json');
    await ctrlS(page);
    await page.waitFor(`!!document.querySelector('.ant-modal')`, 15000);
    await clickIn(page, '.ant-modal-footer button', OVERWRITE);
    await page.waitFor(`!document.querySelector('.ant-modal') && document.querySelector(${q(BAR_STATE)})?.textContent.trim() === 'Saved'`, 15000);
    const overwritten = (await contentBytes(base, conflictID, accountKey)).toString('utf8');
    check(overwritten === 'Z{\n  "a": 1\n}\n', `Overwrite saves the draft over the newer change (${JSON.stringify(overwritten)})`);

    // ---- The unsaved guard: Close with unsaved text asks first; Stay keeps the page, and Save and leave saves and goes on.
    await page.send('Input.insertText', { text: 'Q' });
    await page.waitFor(`document.querySelector(${q(BAR_STATE)})?.textContent.trim() === 'Unsaved changes'`, 10000);
    await page.evaluate(`document.querySelector(${q(`${EDITOR_PAGE} header button[aria-label="Close"]`)}).click(), true`);
    await page.waitFor(`!!document.querySelector('.ant-modal')`, 10000);
    const guard = (await dialogText(page)) ?? '';
    check(guard.includes('Leave this file?'), `Close with unsaved text asks before it leaves (${JSON.stringify(guard)})`);
    await clickIn(page, '.ant-modal-footer button', 'Stay');
    await page.waitFor(`!document.querySelector('.ant-modal')`, 10000);
    const stayed = await page.evaluate(`location.pathname`);
    check(stayed === `/edit/${conflictID}`, `Stay keeps the editor open on the file (${stayed})`);
    await page.evaluate(`document.querySelector(${q(`${EDITOR_PAGE} header button[aria-label="Close"]`)}).click(), true`);
    await page.waitFor(`!!document.querySelector('.ant-modal')`, 10000);
    await clickIn(page, '.ant-modal-footer button', 'Save and leave');
    await page.waitFor(`location.pathname === ${q(`/files/${pf}`)} && !document.querySelector(${q(EDITOR_PAGE)})`, 20000);
    const saved = (await contentBytes(base, conflictID, accountKey)).toString('utf8');
    check(saved.includes('Q'), `Save and leave saves the draft before it goes on (${JSON.stringify(saved.slice(0, 12))})`);

    // ---- A JSON file shows its problem as a marker and a count.
    await openEditor(page, pf, lintID, names.lint);
    await page.waitFor(`!!document.querySelector('.cm-lint-marker')`, 15000);
    const count = await page.evaluate(`document.querySelector('[data-testid="lint-count"]')?.textContent.trim() ?? null`);
    check(typeof count === 'string' && /^\d+ problems?$/.test(count), `a JSON file with a syntax problem shows its marker and count (${count})`);
    await leaveEditor(page, `/files/${pf}`);

    // ---- A long line turns wrapping off, so the page does not lay out thousands of rows.
    await openEditor(page, pf, longID, names.long);
    const wraps = await page.evaluate(`document.querySelector(${q(`${EDITOR} .cm-content`)})?.classList.contains('cm-lineWrapping') ?? null`);
    check(wraps === false, `a file with a line past 2000 characters opens with wrapping off (${wraps})`);
    await leaveEditor(page, `/files/${pf}`);

    // ---- The whole section: no console error and no content-policy violation (the refused saves are named above).
    const errors = page.errors.slice(before);
    check(errors.length === 0, `the code editor logs no console error and no content-policy violation (${errors.join(' | ') || 'none'})`);
  } finally {
    if (pf) {
      const listed = await api(base, 'GET', `/api/v1/folders/${pf}/children`, accountKey).catch(() => null);
      for (const n of listed?.items ?? []) {
        if (Object.values(names).includes(n.name)) await api(base, 'DELETE', `/api/v1/nodes/${n.id}`, accountKey).catch(() => undefined);
      }
    }
  }
}
