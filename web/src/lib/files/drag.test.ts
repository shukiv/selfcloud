// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { bandSelection, boxBetween, dropDecision, edgeScroll, movedFar, touches, type Dest } from "./drag";
import type { Node } from "./types";

function node(id: string, extra: Partial<Node> = {}): Node {
  return {
    id,
    folder_id: "fld_aaaaaaaa",
    parent_id: "nod_parent01",
    name: id,
    kind: "file",
    size: 1,
    created_at: "",
    modified_at: "",
    no_download: false,
    editors_can_share: false,
    ...extra,
  };
}

const both = { move: true, copy: true };
const dest = (extra: Partial<Dest> = {}): Dest => ({ folder: "fld_aaaaaaaa", dir: "nod_target01", role: "editor", ...extra });

describe("dropDecision", () => {
  it("moves into a folder the caller may add to, and copies with Ctrl", () => {
    const n = [node("nod_one00001")];
    expect(dropDecision(n, dest(), false, both)).toEqual({ ok: true, mode: "move", nodes: n });
    expect(dropDecision(n, dest(), true, both)).toEqual({ ok: true, mode: "copy", nodes: n });
  });

  it("refuses a place the caller may only read, with the paste's own rule", () => {
    expect(dropDecision([node("nod_one00001")], dest({ role: "viewer" }), false, both)).toEqual({ ok: false, why: "role" });
    expect(dropDecision([node("nod_one00001")], dest({ role: "commenter" }), true, both)).toEqual({ ok: false, why: "role" });
    expect(dropDecision([node("nod_one00001")], dest({ role: "contributor" }), false, both).ok).toBe(true);
    expect(dropDecision([node("nod_one00001")], dest({ role: undefined }), false, both)).toEqual({ ok: false, why: "role" });
  });

  it("refuses a folder dropped on itself or on something below it", () => {
    const dir = node("nod_dir00001", { kind: "dir" });
    expect(dropDecision([dir], dest({ dir: "nod_dir00001" }), false, both)).toEqual({ ok: false, why: "self" });
    expect(dropDecision([dir], dest({ dir: "nod_deep0001", path: ["fld_aaaaaaaa", "nod_dir00001", "nod_deep0001"] }), true, both)).toEqual({ ok: false, why: "self" });
    // A file with the same id as a path entry is not a folder: only a folder can contain the place.
    expect(dropDecision([node("nod_dir00001")], dest({ path: ["nod_dir00001"] }), false, both).ok).toBe(true);
  });

  it("leaves out items already in the place, and refuses when none is left", () => {
    const inside = node("nod_in000001", { parent_id: "nod_target01" });
    const outside = node("nod_out00001");
    expect(dropDecision([inside, outside], dest(), false, both)).toEqual({ ok: true, mode: "move", nodes: [outside] });
    expect(dropDecision([inside], dest(), false, both)).toEqual({ ok: false, why: "here" });
    expect(dropDecision([inside], dest(), true, both)).toEqual({ ok: false, why: "here" });
    // The root of a folder is dir null; an item at the root is there.
    const atRoot = node("nod_root0001", { parent_id: null });
    expect(dropDecision([atRoot], dest({ dir: null }), false, both)).toEqual({ ok: false, why: "here" });
    // Another space's folder root is another place.
    expect(dropDecision([atRoot], dest({ folder: "fld_bbbbbbbb", dir: null }), false, both).ok).toBe(true);
  });

  it("takes the move and copy permission from the toolbar's actions", () => {
    const n = [node("nod_one00001")];
    expect(dropDecision(n, dest(), false, { move: false, copy: true })).toEqual({ ok: false, why: "move" });
    expect(dropDecision(n, dest(), true, { move: false, copy: true }).ok).toBe(true);
    expect(dropDecision(n, dest(), true, { move: true, copy: false })).toEqual({ ok: false, why: "move" });
  });
});

describe("the rubber band", () => {
  const items = [
    { id: "a", box: { x0: 0, y0: 0, x1: 100, y1: 40 } },
    { id: "b", box: { x0: 0, y0: 40, x1: 100, y1: 80 } },
    { id: "c", box: { x0: 0, y0: 80, x1: 100, y1: 120 } },
  ];

  it("selects what it touches, and not what it only meets at an edge", () => {
    expect(bandSelection(items, boxBetween(10, 30, 50, 50), [], false)).toEqual(["a", "b"]);
    expect(bandSelection(items, boxBetween(10, 40, 50, 80), [], false)).toEqual(["b"]);
    expect(touches(items[0].box, items[1].box)).toBe(false);
  });

  it("works whichever corner it starts from", () => {
    expect(boxBetween(50, 50, 10, 30)).toEqual({ x0: 10, y0: 30, x1: 50, y1: 50 });
    expect(bandSelection(items, boxBetween(50, 100, 10, 60), [], false)).toEqual(["b", "c"]);
  });

  it("adds to what was selected with Ctrl, in list order, and replaces it without", () => {
    expect(bandSelection(items, boxBetween(10, 90, 50, 110), ["a"], true)).toEqual(["a", "c"]);
    expect(bandSelection(items, boxBetween(10, 90, 50, 110), ["a"], false)).toEqual(["c"]);
    expect(bandSelection(items, boxBetween(10, 90, 50, 110), ["c", "a"], true)).toEqual(["a", "c"]);
  });

  it("selects nothing from a band over empty space", () => {
    expect(bandSelection(items, boxBetween(200, 0, 300, 100), [], false)).toEqual([]);
  });
});

describe("the pointer's movement", () => {
  it("starts a gesture only after a few pixels", () => {
    expect(movedFar(2, 2)).toBe(false);
    expect(movedFar(4, 0)).toBe(false);
    expect(movedFar(3, 4)).toBe(true);
  });

  it("scrolls only near the edges, faster the nearer", () => {
    expect(edgeScroll(400, 100, 800)).toBe(0);
    expect(edgeScroll(110, 100, 800)).toBeLessThan(0);
    expect(edgeScroll(790, 100, 800)).toBeGreaterThan(0);
    expect(Math.abs(edgeScroll(101, 100, 800))).toBeGreaterThan(Math.abs(edgeScroll(140, 100, 800)));
    expect(edgeScroll(0, 100, 800)).toBe(-24);
    expect(edgeScroll(900, 100, 800)).toBe(24);
  });
});
