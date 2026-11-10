// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice ce3 (decision 133): the code editor keeps its unsaved text across a sign-in, for the same person only, in this browser tab.
// It runs in the shared run (run.mjs), signed in as the member, or alone with SELFCLOUD_E2E_ONLY=ce3. Checked: the session ends on the
// server while the member has unsaved text; the save is answered 401, the text is kept in this tab, and the notice says so; Sign in
// asks nothing; the member signs in again and the file opens with the text back, its notice, and the text unsaved; a save of it goes
// through and clears the kept text; then the member types again, the session ends again, and the owner signs in in the same tab: the
// member's kept text is gone from the tab before the owner's page shows, and the member, signed in again, sees the saved text only.
// The 401 answers the section causes on purpose are named to the run (ce3Ended); no other console error and no content-policy violation.
const q = (v) => JSON.stringify(v);
const EDITOR_PAGE = '[data-testid="code-editor-page"]';
const EDITOR = `${EDITOR_PAGE} .cm-editor`;
const BAR_STATE = `${EDITOR_PAGE} header [role="status"]`;
const NOTICE_BUTTON = `${EDITOR_PAGE} .ant-alert button`;
// The notice words, as the English catalogue has them (lib/code/text.ts).
const KEPT = 'Unsaved changes are kept in this tab. They come back when the same person signs in and opens this file.';
const RESTORED = 'Unsaved changes from before the sign-in are back.';

// spaTo moves the app to another address inside the page, as the router does (see ce2b2.mjs for why a link click is not used).
const spaTo = (page, path) =>
  page.evaluate(`(() => { history.pushState({}, '', ${q(path)}); window.dispatchEvent(new PopStateEvent('popstate', { state: {} })); return true; })()`);

// leaveEditor moves from the editor page to another address and waits until the editor has left the page.
async function leaveEditor(page, path) {
  await spaTo(page, path);
  await page.waitFor(`location.pathname === ${q(path)} && !document.querySelector(${q(EDITOR_PAGE)})`, 20000);
}

// openEditor opens the editor page for a node, as an editor's address does, and waits until the file's text is drawn.
async function openEditor(page, folder, id, name) {
  await leaveEditor(page, `/files/${folder}`);
  await spaTo(page, `/edit/${id}?mode=edit`);
  await page.waitFor(`!!document.querySelector(${q(`${EDITOR} .cm-content[aria-label=${q(name)}]`)})`, 20000);
  await page.waitFor(`document.querySelectorAll(${q(`${EDITOR} .cm-line`)}).length > 0`, 20000);
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

// bodyHas says the page shows this text anywhere.
const bodyHas = (page, text) => page.evaluate(`document.body.textContent.includes(${q(text)})`);

// clickIn clicks the button of a scope (a selector) that has this label, and says whether it found one.
const clickIn = (page, scope, label) =>
  page.evaluate(`(() => { const b = [...document.querySelectorAll(${q(scope)})].find((x) => x.textContent.trim() === ${q(label)}); if (!b) return false; b.click(); return true; })()`);

// endSession ends the signed-in browser session on the server, as a sign-out elsewhere or an expiry would. The app is not told.
const endSession = (page) =>
  page.evaluate(`(async () => {
    const me = await (await fetch('/api/v1/me', { credentials: 'same-origin' })).json();
    const r = await fetch('/api/v1/auth/logout', { method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': me.csrf_token } });
    return r.status;
  })()`);

// keptFor counts this tab's kept entries for a node.
const keptFor = (page, id) => page.evaluate(`Object.keys(sessionStorage).filter((k) => k.includes(${q(id)})).length`);

// contentText reads a file's bytes as the account key, for the bytes a save wrote.
async function contentText(base, id, key) {
  const res = await fetch(`${base}/api/v1/nodes/${id}/content`, { headers: { Authorization: `Bearer ${key}` } });
  if (!res.ok) throw new Error(`GET content: ${res.status}`);
  return Buffer.from(await res.arrayBuffer()).toString('utf8');
}

// ce3Checks runs the section. ended collects the node IDs whose 401 is expected (the session ended on purpose).
export async function ce3Checks(h, { page, base, accountKey, ended }) {
  const { check, api, rawPut, signInHere, signIn, signOut, OWNER, MEMBER } = h;
  const before = page.errors.length;
  const suffix = Date.now().toString(36);
  const name = `ce3-keep-${suffix}.txt`;
  let pf = null;
  let id = null;
  try {
    const me = await page.evaluate(`fetch('/api/v1/me', { credentials: 'same-origin' }).then((r) => r.json())`);
    check(typeof me.personal_folder_id === 'string', `the keep checks run as the member (${me.user?.email})`);
    pf = me.personal_folder_id;
    await rawPut(base, `/api/v1/folders/${pf}/files?path=${encodeURIComponent(`/${name}`)}`, accountKey, 'saved\n', 'text/plain');
    const listed = await api(base, 'GET', `/api/v1/folders/${pf}/children`, accountKey);
    id = listed.items.find((n) => n.name === name)?.id ?? null;
    check(id !== null, 'the kept-text file exists in My files');
    if (id === null) return;
    ended.add(id);

    // ---- Unsaved text, then the session ends on the server: the save is answered 401, and this tab keeps the text.
    await openEditor(page, pf, id, name);
    await page.send('Input.insertText', { text: 'K' });
    await page.waitFor(`document.querySelector(${q(BAR_STATE)})?.textContent.trim() === 'Unsaved changes'`, 10000);
    const status = await endSession(page);
    check(status === 200 || status === 204, `the session ends on the server (${status})`);
    await ctrlS(page);
    await page.waitFor(`document.body.textContent.includes(${q(KEPT)})`, 15000);
    check(await bodyHas(page, KEPT), 'a save the server answers 401 says the unsaved text is kept in this tab');
    check((await keptFor(page, id)) === 1, 'the text is kept in this tab, for this file');

    // ---- Sign in from that notice asks nothing: the sign-in page opens at once.
    await clickIn(page, NOTICE_BUTTON, 'Sign in');
    await page.waitFor(`location.pathname === '/login' && !!document.getElementById('login-email')`, 15000);
    const asked = await page.evaluate(`!!document.querySelector('.ant-modal')`);
    check(!asked, 'Sign in with the text kept asks nothing');

    // ---- The same person signs in again: the file opens with the text back, its notice, and the text unsaved.
    await signInHere(page, MEMBER);
    await openEditor(page, pf, id, name);
    await page.waitFor(`document.body.textContent.includes(${q(RESTORED)})`, 15000);
    const back = (await editorText(page)) ?? '';
    check(back.startsWith('Ksaved'), `the text kept before the sign-in is back (${JSON.stringify(back.slice(0, 12))})`);
    check((await barState(page)) === 'Unsaved changes', 'the restored text is unsaved');

    // ---- A save of the restored text goes through, and the kept text is cleared.
    await ctrlS(page);
    await page.waitFor(`document.querySelector(${q(BAR_STATE)})?.textContent.trim() === 'Saved'`, 15000);
    const saved = await contentText(base, id, accountKey);
    check(saved === 'Ksaved\n', `the restored text is saved (${JSON.stringify(saved)})`);
    check((await keptFor(page, id)) === 0, 'a save clears the text kept for the file');

    // ---- The member types again and the session ends again; then the owner signs in in the same tab. The member's kept text is
    // gone from the tab before the owner's page shows, and the member sees the saved text only when signed in again.
    await page.send('Input.insertText', { text: 'J' });
    await page.waitFor(`document.querySelector(${q(BAR_STATE)})?.textContent.trim() === 'Unsaved changes'`, 10000);
    const status2 = await endSession(page);
    check(status2 === 200 || status2 === 204, `the session ends on the server again (${status2})`);
    await ctrlS(page);
    await page.waitFor(`document.body.textContent.includes(${q(KEPT)})`, 15000);
    check((await keptFor(page, id)) === 1, 'the second text is kept in this tab');
    await clickIn(page, NOTICE_BUTTON, 'Sign in');
    await page.waitFor(`location.pathname === '/login' && !!document.getElementById('login-email')`, 15000);
    await signInHere(page, OWNER);
    const leftAfterOwner = await keptFor(page, id);
    check(leftAfterOwner === 0, `another person's sign-in clears the member's kept text (${leftAfterOwner} left)`);
    await signOut(page);
    await signIn(page, MEMBER);
    await openEditor(page, pf, id, name);
    const after = (await editorText(page)) ?? '';
    check(after.startsWith('Ksaved') && !after.includes('J'), `the member sees the saved text only (${JSON.stringify(after.slice(0, 12))})`);
    check(!(await bodyHas(page, RESTORED)), 'the member sees no kept-text notice');
    await leaveEditor(page, `/files/${pf}`);

    // ---- The whole section: no console error and no content-policy violation (the 401 answers are named above).
    const errors = page.errors.slice(before);
    check(errors.length === 0, `the keep checks log no console error and no content-policy violation (${errors.join(' | ') || 'none'})`);
  } finally {
    if (pf) {
      const listed = await api(base, 'GET', `/api/v1/folders/${pf}/children`, accountKey).catch(() => null);
      for (const n of listed?.items ?? []) {
        if (n.name === name) await api(base, 'DELETE', `/api/v1/nodes/${n.id}`, accountKey).catch(() => undefined);
      }
    }
  }
}
