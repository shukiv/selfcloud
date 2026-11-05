// SPDX-License-Identifier: AGPL-3.0-or-later

// A new browser confirms by email once the email's sign-in cap is spent (sec2, decision 86). It runs inside the shared
// end-to-end run (run.mjs) in its own browser with its own profile, or alone with SELFCLOUD_E2E_ONLY=sec2. It uses a user of
// its own, made with a link and verified with its password, so no other section's sign-ins are held. Thirty wrong
// sign-ins from the server spend the email's cap (30 an hour). Then a browser with no device cookie signs in: the page says
// to check the email, with no error. The mail holds one link. Another browser cannot use it; the browser that asked can.
import { waitForMail } from './mailsink.mjs';

const EMAIL = 'cap@example.com';
const PASSWORD = 'a passphrase for the cap';
const WRONG = 'wrong password here';
const JSON_HEADERS = { 'Content-Type': 'application/json' };

// signInWith fills in the sign-in page and submits it. The sign-in box is left as it is.
async function signInWith(page, password) {
  await page.waitFor(`!!document.getElementById('login-email')`, 15000);
  await page.setInput('#login-email', EMAIL);
  await page.setInput('#login-password', password);
  await page.waitFor(`(() => { const b = document.querySelector('form button[type="submit"]'); if (!b) return false; b.click(); return true; })()`, 10000);
}

// clickButtonNamed presses the button whose words are text.
async function clickButtonNamed(page, text) {
  await page.waitFor(
    `(() => { const b = [...document.querySelectorAll('button')].find((x) => x.textContent === ${JSON.stringify(text)}); if (!b) return false; b.click(); return true; })()`,
    10000,
  );
}

// onPageTitled waits until a heading on the page has the words given.
async function onPageTitled(page, text) {
  await page.waitFor(`[...document.querySelectorAll('h1')].some((h) => h.textContent === ${JSON.stringify(text)})`, 15000);
}

export async function sec2Checks(h, { page, base, sink, accountID, accountKey }) {
  const { check, api } = h;

  // The user: made with a link by the account's key, then verified with the link and its password.
  const made = await api(base, 'POST', `/api/v1/accounts/${accountID}/users`, accountKey, { email: EMAIL, verification: 'link' });
  const verified = await fetch(base + '/api/v1/auth/verify', {
    method: 'POST',
    headers: JSON_HEADERS,
    body: JSON.stringify({ token: made.verification.token, password: PASSWORD }),
  });
  check(verified.status === 200, `the user made with a link is verified with its password (status ${verified.status})`);

  // Thirty wrong sign-ins from the server spend the email's cap for the hour.
  for (let i = 1; i <= 30; i++) {
    const res = await fetch(base + '/api/v1/auth/login', {
      method: 'POST',
      headers: JSON_HEADERS,
      body: JSON.stringify({ email: EMAIL, password: WRONG }),
    });
    if (res.status !== 401) throw new Error(`wrong sign-in ${i} answered ${res.status}, not 401`);
  }

  // A fresh browser, with no device cookie. A wrong password gets the confirmation, not an error.
  await page.goto(base + '/login');
  await signInWith(page, WRONG);
  await onPageTitled(page, 'Check your email');
  const shownError = await page.evaluate(`!!document.querySelector('.ant-alert-error')`);
  check(!shownError, 'a sign-in held for a new browser shows no error');

  // Back to the form, then the right password: the same answer, and one mail.
  await clickButtonNamed(page, 'Back to the sign-in form');
  await signInWith(page, PASSWORD);
  await onPageTitled(page, 'Check your email');

  // The newest mail to the user, once one has arrived: the one this attempt sent.
  await waitForMail(sink, EMAIL);
  const sent = sink.messages.filter((m) => m.to.includes(EMAIL));
  const mail = sent[sent.length - 1];
  // The mail is quoted-printable: soft line breaks go, and =3D is the equals sign of the fragment, as adminAccess reads it.
  const decoded = mail.data.replace(/=\r\n/g, '').replace(/=([0-9A-F]{2})/g, (_, hex) => String.fromCharCode(parseInt(hex, 16)));
  const token = /confirm-device#token=([A-Za-z0-9_-]+)/.exec(decoded)?.[1] ?? '';
  check(token !== '', 'the right password is mailed a link to confirm the browser');
  check(!decoded.includes(PASSWORD) && !decoded.includes(WRONG), 'the confirmation mail holds no password');

  // Another browser cannot use the link: it signs nothing in, and the link is still usable here.
  const other = await fetch(base + '/api/v1/auth/device-confirm', {
    method: 'POST',
    headers: JSON_HEADERS,
    body: JSON.stringify({ token }),
  });
  const otherBody = await other.json().catch(() => null);
  check(other.status === 403 && otherBody?.error?.details?.reason === 'other_browser',
    `another browser is refused the link, which it does not use (status ${other.status})`);

  // The browser that asked opens the link and presses the button: it is signed in, and it keeps a device cookie.
  await page.goto(`${base}/confirm-device#token=${token}`);
  await clickButtonNamed(page, 'Confirm and sign in');
  await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]')`, 15000);
  check(true, 'the browser that asked is signed in by its link');
  const { cookies } = await page.send('Network.getCookies', { urls: [base] });
  check(cookies.some((c) => c.name === 'selfcloud_device' || c.name === '__Host-selfcloud_device'),
    'the link also sets the device cookie, so the next sign-in from this browser is not held');
}
