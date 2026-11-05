// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_TREE, MAX_EXPANDED, parseTree, readTree, touchExpanded, withExpanded, writeTree } from "./treeState";

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

  // Decision 88 (review L1): the cap forgets by last use. A folder the place passes through moves to the end, so the folder that was
  // opened first and not passed through since is the one forgotten.
  it("moves the folders a place passes through to the end, so the cap forgets by last use", () => {
    let s = DEFAULT_TREE;
    for (let i = 0; i < MAX_EXPANDED; i++) s = withExpanded(s, `f${i}`, true);
    s = touchExpanded(s, ["f0"]);
    expect(s.expanded.at(-1)).toBe("f0");
    s = withExpanded(s, "new", true);
    expect(s.expanded).toContain("f0");
    expect(s.expanded).not.toContain("f1");
    expect(s.expanded).toHaveLength(MAX_EXPANDED);
  });

  it("returns the same state when no open folder is passed through", () => {
    const s = withExpanded(withExpanded(DEFAULT_TREE, "a", true), "b", true);
    expect(touchExpanded(s, ["b"])).toBe(s);
    expect(touchExpanded(s, ["gone"])).toBe(s);
  });

  // The folders on the way that the reveal keeps (decision 88, H1) are not forgotten by the cap, even when they are the oldest.
  it("keeps the folders the way keeps, and forgets others", () => {
    let s = DEFAULT_TREE;
    for (let i = 0; i < MAX_EXPANDED; i++) s = withExpanded(s, `f${i}`, true);
    s = withExpanded(s, "new", true, ["f0", "f1"]);
    expect(s.expanded).toContain("f0");
    expect(s.expanded).toContain("f1");
    expect(s.expanded).not.toContain("f2");
    expect(s.expanded).toHaveLength(MAX_EXPANDED);
  });

  // Decision 69: the state moved to a new key, so the open folders saved before (which the tree used to reveal on its own) are
  // not read, and everyone starts with the tree folded once.
  it("does not read a state saved under the key of before this slice", () => {
    localStorage.setItem("selfcloud.tree.u1", JSON.stringify({ expanded: ["a"] }));
    expect(readTree("u1")).toEqual(DEFAULT_TREE);
    writeTree("u1", { expanded: ["b"] });
    expect(readTree("u1")).toEqual({ expanded: ["b"] });
  });
});
