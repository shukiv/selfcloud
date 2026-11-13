// SPDX-License-Identifier: AGPL-3.0-or-later

// The file menus' submenus (decision 70): Share, Organize and Information hold the entries named here, and the menu is opened
// on the entry by a hover on its submenu's title, as a person does. The entries are read by their labels (the shortcut at the
// right is not part of a label).
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const q = (v) => JSON.stringify(v);

// A row's icon and its label are centred when their centres are within this many pixels of the row's centre.
const ROW_CENTRE_TOLERANCE = 1;

export const SUBMENU_OF = {
  'Share…': 'Share',
  'Copy link': 'Share',
  'Move to…': 'Organize',
  'Copy to…': 'Organize',
  'Add shortcut': 'Organize',
  Cut: 'Organize',
  Copy: 'Organize',
  Paste: 'Organize',
  Star: 'Organize',
  Unstar: 'Organize',
  Details: 'Information',
  Versions: 'Information',
};

// LABELS lists the labels of the open menu's items, in the menu's own order: a submenu's entries follow its title only when it is open.
export const TITLE_SEL = '.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-title-content, .ant-dropdown-menu-submenu-popup .ant-dropdown-menu-title-content';
export const LABELS = `[...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-title-content, .ant-dropdown-menu-submenu-popup .ant-dropdown-menu-title-content')].map((x) => x.textContent.trim())`;

// pickMenuEntry chooses the open menu's entry with this label. An entry in a submenu opens the submenu first (a hover on its
// title, then the entry is there), so the menu is left as a person leaves it: the entry's action runs and the menu closes.
export async function pickMenuEntry(page, label) {
  const sub = SUBMENU_OF[label];
  if (sub) await hoverMenuTitle(page, sub);
  // The entry must be there and enabled, as the older helpers waited for: a disabled entry does not run its action.
  const enabled = `[...document.querySelectorAll(${q(TITLE_SEL)})].some((x) => x.textContent.trim() === ${q(label)} && !x.closest('[role="menuitem"]')?.classList.contains('ant-dropdown-menu-item-disabled'))`;
  await page.waitFor(enabled, 10000).catch(async (err) => {
    const seen = await page.evaluate(`JSON.stringify({ labels: ${LABELS}, open: document.querySelectorAll('.ant-dropdown-menu-submenu-open').length })`);
    throw new Error(`${err.message.slice(0, 80)}; open menus: ${seen}`);
  });
  await page.evaluate(`(() => { const t = [...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-title-content, .ant-dropdown-menu-submenu-popup .ant-dropdown-menu-title-content')].find((x) => x.textContent.trim() === ${q(label)}); t.closest('[role="menuitem"]').click(); return true; })()`);
}

// hoverMenuTitle opens a submenu from its title, as a person does: a hover first, then a press on the title. The title is read again
// at each try, because the menu may still be moving in.
export async function hoverMenuTitle(page, title) {
  const isOpen = `(() => { const t = [...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-title-content, .ant-dropdown-menu-submenu-popup .ant-dropdown-menu-title-content')].find((x) => x.textContent.trim() === ${q(title)}); return !!(t && t.closest('.ant-dropdown-menu-submenu')?.classList.contains('ant-dropdown-menu-submenu-open')); })()`;
  const at = () => page.evaluate(`(() => { const t = [...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-title-content, .ant-dropdown-menu-submenu-popup .ant-dropdown-menu-title-content')].find((x) => x.textContent.trim() === ${q(title)}); if (!t) return null; const r = t.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
  const waitOpen = async (ms) => {
    try {
      await page.waitFor(isOpen, ms);
      return true;
    } catch {
      return false;
    }
  };
  let where = await at();
  if (!where) throw new Error(`no submenu ${title} in the open menu`);
  await page.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: where.x, y: where.y, button: 'none', buttons: 0 });
  let opened = await waitOpen(1500);
  if (!opened) {
    where = (await at()) ?? where;
    await page.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: where.x, y: where.y, button: 'left', buttons: 1, clickCount: 1 });
    await page.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: where.x, y: where.y, button: 'left', buttons: 0, clickCount: 1 });
    opened = await waitOpen(3000);
  }
  if (!opened) throw new Error(`submenu ${title} did not open`);
  // The entries mount after the popup opens: the caller reads them only once the popup has its items.
  await page.waitFor(`document.querySelectorAll('.ant-dropdown-menu-submenu-popup .ant-dropdown-menu-item').length > 0`, 5000);
  await new Promise((r) => setTimeout(r, 350));
}


// menuHeaderText is the text of the open menu's header (decision 95): the words the menu is named by, through aria-labelledby. A
// menu with no header gives ''. The header is not one of the menu's items, so an item list does not carry it.
// The menu is the open one that carries a name: a language or theme menu that is open at the same time has none, and is skipped.
export const menuHeaderText = (page) =>
  page.evaluate(`(() => { const named = [...document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) ul[role="menu"][aria-labelledby]')]; if (named.length === 0) return ''; const m = named[named.length - 1]; return (m.getAttribute('aria-labelledby') || '').split(' ').map((id) => (document.getElementById(id) || {}).textContent || '').join(' ').trim(); })()`);

// MENU_ROWS is every row of the dropdowns and submenu popups that are open now: a plain item's row is its li, a submenu's row is
// its title (the title is where the row is drawn; the li around it carries the styling but not the height). For each row it gives
// the centre of the row's box, of the icon's box and of the box the label's text is drawn in, in CSS pixels from the viewport top,
// and for a submenu title the styles it was laid out with and the height of its wrapper, so a row that is off can be explained.
export const MENU_ROWS = `(() => {
  const hidden = (el) => !!el.closest('.ant-dropdown-hidden, .ant-dropdown-menu-submenu-hidden, .ant-menu-hidden, [hidden]');
  const shown = (el) => {
    if (hidden(el)) return false;
    const r = el.getBoundingClientRect();
    const s = getComputedStyle(el);
    return r.width > 0 && r.height > 0 && s.display !== 'none' && s.visibility !== 'hidden';
  };
  const middle = (r) => r.top + r.height / 2;
  const rows = [];
  for (const row of document.querySelectorAll('.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item, .ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-submenu-title, .ant-dropdown-menu-submenu-popup .ant-dropdown-menu-item, .ant-dropdown-menu-submenu-popup .ant-dropdown-menu-submenu-title')) {
    if (!shown(row)) continue;
    const tc = row.querySelector(':scope > .ant-dropdown-menu-title-content') ?? row.querySelector('.ant-dropdown-menu-title-content');
    const icon = row.querySelector(':scope > .ant-dropdown-menu-item-icon') ?? row.querySelector('.ant-dropdown-menu-item-icon');
    const name = (tc ?? row).textContent.trim().replace(/\\s+/g, ' ');
    const r = row.getBoundingClientRect();
    let text = null;
    if (tc) {
      const range = document.createRange();
      range.selectNodeContents(tc);
      const t = range.getBoundingClientRect();
      if (t.height > 0) text = middle(t);
    }
    const sub = row.classList.contains('ant-dropdown-menu-submenu-title');
    const wrap = sub ? row.closest('.ant-dropdown-menu-submenu') : null;
    const cs = getComputedStyle(row);
    rows.push({
      name,
      row: middle(r),
      rowH: r.height,
      icon: icon && shown(icon) ? middle(icon.getBoundingClientRect()) : null,
      text,
      title: sub
        ? {
            display: cs.display,
            alignItems: cs.alignItems,
            height: cs.height,
            lineHeight: cs.lineHeight,
            paddingTop: cs.paddingTop,
            paddingBottom: cs.paddingBottom,
            blockSize: cs.getPropertyValue('block-size'),
            wrapperH: wrap ? wrap.getBoundingClientRect().height : null,
            wrapperComputedH: wrap ? getComputedStyle(wrap).height : null,
          }
        : null,
    });
  }
  return rows;
})()`;

// menuRowChecks measures every row of the menus that are open now and checks that each icon and each label sits on the row's
// middle, as the row is seen: within one pixel. It also checks that a submenu's title (the element that paints the row) is the
// 40 or 44 px row, not a taller box around one. A row that is off is reported with the styles that laid it out.
export async function menuRowChecks(page, check, where) {
  const rows = await page.evaluate(MENU_ROWS);
  check(rows.length > 0, `${where}: the open menu has rows to measure (${rows.length})`);
  const titles = rows.filter((r) => r.title !== null);
  const badHeight = titles.filter((r) => ![40, 44].some((h) => Math.abs(r.rowH - h) <= 0.5));
  const heights = titles.map((r) => `${q(r.name)} ${r.rowH.toFixed(1)} px (wrapper ${r.title.wrapperH === null ? '?' : `${r.title.wrapperH.toFixed(1)} px`})`);
  check(badHeight.length === 0, `${where}: every submenu title is a 40 or 44 px row (${heights.join('; ') || 'no submenu titles'})`);
  const off = [];
  for (const r of rows) {
    const moved = [];
    if (r.icon !== null && Math.abs(r.icon - r.row) > ROW_CENTRE_TOLERANCE) moved.push(`icon ${(r.icon - r.row).toFixed(1)} px`);
    if (r.text !== null && Math.abs(r.text - r.row) > ROW_CENTRE_TOLERANCE) moved.push(`label ${(r.text - r.row).toFixed(1)} px`);
    if (moved.length > 0) {
      const why = r.title === null ? `row ${r.rowH.toFixed(1)} px` : `row ${r.rowH.toFixed(1)} px; ${Object.entries(r.title).map(([k, v]) => `${k}=${v}`).join(', ')}`;
      off.push(`${q(r.name)} ${moved.join(', ')} [${why}]`);
    }
  }
  check(off.length === 0, `${where}: every row's icon and label is centred on the row (${rows.length} rows; off: ${off.join('; ') || 'none'})`);
  return rows;
}

// menuShot saves a menu's picture at device scale 2, as the chain's pictures are: the CSS viewport stays the same. The caller
// saves it only when SELFCLOUD_E2E_SHOTS names a folder.
export async function menuShot(page, dir, name) {
  await mkdir(dir, { recursive: true });
  await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 2, mobile: false });
  try {
    await new Promise((r) => setTimeout(r, 300));
    const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
    await writeFile(join(dir, name), Buffer.from(data, 'base64'));
  } finally {
    await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
  }
}
