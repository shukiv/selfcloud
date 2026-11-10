// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import type { FolderAccess } from "../files/types";
import { locationOf } from "./location";

// Two spaces the caller reaches: a team space and the caller's own folder. root_node_id is each folder's root.
const folders = [
  { id: "f_team", name: "Finance", kind: "team", root_node_id: "root_team" },
  { id: "f_mine", name: "Mine", kind: "personal", root_node_id: "root_mine" },
] as FolderAccess[];

describe("locationOf says where an item sits (decision 136)", () => {
  it("names the parent the server gives", () => {
    expect(locationOf({ place: "Budget", folder_id: "f_team", parent_id: "dir_b" }, folders, "My files")).toBe("Budget");
  });

  it("names a top-level item's space, and the caller's own folder as My files", () => {
    expect(locationOf({ folder_id: "f_team", parent_id: "root_team" }, folders, "My files")).toBe("Finance");
    expect(locationOf({ folder_id: "f_mine", parent_id: "root_mine" }, folders, "My files")).toBe("My files");
  });

  it("shows nothing when the parent is out of reach, rather than guessing the space", () => {
    expect(locationOf({ folder_id: "f_team", parent_id: "dir_hidden" }, folders, "My files")).toBe("");
  });

  it("shows nothing for an item whose folder the caller does not list", () => {
    expect(locationOf({ folder_id: "f_gone", parent_id: "root_gone" }, folders, "My files")).toBe("");
  });

  it("prefers the server's place over the top-level rule", () => {
    expect(locationOf({ place: "Notes", folder_id: "f_team", parent_id: "root_team" }, folders, "My files")).toBe("Notes");
  });
});
