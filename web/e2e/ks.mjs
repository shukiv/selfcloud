// SPDX-License-Identifier: AGPL-3.0-or-later

// "Keep me signed in" on the sign-in page (slice ks, decision 75), run inside the shared end-to-end run (run.mjs) in its own
// browser with its own profile, or alone with SELFCLOUD_E2E_ONLY=ks. The box is unticked on a fresh browser. A sign-in with it
// unticked gives a browser-session cookie (no expiry); ticked, a cookie that lasts about 30 days. The browser remembers the
// last choice across a reload. In Hebrew the box is on the right, with its words on its left (measured as text, against the box). The shared signIn helper in
// run.mjs does not tick the box, so every other section signs in as before.

// cookieOf reads the session cookie the browser holds for this origin, by CDP, as the browser sees it.
async function cookieOf(page, base) {
  const { cookies } = await page.send('Network.getCookies', { urls: [base] });
  return cookies.find((c) => c.name === 'selfcloud_session' || c.name === '__Host-selfcloud_session') ?? null;
}

// signInWith fills in the sign-in page and submits it, with the box as it is.
async function signInWith(page, who) {
  await page.waitFor(`!!document.getElementById('login-email')`);
  await page.setInput('#login-email', who.email);
  await page.setInput('#login-password', who.password);
  await page.waitFor(`(() => { const b = document.querySelector('form button[type="submit"]'); if (!b) return false; b.click(); return true; })()`, 10000);
  await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]')`, 15000);
}

// setBox ticks or unticks the box by its own input, as a click on its label does.
async function setBox(page, want) {
  await page.waitFor(`!!document.getElementById('login-keep')`);
  const now = await page.evaluate(`document.getElementById('login-keep').checked`);
  if (now !== want) await page.evaluate(`document.getElementById('login-keep').click()`);
}

export async function ksChecks(h, { page, base, MEMBER }) {
  const { check, signOut } = h;

  // A fresh browser: the box is unticked.
  await page.goto(base + '/login');
  await page.waitFor(`!!document.getElementById('login-keep')`, 15000);
  const fresh = await page.evaluate(`document.getElementById('login-keep').checked`);
  check(fresh === false, 'on a fresh browser the "Keep me signed in" box is unticked');

  // Unticked: a session cookie, which the browser drops when it closes.
  await signInWith(page, MEMBER);
  const unticked = await cookieOf(page, base);
  check(unticked !== null && unticked.session === true && unticked.expires === -1,
    `a sign-in with the box unticked gives a browser-session cookie (session ${unticked?.session}, expires ${unticked?.expires})`);
  await signOut(page);

  // Ticked: a cookie that lasts about 30 days.
  await page.waitFor(`!!document.getElementById('login-keep')`, 15000);
  await setBox(page, true);
  await signInWith(page, MEMBER);
  const ticked = await cookieOf(page, base);
  const days = ticked ? (ticked.expires - Date.now() / 1000) / 86400 : NaN;
  check(ticked !== null && ticked.session === false && days > 29 && days < 31,
    `a sign-in with the box ticked gives a cookie that lasts about 30 days (${days.toFixed(2)} days)`);
  await signOut(page);

  // The browser remembers the last choice: the box shows it again after a reload, and a new choice is remembered too.
  await page.goto(base + '/login');
  await page.waitFor(`!!document.getElementById('login-keep')`, 15000);
  await page.evaluate(`location.reload()`);
  await page.waitFor(`!!document.getElementById('login-keep')`, 15000);
  await page.waitFor(`document.getElementById('login-keep').checked === true`, 10000);
  check(true, 'after a reload the box shows the last choice (ticked)');
  await setBox(page, false);
  await page.evaluate(`location.reload()`);
  await page.waitFor(`!!document.getElementById('login-keep')`, 15000);
  await page.waitFor(`document.getElementById('login-keep').checked === false`, 10000);
  check(true, 'a choice to untick is remembered after a reload');

  // Hebrew: the page is right to left, the box sits on the right, and its words are on its left.
  await page.evaluate(`localStorage.setItem('selfcloud.lang', 'he')`);
  await page.evaluate(`location.reload()`);
  await page.waitFor(`document.documentElement.dir === 'rtl' && !!document.getElementById('login-keep')`, 15000);
  // The words are measured as text (a range over the label's text), not as the label's box: the words must end where the
  // box starts, on its left, and the box must be on the right of the form.
  const layout = await page.evaluate(`(() => {
    const label = document.querySelector('.login-keep');
    const range = document.createRange();
    range.selectNodeContents(label.lastElementChild);
    const text = range.getBoundingClientRect();
    const box = label.querySelector('.ant-checkbox').getBoundingClientRect();
    const form = document.querySelector('form').getBoundingClientRect();
    return {
      words: label.lastElementChild.textContent.trim(),
      textLeft: text.left, textRight: text.right, textWidth: text.width,
      boxLeft: box.left, boxRight: box.right, formMid: form.left + form.width / 2,
    };
  })()`);
  check(layout.textWidth > 0 && layout.textRight <= layout.boxLeft && layout.boxLeft > layout.formMid,
    `in Hebrew the words are on the left of the box, and the box on the right of the form (words ${Math.round(layout.textLeft)}-${Math.round(layout.textRight)}px, box ${Math.round(layout.boxLeft)}-${Math.round(layout.boxRight)}px, form middle ${Math.round(layout.formMid)}px): ${layout.words}`);

  // At phone width with a finger (touch emulation, so the coarse-pointer rule applies), the box's target is 44 px tall
  // and the page does not scroll sideways.
  await page.send('Emulation.setDeviceMetricsOverride', { width: 375, height: 812, deviceScaleFactor: 1, mobile: true });
  await page.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
  await page.waitFor(`!!document.getElementById('login-keep')`, 15000);
  const phone = await page.evaluate(`(() => ({
    h: document.querySelector('.login-keep').getBoundingClientRect().height,
    coarse: matchMedia('(pointer: coarse)').matches,
    overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
  }))()`);
  check(phone.coarse && phone.h >= 44, `at 375px with a finger the box's target is 44 px or more (${Math.round(phone.h)} px, coarse pointer ${phone.coarse})`);
  check(phone.overflow <= 0, `the sign-in page has no sideways scroll at 375px with the box (overflow ${phone.overflow}px)`);
  await page.send('Emulation.setTouchEmulationEnabled', { enabled: false });
  await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
}
