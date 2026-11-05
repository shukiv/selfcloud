// SPDX-License-Identifier: AGPL-3.0-or-later

// keepInView moves the scrolling menu box, and only that box, until item is whole inside it. The page never scrolls: a
// scrollIntoView would also move the page when the sidebar sits below the fold.
export function keepInView(box: HTMLElement, item: HTMLElement): void {
  const b = box.getBoundingClientRect();
  const r = item.getBoundingClientRect();
  if (r.bottom > b.bottom) box.scrollTop += r.bottom - b.bottom;
  else if (r.top < b.top) box.scrollTop -= b.top - r.top;
}

// followTarget is what the sidebar keeps in view: the menu's selected item, or the current place in the tree. The tree marks the
// place on its treeitem, which also holds the open folders under it; only the place's own row is kept in view, or a place with
// many open folders would scroll its own row off the top (decision 81, gate stfu4r1).
export function followTarget(region: ParentNode): HTMLElement | null {
  const item = region.querySelector<HTMLElement>('.ant-menu-item-selected, [aria-current="location"]');
  return item?.querySelector<HTMLElement>(":scope > .tree-row") ?? item;
}
