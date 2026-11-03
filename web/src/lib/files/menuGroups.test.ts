// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { groupMenu, type GroupableAct } from "./menuGroups";

function act(key: string, section: GroupableAct["section"], over: Partial<GroupableAct> = {}): GroupableAct {
  return { key, section, hidden: false, fits: true, ...over };
}

// describe turns the pieces into a short text, so each test reads as the menu the person sees.
function describeMenu(pieces: ReturnType<typeof groupMenu<GroupableAct>>): string[] {
  return pieces.map((p) => {
    if (p.kind === "divider") return "|";
    if (p.kind === "item") return p.act.key;
    return `${p.section}[${p.acts.map((a) => a.key).join(",")}]`;
  });
}

describe("groupMenu: the menu is grouped as Drive groups it (decision 70)", () => {
  it("puts the first group flat, then the three submenus, then Move to trash, with a divider between each group", () => {
    const acts = [
      act("preview", "first"),
      act("download", "first"),
      act("rename", "first"),
      act("share", "share"),
      act("copyLink", "share"),
      act("move", "organize"),
      act("paste", "organize"),
      act("details", "info"),
      act("versions", "info"),
      act("delete", "trash"),
    ];
    expect(describeMenu(groupMenu(acts))).toEqual([
      "preview",
      "download",
      "rename",
      "|",
      "share[share,copyLink]",
      "|",
      "organize[move,paste]",
      "|",
      "info[details,versions]",
      "|",
      "delete",
    ]);
  });

  it("keeps the order of the actions inside a group as they are listed", () => {
    const pieces = groupMenu([act("copyLink", "share"), act("share", "share")]);
    expect(describeMenu(pieces)).toEqual(["share[copyLink,share]"]);
  });

  it("leaves out a hidden action and one that does not fit the selection", () => {
    const pieces = groupMenu([act("download", "first", { hidden: true }), act("zip", "first", { fits: false }), act("rename", "first")]);
    expect(describeMenu(pieces)).toEqual(["rename"]);
  });

  it("drops a submenu whose entries are all hidden or do not fit, and its divider with it", () => {
    const pieces = groupMenu([
      act("rename", "first"),
      act("share", "share", { hidden: true }),
      act("copyLink", "share", { fits: false }),
      act("delete", "trash"),
    ]);
    expect(describeMenu(pieces)).toEqual(["rename", "|", "delete"]);
  });

  it("shows no divider when only one group is left", () => {
    expect(describeMenu(groupMenu([act("details", "info"), act("versions", "info", { hidden: true })]))).toEqual(["info[details]"]);
  });

  it("returns nothing for an empty menu", () => {
    expect(groupMenu([])).toEqual([]);
  });
});
