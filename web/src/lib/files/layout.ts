// SPDX-License-Identifier: AGPL-3.0-or-later

// The sizes the file browser's sticky parts share, in pixels. The shell's header is 56 px and stays on screen; the toolbar's
// row sits under it; in the list view the column header sits under the toolbar. A row scrolled to by the keyboard keeps
// clear of all three, and the column header is pinned at the toolbar's bottom edge.
export const HEADER_H = 56;

// BUTTON_H is a toolbar button's height: 32 with a mouse, a 44 pixel target on a finger.
export const buttonH = (coarse: boolean): number => (coarse ? 44 : 32);

// The selection panel is its buttons with 4 px of the bar's colour above and below, so no button is flush with the bar's edge.
export const PANEL_INSET = 4;
export const panelH = (coarse: boolean): number => buttonH(coarse) + 2 * PANEL_INSET;

// The toolbar's row is as tall as the panel plus the row's own 4 px above and below on a mouse; on a finger the panel fills the
// row (52), so the row does not grow for the larger target.
export const rowPad = (coarse: boolean): number => (coarse ? 0 : 4);
export const toolbarH = (coarse: boolean): number => panelH(coarse) + 2 * rowPad(coarse);

// stickyTop is where the column header is pinned: below the shell's header and the toolbar.
export const stickyTop = (coarse: boolean): number => HEADER_H + toolbarH(coarse);

// THEAD_H is the height of the list's column header (antd's middle table size).
export const THEAD_H = 48;
