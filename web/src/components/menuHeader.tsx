// SPDX-License-Identifier: AGPL-3.0-or-later

import type { CSSProperties, ReactNode } from "react";
import { theme } from "antd";
import { antdTokens } from "../theme/theme";
import { PALETTE, SIDEBAR } from "../theme/tokens";

// A menu's header (decision 95): a line of text above a menu's items, for information that is not an action, such as the
// account's "Signed in as" or a New menu's read-only reason. It is not a menu item, so it has no role, no disabled state and no hover,
// and the keyboard goes past it to the first item. It is drawn in the popup's own ground with the text colours that read 4.5:1 on
// it (see menuHeaderInks). The menu is named by the header's text (aria-labelledby on the menu), so a screen reader hears it once.

export type MenuHeaderMode = "light" | "dark";

// menuHeaderInks are the header's colours in a mode, as the app's antd tokens give them: the popup's ground, the label's ink (the
// secondary text) and the email's or the reason's ink (the primary text). The contrast test reads these values.
export function menuHeaderInks(mode: MenuHeaderMode): { ground: string; label: string; email: string } {
  const token = antdTokens(mode, PALETTE[mode], SIDEBAR).token ?? {};
  return {
    ground: String(token.colorBgElevated),
    label: String(token.colorTextSecondary),
    email: String(token.colorText),
  };
}

// MenuHeader is the header's frame. aria-hidden: the menu's name (aria-labelledby) carries its text to a screen reader, once.
export function MenuHeader({ children }: { children: ReactNode }) {
  const { token } = theme.useToken();
  const frame: CSSProperties = {
    boxSizing: "border-box",
    minInlineSize: 180,
    maxInlineSize: 280,
    padding: "8px 12px",
    color: token.colorText,
    background: token.colorBgElevated,
  };
  return (
    <div aria-hidden="true" style={frame}>
      {children}
    </div>
  );
}

// headerPopup is the Dropdown's popupRender for a menu with a header: the header above the menu. With no header the menu is drawn as is.
export function headerPopup(header: ReactNode | null): ((menu: ReactNode) => ReactNode) | undefined {
  if (header === null) return undefined;
  return (menu) => (
    <>
      {header}
      {menu}
    </>
  );
}
