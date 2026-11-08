// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Node } from "./types";

const refreshMock = vi.hoisted(() => vi.fn(() => Promise.resolve()));
vi.mock("../query", () => ({ refreshTree: refreshMock }));

import {
  decodeEditorName,
  findCopy,
  isSameName,
  makeAnnouncer,
  makeReceiver,
  parseNodeChanged,
  type NodeChanged,
} from "./officeSync";

function file(id: string, name: string, kind: "file" | "dir" = "file"): Node {
  return { id, folder_id: "fld_b", parent_id: "nod_dir", name, kind, size: 1, created_at: "", modified_at: "", no_download: false, editors_can_share: false } as Node;
}

const CHANGE: NodeChanged = { node: "nod_a1", folder: "fld_b", parent: "nod_dir", user: "usr_1" };

describe("decodeEditorName", () => {
  it("decodes a percent-encoded name the editor sends", () => {
    expect(decodeEditorName("Quarterly%20report")).toBe("Quarterly report");
    expect(decodeEditorName("letter%20(2).docx")).toBe("letter (2).docx");
  });

  it("returns null for a malformed encoding, instead of throwing", () => {
    expect(() => decodeEditorName("%E0%A4%A")).not.toThrow();
    expect(decodeEditorName("%E0%A4%A")).toBeNull();
    expect(decodeEditorName("100%")).toBeNull();
  });

  it("returns null for anything that is not a name", () => {
    expect(decodeEditorName(undefined)).toBeNull();
    expect(decodeEditorName(42)).toBeNull();
    expect(decodeEditorName("")).toBeNull();
    expect(decodeEditorName("   ")).toBeNull();
  });
});

describe("isSameName", () => {
  it("matches the exact name, and a name without its extension", () => {
    expect(isSameName("letter.docx", "letter.docx")).toBe(true);
    expect(isSameName("letter.docx", "letter")).toBe(true);
  });

  it("does not match another name, or the same base with another extension", () => {
    expect(isSameName("letter.docx", "letter (2).docx")).toBe(false);
    expect(isSameName("letter.docx", "letter.pdf")).toBe(false);
    expect(isSameName("letter.docx", "letter.pdf.docx")).toBe(false);
  });
});

describe("findCopy", () => {
  it("finds the child with exactly that name, among files", () => {
    const items = [file("nod_x", "other.docx"), file("nod_c2", "letter (2).docx")];
    expect(findCopy(items, "letter (2).docx")?.id).toBe("nod_c2");
  });

  it("never guesses: a near name, or a folder with the name, is no match", () => {
    expect(findCopy([file("nod_c2", "letter (2).docx")], "letter (9).docx")).toBeNull();
    expect(findCopy([file("nod_d", "letter (2).docx", "dir")], "letter (2).docx")).toBeNull();
    expect(findCopy([], "letter.docx")).toBeNull();
  });
});

describe("parseNodeChanged", () => {
  it("reads the ids and the user id of a change", () => {
    expect(parseNodeChanged({ kind: "node-changed", ...CHANGE })).toEqual({ kind: "node-changed", ...CHANGE });
  });

  it("refuses a message with a missing or wrong id, or a missing user", () => {
    expect(parseNodeChanged({ kind: "node-changed", ...CHANGE, node: "not an id" })).toBeNull();
    expect(parseNodeChanged({ kind: "node-changed", ...CHANGE, folder: undefined })).toBeNull();
    expect(parseNodeChanged({ kind: "node-changed", ...CHANGE, parent: 7 })).toBeNull();
    expect(parseNodeChanged({ kind: "node-changed", ...CHANGE, user: "" })).toBeNull();
    expect(parseNodeChanged({ kind: "other", ...CHANGE })).toBeNull();
    expect(parseNodeChanged("not an object")).toBeNull();
    expect(parseNodeChanged(null)).toBeNull();
  });
});

describe("makeAnnouncer", () => {
  it("sends one message per node in five seconds, and a node's second change waits", () => {
    const sent: NodeChanged[] = [];
    let now = 1_000;
    const announce = makeAnnouncer((m) => sent.push(m), () => now);
    expect(announce(CHANGE)).toBe(true);
    now += 4_999;
    expect(announce(CHANGE)).toBe(false);
    now += 1;
    expect(announce(CHANGE)).toBe(true);
    expect(sent).toHaveLength(2);
  });

  it("keeps each node on its own clock", () => {
    const sent: NodeChanged[] = [];
    const announce = makeAnnouncer((m) => sent.push(m), () => 1_000);
    expect(announce(CHANGE)).toBe(true);
    expect(announce({ ...CHANGE, node: "nod_c2" })).toBe(true);
    expect(sent.map((m) => m.node)).toEqual(["nod_a1", "nod_c2"]);
  });

  it("sends only the ids and the user id, never a name, a token or a session key", () => {
    const sent: NodeChanged[] = [];
    makeAnnouncer((m) => sent.push(m), () => 0)({ ...CHANGE, user: "usr_1" });
    expect(Object.keys(sent[0] ?? {}).sort()).toEqual(["folder", "node", "parent", "user"]);
  });
});

describe("makeReceiver", () => {
  beforeEach(() => {
    refreshMock.mockClear();
  });

  it("refreshes the branch a change names, for the signed-in user only", async () => {
    const r = makeReceiver({} as never, () => "usr_1", 0);
    // The other person's change names another directory, so a read of it would show in the branches.
    r.receive({ kind: "node-changed", ...CHANGE, user: "usr_2", parent: "nod_other" });
    r.receive({ kind: "node-changed", ...CHANGE });
    await new Promise((res) => setTimeout(res, 10));
    expect(refreshMock).toHaveBeenCalledTimes(1);
    expect(refreshMock).toHaveBeenCalledWith({}, [{ folder: "fld_b", dir: "nod_dir" }]);
    r.stop();
  });

  it("coalesces several changes in one burst into one refresh per branch", async () => {
    const r = makeReceiver({} as never, () => "usr_1", 20);
    for (let i = 0; i < 5; i++) r.receive({ kind: "node-changed", ...CHANGE, node: `nod_${i}aa` });
    r.receive({ kind: "node-changed", ...CHANGE, parent: "nod_other" });
    await new Promise((res) => setTimeout(res, 60));
    expect(refreshMock).toHaveBeenCalledTimes(1);
    const [, branches] = refreshMock.mock.calls[0] as unknown as [unknown, { folder: string; dir: string | null }[]];
    expect(branches.map((b) => b.dir).sort()).toEqual(["nod_dir", "nod_other"]);
    r.stop();
  });

  it("ignores a message that names no usable ids", async () => {
    const r = makeReceiver({} as never, () => "usr_1", 0);
    r.receive({ kind: "node-changed", user: "usr_1" });
    r.receive("garbage");
    await new Promise((res) => setTimeout(res, 10));
    expect(refreshMock).not.toHaveBeenCalled();
    r.stop();
  });

  it("ignores every message when no one is signed in", async () => {
    const r = makeReceiver({} as never, () => null, 0);
    r.receive({ kind: "node-changed", ...CHANGE });
    await new Promise((res) => setTimeout(res, 10));
    expect(refreshMock).not.toHaveBeenCalled();
    r.stop();
  });
});
