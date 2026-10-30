// SPDX-License-Identifier: AGPL-3.0-or-later

// keepInView moves the scrolling menu box, and only that box, until item is whole inside it. The page never scrolls: a
// scrollIntoView would also move the page when the sidebar sits below the fold.
export function keepInView(box: HTMLElement, item: HTMLElement): void {
  const b = box.getBoundingClientRect();
  const r = item.getBoundingClientRect();
  if (r.bottom > b.bottom) box.scrollTop += r.bottom - b.bottom;
  else if (r.top < b.top) box.scrollTop -= b.top - r.top;
}
