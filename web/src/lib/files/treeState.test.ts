// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_TREE, MAX_EXPANDED, parseTree, readTree, revealed, withExpanded, writeTree } from "./treeState";

afterEach(() => {
  localStorage.clear();
  vi.restoreAllMocks();
});

describe("the tree's saved state", () => {
  it("keeps one state for each person", () => {
    writeTree("u1", { expanded: ["a"] });
    writeTree("u2", { expanded: ["b"] });
    expect(readTree("u1")).toEqual({ expanded: ["a"] });
    expect(readTree("u2")).toEqual({ expanded: ["b"] });
    expect(readTree("u3")).toEqual(DEFAULT_TREE);
  });

  it("ignores what it does not recognise", () => {
    expect(parseTree("not json")).toEqual(DEFAULT_TREE);
    expect(parseTree(JSON.stringify({ open: "no", expanded: [1, "x", null] }))).toEqual({ expanded: ["x"] });
    expect(parseTree(null)).toEqual(DEFAULT_TREE);
  });

  it("works when storage is blocked", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(readTree("u1")).toEqual(DEFAULT_TREE);
    expect(() => writeTree("u1", DEFAULT_TREE)).not.toThrow();
  });

  it("opens and closes a folder, and forgets the oldest when the list is full", () => {
    const s = withExpanded(DEFAULT_TREE, "a", true);
    expect(withExpanded(s, "a", false).expanded).toEqual([]);
    let big = DEFAULT_TREE;
    for (let i = 0; i < MAX_EXPANDED + 5; i++) big = withExpanded(big, `f${i}`, true);
    expect(big.expanded).toHaveLength(MAX_EXPANDED);
    expect(big.expanded).not.toContain("f0");
    expect(big.expanded).toContain(`f${MAX_EXPANDED + 4}`);
  });

  it("reveals the way to a place, and returns the same state when it is already open", () => {
    const s = revealed(DEFAULT_TREE, ["f", "d1"]);
    expect(s.expanded).toEqual(["f", "d1"]);
    expect(revealed(s, ["f", "d1"])).toBe(s);
  });
});
