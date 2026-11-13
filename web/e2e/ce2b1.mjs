// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice ce2b-1 (decision 124): the code editor and its save. It runs in the shared run (run.mjs), signed in as the member, or
// alone with SELFCLOUD_E2E_ONLY=ce2b-1. The member's My files gets a JSON file, a JSON file with a byte order mark and CRLF line
// breaks, and a Markdown file; Team B (a read grant for the member, so a viewer there) gets one JSON file. Checked: the pencil in
// the preview for an editor, and none for a viewer; the editor page opens the file with its text; typing and Ctrl+S save it
// ("Saved", and the server holds the text); a reload shows the change; a byte order mark and CRLF come back as they were; a save
// against a file changed on the server shows the conflict and its Reload choice, which reloads the saved text; a viewer's address
// shows "View only" and no Save; Hebrew keeps the gutter on the left. No console error and no content-policy violation over the
// section, but for the one stale save (412), which the run expects by name. The seeded files are removed after.
import { closePreview, setLanguage, sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);
const PREVIEW = '[data-testid="preview"]';
const EDITOR_PAGE = '[data-testid="code-editor-page"]';
const EDITOR = `${EDITOR_PAGE} .cm-editor`;
// The conflict's words, as the English catalogue has them (lib/code/text.ts).
const CONFLICT = 'This file changed since you opened it. Reload it to see the saved version.';

// openFile opens a file of the listing by a double click, and waits for the preview to show its text.
async function openFile(page, name) {
  const item = `[role="row"][aria-label=${q(name)}], [role="option"][aria-label=${q(name)}]`;
  await page.waitFor(`!!document.querySelector(${q(item)})`, 15000);
  await page.evaluate(`document.querySelector(${q(item)}).dispatchEvent(new MouseEvent('dblclick', { bubbles: true })), true`);
  await page.waitFor(`!!document.querySelector(${q(`${PREVIEW} .cm-line`)})`, 20000);
}

// typeText types text at the caret of the focused editor, as the keyboard does.
const typeText = (page, text) => page.send('Input.insertText', { text });

// spaTo moves the app to another address inside the page, as the router does: the history entry is pushed and the router is
// told. A link click loads a new document instead, and an editor check could then read the page that is leaving (the
// reload race of the Ctrl+S section).
const spaTo = (page, path) =>
  page.evaluate(`(() => { history.pushState({}, '', ${q(path)}); window.dispatchEvent(new PopStateEvent('popstate', { state: {} })); return true; })()`);

// leaveEditor moves from the editor page to another address and waits until the editor has left the page. The router changes
// the address at once, but React draws the new page a moment later: until then the old editor is still on the screen, and a
// check that reads it would take it for the page it asked for.
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

// status is the bar's save state text, or null.
const statusText = (page) =>
  page.evaluate(`(() => { const s = document.querySelector(${q(`${EDITOR_PAGE} header [role="status"]`)}); return s ? s.textContent.trim() : null; })()`);

// openEditor opens the editor page for a node, as an editor's address does. It goes through the member's folder first, so a
// second open of the same address is a fresh page.
async function openEditor(h, page, folder, id, name) {
  await leaveEditor(page, `/files/${folder}`);
  await spaTo(page, `/edit/${id}?mode=edit`);
  await editorShows(page, name);
}

// editorShows waits until the editor page shows the file with this name: its code area is labelled with the name, so an editor
// of an earlier file, still on the screen, is never read as this one.
async function editorShows(page, name) {
  await page.waitFor(`!!document.querySelector(${q(`${EDITOR} .cm-content[aria-label=${q(name)}]`)})`, 20000);
  await page.waitFor(`document.querySelectorAll(${q(`${EDITOR} .cm-line`)}).length > 0`, 20000);
}

// contentBytes reads a file's bytes as the account key, for the bytes the save wrote.
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

// stale names the save the section makes against a changed file: the server answers 412 on purpose, and only for that node.
export async function ce2b1Checks(h, { page, base, accountKey, teamB, stale: staleIDs = new Set() }) {
  const { check, api, rawPut } = h;
  const navigateTo = spaTo;
  const before = page.errors.length;
  const suffix = Date.now().toString(36);
  const names = {
    json: `ce2b1-data-${suffix}.json`,
    crlf: `ce2b1-crlf-${suffix}.json`,
    md: `ce2b1-note-${suffix}.md`,
    viewer: `ce2b1-viewer-${suffix}.json`,
  };
  const enc = encodeURIComponent;
  let pf = null;
  const teamBNames = [names.viewer];
  try {
    const me = await page.evaluate(`fetch('/api/v1/me', { credentials: 'same-origin' }).then((r) => r.json())`);
    check(typeof me.personal_folder_id === 'string', `the editor's checks run as the member (${me.user?.email})`);
    pf = me.personal_folder_id;
    await rawPut(base, `/api/v1/folders/${pf}/files?path=${enc(`/${names.json}`)}`, accountKey, '{\n  "a": 1\n}\n', 'application/json');
    const crlf = new Uint8Array([0xef, 0xbb, 0xbf, ...Buffer.from('{\r\n  "a": 1\r\n}\r\n', 'utf8')]);
    await rawPut(base, `/api/v1/folders/${pf}/files?path=${enc(`/${names.crlf}`)}`, accountKey, crlf, 'application/json');
    await rawPut(base, `/api/v1/folders/${pf}/files?path=${enc(`/${names.md}`)}`, accountKey, '# Title\n\nSome text.\n', 'text/markdown');
    await rawPut(base, `/api/v1/folders/${teamB}/files?path=${enc(`/${names.viewer}`)}`, accountKey, '{\n  "v": 1\n}\n', 'application/json');
    const mine = await api(base, 'GET', `/api/v1/folders/${pf}/children`, accountKey);
    const idOf = (list, name) => list.items.find((n) => n.name === name)?.id ?? null;
    const jsonID = idOf(mine, names.json);
    const crlfID = idOf(mine, names.crlf);
    const mdID = idOf(mine, names.md);
    const viewerID = idOf(await api(base, 'GET', `/api/v1/folders/${teamB}/children`, accountKey), names.viewer);
    check(jsonID && crlfID && mdID && viewerID, 'the editor section files exist (JSON, CRLF JSON, Markdown, and one in Team B)');

    // ---- A viewer has no pencil: Team B is read-only for the member.
    await navigateTo(page, `/files/${teamB}`);
    await openFile(page, names.viewer);
    const viewerPreview = await page.evaluate(`({ pencil: !!document.querySelector(${q(`${PREVIEW} .editor-btn`)}), text: !!document.querySelector(${q(`${PREVIEW} .cm-editor`)}) })`);
    check(!viewerPreview.pencil && viewerPreview.text, `a viewer's preview shows the text and no pencil (${JSON.stringify(viewerPreview)})`);
    await closePreview(page);

    // ---- An editor has the pencil in the preview, with its tooltip's name.
    await navigateTo(page, `/files/${pf}`);
    await openFile(page, names.json);
    const pencil = await page.evaluate(`(() => { const b = document.querySelector(${q(`${PREVIEW} .editor-btn`)}); return b ? b.getAttribute('aria-label') : null; })()`);
    check(pencil === 'Open in editor', `an editor's preview has the pencil, named by its tooltip (${pencil})`);
    await closePreview(page);

    // ---- The editor page opens the file, edits it, saves with Ctrl+S, and a reload shows the change.
    await openEditor(h, page, pf, jsonID, names.json);
    const opened = await editorText(page);
    check(opened === '{\n  "a": 1\n}\n', `the editor page shows the file's text (${JSON.stringify(opened)})`);
    await typeText(page, 'X');
    await page.waitFor(`document.querySelector(${q(`${EDITOR_PAGE} header [role="status"]`)})?.textContent.trim() === 'Unsaved changes'`, 10000);
    const unsaved = await page.evaluate(`document.querySelector(${q(`${EDITOR_PAGE} header [role="status"]`)})?.textContent.trim()`);
    check(unsaved === 'Unsaved changes', `a change shows "Unsaved changes" before any save (${JSON.stringify(unsaved)})`);
    await ctrlS(page);
    await page.waitFor(`document.querySelector(${q(`${EDITOR_PAGE} header [role="status"]`)})?.textContent.trim() === 'Saved'`, 15000);
    const stored = (await contentBytes(base, jsonID, accountKey)).toString('utf8');
    check(stored === 'X{\n  "a": 1\n}\n', `Ctrl+S saves the text, and the server holds it (${JSON.stringify(stored)})`);
    await leaveEditor(page, `/files/${pf}`);
    await navigateTo(page, `/edit/${jsonID}?mode=edit`);
    await editorShows(page, names.json);
    const reloaded = await editorText(page);
    check(reloaded === 'X{\n  "a": 1\n}\n', `a reload of the editor shows the saved change (${JSON.stringify(reloaded)})`);

    // ---- The Save button stays enabled during a save and keeps the focus after it.
    // A click in the code area puts the focus there, as a person's click does. The area is waited for first: after a reload the
    // page is drawn again.
    await page.waitFor(`!!document.querySelector(${q(`${EDITOR_PAGE} .cm-content[aria-label=${q(names.json)}]`)})`, 20000);
    const spot = await page.evaluate(`(() => { const b = document.querySelector(${q(`${EDITOR_PAGE} .cm-content`)}).getBoundingClientRect(); return { x: b.left + 20, y: b.top + 10 }; })()`);
    await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: spot.x, y: spot.y, button: 'left', clickCount: 1 });
    await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: spot.x, y: spot.y, button: 'left', clickCount: 1 });
    await typeText(page, 'Z');
    await page.waitFor(`document.querySelector(${q(`${EDITOR_PAGE} header [role="status"]`)})?.textContent.trim() === 'Unsaved changes'`, 10000);
    const saveFocus = await page.evaluate(`(() => { const b = document.querySelector(${q(`${EDITOR_PAGE} header button[aria-label="Save"]`)}); if (!b) return null; b.focus(); b.click(); return { disabledAtClick: b.disabled, focusedAfter: document.activeElement === b }; })()`);
    await page.waitFor(`document.querySelector(${q(`${EDITOR_PAGE} header [role="status"]`)})?.textContent.trim() === 'Saved'`, 15000);
    const focusAfter = await page.evaluate(`(() => { const b = document.querySelector(${q(`${EDITOR_PAGE} header button[aria-label="Save"]`)}); return b ? { disabled: b.disabled, focused: document.activeElement === b } : null; })()`);
    check(saveFocus !== null && !saveFocus.disabledAtClick && focusAfter !== null && !focusAfter.disabled && focusAfter.focused, `Save stays enabled during a save and keeps the focus after it (${JSON.stringify({ saveFocus, focusAfter })})`);

    // ---- A byte order mark and CRLF line breaks come back as they were.
    await openEditor(h, page, pf, crlfID, names.crlf);
    await typeText(page, 'Y');
    await ctrlS(page);
    await page.waitFor(`document.querySelector(${q(`${EDITOR_PAGE} header [role="status"]`)})?.textContent.trim() === 'Saved'`, 15000);
    const bytes = await contentBytes(base, crlfID, accountKey);
    // The bytes the save wrote are the file's own: the mark, the text with Y at its start, and CRLF on every line.
    const kept = bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf && bytes.subarray(3).toString('utf8') === 'Y{\r\n  "a": 1\r\n}\r\n';
    check(kept, `a file with a byte order mark and CRLF is saved with both (${JSON.stringify(bytes.subarray(0, 12).toString('hex'))})`);

    // ---- Markdown opens as source.
    await openEditor(h, page, pf, mdID, names.md);
    const md = await editorText(page);
    check(md !== null && md.startsWith('# Title'), `a Markdown file opens in the editor as source (${JSON.stringify(md)})`);

    // ---- A save against a file changed on the server: the conflict, its Reload choice, and the reload of the saved text.
    await openEditor(h, page, pf, jsonID, names.json);
    const current = (await api(base, 'GET', `/api/v1/nodes/${jsonID}`, accountKey)).etag;
    staleIDs.add(jsonID);
    await putIfMatch(base, jsonID, accountKey, current, 'changed by someone else\n', 'application/json');
    await typeText(page, 'Z');
    await ctrlS(page);
    // The conflict is a dialog (decision 130): its title and message, and the draft kept on the page under it.
    await page.waitFor(`!!document.querySelector('.ant-modal')`, 15000);
    const dialog = (await page.evaluate(`document.querySelector('.ant-modal')?.textContent ?? ''`)) ?? '';
    check(dialog.includes('This file changed') && dialog.includes(CONFLICT), `a save against a changed file opens the conflict dialog (${dialog})`);
    check((await editorText(page)).startsWith('Z'), 'the conflict keeps the draft on the page');
    check((page.expectedAnswers.get('stale save')?.length ?? 0) === 1, 'the server refused the stale save once, and the browser logged it');
    await page.evaluate(`[...document.querySelectorAll('.ant-modal-footer button')].find((b) => b.textContent.trim() === 'Reload theirs').click(), true`);
    await page.waitFor(`!!document.querySelector('.ant-popover:not(.ant-popover-hidden)')`, 10000);
    await page.evaluate(`[...document.querySelectorAll('.ant-popover:not(.ant-popover-hidden) button')].find((b) => b.textContent.trim() === 'Reload').click(), true`);
    await page.waitFor(`(() => { const c = document.querySelector(${q(`${EDITOR} .cm-content`)}); return !!c && c.textContent.includes('changed by someone else'); })()`, 15000);
    await page.waitFor(`!document.querySelector('.ant-modal')`, 10000);
    const theirs = (await editorText(page)) ?? '';
    check(theirs.includes('changed by someone else'), `Reload theirs shows the saved text, and the dialog goes (${JSON.stringify(theirs)})`);

    // ---- A viewer's address shows "View only" and no Save.
    await navigateTo(page, `/edit/${viewerID}?mode=edit`);
    await editorShows(page, names.viewer);
    const viewer = await page.evaluate(`({ status: document.querySelector(${q(`${EDITOR_PAGE} header [role="status"]`)})?.textContent.trim() ?? null, save: !!document.querySelector(${q(`${EDITOR_PAGE} button[aria-label="Save"]`)}), readonly: document.querySelector(${q(`${EDITOR} .cm-content`)})?.getAttribute('aria-readonly') })`);
    check(viewer.status === 'View only' && !viewer.save && viewer.readonly === 'true', `a viewer's address shows "View only" and no Save (${JSON.stringify(viewer)})`);

    // ---- Hebrew: the page is right to left, the code area left to right, and the gutter stays on the left.
    // The language is chosen in the shell's menu, which the editor page does not draw: it is chosen on My files first.
    await navigateTo(page, `/files/${pf}`);
    await setLanguage(page, 'עברית');
    await page.waitFor(`document.documentElement.dir === 'rtl'`, 10000);
    await openEditor(h, page, pf, jsonID, names.json);
    const he = await page.evaluate(`(() => {
      const gutters = document.querySelector(${q(`${EDITOR} .cm-gutters`)}).getBoundingClientRect();
      const content = document.querySelector(${q(`${EDITOR} .cm-content`)}).getBoundingClientRect();
      const save = document.querySelector(${q(`${EDITOR_PAGE} button[aria-label="שמירה"]`)});
      return { gutter: Math.round(gutters.left), content: Math.round(content.left), dir: document.querySelector(${q(`${EDITOR_PAGE} .code-editor`)})?.getAttribute('dir') ?? null, save: !!save };
    })()`);
    check(he.dir === 'ltr' && he.gutter < he.content && he.save, `in Hebrew the code area is left to right, the gutter is on the left and Save is named in Hebrew (${JSON.stringify(he)})`);
    await navigateTo(page, `/files/${pf}`);
    await setLanguage(page, 'English');

    // ---- The whole section: no console error, no content-policy violation (the stale save is expected by name, above).
    const errors = page.errors.slice(before);
    check(errors.length === 0, `the editor logs no console error and no content-policy violation (${errors.join(' | ') || 'none'})`);
  } finally {
    await setLanguage(page, 'English').catch(() => undefined);
    if (pf) {
      const listed = await api(base, 'GET', `/api/v1/folders/${pf}/children`, accountKey).catch(() => null);
      for (const n of listed?.items ?? []) {
        if ([names.json, names.crlf, names.md].includes(n.name)) await api(base, 'DELETE', `/api/v1/nodes/${n.id}`, accountKey).catch(() => undefined);
      }
    }
    const inB = await api(base, 'GET', `/api/v1/folders/${teamB}/children`, accountKey).catch(() => null);
    for (const n of inB?.items ?? []) {
      if (teamBNames.includes(n.name)) await api(base, 'DELETE', `/api/v1/nodes/${n.id}`, accountKey).catch(() => undefined);
    }
  }
}
