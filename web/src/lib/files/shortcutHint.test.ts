// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { isMacPlatform, shortcutHint, shortcutKeys } from "./shortcutHint";

describe("isMacPlatform: the platform's own key names", () => {
  it("reads a Mac from the platform string", () => {
    expect(isMacPlatform({ platform: "MacIntel" })).toBe(true);
  });

  it("reads an iPad as Apple, as the browsers report it", () => {
    expect(isMacPlatform({ platform: "iPad" })).toBe(true);
  });

  it("reads the newer user-agent data before the old platform string", () => {
    expect(isMacPlatform({ platform: "Linux x86_64", userAgentData: { platform: "macOS" } })).toBe(true);
    expect(isMacPlatform({ platform: "MacIntel", userAgentData: { platform: "Windows" } })).toBe(false);
  });

  it("reads Windows and Linux as not Apple", () => {
    expect(isMacPlatform({ platform: "Win32" })).toBe(false);
    expect(isMacPlatform({ platform: "Linux x86_64" })).toBe(false);
  });
});

describe("shortcutHint: the keys each action answers, as the menu shows them", () => {
  it("shows Ctrl on Windows and Linux, and the command sign on a Mac", () => {
    expect(shortcutHint("cut", false)).toBe("Ctrl+X");
    expect(shortcutHint("copyClip", false)).toBe("Ctrl+C");
    expect(shortcutHint("paste", false)).toBe("Ctrl+V");
    expect(shortcutHint("cut", true)).toBe("⌘X");
    expect(shortcutHint("paste", true)).toBe("⌘V");
  });

  it("shows F2 for Rename, Delete for Move to trash, and Enter for opening", () => {
    expect(shortcutHint("rename", false)).toBe("F2");
    expect(shortcutHint("delete", false)).toBe("Delete");
    expect(shortcutHint("preview", false)).toBe("Enter");
    expect(shortcutHint("open", false)).toBe("Enter");
  });

  it("shows nothing for an action that has no key", () => {
    expect(shortcutHint("share", false)).toBeUndefined();
    expect(shortcutHint("copyLink", true)).toBeUndefined();
    expect(shortcutHint("move", false)).toBeUndefined();
  });
});

// Review fix 8 and 9: Move to trash shows the Mac's key, and each shortcut has the name a screen reader reads (aria-keyshortcuts).
describe("shortcutHint: Mac Delete, and the names for aria-keyshortcuts", () => {
  it("Move to trash shows Command+Backspace on a Mac, and Delete elsewhere", () => {
    expect(shortcutHint("delete", true)).toBe("⌘⌫");
    expect(shortcutHint("delete", false)).toBe("Delete");
  });
  it("shortcutKeys names each shortcut for a screen reader, in the platform's keys", () => {
    expect(shortcutKeys("cut", false)).toBe("Control+X");
    expect(shortcutKeys("cut", true)).toBe("Meta+X");
    expect(shortcutKeys("delete", true)).toBe("Meta+Backspace");
    expect(shortcutKeys("delete", false)).toBe("Delete");
    expect(shortcutKeys("rename", false)).toBe("F2");
    expect(shortcutKeys("open", false)).toBe("Enter");
    expect(shortcutKeys("share", false)).toBeUndefined();
  });
});
