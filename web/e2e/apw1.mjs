// SPDX-License-Identifier: AGPL-3.0-or-later

// App passwords (apw1, decision 236): the account-menu page that makes, lists and revokes the credentials of the
// read-only drive mount. It runs inside the shared end-to-end run (run.mjs) in its own browser with its own profile,
// or alone with SELFCLOUD_E2E_ONLY=apw1. The member opens /app-passwords from the account menu; with the mount off the
// page says the administrator has not turned it on and offers no New. The admin turns dav.enabled on through the
// settings API; after a reload the member makes "e2e mount", the secret shows once and starts with scp_, and the row is
// listed. A PROPFIND of /dav/ with Basic (the member's email : the secret) and Depth 1 answers 207; Revoke removes the
// row and the same PROPFIND answers 401. In a finally the setting is set back to false and anything the section made is
// revoked. The secret lives only in this section's own variable and the ShowOnce dialog; it is never printed.
const q = (v) => JSON.stringify(v);

export async function apw1Checks(h, { page, base, adminKey, MEMBER }) {
  const { check, api } = h;
  // The secret the page shows once. It is read from the ShowOnce field and kept here only, never logged.
  let secret = '';
  const clickText = (selector, text, timeout = 10000) =>
    page.waitFor(
      `(() => { const e = [...document.querySelectorAll(${q(selector)})].find((x) => x.textContent.trim() === ${q(text)}); if (!e) return false; e.click(); return true; })()`,
      timeout,
    );
  const propfind = (cred) =>
    fetch(base + '/dav/', { method: 'PROPFIND', headers: { Authorization: `Basic ${Buffer.from(cred).toString('base64')}`, Depth: '1' } });
  try {
    await h.signIn(page, MEMBER);
    // The member opens the page from the account menu, as the menu offers it.
    await h.clickOn(page, '[data-testid="account-menu"]');
    await clickText('.ant-dropdown-menu-item', 'App passwords');
    await page.waitFor(`location.pathname === '/app-passwords'`, 20000);

    // With the mount off the page says so and offers no New.
    await page.waitFor(`[...document.querySelectorAll('.ant-alert')].some((a) => a.textContent.includes('The administrator has not turned on the drive mount.'))`, 20000);
    check(true, 'with the mount off the page says the administrator has not turned on the drive mount');
    check(
      await page.evaluate(`![...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'New app password')`),
      'with the mount off there is no New button',
    );

    // The admin turns the mount on; after a reload the member can make an app password.
    await api(base, 'PATCH', '/api/v1/settings', adminKey, { set: { 'dav.enabled': true } });
    await page.send('Page.reload');
    await page.waitFor(`location.pathname === '/app-passwords' && [...document.querySelectorAll('button')].some((b) => b.textContent.trim() === 'New app password')`, 20000);
    check(true, 'with the mount on the page offers New');

    await clickText('button', 'New app password');
    await page.waitFor(`!!document.getElementById('apppw-name')`, 10000);
    await page.setInput('#apppw-name', 'e2e mount');
    await h.clickOn(page, '.ant-modal button[type="submit"]');
    await page.waitFor(`!!document.getElementById('apppw-secret-value')`, 15000);
    secret = await page.evaluate(`document.getElementById('apppw-secret-value').value`);
    check(typeof secret === 'string' && secret.startsWith('scp_'), 'the secret shows once and starts with scp_');
    check(
      await page.evaluate(`[...document.querySelectorAll('.ant-modal .ant-alert')].some((a) => a.textContent.includes('not shown again'))`),
      'the ShowOnce dialog warns the secret is not shown again',
    );
    // Close the dialog; the secret is gone from the page, and the row is listed.
    await h.clickOn(page, '.ant-modal .ant-btn-primary');
    await page.waitFor(`!document.getElementById('apppw-secret-value')`, 10000);
    check(!(await page.evaluate(`document.body.textContent.includes(${q(secret)})`)), 'the secret is gone once the dialog closes');
    await page.waitFor(`[...document.querySelectorAll('tbody tr')].some((r) => r.textContent.includes('e2e mount'))`, 15000);
    check(true, 'the made app password is listed');

    // The mount accepts the member's email and the secret as Basic, and answers a Depth 1 PROPFIND with 207.
    const live = await propfind(`${MEMBER.email}:${secret}`);
    check(live.status === 207, `the mount answers the member's PROPFIND with 207 (${live.status})`);

    // Revoke asks first, then removes the row; the same PROPFIND is refused.
    await page.waitFor(
      `(() => { const b = [...document.querySelectorAll('button')].find((x) => x.getAttribute('aria-label') === 'Revoke e2e mount'); if (!b) return false; b.click(); return true; })()`,
      10000,
    );
    await page.waitFor(`!!document.querySelector('.ant-popconfirm')`, 10000);
    await page.waitFor(
      `(() => { const b = [...document.querySelectorAll('.ant-popconfirm button')].find((x) => x.textContent.trim() === 'Revoke'); if (!b) return false; b.click(); return true; })()`,
      10000,
    );
    await page.waitFor(`![...document.querySelectorAll('tbody tr')].some((r) => r.textContent.includes('e2e mount'))`, 15000);
    check(true, 'Revoke removes the row');
    const gone = await propfind(`${MEMBER.email}:${secret}`);
    check(gone.status === 401, `the revoked secret answers 401 (${gone.status})`);
  } finally {
    await api(base, 'PATCH', '/api/v1/settings', adminKey, { set: { 'dav.enabled': false } }).catch(() => undefined);
    // Revoke anything the section made, through the page's own session: the routes refuse a key. The normal path already
    // revoked the one it made, so this usually finds nothing.
    try {
      for (let i = 0; i < 25; i++) {
        const has = await page.evaluate(`!!document.querySelector('tbody tr[data-id] button[aria-label^="Revoke "]')`).catch(() => false);
        if (!has) break;
        await page.evaluate(`document.querySelector('tbody tr[data-id] button[aria-label^="Revoke "]').click()`);
        await page.waitFor(`!!document.querySelector('.ant-popconfirm')`, 5000);
        await page.evaluate(`[...document.querySelectorAll('.ant-popconfirm button')].find((b) => b.textContent.trim() === 'Revoke')?.click()`);
        await page.waitFor(`!document.querySelector('.ant-popconfirm')`, 5000);
      }
    } catch {
      // The page may be gone; the setting is back off either way.
    }
  }
}
