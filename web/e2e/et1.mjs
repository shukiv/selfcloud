// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice et1 (decision 135): the code editor takes a file's version from its node's JSON answer, never from a response header. A proxy
// in front of the server may drop the ETag of a content answer, so this section strips it, on every content answer the page gets
// (the open's GET and the save's PUT), with the CDP Fetch domain. The editor must still open the file and save it. It runs in the
// shared run (run.mjs), signed in as the member, or alone with SELFCLOUD_E2E_ONLY=et1. The member's My files gets the file the
// section needs; it is removed after. Checked: the content answers the page got had their ETag stripped (named, so the run proves the
// interception ran); the editor shows the file's text; a save of a draft is answered and the file holds the draft; the page shows no
// failure; and the section logs no console error and no content-policy violation.
const q = (v) => JSON.stringify(v);
const EDITOR_PAGE = '[data-testid="code-editor-page"]';
const EDITOR = `${EDITOR_PAGE} .cm-editor`;
const BAR_STATE = `${EDITOR_PAGE} header [role="status"]`;
const OPEN_FAILED = 'The editor could not be opened. Try again.';

// spaTo moves the app to another address inside the page, as the router does (see ce2b2.mjs for why a link click is not used).
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

// etagFree says a list of response headers carries no ETag, whatever its case.
const hasETag = (headers) => (headers ?? []).some((h) => h.name.toLowerCase() === 'etag');

// et1Checks runs the section. The interception is on for this file's content answers only, and it is off again when the section ends.
export async function et1Checks(h, { page, base, accountKey }) {
  const { check, api, rawPut } = h;
  const before = page.errors.length;
  const suffix = Date.now().toString(36);
  const name = `et1-noetag-${suffix}.html`;
  const enc = encodeURIComponent;
  const stripped = [];
  let pf = null;
  let fileID = null;
  // onPaused answers a paused content answer with its headers, minus the ETag. Every paused request is answered, so none hangs.
  const onPaused = (p) => {
    if (p.responseStatusCode === undefined) {
      page.send('Fetch.continueRequest', { requestId: p.requestId }).catch(() => undefined);
      return;
    }
    const headers = p.responseHeaders ?? [];
    const kept = headers.filter((x) => x.name.toLowerCase() !== 'etag');
    if (hasETag(headers)) stripped.push({ status: p.responseStatusCode, method: p.request?.method ?? '' });
    page.send('Fetch.continueResponse', { requestId: p.requestId, responseHeaders: kept }).catch(() => undefined);
  };
  try {
    const me = await page.evaluate(`fetch('/api/v1/me', { credentials: 'same-origin' }).then((r) => r.json())`);
    check(typeof me.personal_folder_id === 'string', `the et1 checks run as the member (${me.user?.email})`);
    pf = me.personal_folder_id;
    const body = '<p>et1</p>\n';
    await rawPut(base, `/api/v1/folders/${pf}/files?path=${enc(`/${name}`)}`, accountKey, body, 'text/html');
    const listed = await api(base, 'GET', `/api/v1/folders/${pf}/children`, accountKey);
    fileID = listed.items.find((n) => n.name === name)?.id ?? null;
    check(fileID !== null, 'the et1 file exists in My files');

    // The proxy's part: this file's content answers come back without an ETag header. Other requests are not paused.
    page.on('Fetch.requestPaused', onPaused);
    await page.send('Fetch.enable', { patterns: [{ urlPattern: `*/api/v1/nodes/${fileID}/content*`, requestStage: 'Response' }] });

    // ---- The editor opens the file, though its content answer has no ETag header.
    await openEditor(page, pf, fileID, name);
    const shown = (await editorText(page)) ?? '';
    check(shown.includes('<p>et1</p>'), `the editor opens a file whose content answer has no ETag (${JSON.stringify(shown)})`);
    const failed = await page.evaluate(`document.body.textContent.includes(${q(OPEN_FAILED)})`);
    check(failed === false, 'the editor shows no open failure');
    check(stripped.some((s) => s.status === 200 && s.method === 'GET'), `the open's content answer had its ETag stripped (${stripped.length} answers stripped)`);

    // ---- A save works: the draft is sent with the version the node gave, and the file holds it.
    await page.send('Input.insertText', { text: 'Z' });
    await page.waitFor(`document.querySelector(${q(BAR_STATE)})?.textContent.trim() === 'Unsaved changes'`, 10000);
    await ctrlS(page);
    await page.waitFor(`document.querySelector(${q(BAR_STATE)})?.textContent.trim() === 'Saved'`, 15000);
    const saved = (await contentBytes(base, fileID, accountKey)).toString('utf8');
    check(saved === `Z${body}`, `a save of a file whose content answers have no ETag is kept (${JSON.stringify(saved)})`);
    check(stripped.some((s) => s.method === 'PUT' && s.status === 200), 'the save\'s answer had its ETag stripped too');
    check((await barState(page)) === 'Saved', 'the save state reads Saved');

    await leaveEditor(page, `/files/${pf}`);
    // ---- The whole section: no console error and no content-policy violation.
    const errors = page.errors.slice(before);
    check(errors.length === 0, `the et1 section logs no console error and no content-policy violation (${errors.join(' | ') || 'none'})`);
  } finally {
    page.off('Fetch.requestPaused', onPaused);
    await page.send('Fetch.disable').catch(() => undefined);
    if (pf) {
      const listed = await api(base, 'GET', `/api/v1/folders/${pf}/children`, accountKey).catch(() => null);
      for (const n of listed?.items ?? []) {
        if (n.name === name) await api(base, 'DELETE', `/api/v1/nodes/${n.id}`, accountKey).catch(() => undefined);
      }
    }
  }
}
