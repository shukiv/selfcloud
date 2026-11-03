// SPDX-License-Identifier: AGPL-3.0-or-later

// The file menus' submenus (decision 70): Share, Organize and Information hold the entries named here, and the menu is opened
// on the entry by a hover on its submenu's title, as a person does. The entries are read by their labels (the shortcut at the
// right is not part of a label).
const q = (v) => JSON.stringify(v);

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
  // The entry must be there and enabled, as the older helpers waited for (review fix 13): a disabled entry does not run its action.
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

