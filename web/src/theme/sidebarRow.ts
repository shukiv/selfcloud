// SPDX-License-Identifier: AGPL-3.0-or-later

import { RADIUS } from "./tokens";

// The sidebar's rows share one set of metrics (decision 81): the menu's items and every row of the folder tree. They are read here
// from the same tokens, so the two cannot drift apart. The height is antd's menu item height (controlHeightLG unless set) and,
// on a finger, the touch target; the inline margin is antd's item margin (marginXXS); the radius is the menu's item radius (RADIUS,
// theme/theme.tsx). The Shell's menu is given these values through its ConfigProvider.

// A row is at least this tall on a finger, the same as the other touch targets (decision 66).
export const SIDEBAR_TOUCH_ROW = 44;
// A mouse's folder arrow is 24 px wide: the icon column starts after it.
export const SIDEBAR_ARROW = 24;

export interface SidebarRow {
  height: number;
  inline: number;
  radius: number;
  // indent is the start of a row's icon column, from the row's start edge: the menu's inline indent (its Menu prop) and the folder
  // tree's arrow, which the tree's icon follows. A mouse 24 px, a finger 44 px (the arrow's touch target).
  indent: number;
}

export function sidebarRow(token: { controlHeightLG: number; marginXXS: number }, coarse: boolean): SidebarRow {
  return {
    height: coarse ? SIDEBAR_TOUCH_ROW : token.controlHeightLG,
    inline: token.marginXXS,
    radius: RADIUS,
    indent: coarse ? SIDEBAR_TOUCH_ROW : SIDEBAR_ARROW,
  };
}
