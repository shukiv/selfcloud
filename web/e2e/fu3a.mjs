// SPDX-License-Identifier: AGPL-3.0-or-later

// The web follow-ups of slice fu3a (decision 76), run inside the shared end-to-end run (run.mjs) in its own browser with its
// own profile, or alone with SELFCLOUD_E2E_ONLY=fu3a. The owner opens the invitation dialog: its email field has the focus
// at once, so the owner can type without a click.

// signInWith fills in the sign-in page and submits it.
async function signInWith(page, who) {
  await page.waitFor(`!!document.getElementById('login-email')`);
  await page.setInput('#login-email', who.email);
  await page.setInput('#login-password', who.password);
  await page.waitFor(`(() => { const b = document.querySelector('form button[type="submit"]'); if (!b) return false; b.click(); return true; })()`, 10000);
  await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]')`, 15000);
}

export async function fu3aChecks(h, { page, base, OWNER }) {
  const { check } = h;

  await page.goto(base + '/login');
  await signInWith(page, OWNER);
  await h.navigateTo(page, '/manage/people');
  await page.waitFor(`location.pathname === '/manage/people' && !!document.querySelector('main')`);
  await h.clickButton(page, 'Invite person');
  await page.waitFor(`!!document.getElementById('invite-email')`, 10000);
  // antd moves the focus into the dialog as it opens; the field takes it after that, so the wait is for the field itself.
  await page.waitFor(`document.activeElement !== null && document.activeElement.id === 'invite-email'`, 10000);
  check(true, 'the invitation dialog opens with the email field focused');

  // Cancel closes the dialog with nothing sent: the list is still there, and no invitation was made.
  await h.clickButton(page, 'Cancel');
  await page.waitFor(`!document.getElementById('invite-email')`, 10000);
  check(true, 'Cancel closes the invitation dialog');
}
