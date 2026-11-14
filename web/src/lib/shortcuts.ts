// SPDX-License-Identifier: AGPL-3.0-or-later

// The shell's global keys and the keyboard shortcuts list (ks1, decision 174). The reader is pure, as the listing's
// (lib/files/keys.ts) and the New chord's (lib/files/newShortcut.ts) are: it takes the key, whether the key is blocked
// (a text field or an open overlay, read by chordBlocked), and the shell's state, and says what to do. The table below is
// the list the dialog draws: only shortcuts the code answers are named, each with the handler that answers it.

// GlobalKeyAction is what a global key asks the shell to do: focus the search box, show the list, or toggle the list.
export type GlobalKeyAction = "search" | "shortcuts" | "shortcutsToggle";

// GlobalKeyFields is the part of a keydown the reader needs. code is the key's position, which does not change with the layout.
export interface GlobalKeyFields {
  key: string;
  code?: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
}

// GlobalKeyContext is the shell's state the reader needs: whether there is a search box to focus (an admin has none), and
// whether the list is open (its own overlay must not block the chord that closes it).
export interface GlobalKeyContext {
  search: boolean;
  shortcutsOpen: boolean;
}

// readGlobalKey reads one keydown. "/" focuses the search box; "?" shows the list; Ctrl+/ (Command+/ on a Mac) toggles it.
// The slash is read by its character or by its position, so a layout that types another character there still toggles the
// list with Ctrl. A blocked key does nothing, except the toggle while the list itself is the open overlay. A key with Alt
// (AltGr) is left to the character it types.
export function readGlobalKey(e: GlobalKeyFields, blocked: boolean, ctx: GlobalKeyContext): GlobalKeyAction | null {
  const accel = e.ctrlKey || e.metaKey;
  const slash = e.key === "/" || e.code === "Slash";
  if (accel && !e.altKey && slash) {
    // The open list is the overlay the key is in, not another one: the chord still closes it.
    return blocked && !ctx.shortcutsOpen ? null : "shortcutsToggle";
  }
  if (blocked || accel || e.altKey) return null;
  if (e.key === "?") return "shortcuts";
  if (e.key === "/") return ctx.search ? "search" : null;
  return null;
}

// ShortcutRow is one line of the list. keys holds the ways to press it: each inner array is one way (its keys together),
// and more than one inner array are alternatives. then holds the letters of a chord (Alt+C, then a letter). macOnly marks
// a row only Apple's systems answer. label is a catalogue key of the shortcuts namespace.
export interface ShortcutRow {
  keys: string[][];
  then?: string[];
  label: string;
  macOnly?: boolean;
}

// ShortcutGroup is a titled part of the list; note names the place its keys act (the files page, or the open player).
export interface ShortcutGroup {
  title: string;
  note?: string;
  rows: ShortcutRow[];
}

// SHORTCUT_GROUPS is the list, in the order the dialog draws it. Every row is a shortcut the code answers: the listing's
// keys (lib/files/keys.ts, read by FilesPage), the drag's modifiers (useGestures), the New chord (newShortcut, read by
// SidebarNew), the player's keys (mediaKeys, read by MediaControls), and the shell's own keys above.
export const SHORTCUT_GROUPS: readonly ShortcutGroup[] = [
  {
    title: "shortcuts.group.general",
    rows: [
      { keys: [["/"]], label: "shortcuts.focusSearch" },
      { keys: [["Ctrl", "/"]], label: "shortcuts.toggleList" },
      { keys: [["?"]], label: "shortcuts.showList" },
    ],
  },
  {
    title: "shortcuts.group.move",
    note: "shortcuts.filesNote",
    rows: [
      { keys: [["↑", "↓", "←", "→"]], label: "shortcuts.move" },
      { keys: [["Home"]], label: "shortcuts.first" },
      { keys: [["End"]], label: "shortcuts.last" },
      { keys: [["Enter"]], label: "shortcuts.open" },
      { keys: [["Backspace"], ["Alt", "↑"]], label: "shortcuts.up" },
    ],
  },
  {
    title: "shortcuts.group.selection",
    note: "shortcuts.filesNote",
    rows: [
      { keys: [["Space"]], label: "shortcuts.selectToggle" },
      { keys: [["Shift", "↑", "↓", "←", "→"]], label: "shortcuts.extend" },
      { keys: [["Ctrl", "A"]], label: "shortcuts.selectAll" },
      { keys: [["Escape"]], label: "shortcuts.clearSelection" },
    ],
  },
  {
    title: "shortcuts.group.files",
    note: "shortcuts.filesNote",
    rows: [
      { keys: [["F2"]], label: "shortcuts.rename" },
      { keys: [["Delete"]], label: "shortcuts.trash" },
      { keys: [["Meta", "Backspace"]], label: "shortcuts.trash", macOnly: true },
      { keys: [["Ctrl", "C"]], label: "shortcuts.copy" },
      { keys: [["Ctrl", "X"]], label: "shortcuts.cut" },
      { keys: [["Ctrl", "V"]], label: "shortcuts.paste" },
      { keys: [["Escape"]], label: "shortcuts.cancelCut" },
      { keys: [["Shift", "F10"], ["ContextMenu"]], label: "shortcuts.itemMenu" },
      { keys: [["Ctrl"]], label: "shortcuts.dragCopy" },
      { keys: [["Escape"]], label: "shortcuts.dragCancel" },
    ],
  },
  {
    title: "shortcuts.group.new",
    rows: [
      { keys: [["Alt", "C"]], then: ["F"], label: "shortcuts.newFolder" },
      { keys: [["Alt", "C"]], then: ["U"], label: "shortcuts.upload" },
      { keys: [["Alt", "C"]], then: ["I"], label: "shortcuts.uploadFolder" },
      { keys: [["Alt", "C"]], then: ["T"], label: "shortcuts.document" },
      { keys: [["Alt", "C"]], then: ["S"], label: "shortcuts.spreadsheet" },
      { keys: [["Alt", "C"]], then: ["P"], label: "shortcuts.presentation" },
    ],
  },
  {
    title: "shortcuts.group.media",
    note: "shortcuts.mediaNote",
    rows: [
      { keys: [["Space"], ["K"]], label: "shortcuts.playPause" },
      { keys: [["J"]], label: "shortcuts.back10" },
      { keys: [["L"]], label: "shortcuts.forward10" },
      { keys: [["←"]], label: "shortcuts.back5" },
      { keys: [["→"]], label: "shortcuts.forward5" },
      { keys: [["↑"]], label: "shortcuts.volumeUp" },
      { keys: [["↓"]], label: "shortcuts.volumeDown" },
      { keys: [["M"]], label: "shortcuts.mute" },
      { keys: [["F"]], label: "shortcuts.fullscreen" },
      { keys: [["N"]], label: "shortcuts.next" },
      { keys: [["P"]], label: "shortcuts.previous" },
      { keys: [["Escape"]], label: "shortcuts.closeMedia" },
    ],
  },
];
