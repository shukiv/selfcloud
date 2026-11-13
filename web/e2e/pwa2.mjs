// SPDX-License-Identifier: AGPL-3.0-or-later

// The slice pwa2 checks (decision 82): the share target in a real browser on 127.0.0.1. The manifest carries the share target;
// with no worker in control the server answers a share post with 303 to the share page, and keeps nothing; with the worker in
// control, a form post of a file to /share-target lands on the share page, the file is listed with its name and size and the
// note that it came from another app; "Change the place" opens the chooser; Upload puts the file in My files with the bytes that
// were shared; a revisit of the address after Upload lists nothing; a share from another site is not listed; a share opened while
// signed out waits for the sign-in and then shows its files; the page reads in Hebrew and fits 390 pixels. With
// SELFCLOUD_PWA2_BIG set to a file path, a 1 GiB file is shared the same way and the page's memory is measured.
import { setLanguage, setPhone, sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);

// clickButton presses the first enabled button whose text is the given text, as a person does.
async function clickButton(page, text) {
  const done = await page.evaluate(
    `(() => { const b = [...document.querySelectorAll('button')].find((x) => x.textContent.trim() === ${q(text)} && !x.disabled); if (!b) return false; b.click(); return true; })()`,
  );
  if (!done) throw new Error(`no enabled button "${text}"`);
}

// clickIconButton presses the icon button with the given name (its aria-label), as a person does.
async function clickIconButton(page, label) {
  const done = await page.evaluate(
    `(() => { const b = [...document.querySelectorAll('button')].find((x) => x.getAttribute('aria-label') === ${q(label)}); if (!b) return false; b.click(); return true; })()`,
  );
  if (!done) throw new Error(`no icon button "${label}"`);
}

// postShare submits a form from the page's own document: a multipart post of one text file to the share target, with the
// target's address given (the same origin, or another site for the refusal check).
function postShare(action, name, text) {
  return `(() => {
    const f = document.createElement('form');
    f.method = 'POST';
    f.action = ${q(action)};
    f.enctype = 'multipart/form-data';
    const input = document.createElement('input');
    input.type = 'file';
    input.name = 'files';
    const dt = new DataTransfer();
    dt.items.add(new File([${q(text)}], ${q(name)}, { type: 'text/plain' }));
    input.files = dt.files;
    f.append(input);
    document.body.append(f);
    f.submit();
    return true;
  })()`;
}

// The page's text, as a person reads it: the visible words of the document. Parenthesised, so a test on it binds as one value.
const bodyText = `(document.body ? document.body.innerText : '')`;

// shareID is the id in the share page's address, or ''.
const shareID = `(new URLSearchParams(location.search).get('id') || '')`;

export async function pwa2Checks(h, { page, base, check, MEMBER, openProbe }) {
  const report = check ?? h.check;
  const port = new URL(base).port;

  // 1. The manifest: the share target, files only (decision 82).
  await page.goto(`${base}/manifest.webmanifest`);
  const manifest = JSON.parse(await page.evaluate(`document.body.innerText`));
  const wantTarget = {
    action: '/share-target',
    method: 'POST',
    enctype: 'multipart/form-data',
    params: { files: [{ name: 'files', accept: ['*/*'] }] },
  };
  report(JSON.stringify(manifest.share_target) === JSON.stringify(wantTarget), 'pwa2: the manifest declares the share target for files only');

  // 2. With no worker in control, a share post is answered 303 to the share page, and nothing is kept (the server reads no body).
  const form = new FormData();
  form.append('files', new Blob(['server body'], { type: 'text/plain' }), 'no-worker.txt');
  const bare = await fetch(`${base}/share-target`, { method: 'POST', body: form, redirect: 'manual' });
  report(bare.status === 303 && bare.headers.get('location') === '/share', 'pwa2: with no worker, POST /share-target is 303 to /share');

  // 3. Signed in, with the worker in control of the page: a share from the same origin is held and listed on the share page.
  // The section before this one may have left the member signed in: sign in only when the sign-in form is showing.
  await page.goto(`${base}/files`);
  await sleep(500);
  const needsSignIn = await page.evaluate(`!!document.getElementById('login-email')`);
  if (needsSignIn) await h.signInHere(page, MEMBER);
  await page.goto(`${base}/files`);
  // The share page's words are checked in English: the member's own language may differ.
  await setLanguage(page, 'English');
  await page.waitFor(`navigator.serviceWorker.controller !== null`, 20000);
  await page.evaluate(postShare('/share-target', 'share-one.txt', 'first share body'));
  await page.waitFor(`location.pathname === '/share' && location.search.indexOf('id=') > 0`, 20000);
  await page.waitFor(`${bodyText}.indexOf('share-one.txt') >= 0`, 20000);
  const shareOne = await page.evaluate(shareID);
  const listed = await page.evaluate(bodyText);
  if (listed.indexOf('came from another app') < 0) {
    const where = await page.evaluate('location.pathname + location.search');
    console.log('pwa2 page at:', where, 'text:', JSON.stringify(listed.slice(0, 3000)));
  }
  report(listed.indexOf('came from another app') >= 0, 'pwa2: the share page says the files came from another app');
  report(listed.indexOf('text/plain') >= 0, 'pwa2: the share page shows the type of each file');
  report(listed.indexOf('share-one.txt') < listed.indexOf('came from another app'), 'pwa2: the names come before the notice');

  // 3b. Change the place: the chooser opens at My files, and Save here keeps My files as the place.
  await clickIconButton(page, 'Change the place');
  await page.waitFor(`!!document.querySelector('.ant-modal') && document.querySelector('.ant-modal').textContent.indexOf('Save to') >= 0`, 15000);
  await clickButton(page, 'Save here');
  await page.waitFor(`!document.querySelector('.ant-modal')`, 15000);
  const afterChange = await page.evaluate(bodyText);
  report(afterChange.indexOf('My files') >= 0, 'pwa2: Change the place opens the chooser, and Save here keeps My files');

  // 4. Upload puts the file in My files with the bytes that were shared; the revisit of the address lists nothing.
  await clickButton(page, 'Upload');
  await page.waitFor(`location.pathname.indexOf('/files/') === 0 || location.pathname === '/files'`, 20000);
  await page.waitFor(`${bodyText}.indexOf('share-one.txt') >= 0`, 30000);
  report(true, 'pwa2: Upload queues the shared file and opens the folder it was saved to');
  // The file is in My files once its upload has finished, so the read waits for it (up to a minute).
  const READ_BYTES = `(async () => {
    const me = await (await fetch('/api/v1/me', { credentials: 'same-origin' })).json();
    const list = await (await fetch('/api/v1/folders/' + me.personal_folder_id + '/children?path=%2F&limit=200', { credentials: 'same-origin' })).json();
    const hit = list.items.find((n) => n.name === 'share-one.txt');
    if (!hit) return null;
    return await (await fetch('/api/v1/nodes/' + hit.id + '/content?disposition=attachment', { credentials: 'same-origin' })).text();
  })()`;
  await page.waitFor(`(async () => { const t = await ${READ_BYTES}; return t === 'first share body' ? 'yes' : ''; })()`, 60000);
  const bytes = await page.evaluate(READ_BYTES);
  report(bytes === 'first share body', 'pwa2: the uploaded file in My files holds the bytes that were shared');
  await page.goto(`${base}/share?id=${shareOne}`);
  await page.waitFor(`${bodyText}.indexOf('Share the files again') >= 0`, 20000);
  const revisit = await page.evaluate(bodyText);
  report(revisit.indexOf('share-one.txt') < 0, 'pwa2: a revisit of the address after Upload lists nothing');

  // 5. Another site's form post is refused: the worker answers 303 to the share page with nothing held, so no file is listed.
  // The probe page is a document of the other origin (localhost, the same server), which has no worker of its own. It runs in a
  // tab of the shared browser, with its own page, so the shared page, whose requests the run checks for same-origin, never
  // leaves this origin.
  let probe = null;
  try {
    probe = await openProbe();
    const probePage = probe.page;
    await probePage.goto(`http://localhost:${port}/robots.txt`);
    await probePage.evaluate(postShare(`${base}/share-target`, 'cross-site.txt', 'attack body'));
    await probePage.waitFor(`location.origin === ${q(base)} && location.pathname === '/share'`, 20000);
    await probePage.waitFor(`${bodyText}.indexOf('Nothing was shared here') >= 0 || ${bodyText}.indexOf('Share the files again') >= 0`, 20000);
    const refused = await probePage.evaluate(bodyText);
    report(refused.indexOf('cross-site.txt') < 0, 'pwa2: a share from another site is refused, and its file is not listed');
  } finally {
    if (probe) await probe.close();
  }

  // 6. A share opened while signed out: its files are taken at once, the person signs in, and the share page shows them.
  await h.signOut(page);
  await page.goto(`${base}/login`);
  await page.waitFor(`navigator.serviceWorker.controller !== null`, 20000);
  await page.evaluate(postShare('/share-target', 'signed-out.txt', 'signed out body'));
  await page.waitFor(`location.pathname === '/login' || location.pathname === '/sign-in'`, 20000);
  await h.signInHere(page, MEMBER);
  await page.waitFor(`location.pathname === '/share' && location.search.indexOf('id=') > 0`, 20000);
  await page.waitFor(`${bodyText}.indexOf('signed-out.txt') >= 0`, 20000);
  report(true, 'pwa2: a share opened while signed out is shown after the sign-in');
  await clickButton(page, 'Cancel');
  await page.waitFor(`location.pathname === '/files'`, 20000);

  // 7. Hebrew: the share page's title is read in Hebrew, and the page fits a 390 pixel screen.
  await page.goto(`${base}/files`);
  await page.waitFor(`navigator.serviceWorker.controller !== null`, 20000);
  await setLanguage(page, 'עברית');
  await setPhone(page, true);
  await page.evaluate(postShare('/share-target', 'share-he.txt', 'hebrew body'));
  await page.waitFor(`${bodyText}.indexOf('share-he.txt') >= 0`, 20000);
  const he = await page.evaluate(bodyText);
  report(he.indexOf('שמירת קבצים ששותפו') >= 0, 'pwa2: the share page title reads in Hebrew');
  const fits = await page.evaluate(`document.documentElement.scrollWidth <= window.innerWidth`);
  report(fits === true, 'pwa2: the share page has no horizontal scroll at 390 pixels');
  await setPhone(page, false);
  await setLanguage(page, 'English');
  await sleep(200);

  // 8. Optional: a 1 GiB file, shared from disk, and the page's memory while it is held and listed (decision 82).
  if (process.env.SELFCLOUD_PWA2_BIG) {
    await page.goto(`${base}/files`);
    await page.waitFor(`navigator.serviceWorker.controller !== null`, 20000);
    await page.evaluate(`(() => { const f = document.createElement('form'); f.id = 'pwa2big'; f.method = 'POST'; f.action = '/share-target'; f.enctype = 'multipart/form-data'; const i = document.createElement('input'); i.type = 'file'; i.name = 'files'; i.id = 'pwa2bigin'; f.append(i); document.body.append(f); return true; })()`);
    const heapBefore = await page.evaluate(`performance.memory ? performance.memory.usedJSHeapSize : -1`);
    const started = Date.now();
    await page.setFiles('#pwa2bigin', [process.env.SELFCLOUD_PWA2_BIG]);
    await page.evaluate(`(() => { document.getElementById('pwa2big').submit(); return true; })()`).catch(() => undefined);
    await page.waitFor(`location.pathname === '/share' && location.search.indexOf('id=') > 0`, 300000);
    await page.waitFor(`${bodyText}.indexOf('pwa2-big.bin') >= 0`, 300000);
    const ms = Date.now() - started;
    const heapAfter = await page.evaluate(`performance.memory ? performance.memory.usedJSHeapSize : -1`);
    const size = await page.evaluate(`(() => { const t = document.body.innerText; const i = t.indexOf('pwa2-big.bin'); return t.slice(i, i + 120).replace(/\\s+/g, ' '); })()`);
    console.log(`PWA2-BIG listed-in-ms=${ms} heapBytesBefore=${heapBefore} heapBytesAfter=${heapAfter} row=${JSON.stringify(size)}`);
    report(true, 'pwa2: a 1 GiB shared file is listed by the share page (measured, see the log)');
    await clickButton(page, 'Cancel');
    await page.waitFor(`location.pathname === '/files'`, 20000);
  }
}
