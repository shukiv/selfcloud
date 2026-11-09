// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Node } from "./types";

const refreshMock = vi.hoisted(() => vi.fn(() => Promise.resolve()));
vi.mock("../query", () => ({ refreshTree: refreshMock }));

import {
  copyCandidates,
  decodeEditorName,
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

// ef1 (decision 107): a Save As copy is one whose name is the requested name or its free form, made at or after the request.
describe("copyCandidates", () => {
  const REQUEST = Date.parse("2026-11-08T10:00:00Z");
  const NONE = new Set<string>();
  function made(id: string, name: string, created: string): Node {
    return { ...file(id, name), created_at: created } as Node;
  }

  it("takes the requested name, and the free form the server gives a taken name", () => {
    const items = [
      made("nod_c2", "letter (2).docx", "2026-11-08T10:00:05Z"),
      made("nod_c3", "letter.docx", "2026-11-08T10:00:06Z"),
    ];
    expect(copyCandidates(items, "letter.docx", REQUEST, NONE).map((n) => n.id)).toEqual(["nod_c2", "nod_c3"]);
  });

  it("takes a name whose free form was asked for (the request is 'letter (2).docx')", () => {
    const items = [made("nod_c4", "letter (2) (2).docx", "2026-11-08T10:00:05Z")];
    expect(copyCandidates(items, "letter (2).docx", REQUEST, NONE).map((n) => n.id)).toEqual(["nod_c4"]);
  });

  it("refuses any other name, including the same name in another case, and a folder", () => {
    const items = [
      made("nod_o1", "other (2).docx", "2026-11-08T10:00:05Z"),
      made("nod_o2", "Letter.docx", "2026-11-08T10:00:05Z"),
      { ...made("nod_d", "letter.docx", "2026-11-08T10:00:05Z"), kind: "dir" } as Node,
    ];
    expect(copyCandidates(items, "letter.docx", REQUEST, NONE)).toEqual([]);
  });

  it("refuses a copy made before the request, beyond the clock allowance", () => {
    const items = [made("nod_old", "letter.docx", "2026-10-01T00:00:00Z")];
    expect(copyCandidates(items, "letter.docx", REQUEST, NONE)).toEqual([]);
    // Thirty seconds before the request is inside the allowance for clock skew; a minute before is not.
    expect(copyCandidates([made("nod_skew", "letter.docx", "2026-11-08T09:59:40Z")], "letter.docx", REQUEST, NONE).length).toBe(1);
    expect(copyCandidates([made("nod_early", "letter.docx", "2026-11-08T09:59:00Z")], "letter.docx", REQUEST, NONE)).toEqual([]);
  });

  it("refuses a node with no usable creation time", () => {
    const items = [made("nod_x", "letter.docx", "")];
    expect(copyCandidates(items, "letter.docx", REQUEST, NONE)).toEqual([]);
  });

  it("reads the dot and the brackets of a name as plain characters", () => {
    const items = [made("nod_y", "a.b (2).docx", "2026-11-08T10:00:05Z"), made("nod_z", "aXb (2).docx", "2026-11-08T10:00:05Z")];
    // "a.b (2).docx" is the free form of "a.b.docx"; "aXb (2).docx" is not, since the dot is a dot.
    expect(copyCandidates(items, "a.b.docx", REQUEST, NONE).map((n) => n.id)).toEqual(["nod_y"]);
  });

  it("takes a name the editor types in another Unicode form (NFD), since the server stores NFC", () => {
    const nfd = "cafe\u0301.docx";
    expect(copyCandidates([made("nod_n", "caf\u00e9.docx", "2026-11-08T10:00:05Z")], nfd, REQUEST, NONE).map((n) => n.id)).toEqual(["nod_n"]);
    expect(copyCandidates([made("nod_f", "caf\u00e9 (2).docx", "2026-11-08T10:00:05Z")], nfd, REQUEST, NONE).map((n) => n.id)).toEqual(["nod_f"]);
  });

  it("takes the free form only for n from 1 to 1000, the range the server counts", () => {
    const at = "2026-11-08T10:00:05Z";
    for (const name of ["letter (1).docx", "letter (9).docx", "letter (10).docx", "letter (100).docx", "letter (1000).docx"]) {
      expect(copyCandidates([made("nod", name, at)], "letter.docx", REQUEST, NONE).length, name).toBe(1);
    }
    for (const name of ["letter (0).docx", "letter (01).docx", "letter (1001).docx", "letter (9999).docx"]) {
      expect(copyCandidates([made("nod", name, at)], "letter.docx", REQUEST, NONE), name).toEqual([]);
    }
  });

  it("splits off an extension only when it is 32 bytes or less, with its dot, as the server does", () => {
    const at = "2026-11-08T10:00:05Z";
    const longExt = "." + "x".repeat(32); // 33 bytes with the dot: no extension, so the free form is the whole name plus " (1)"
    const name = "a" + longExt;
    expect(copyCandidates([made("nod_l", `${name} (1)`, at)], name, REQUEST, NONE).map((n) => n.id)).toEqual(["nod_l"]);
    expect(copyCandidates([made("nod_w", `a (1)${longExt}`, at)], name, REQUEST, NONE)).toEqual([]);
    const okExt = "." + "x".repeat(31); // 32 bytes: split
    expect(copyCandidates([made("nod_s", `a (1)${okExt}`, at)], "a" + okExt, REQUEST, NONE).map((n) => n.id)).toEqual(["nod_s"]);
  });

  it("fails closed on a free form the server would cut to fit 255 bytes, and still takes the exact name", () => {
    const at = "2026-11-08T10:00:05Z";
    const req = "b".repeat(245) + ".docx"; // "b...b (1000).docx" would be 257 bytes, so the server cuts the base
    const items = [made("nod_e", req, at), made("nod_f", "b".repeat(245) + " (1).docx", at)];
    expect(copyCandidates(items, req, REQUEST, NONE).map((n) => n.id)).toEqual(["nod_e"]);
  });

  it("refuses a file that was in the folder when the Save As was asked", () => {
    const at = "2026-11-08T10:00:05Z";
    const before = new Set(["nod_old"]);
    const items = [made("nod_old", "letter (1).docx", at), made("nod_new", "letter (2).docx", at)];
    expect(copyCandidates(items, "letter.docx", REQUEST, before).map((n) => n.id)).toEqual(["nod_new"]);
  });

  it("reads a creation time with more than three fraction digits, and refuses one that is not a time", () => {
    const items = [made("nod_p", "letter.docx", "2026-11-08T10:00:05.123456789Z"), made("nod_q", "letter.docx", "not a time")];
    expect(copyCandidates(items, "letter.docx", REQUEST, NONE).map((n) => n.id)).toEqual(["nod_p"]);
  });

  it("allows exactly thirty seconds of clock skew, not a millisecond more", () => {
    expect(copyCandidates([made("nod_b", "letter.docx", "2026-11-08T09:59:30.000Z")], "letter.docx", REQUEST, NONE).length).toBe(1);
    expect(copyCandidates([made("nod_c", "letter.docx", "2026-11-08T09:59:29.999Z")], "letter.docx", REQUEST, NONE)).toEqual([]);
  });
});
