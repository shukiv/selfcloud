// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice cli4: device sign-in for the command-line client (decision 125). A terminal starts a sign-in with no credential and
// shows its code. The member types the code on /cli-approve, sees the terminal's name, and approves. The terminal's poll then
// receives a device token once, which works on the API. The member sees the device on Signed-in devices and signs it out
// there; the token stops working at once. The section signs the member in itself, and restores nothing it did not change.

const q = (v) => JSON.stringify(v);

// clickByLabel clicks the first enabled button on the page with this accessible name (aria-label or text), inside scope.
async function clickByLabel(page, label, scope = 'document') {
  await page.waitFor(
    `(() => { const root = ${scope}; const b = [...root.querySelectorAll('button')].find((x) => !x.disabled && ((x.getAttribute('aria-label') || x.textContent.trim()) === ${q(label)})); if (!b) return false; b.click(); return true; })()`,
    15000,
  );
}

// cli4Checks runs the section. opts: the browser page, the base address, and the member the page is signed in as.
export async function cli4Checks(h, { page, base, MEMBER }) {
  const { check, api, navigateTo, signIn, signOut } = h;
  const suffix = Date.now().toString(36);
  const terminal = `e2e terminal ${suffix}`;

  // The member signs in in this browser, so the approval and the list are the member's own. The page may be signed in
  // already (the sections before this one leave it so), and /login would then send it on, so it signs out first.
  await signOut(page);
  await signIn(page, MEMBER);

  // 1. The terminal starts a sign-in. It sends no credential, so no header is needed.
  const started = await api(base, 'POST', '/api/v1/auth/cli/start', null, { client_name: terminal });
  const code = started.user_code;
  check(/^[A-Z]{4}-[A-Z]{4}$/.test(code), `the terminal's user code has the shape XXXX-XXXX (${code})`);

  // 2. The member types the code on the approval page. The page shows the terminal's name before anything is approved.
  await navigateTo(page, '/cli-approve');
  await page.waitFor(`!!document.getElementById('cli-code')`, 15000);
  const addressBefore = await page.evaluate(`location.pathname + location.search`);
  await page.setInput('#cli-code', code);
  await clickByLabel(page, 'Continue');
  await page.waitFor(`document.body.innerText.includes(${q(terminal)})`, 15000);
  check(true, 'the approval page shows the terminal\'s name before anything is approved');
  const addressAfter = await page.evaluate(`location.pathname + location.search`);
  check(addressBefore === addressAfter && !addressAfter.includes(code.replace('-', '')), 'the code is not in the address of the approval page');

  // 3. The member approves. The page says so, and the terminal's next poll receives the token.
  await clickByLabel(page, 'Approve sign-in');
  await page.waitFor(`document.body.innerText.includes(${q('Approved. You can go back to the terminal.')})`, 15000);
  check(true, 'the approval page says the sign-in is approved');

  const polled = await api(base, 'POST', '/api/v1/auth/cli/poll', null, { device_code: started.device_code });
  check(typeof polled.token === 'string' && polled.token.startsWith('scd_'), 'the terminal\'s poll receives a device token (scd_)');
  const token = polled.token;
  const works = await api(base, 'GET', '/api/v1/folders', token);
  check(works !== null, 'the device token reads the API');

  // The token is given once: a second poll gets expired_token, and nothing is sent in its place.
  let again = null;
  try {
    await api(base, 'POST', '/api/v1/auth/cli/poll', null, { device_code: started.device_code });
  } catch (err) {
    again = String(err.message);
  }
  check(again !== null && again.includes('expired_token'), 'a second poll for the same sign-in gets expired_token, not a second token');

  // 4. Signed-in devices lists the device, by the terminal's name, to the member who approved it.
  await navigateTo(page, '/devices');
  await page.waitFor(`document.body.innerText.includes(${q(terminal)})`, 15000);
  check(true, 'Signed-in devices lists the approved terminal by its name');

  // 5. The member signs the device out from the list: the icon asks first, and the sign-out ends the token.
  await clickByLabel(page, `Sign out this device: ${terminal}`);
  await page.waitFor(`!!document.querySelector('.ant-modal')`, 10000);
  await clickByLabel(page, 'Sign out', `document.querySelector('.ant-modal')`);
  await page.waitFor(`!document.body.innerText.includes(${q(terminal)})`, 15000);
  check(true, 'signing the device out removes it from the list');

  let refused = null;
  try {
    await api(base, 'GET', '/api/v1/folders', token);
  } catch (err) {
    refused = String(err.message);
  }
  check(refused !== null && /401/.test(refused), `the signed-out device's token is refused (${refused ?? 'accepted'})`);
}
