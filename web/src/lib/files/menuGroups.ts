// SPDX-License-Identifier: AGPL-3.0-or-later

// The menu of one file, a folder or a selection is grouped the way Google Drive groups it (decision 70): the first group
// flat, then three submenus, then Move to trash. Each group is separated by a divider. This module only says where each
// action goes; the page builds the antd items from the pieces.

export type MenuSection = "first" | "share" | "organize" | "info" | "trash";

export interface GroupableAct {
  key: string;
  section: MenuSection;
  // hidden: the caller may never do it here. fits: the selection is of a kind the action applies to.
  hidden: boolean;
  fits: boolean;
}

export type MenuPiece<T extends GroupableAct> =
  | { kind: "item"; act: T }
  | { kind: "divider" }
  | { kind: "group"; section: Exclude<MenuSection, "first" | "trash">; acts: T[] };

// SECTIONS is the order the menu shows its parts in. Inside a section the actions keep the order the page lists them in.
export const SECTIONS: readonly MenuSection[] = ["first", "share", "organize", "info", "trash"];

// groupMenu turns the actions into the menu's pieces. Hidden and non-fitting actions are left out; a submenu with nothing
// left is left out with its divider; a divider sits between two parts that are both shown.
export function groupMenu<T extends GroupableAct>(acts: T[]): MenuPiece<T>[] {
  const pieces: MenuPiece<T>[] = [];
  for (const section of SECTIONS) {
    const shown = acts.filter((a) => a.section === section && !a.hidden && a.fits);
    if (shown.length === 0) continue;
    if (pieces.length > 0) pieces.push({ kind: "divider" });
    if (section === "first" || section === "trash") {
      for (const act of shown) pieces.push({ kind: "item", act });
    } else {
      pieces.push({ kind: "group", section, acts: shown });
    }
  }
  return pieces;
}
