// SPDX-License-Identifier: AGPL-3.0-or-later

// Decision 140: a folder more than 64 levels down keeps its trail, marked with "…". The fixture is a path of 66 folders in a team
// space, made in one request. Opened as the member the shared page is signed in as:
//  1. The API marks the answer for the 66th folder truncated, and not the one for the 64th.
//  2. At 1280 wide, in English: the breadcrumbs start with the mark for the folders above, which is not a link, is named "More
//     folders above" and shows that as a tooltip on hover; no crumb is the space's root; the last crumb is the folder shown, and the
//     one before it links its parent. The space keeps its own row in the folder tree.
//  3. In Hebrew (right to left): the mark comes first in reading order, at the right of the trail, with the Hebrew name.
//  4. On a 375 wide phone: the page does not scroll sideways.
//  5. A folder 64 levels down is as before: its first crumb links the space's root, and there is no mark.
import { hoverOver, setLanguage, setPhone, sleep } from './media.mjs';

const MARK = '.ant-breadcrumb [data-testid="crumbs-above"]';

// crumbs reads the breadcrumbs, in the order of the list: each crumb's text, whether it is the mark, its link and the current mark.
const crumbs = (page) =>
  page.evaluate(`[...document.querySelectorAll('.ant-breadcrumb ol > li:not(.ant-breadcrumb-separator)')].map((li) => ({
    text: li.textContent.trim(),
    mark: !!li.querySelector('[data-testid="crumbs-above"]'),
    href: li.querySelector('a')?.getAttribute('href') ?? null,
    current: !!li.querySelector('[aria-current="location"]'),
  }))`);

export async function an2Checks(h, { page, base, accountKey, teamA }) {
  const { check, api, navigateTo } = h;
  const top = `an2 ${Date.now().toString(36)}`;
  const names = [top, ...Array.from({ length: 65 }, (_, i) => `l${i + 2}`)];
  const made = await api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `/${names.join('/')}`, parents: true });
  check(made?.name === 'l66', `the an2 fixture is 66 folders down (${made?.name})`);
  if (made?.name !== 'l66') return;
  const answer = await api(base, 'GET', `/api/v1/nodes/${made.id}/ancestors`, accountKey);
  const byName = new Map(answer.ancestors.map((n) => [n.name, n.id]));
  check(answer.truncated === true && answer.ancestors.length === 64 && answer.ancestors[0].name === 'l2',
    `the answer for the 66th folder is the 64 nearest, l2 first, truncated (${answer.ancestors.length}, ${answer.ancestors[0]?.name}, ${answer.truncated})`);
  const at64 = byName.get('l64');
  const at64Answer = at64 ? await api(base, 'GET', `/api/v1/nodes/${at64}/ancestors`, accountKey) : null;
  check(at64Answer?.truncated === false && at64Answer.ancestors.length === 64 && at64Answer.ancestors[0].parent_id === null,
    `the answer for the 64th folder starts at the root and is not truncated (${at64Answer?.ancestors.length}, ${at64Answer?.truncated})`);

  // ---- 2. English, 1280 wide.
  await setPhone(page, false);
  await setLanguage(page, 'English');
  await navigateTo(page, `/files/${teamA}/${made.id}`);
  await page.waitFor(`!!document.querySelector(${JSON.stringify(MARK)})`, 20000);
  await sleep(300);
  const en = await crumbs(page);
  console.log(`INFO an2 crumbs: ${en.map((c) => (c.mark ? '[mark]' : c.text)).join(' > ')}`);
  check(en[0]?.mark === true && en[0].href === null, `the first crumb is the mark for the folders above, not a link (${JSON.stringify(en[0])})`);
  check(en[0]?.text.includes('More folders above'), `the mark is named "More folders above" (${en[0]?.text})`);
  check(en.filter((c) => c.mark).length === 1, `there is one mark (${en.filter((c) => c.mark).length})`);
  check(!en.some((c) => c.href === `/files/${teamA}`), `no crumb links the space's root (${en.map((c) => c.href).join(', ')})`);
  check(en[1]?.text === 'l2' && en[1].href === `/files/${teamA}/${byName.get('l2')}`, `the crumb after the mark links l2, the topmost of the 64 (${JSON.stringify(en[1])})`);
  const last = en[en.length - 1];
  check(last?.text === 'l66' && last.current, `the last crumb is the folder shown, marked current (${JSON.stringify(last)})`);
  check(en[en.length - 2]?.href === `/files/${teamA}/${byName.get('l65')}`, `the crumb before it links its parent (${JSON.stringify(en[en.length - 2])})`);
  const titled = await page.evaluate(`document.getElementById('browser-title')?.textContent ?? ''`);
  check(titled === 'l66', `the page's heading names the folder shown (${titled})`);
  const spaceRow = await page.evaluate(`!!document.querySelector('.shell-sider [data-testid="folder-tree"] .tree-row[data-drop-folder="${teamA}"][data-drop-dir=""]')`);
  check(spaceRow, 'the folder tree keeps the space\'s own row');
  await hoverOver(page, MARK);
  const tip = await page
    .waitFor(`[...document.querySelectorAll('.ant-tooltip:not(.ant-tooltip-hidden)')].some((t) => t.textContent.trim() === 'More folders above')`, 5000)
    .then(() => true)
    .catch(() => false);
  check(tip, 'hovering the mark shows "More folders above" as a tooltip');
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 5, y: 790, button: 'none', buttons: 0 });

  // ---- 3. Hebrew, right to left.
  await setLanguage(page, 'עברית');
  await page.waitFor(`document.documentElement.dir === 'rtl'`, 10000);
  await page.waitFor(`(document.querySelector(${JSON.stringify(MARK)})?.textContent ?? '').includes('תיקיות')`, 10000).catch(() => undefined);
  await sleep(300);
  const he = await page.evaluate(`(() => {
    const items = [...document.querySelectorAll('.ant-breadcrumb ol > li:not(.ant-breadcrumb-separator)')];
    const box = (el) => el.getBoundingClientRect();
    return { first: !!items[0]?.querySelector('[data-testid="crumbs-above"]'), text: items[0]?.textContent.trim() ?? '',
      markRight: box(items[0]).right, nextRight: items[1] ? box(items[1]).right : null };
  })()`);
  check(he.first && he.text.includes('תיקיות נוספות למעלה'), `in Hebrew the mark is first, with its Hebrew name (${he.text})`);
  check(he.nextRight !== null && he.markRight > he.nextRight, `in Hebrew the mark sits at the start, right of the next crumb (${he.markRight} vs ${he.nextRight})`);
  await setLanguage(page, 'English');
  await page.waitFor(`document.documentElement.dir === 'ltr'`, 10000);

  // ---- 4. A 375 wide phone.
  await setPhone(page, true);
  await page.send('Emulation.setDeviceMetricsOverride', { width: 375, height: 800, deviceScaleFactor: 1, mobile: true });
  await navigateTo(page, `/files/${teamA}/${made.id}`);
  await page.waitFor(`!!document.querySelector(${JSON.stringify(MARK)})`, 20000);
  await sleep(500);
  const wide = await page.evaluate(`(() => {
    const nav = document.querySelector('.ant-breadcrumb')?.closest('nav');
    const r = nav?.getBoundingClientRect();
    return { doc: document.documentElement.scrollWidth, body: document.body.scrollWidth, inner: window.innerWidth,
      navLeft: r?.left ?? null, navRight: r?.right ?? null, navScroll: nav ? nav.scrollWidth - nav.clientWidth : null };
  })()`);
  check(wide.doc <= wide.inner && wide.body <= wide.inner, `at 375 the page does not scroll sideways (document ${wide.doc}, body ${wide.body}, window ${wide.inner})`);
  check(wide.navLeft !== null && wide.navLeft >= 0 && wide.navRight <= wide.inner && wide.navScroll <= 0,
    `at 375 the breadcrumbs fit the window (${wide.navLeft} to ${wide.navRight}, overflow ${wide.navScroll})`);
  await setPhone(page, false);

  // ---- 5. 64 levels down, as before.
  await navigateTo(page, `/files/${teamA}/${at64}`);
  await page.waitFor(`[...document.querySelectorAll('.ant-breadcrumb [aria-current="location"]')].some((e) => e.textContent.trim() === 'l64')`, 20000);
  await sleep(300);
  const at64Crumbs = await crumbs(page);
  check(!at64Crumbs.some((c) => c.mark), `a folder 64 levels down has no mark (${at64Crumbs.map((c) => c.text).join(' > ')})`);
  check(at64Crumbs[0]?.href === `/files/${teamA}`, `a folder 64 levels down starts at the space's root (${JSON.stringify(at64Crumbs[0])})`);
}
