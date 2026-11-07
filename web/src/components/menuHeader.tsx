// SPDX-License-Identifier: AGPL-3.0-or-later

import { theme } from "antd";

// A menu's header (decision 95): the words above a menu's items that say what the menu is about, such as the account's "Signed in
// as" or a New menu's read-only reason. It is the title of an antd group that holds the menu's items. An antd group title is not
// focusable and not an item, so the keyboard goes straight to the first item; the title is text, not a disabled item. Its colours are
// set inline from menuHeaderInks, because antd paints a group title in colorTextDescription, which is 3.68:1 in dark.

// menuHeaderInks are the header's two inks, from antd's tokens: the label's ink (the secondary text) and the value's ink (the primary
// text). The menu is drawn on colorBgElevated, which is the ground the contrast is read against. The app sets each of these tokens
// for each mode in antdTokens (theme/theme.tsx), so the values here are the palette's own. A custom brand changes only the accent
// family of tokens (lib/brand.ts, TOKEN_KEYS), none of which these read, so the default palette is the one to test.
export interface MenuHeaderTokens {
  colorBgElevated?: string;
  colorTextSecondary?: string;
  colorText?: string;
}

export function menuHeaderInks(token: MenuHeaderTokens): { label: string; email: string } {
  return {
    label: String(token.colorTextSecondary),
    email: String(token.colorText),
  };
}

// useMenuHeaderInks is menuHeaderInks for the app's computed token, in the mode being painted.
export function useMenuHeaderInks(): { label: string; email: string } {
  const { token } = theme.useToken();
  return menuHeaderInks(token);
}

// trailingLabel is the words of a "Signed in as {name}" template before its name, trimmed. A trailing hyphen (Hebrew's template ends
// its words with one) is dropped, since the label stands on its own line above the address.
export function trailingLabel(parts: { text: string; name: boolean }[]): string {
  return parts
    .filter((p) => !p.name)
    .map((p) => p.text)
    .join("")
    .trim()
    .replace(/[‐‑-]+$/, "")
    .trim();
}
