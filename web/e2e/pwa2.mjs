// SPDX-License-Identifier: AGPL-3.0-or-later

// The slice pwa2 checks (decision 82): the share target in a real browser on 127.0.0.1. The manifest carries the share target;
// with no worker in control the server answers a share post with 303 to the share page, and keeps nothing; with the worker in
// control, a form post of a file to /share-target lands on the share page, the file is listed and says where it came from, and
// Upload puts it in My files; a share from another site is refused (303, nothing listed); the page reads in Hebrew and fits
// 390 pixels.
import { setLanguage, setPhone, sleep } from './media.mjs';

const q = (v) => JSON.stringify(v);

// clickButton presses the first button whose text is the given text, as a person does.
async function clickButton(page, text) {
  const done = await page.evaluate(
    `(() => { const b = [...document.querySelectorAll('button')].find((x) => x.textContent.trim() === ${q(text)} && !x.disabled); if (!b) return false; b.click(); return true; })()`,
  );
  if (!done) throw new Error(`no enabled button "${text}"`);
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

// The page's text, as a person reads it: the visible words of the document.
const bodyText = `(document.body ? document.body.innerText : '')`;

export async function pwa2Checks(h, { page, base, check, MEMBER }) {
  const { check: ok } = h;
  const report = check ?? ok;
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
  const listed = await page.evaluate(bodyText);
  if (listed.indexOf('came from another app') < 0) {
    const where = await page.evaluate('location.pathname + location.search');
    console.log('pwa2 page at:', where, 'text:', JSON.stringify(listed.slice(0, 3000)));
  }
  report(listed.indexOf('came from another app') >= 0, 'pwa2: the share page says the files came from another app');
  report(listed.indexOf('text/plain') >= 0, 'pwa2: the share page shows the type of each file');

  // 4. Nothing is uploaded until Upload is pressed; Upload puts the file in My files and opens that folder.
  await clickButton(page, 'Upload');
  await page.waitFor(`location.pathname.indexOf('/files/') === 0 || location.pathname === '/files'`, 20000);
  await page.waitFor(`${bodyText}.indexOf('share-one.txt') >= 0`, 30000);
  report(true, 'pwa2: Upload queues the shared file and opens the folder it was saved to');

  // 5. Another site's form post is refused: the worker answers 303 to the share page with nothing held, so no file is listed.
  // The probe page is a document of the other origin (localhost, the same server), which has no worker of its own.
  await page.goto(`http://localhost:${port}/robots.txt`);
  await page.evaluate(postShare(`${base}/share-target`, 'cross-site.txt', 'attack body'));
  await page.waitFor(`location.origin === ${q(base)} && location.pathname === '/share'`, 20000);
  await page.waitFor(`${bodyText}.indexOf('Nothing was shared here') >= 0 || ${bodyText}.indexOf('Share the files again') >= 0`, 20000);
  const refused = await page.evaluate(bodyText);
  report(refused.indexOf('cross-site.txt') < 0, 'pwa2: a share from another site is refused, and its file is not listed');

  // 6. Hebrew: the share page's title and notice are read in Hebrew, and the page fits a 390 pixel screen.
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
}
