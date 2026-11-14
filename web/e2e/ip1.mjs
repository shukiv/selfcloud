// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice ip1, the phone's install card (decision 179): on a finger's screen with a browser offer, the shell asks once, two
// seconds after it is drawn, naming the installation. Not now and the close icon keep the time under one browser key,
// "Don't remind me again" keeps the choice for good, and a reload reads either. Escape inside the card answers Not now, and
// Install uses the browser's offer once. The offer is synthetic: a beforeinstallprompt whose prompt() answers accepted,
// injected before every document draws, so the card's gate reads it as the browser's own.
import { join } from 'node:path';
import { launchBrowser, Page } from './cdp.mjs';
import { sleep, tap } from './media.mjs';

const q = (v) => JSON.stringify(v);
const CARD = '[data-testid="install-prompt"]';
const KEY = 'selfcloud.installPrompt';

// OFFER runs before the page's own scripts on every document. The app keeps the offer at startup (main.tsx), which a module
// script does before DOMContentLoaded, so the event fires then; a document already past it fires at once. prompt() counts the
// uses, so the section can see the card used the offer exactly once. The app is installable, so the browser makes its own
// offer too: a trusted one is answered with the synthetic after it, and the app's listener keeps the later event.
const OFFER = `
window.__ip1Prompted = 0;
window.__ip1Offer = () => {
  const e = new Event('beforeinstallprompt', { cancelable: true });
  e.prompt = () => { window.__ip1Prompted += 1; return Promise.resolve({ outcome: 'accepted' }); };
  window.dispatchEvent(e);
};
window.addEventListener('beforeinstallprompt', (e) => {
  if (e.isTrusted) { e.preventDefault(); setTimeout(() => window.__ip1Offer(), 0); }
});
if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', () => window.__ip1Offer());
else window.__ip1Offer();
`;

// button finds the card's button with this text; the close icon has none, and is named by its aria-label.
const button = (text) => `[...document.querySelectorAll(${q(CARD + ' button')})].find((b) => b.textContent.trim() === ${q(text)})`;
const closeButton = `document.querySelector(${q(`${CARD} button[aria-label="Close"]`)})`;

// tapButton taps the center of the button the expression finds, as a finger does.
async function tapButton(page, expr) {
  const at = await page.evaluate(`(() => { const b = ${expr}; if (!b) return null; const r = b.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
  if (!at) throw new Error(`no button for ${expr}`);
  await tap(page, at);
}

// reload loads the page again and waits for the shell; the injected offer fires on the new document too.
async function reload(page) {
  await page.send('Page.reload');
  await page.waitFor(`document.readyState === 'complete'`, 15000);
  await page.waitFor(`!!document.querySelector('[data-testid="account-menu"]')`, 15000);
}

// cardHere waits for the card (two seconds after the shell, plus its words); cardGone for its leaving.
const cardHere = (page) => page.waitFor(`!!document.querySelector(${q(CARD)})`, 15000);
const cardGone = (page) => page.waitFor(`!document.querySelector(${q(CARD)})`, 10000);
// staysHidden waits past the card's delay and polls, then reads that nothing drew.
async function staysHidden(page) {
  await sleep(4000);
  return page.evaluate(`!document.querySelector(${q(CARD)})`);
}
const clearChoice = (page) => page.evaluate(`window.localStorage.removeItem(${q(KEY)})`);
const choice = (page) => page.evaluate(`JSON.parse(window.localStorage.getItem(${q(KEY)}) ?? 'null')`);

// ip1Checks runs the section in its own browser with a finger's screen, signed in as the member.
export async function ip1Checks(h, { base, dir, browserBinary, MEMBER, EXPECTED, signIn, brandName }) {
  const { check } = h;
  const launch = await launchBrowser(browserBinary, join(dir, 'ip1-profile'), { finger: true });
  const page = new Page(launch.wsUrl);
  page.base = base;
  let scriptID = null;
  try {
    await page.start(EXPECTED);
    await page.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
    await page.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 });
    const added = await page.send('Page.addScriptToEvaluateOnNewDocument', { source: OFFER });
    scriptID = added.identifier;
    check(await page.evaluate(`matchMedia('(pointer: coarse)').matches`), 'the phone has a coarse pointer');

    // The sign-in page offers nothing: the card is the shell's, for a signed-in person.
    await page.goto(base + '/login');
    await page.waitFor(`!!document.getElementById('login-email')`, 15000);
    check(await staysHidden(page), 'the sign-in page shows no install card');
    await signIn(page, MEMBER);

    // The card names the installation, and is a labelled region, not a modal: it takes no focus and blocks nothing.
    await cardHere(page);
    const drawn = await page.evaluate(`(() => {
      const c = document.querySelector(${q(CARD)});
      const r = c.getBoundingClientRect();
      const btn = (t) => [...c.querySelectorAll('button')].find((b) => b.textContent.trim() === t);
      return {
        label: c.getAttribute('aria-label'), role: c.getAttribute('role'), modal: c.getAttribute('aria-modal'),
        text: c.textContent, focusInside: c.contains(document.activeElement),
        left: Math.round(r.left), right: Math.round(r.right), bottom: Math.round(r.bottom), width: Math.round(r.width),
        notNowH: Math.round(btn('Not now').getBoundingClientRect().height), installH: Math.round(btn('Install').getBoundingClientRect().height),
        never: !!btn("Don't remind me again"), close: !!c.querySelector('button[aria-label="Close"]'),
      };
    })()`);
    check(drawn.label === `Install ${brandName}` && drawn.role === 'region', `the card is a region named Install ${brandName} (${drawn.label})`);
    check(drawn.modal === null && !drawn.focusInside, 'the card is no modal and takes no focus');
    check(drawn.text.includes(`Install ${brandName} for one-tap access.`), 'the card says what the install is for');
    check(drawn.never && drawn.close, 'the card holds the never link and the close icon');
    check(drawn.notNowH >= 44 && drawn.installH >= 44, `a finger's buttons are 44 px (${drawn.notNowH}, ${drawn.installH})`);
    check(drawn.left >= 15 && drawn.right <= 375 && drawn.bottom <= 844 && drawn.width <= 480, `the card fits the phone's screen (${drawn.left}..${drawn.right}, bottom ${drawn.bottom}, width ${drawn.width})`);

    // Not now keeps the time under the one key, and a reload reads it: the card stays away.
    await tapButton(page, button('Not now'));
    await cardGone(page);
    let kept = await choice(page);
    check(kept && kept.forever === false && Math.abs(Date.now() - kept.at) < 10000, 'Not now keeps the time under the one key');
    await reload(page);
    check(await staysHidden(page), 'a reload after Not now shows no card');

    // Don't remind me again keeps the choice for good, and a reload reads that too.
    await clearChoice(page);
    await reload(page);
    await cardHere(page);
    await tapButton(page, button("Don't remind me again"));
    await cardGone(page);
    kept = await choice(page);
    check(kept && kept.forever === true, "Don't remind me again keeps the choice for good");
    await reload(page);
    check(await staysHidden(page), "a reload after Don't remind me again shows no card");

    // The close icon answers as Not now does.
    await clearChoice(page);
    await reload(page);
    await cardHere(page);
    await tapButton(page, closeButton);
    await cardGone(page);
    kept = await choice(page);
    check(kept && kept.forever === false && Math.abs(Date.now() - kept.at) < 10000, 'the close icon keeps the time as Not now does');

    // Escape while the focus is inside the card answers Not now.
    await clearChoice(page);
    await reload(page);
    await cardHere(page);
    await page.evaluate(`${button('Not now')}.focus()`);
    await page.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
    await page.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
    await cardGone(page);
    kept = await choice(page);
    check(kept && kept.forever === false, 'Escape inside the card answers Not now');

    // Install uses the browser's offer once, and the card goes with it.
    await clearChoice(page);
    await reload(page);
    await cardHere(page);
    check((await page.evaluate(`window.__ip1Prompted`)) === 0, 'the offer is unused before Install');
    await tapButton(page, button('Install'));
    await cardGone(page);
    const used = await page.evaluate(`window.__ip1Prompted`);
    check(used === 1, `Install uses the browser offer exactly once (${used})`);

    check(page.errors.length === 0, `the install card's browser has no console errors or policy violations (${page.errors.join(' | ') || 'none'})`);
  } finally {
    // The injected offer leaves with the section: nothing later in the run is offered an install it did not ask for.
    if (scriptID) await page.send('Page.removeScriptToEvaluateOnNewDocument', { identifier: scriptID }).catch(() => undefined);
    page.close();
    launch.proc.kill();
  }
}
