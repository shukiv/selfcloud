// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice tl1: the admin lists are tables. Checked on the signed-in admin's pages: the tenants list at 375 pixels
// has no sideways scroll; the Tab key reaches a sortable header from the search box, and Enter there turns its aria-sort; on
// the admins list a second admin's row has its Remove icon while the signed-in admin's own row has none; on a tenant's
// people, a busy row keeps the focus on its pressed icon, which shows no hover disc under the pointer, and Remove asks first;
// an empty keys table keeps its headers and its New key, and its empty block is measured against the approved mockup. The
// second admin, the pending person and the empty tenant are made through the API for the checks and removed after them, and
// the desktop width comes back whatever happens. With SELFCLOUD_E2E_SHOTS set (a folder), the run saves the empty keys table
// there as empty-keys.png (1280 wide, at twice the pixels).
// Run alone with SELFCLOUD_E2E_ONLY=tl1: run.mjs signs the admin in first.
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { sleep } from './media.mjs';
import { pressKey } from './accountKeys.mjs';

const q = (v) => JSON.stringify(v);

// sideways reads whether the page is wider than its window, which is a sideways scroll on a phone.
const sideways = (page) => page.evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`);

// setWidth gives the page a window of this width (a phone at 375 pixels, or the desktop at 1280), at scale device pixels to
// each CSS pixel (2 for a picture).
async function setWidth(page, width, scale = 1) {
  await page.send('Emulation.setDeviceMetricsOverride', { width, height: 800, deviceScaleFactor: scale, mobile: width < 800 });
  await sleep(300);
}

// OFF is a point of the 1280 by 800 window with nothing under it that reacts to the pointer.
const OFF = { x: 1270, y: 790 };

// hoverRead reads an element before and under the pointer. The pointer goes to OFF first, so the move onto the element's
// middle is a real entry, and the reading waits out antd's 0.2 s transition. Under the pointer it reads the element's own
// background, the colour seen there (before the pointer too), its colour, the colour of antd's colorTextDisabled, the cursor
// of what is under the pointer, and whether the element's parent is hovered (the pointer really is there). The pointer goes
// back to OFF after.
async function hoverRead(page, el) {
  const read = `(() => {
    const e = ${el};
    const probe = document.createElement('span');
    probe.style.color = 'var(--ant-color-text-disabled)';
    document.body.append(probe);
    const disabled = getComputedStyle(probe).color;
    probe.remove();
    const r = e.getBoundingClientRect();
    const top = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    const s = getComputedStyle(e);
    // seen is the colour the eye gets there: the element's background, or, while it is transparent, its nearest ancestor's.
    let seen = 'rgba(0, 0, 0, 0)';
    for (let a = e; a && seen === 'rgba(0, 0, 0, 0)'; a = a.parentElement) seen = getComputedStyle(a).backgroundColor;
    return { background: s.backgroundColor, seen, color: s.color, disabled, cursor: top ? getComputedStyle(top).cursor : null, entered: !!e.parentElement?.matches(':hover') };
  })()`;
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: OFF.x, y: OFF.y, button: 'none', buttons: 0 });
  await sleep(400);
  const { seen: before } = await page.evaluate(read);
  const at = await page.evaluate(`(() => { const r = ${el}.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: at.x, y: at.y, button: 'none', buttons: 0 });
  await sleep(500);
  const under = await page.evaluate(read);
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: OFF.x, y: OFF.y, button: 'none', buttons: 0 });
  return { before, ...under };
}

// The Name header's cell, found by its label and its sort button.
const NAME_TH = `[...document.querySelectorAll('[role="region"] thead th')].find((x) => x.querySelector('button') && x.textContent.trim().startsWith(${q('Name')}))`;

// rowsOf reads the admins list's rows: each row's name cell text, whether it has the You badge, and its buttons' names.
const ROWS = `[...document.querySelectorAll('[role="region"] tbody tr[data-id]')].map((r) => ({ text: r.textContent, you: [...r.querySelectorAll('.ant-tag')].some((s) => s.textContent.trim() === ${q('You')}), buttons: [...r.querySelectorAll('button')].map((b) => b.getAttribute('aria-label') || '') }))`;

// The row of a tenant's person, by its accessible name, and one of its buttons by its name.
const personRow = (name) => `document.querySelector('[role="region"] tbody tr[aria-label=${q(name)}]')`;
const rowButton = (name, label) => `${personRow(name)}?.querySelector('button[aria-label=${q(label)}]')`;

// focusNow names the focused element: its tag and its accessible name, or its tag alone.
const focusNow = (page) =>
  page.evaluate(`(() => { const a = document.activeElement; return a ? a.tagName + (a.getAttribute('aria-label') ? ':' + a.getAttribute('aria-label') : '') : 'none'; })()`);

// holdRequests pauses the browser's requests whose address matches pattern until the returned release is called. paused()
// resolves at the first one, and fails after ms (15 s) with the pattern in its message, so a request that is never sent
// fails the section at once instead of hanging the run. The listener and the interception are removed by release.
async function holdRequests(page, pattern) {
  const held = [];
  let first;
  const arrived = new Promise((resolve) => (first = resolve));
  const onPaused = (p) => {
    held.push(p.requestId);
    first();
  };
  page.on('Fetch.requestPaused', onPaused);
  await page.send('Fetch.enable', { patterns: [{ urlPattern: pattern, requestStage: 'Request' }] });
  return {
    paused: async (ms = 15000) => {
      let timer;
      const late = new Promise((_resolve, reject) => {
        timer = setTimeout(() => reject(new Error(`the request ${pattern} was not sent within ${ms / 1000} s`)), ms);
      });
      try {
        await Promise.race([arrived, late]);
      } finally {
        clearTimeout(timer);
      }
    },
    release: async () => {
      page.off('Fetch.requestPaused', onPaused);
      for (const id of held) await page.send('Fetch.continueRequest', { requestId: id }).catch(() => undefined);
      await page.send('Fetch.disable');
    },
  };
}

// tenantPeopleChecks (the re-check's items 2 and 3) runs on a pending person of the run's account, made through the API.
// While the person's resend is on its way (the browser holds the request), the row is busy: the Resend icon pressed with
// the keyboard keeps the focus, disabled, and keeps it when the answer comes. Then the ⋮ menu's Remove asks first, and its
// footer's danger button removes the person. A person left behind by a failure is removed through the API.
async function tenantPeopleChecks(h, { page, base, accountID, accountKey }) {
  const { check, navigateTo } = h;
  const pat = { email: 'pat.pending@example.com', display_name: 'Pat Pending', role: 'member', verification: 'email' };
  const resend = `Resend invitation: ${pat.display_name}`;
  let patID = null;
  let hold = null;
  try {
    patID = (await h.api(base, 'POST', `/api/v1/accounts/${accountID}/users`, accountKey, pat)).id;
    await navigateTo(page, `/admin/tenants/${accountID}?tab=users`);
    await page.waitFor(`!!${rowButton(pat.display_name, resend)}`, 15000);

    // 4. The busy row in a real browser: Enter on the Resend icon, the request held, the focus read while it is held.
    hold = await holdRequests(page, `*/api/v1/accounts/${accountID}/users/${patID}/verification*`);
    await page.evaluate(`${rowButton(pat.display_name, resend)}.focus()`);
    await pressKey(page, 'Enter', 'Enter', 13);
    await hold.paused();
    await page.waitFor(`(() => { const b = ${rowButton(pat.display_name, resend)}; return !!b && (b.disabled || b.getAttribute('aria-disabled') === 'true'); })()`, 5000);
    const busyFocus = await focusNow(page);
    check(busyFocus === `BUTTON:${resend}`, `while the row is busy, the pressed Resend icon keeps the focus, disabled (${busyFocus})`);
    // Under the pointer the busy icon looks disabled: antd gives a text button that is not :disabled its hover
    // background, so an aria-disabled icon must be kept from it, and it shows the disabled colour and the not-allowed cursor.
    const busyHover = await hoverRead(page, rowButton(pat.display_name, resend));
    check(
      busyHover.entered && busyHover.background === 'rgba(0, 0, 0, 0)',
      `under the pointer, the busy Resend icon has no hover background (${busyHover.background}; pointer there: ${busyHover.entered})`,
    );
    check(
      busyHover.color === busyHover.disabled && busyHover.cursor === 'not-allowed',
      `under the pointer, the busy Resend icon has the disabled colour and the not-allowed cursor (${busyHover.color}, disabled ${busyHover.disabled}; ${busyHover.cursor})`,
    );
    const busyFocusAfterHover = await focusNow(page);
    check(busyFocusAfterHover === `BUTTON:${resend}`, `the pointer leaves the focus on the busy Resend icon (${busyFocusAfterHover})`);
    await hold.release();
    hold = null;
    await page.waitFor(`document.body.textContent.includes(${q('The email was sent.')}) || document.body.textContent.includes(${q('The email could not be sent')})`, 15000);
    await page.waitFor(`(() => { const b = ${rowButton(pat.display_name, resend)}; return !!b && !b.disabled && b.getAttribute('aria-disabled') !== 'true'; })()`, 5000);
    const afterFocus = await focusNow(page);
    check(afterFocus === `BUTTON:${resend}`, `when the answer comes, the Resend icon still has the focus (${afterFocus})`);

    // 5. Remove from account: the ⋮ entry is a danger one, the dialog asks, and its footer's danger button removes.
    await h.clickOn(page, `[role="region"] tbody tr[aria-label=${q(pat.display_name)}] button[aria-label=${q(`More actions for ${pat.display_name}`)}]`);
    await page.waitFor(
      `(() => { const e = [...document.querySelectorAll('.ant-dropdown-menu-item')].find((x) => x.textContent.trim() === ${q('Remove from account…')}); if (!e) return false; window.__tl1Danger = e.classList.contains('ant-dropdown-menu-item-danger'); e.click(); return true; })()`,
    );
    check(await page.evaluate(`window.__tl1Danger === true`), 'the ⋮ menu has Remove from account, a danger entry');
    await page.waitFor(`[...document.querySelectorAll('.ant-modal-title')].some((x) => x.textContent.trim() === ${q(`Remove ${pat.display_name}?`)})`, 10000);
    const footerDanger = await page.evaluate(`(() => { const b = document.querySelector('.ant-modal-footer .ant-btn-dangerous'); return b ? b.textContent.trim() : null; })()`);
    check(footerDanger === 'Remove', `the confirmation's footer has the danger button Remove (${footerDanger})`);
    await h.clickOn(page, '.ant-modal-footer .ant-btn-dangerous');
    await page.waitFor(`document.body.textContent.includes(${q(`${pat.display_name} was removed.`)}) && !${personRow(pat.display_name)}`, 15000);
    let gone = false;
    try {
      await h.api(base, 'GET', `/api/v1/accounts/${accountID}/users/${patID}`, accountKey);
    } catch (err) {
      gone = String(err.message).includes(': 404 ');
    }
    check(gone, 'the removed person is gone from the account, and from its list');
    if (gone) patID = null;
  } finally {
    if (hold) await hold.release();
    if (patID) await h.api(base, 'DELETE', `/api/v1/accounts/${accountID}/users/${patID}`, accountKey).catch(() => undefined);
  }
}

// emptyTableChecks (the owner's rule of 2026-11-09, decision 114): a new tenant, made through the API, has no API key. Its
// keys table keeps its header row, its body holds antd's Empty with the title and the New key action, and the action opens
// the new key form. The tenant is deleted through the API after.
async function emptyTableChecks(h, { page, base, adminKey }) {
  const { check, navigateTo } = h;
  let tenantID = null;
  try {
    tenantID = (await h.api(base, 'POST', '/api/v1/accounts', adminKey, { name: 'Empty Tables Ltd' })).id;
    // The tenant page keeps the tab it had when the address moves to another tenant inside the app, so the keys tab is
    // chosen as a person would, with its own button.
    await navigateTo(page, `/admin/tenants/${tenantID}?tab=keys`);
    await page.waitFor(`location.pathname === ${q(`/admin/tenants/${tenantID}`)} && !!document.getElementById('tab-keys')`, 15000);
    await h.clickOn(page, '#tab-keys');
    const region =`[...document.querySelectorAll('[role="region"]')].find((r) => r.getAttribute('aria-label') === ${q('API keys')})`;
    await page.waitFor(`!!${region}?.querySelector('tbody .ant-empty .empty-title')`, 15000);
    const seen = await page.evaluate(`(() => {
      const r = ${region};
      return {
        headers: [...r.querySelectorAll('thead th')].map((th) => th.textContent.trim()).filter(Boolean),
        title: r.querySelector('tbody .empty-title')?.textContent ?? null,
        action: r.querySelector('tbody .ant-empty button')?.textContent.trim() ?? null,
      };
    })()`);
    check(
      seen.headers.join(', ') === 'Name, Created, Last used, Expires, Status',
      `an empty keys table keeps its header row (${seen.headers.join(', ')})`,
    );
    check(seen.title === 'No API keys yet' && seen.action === 'New key', `its body has the Empty, with its title (no full stop, as the mockup's) and New key (${seen.title}; ${seen.action})`);

    // The empty block against the approved mockup's (Main.dc.html, the empty table), in CSS pixels at 1280
    // wide, each within 1 pixel: the picture 64 by 41; 14 from the picture to the title, 6 from the title to the description and
    // 16 from the description to the button; the button 36 high, padded 16 on each side with no border, its + 16 by 16 and 16
    // from its edge; the block 48 from the header row to the picture and 56 from the button to the block's end. The title is at
    // weight 600, the description at most 420 wide, and the block shows no hover band (the mockup's has none).
    const m = await page.evaluate(`(() => {
      const r = ${region};
      const box = (e) => e.getBoundingClientRect();
      const td = r.querySelector('tbody .ant-table-placeholder > td');
      const title = r.querySelector('tbody .empty-title');
      const desc = r.querySelector('tbody .empty-desc');
      const btn = r.querySelector('tbody .ant-empty button');
      const img = box(r.querySelector('tbody .ant-empty-image svg'));
      const glyph = box(btn.querySelector('svg'));
      const b = box(btn), t = box(title), d = box(desc), c = box(td);
      const bs = getComputedStyle(btn), cs = getComputedStyle(td);
      return {
        img: [img.width, img.height],
        picTitle: t.top - img.bottom,
        titleDesc: d.top - t.bottom,
        descButton: b.top - d.bottom,
        buttonH: b.height,
        above: img.top - c.top - parseFloat(cs.borderTopWidth),
        below: c.bottom - parseFloat(cs.borderBottomWidth) - b.bottom,
        pad: [bs.paddingInlineStart, bs.paddingInlineEnd, bs.borderInlineStartWidth, bs.borderInlineEndWidth].join(' '),
        inset: glyph.left - b.left,
        glyph: [glyph.width, glyph.height],
        weight: getComputedStyle(title).fontWeight,
        maxWidth: getComputedStyle(desc).maxWidth,
      };
    })()`);
    const near = (v, want) => Math.abs(v - want) <= 1;
    const px = (v) => Math.round(v * 10) / 10;
    // Every number on one line before the checks, so a failure shows them all (the checks stop at the first).
    console.log(
      `empty block, measured (mockup): picture ${m.img.map(px).join('x')} (64x41); picture-title ${px(m.picTitle)} (14); title-description ${px(m.titleDesc)} (6); ` +
        `description-button ${px(m.descButton)} (16); button height ${px(m.buttonH)} (36); above ${px(m.above)} (48); below ${px(m.below)} (56); ` +
        `button padding and border ${m.pad} (16px 16px 0px 0px); + inset ${px(m.inset)} (16); + ${m.glyph.map(px).join('x')} (16x16); title weight ${m.weight} (600); description max ${m.maxWidth} (420px)`,
    );
    check(near(m.img[0], 64) && near(m.img[1], 41), `the empty picture is 64 by 41, as the mockup's (${m.img.map(px).join(' by ')})`);
    check(near(m.picTitle, 14), `14 px from the picture to the title, as the mockup's (${px(m.picTitle)})`);
    check(near(m.titleDesc, 6), `6 px from the title to the description, as the mockup's (${px(m.titleDesc)})`);
    check(near(m.descButton, 16), `16 px from the description to the button, as the mockup's (${px(m.descButton)})`);
    check(near(m.buttonH, 36), `the button is 36 px high, as the mockup's (${px(m.buttonH)})`);
    check(near(m.above, 48) && near(m.below, 56), `the empty block is 48 px from the header row to the picture and 56 from the button to its end, as the mockup's (${px(m.above)}, ${px(m.below)})`);
    check(
      m.pad === '16px 16px 0px 0px' && near(m.inset, 16) && near(m.glyph[0], 16) && near(m.glyph[1], 16),
      `the button is padded 16 px on each side with no border, its + 16 by 16 at 16 from its edge, as the mockup's (${m.pad}; ${px(m.inset)}; ${m.glyph.map(px).join(' by ')})`,
    );
    check(m.weight === '600' && m.maxWidth === '420px', `the title is at weight 600 and the description at most 420 px wide (${m.weight}, ${m.maxWidth})`);
    const blockHover = await hoverRead(page, `${region}.querySelector('tbody .ant-table-placeholder > td')`);
    // antd paints the empty row's cell in colorBgContainer under the pointer; what counts is the colour seen, which must not change.
    check(blockHover.entered && blockHover.seen === blockHover.before, `the empty block shows no hover band (seen ${blockHover.before}, then ${blockHover.seen})`);

    // The picture for the owner: the empty keys table in the light theme, 1280 wide at twice the pixels.
    const shots = process.env.SELFCLOUD_E2E_SHOTS || null;
    if (shots) {
      await mkdir(shots, { recursive: true });
      await setWidth(page, 1280, 2);
      await page.evaluate(`window.scrollTo(0, 0)`);
      const theme = await page.evaluate(`document.documentElement.dataset.theme ?? null`);
      const clip = await page.evaluate(`(() => {
        const r = ${region};
        const b = (r.closest('[role="tabpanel"]') ?? r).getBoundingClientRect();
        const x = Math.max(0, b.left - 16), y = Math.max(0, b.top - 16);
        return { x, y, width: Math.min(innerWidth - x, b.width + 32), height: Math.min(innerHeight - y, b.height + 32), scale: 1 };
      })()`);
      const { data } = await page.send('Page.captureScreenshot', { format: 'png', clip });
      await writeFile(join(shots, 'empty-keys.png'), Buffer.from(data, 'base64'));
      check(theme === 'light', `the empty keys table is saved as empty-keys.png, in the light theme (${theme})`);
      await setWidth(page, 1280);
    }

    await page.evaluate(`${region}.querySelector('tbody .ant-empty button').click()`);
    await page.waitFor(`!!document.getElementById('new-key-name')`, 10000);
    check(true, 'the empty table\'s New key opens the new key form');
    // The form's own Cancel closes it.
    await page.evaluate(
      `[...document.getElementById('new-key-name').closest('.ant-modal').querySelectorAll('button')].find((b) => b.textContent.trim() === ${q('Cancel')}).click()`,
    );
    await page.waitFor(`!document.getElementById('new-key-name')`, 10000);
  } finally {
    if (tenantID) await h.api(base, 'DELETE', `/api/v1/accounts/${tenantID}`, adminKey).catch(() => undefined);
  }
}

export async function tl1Checks(h, { page, base, adminKey, accountID, accountKey }) {
  const { check, navigateTo } = h;
  const second = { email: 'tess.second@example.com', display_name: 'Tess Second', verification: 'email' };
  let secondID = null;
  try {
    // 1. The tenants list at 375 pixels: a table with a name column, and no sideways scroll.
    await setWidth(page, 375);
    await navigateTo(page, '/admin/tenants');
    // The router shows the page a moment after the click, and the page before it can hold a table too: the wait is for this page.
    await page.waitFor(
      `location.pathname === '/admin/tenants' && !!document.getElementById('tenant-search') && ([...document.querySelectorAll('[role="region"] tbody tr')].length > 0 || !!document.querySelector('[role="region"] .ant-empty'))`,
      15000,
    );
    check(!(await sideways(page)), 'the tenants list at 375 px has no sideways scroll');

    // 2. The keyboard reaches the Name header with Tab from the search box (the box is focused by script, the rest is keys),
    //    and Enter on it turns its aria-sort.
    const before = await page.evaluate(`(() => { const th = ${NAME_TH}; return th ? th.getAttribute('aria-sort') : null; })()`);
    await page.evaluate(`document.getElementById('tenant-search').focus()`);
    let reached = false;
    for (let i = 0; i < 12 && !reached; i++) {
      await pressKey(page, 'Tab', 'Tab', 9);
      reached = await page.evaluate(`(() => { const th = ${NAME_TH}; return !!th && th.querySelector('button') === document.activeElement; })()`);
    }
    check(reached, 'Tab from the search box reaches the Name header');
    await pressKey(page, 'Enter', 'Enter', 13);
    await sleep(400);
    const after = await page.evaluate(`(() => { const th = ${NAME_TH}; return th ? th.getAttribute('aria-sort') : null; })()`);
    check(reached && before !== null && after !== null && before !== after, `Enter on the Name header turns its aria-sort (${before} to ${after})`);

    // 3. The admins list: a second admin's row has a Remove icon (the positive control), and the signed-in admin's row, with
    //    the You badge, has none.
    secondID = (await h.api(base, 'POST', '/api/v1/admins', adminKey, second)).id;
    await setWidth(page, 1280);
    await navigateTo(page, '/admin/admins');
    await page.waitFor(`(() => { const rows = ${ROWS}; return rows.some((r) => r.you) && rows.some((r) => r.text.includes(${q(second.display_name)})); })()`, 15000);
    const rows = await page.evaluate(ROWS);
    const own = rows.filter((r) => r.you);
    const other = rows.find((r) => r.text.includes(second.display_name));
    check(!!other && other.buttons.includes(`Remove: ${second.display_name}`), `a second admin's row has its Remove icon (${other ? other.buttons.join(', ') : 'no row'})`);
    check(own.length === 1 && !own[0].buttons.some((b) => b.startsWith('Remove')), 'the signed-in admin has the You badge and no Remove on the admins list');

    await tenantPeopleChecks(h, { page, base, accountID, accountKey });
    await emptyTableChecks(h, { page, base, adminKey });
  } finally {
    if (secondID) await h.api(base, 'DELETE', `/api/v1/admins/${secondID}`, adminKey);
    await setWidth(page, 1280);
  }
}
