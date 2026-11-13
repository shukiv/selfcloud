// SPDX-License-Identifier: AGPL-3.0-or-later

// The keyboard shortcut a menu entry shows at its right. Only the keys the listing answers are named (lib/files/keys.ts).
// The key names are not translated: Ctrl, Delete, Enter and F2 are the names on the keyboard.

export interface PlatformHints {
  platform?: string;
  userAgentData?: { platform?: string };
}

// isMacPlatform says whether the browser runs on Apple's systems. The newer user-agent data is read first, since it is the
// one the browser states; the older platform string is the fallback.
export function isMacPlatform(nav: PlatformHints): boolean {
  const brand = nav.userAgentData?.platform;
  if (brand) return brand === "macOS";
  return /Mac|iPhone|iPad|iPod/.test(nav.platform ?? "");
}

// Each action with a key, and the letter the key types with Ctrl (or the command sign on a Mac).
const ACCEL: Record<string, string> = { cut: "X", copyClip: "C", paste: "V" };
const PLAIN: Record<string, string> = { rename: "F2", delete: "Delete", preview: "Enter", open: "Enter" };

// shortcutHint returns the hint for an action on the given kind of platform, or undefined for an action with no key.
export function shortcutHint(key: string, mac: boolean): string | undefined {
  const letter = ACCEL[key];
  if (letter) return mac ? `⌘${letter}` : `Ctrl+${letter}`;
  // Move to trash is Command+Backspace on a Mac.
  if (key === "delete" && mac) return "⌘⌫";
  return PLAIN[key];
}

// shortcutKeys names the same shortcut as a screen reader reads it (the aria-keyshortcuts attribute), in the platform's own keys.
export function shortcutKeys(key: string, mac: boolean): string | undefined {
  const letter = ACCEL[key];
  if (letter) return mac ? `Meta+${letter}` : `Control+${letter}`;
  if (key === "delete") return mac ? "Meta+Backspace" : "Delete";
  return PLAIN[key];
}
