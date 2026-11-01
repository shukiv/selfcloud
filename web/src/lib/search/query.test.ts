// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import {
  resolveLabels,
  MAX_LABELS,
  MAX_QUERY_BYTES,
  MAX_TERM_BYTES,
  labelValueProblem,
  paramsFromState,
  queryProblem,
  searchParamsFor,
  stateFromParams,
  emptyState,
  type SearchState,
} from "./query";

const base = (over: Partial<SearchState> = {}): SearchState => ({ ...emptyState(), ...over });

describe("the address is the search state", () => {
  it("reads a full address and writes the same one back", () => {
    const p = new URLSearchParams(
      "q=budget+2026&kind=file&ext=PDF,.docx&modified=30&size=large&label=fld_abc1:Acme&label=fld_abc2:5..10&in=name",
    );
    const s = stateFromParams(p);
    expect(s).toEqual({
      q: "budget 2026",
      inText: false,
      kind: "file",
      exts: ["pdf", "docx"],
      modified: "30",
      size: "large",
      labels: [
        { field: "fld_abc1", value: "Acme" },
        { field: "fld_abc2", value: "5..10" },
      ],
    });
    const again = stateFromParams(paramsFromState(s));
    expect(again).toEqual(s);
  });

  it("a text search has no kind: a kind in the address is dropped, so no chip shows a filter that is not applied", () => {
    const s = stateFromParams(new URLSearchParams("in=content&kind=dir&q=ab"));
    expect(s.inText).toBe(true);
    expect(s.kind).toBe("");
    expect(paramsFromState(s).has("kind")).toBe(false);
  });

  it("drops what it does not recognise, so a hand-typed address never breaks the page", () => {
    const s = stateFromParams(
      new URLSearchParams("kind=folderz&modified=3&size=huge&ext=../etc,pdf,&label=bad&label=fld_x:&in=content"),
    );
    expect(s.kind).toBe("");
    expect(s.modified).toBe("");
    expect(s.size).toBe("");
    expect(s.exts).toEqual(["pdf"]);
    expect(s.labels).toEqual([]);
    expect(s.inText).toBe(true);
  });

  it("keeps at most 4 label filters and 8 extensions", () => {
    const labels = Array.from({ length: 6 }, (_, i) => `label=fld_a${i}b:v${i}`).join("&");
    const exts = Array.from({ length: 10 }, (_, i) => `e${i}`).join(",");
    const s = stateFromParams(new URLSearchParams(`${labels}&ext=${exts}`));
    expect(s.labels).toHaveLength(MAX_LABELS);
    expect(s.exts).toHaveLength(8);
  });

  it("writes only what is set", () => {
    expect(paramsFromState(base()).toString()).toBe("");
    expect(paramsFromState(base({ q: "a b" })).toString()).toBe("q=a+b");
  });
});

describe("queryProblem: the checks the server would make, before the request", () => {
  it("accepts a query of words of two characters or more", () => {
    expect(queryProblem(base({ q: "budget 2026" }))).toBeNull();
    expect(queryProblem(base({ q: "  " }))).toBeNull();
    expect(queryProblem(base({ q: "דוח שנתי" }))).toBeNull();
  });

  it("refuses a one-character word, which the server refuses too", () => {
    expect(queryProblem(base({ q: "a" }))).toBe("words");
    expect(queryProblem(base({ q: "budget a" }))).toBe("words");
    expect(queryProblem(base({ q: "ד" }))).toBe("words");
  });

  it("counts a query's bytes, not its characters: Hebrew is two bytes a letter", () => {
    // Eight words of twelve letters are 8 x 24 bytes, plus seven spaces: 199 bytes, inside the 200-byte limit.
    const fits = Array.from({ length: 8 }, () => "א".repeat(12)).join(" ");
    expect(new TextEncoder().encode(fits).length).toBe(199);
    expect(queryProblem(base({ q: fits }))).toBeNull();
    // Thirteen letters a word is 8 x 26 bytes plus seven spaces: 215 bytes. Its characters (111) would fit; its bytes do not.
    const over = Array.from({ length: 8 }, () => "א".repeat(13)).join(" ");
    expect(over.length).toBeLessThan(MAX_QUERY_BYTES);
    expect(queryProblem(base({ q: over }))).toBe("tooLong");
  });

  it("refuses more than 8 words and a word over 64 bytes", () => {
    expect(queryProblem(base({ q: "aa bb cc dd ee ff gg hh" }))).toBeNull();
    expect(queryProblem(base({ q: "aa bb cc dd ee ff gg hh ii" }))).toBe("terms");
    expect(queryProblem(base({ q: "a".repeat(MAX_TERM_BYTES) }))).toBeNull();
    expect(queryProblem(base({ q: "a".repeat(MAX_TERM_BYTES + 1) }))).toBe("tooLong");
  });

  it("asks for words in a text search", () => {
    expect(queryProblem(base({ inText: true }))).toBe("needWords");
    expect(queryProblem(base({ inText: true, q: "contract" }))).toBeNull();
  });
});

describe("resolveLabels: only the label filters the account has", () => {
  const fields = [
    { id: "fld_cl1", type: "text", choices: [] },
    { id: "fld_st1", type: "choice", choices: [{ id: "chc_open", archived: false }, { id: "chc_old", archived: true }] },
    { id: "fld_us1", type: "user", choices: [] },
  ];
  const l = (field: string, value: string) => ({ field, value });

  it("keeps a text label of a known field, and a live choice", () => {
    expect(resolveLabels([l("fld_cl1", "Acme"), l("fld_st1", "chc_open")], fields, false)).toEqual([
      l("fld_cl1", "Acme"),
      l("fld_st1", "chc_open"),
    ]);
  });

  it("drops an unknown field, a user field, a choice ID that is not a choice, and an archived choice", () => {
    expect(resolveLabels([l("fld_zz9", "x"), l("fld_us1", "usr_a"), l("fld_st1", "cho_xyz"), l("fld_st1", "chc_old")], fields, false)).toEqual([]);
  });

  it("while the fields are not read, keeps them as they are, so they can be checked", () => {
    expect(resolveLabels([l("fld_zz9", "x")], null, false)).toEqual([l("fld_zz9", "x")]);
  });

  it("when the fields could not be read, drops every label, since none can be checked", () => {
    expect(resolveLabels([l("fld_cl1", "Acme")], null, true)).toEqual([]);
  });
});

describe("labelValueProblem: the value by its field's type", () => {
  it("checks text, number, date and choice values", () => {
    expect(labelValueProblem("text", "Acme")).toBeNull();
    expect(labelValueProblem("text", "")).toBe("labelValue");
    expect(labelValueProblem("number", "5")).toBeNull();
    expect(labelValueProblem("number", "5..10")).toBeNull();
    expect(labelValueProblem("number", "-2.5")).toBeNull();
    expect(labelValueProblem("number", "five")).toBe("labelValue");
    expect(labelValueProblem("date", "2026-01-31")).toBeNull();
    expect(labelValueProblem("date", "2026-01-01..2026-01-31")).toBeNull();
    expect(labelValueProblem("date", "31/01/2026")).toBe("labelValue");
    expect(labelValueProblem("choice", "chc_1")).toBeNull();
    expect(labelValueProblem("choice", "")).toBe("labelValue");
  });
});

describe("searchParamsFor: what one request asks", () => {
  const now = 1_800_000_000_000;

  it("sends the words, the limit and the filters the server reads", () => {
    const p = searchParamsFor(
      base({ q: " budget ", kind: "dir", exts: ["pdf", "docx"], modified: "7", size: "medium", labels: [{ field: "fld_a1", value: "x" }] }),
      now,
    );
    expect(p.get("q")).toBe("budget");
    expect(p.get("kind")).toBe("dir");
    expect(p.get("ext")).toBe("pdf,docx");
    expect(p.get("modified_after")).toBe(String(now - 7 * 86_400_000));
    expect(p.get("size_min")).toBe(String(1_048_576));
    expect(p.get("size_max")).toBe(String(100 * 1_048_576 - 1));
    expect(p.getAll("label")).toEqual(["fld_a1:x"]);
    expect(p.get("limit")).toBe("25");
    expect(p.has("cursor")).toBe(false);
  });

  it("a text search sends in=content and leaves the kind out, since it finds files only", () => {
    const p = searchParamsFor(base({ q: "contract", inText: true, kind: "dir" }), now);
    expect(p.get("in")).toBe("content");
    expect(p.has("kind")).toBe(false);
  });

  it("the size bands are whole megabytes, and the large band has no top", () => {
    expect(searchParamsFor(base({ size: "small" }), now).get("size_max")).toBe(String(1_048_575));
    const large = searchParamsFor(base({ size: "large" }), now);
    expect(large.get("size_min")).toBe(String(100 * 1_048_576));
    expect(large.has("size_max")).toBe(false);
  });
});
