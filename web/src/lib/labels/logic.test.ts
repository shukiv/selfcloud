// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { ApiError } from "../apiError";
import type { LabelField, NodeLabel } from "./api";
import {
  choiceProblem,
  isChoice,
  isDate,
  isUser,
  labelFailure,
  nameProblem,
  parseNumber,
  showNumber,
  textProblem,
  visibleFields,
} from "./logic";

describe("parseNumber", () => {
  it("reads a decimal point and a decimal comma, and refuses what is not a finite number", () => {
    expect(parseNumber("3.5")).toBe(3.5);
    expect(parseNumber(" 3,5 ")).toBe(3.5);
    expect(parseNumber("-2")).toBe(-2);
    expect(parseNumber("")).toBeNull();
    expect(parseNumber("abc")).toBeNull();
    expect(parseNumber("1e999")).toBeNull();
  });
});

describe("isDate", () => {
  it("accepts a real YYYY-MM-DD date and nothing else", () => {
    expect(isDate("2026-02-03")).toBe(true);
    expect(isDate("2024-02-29")).toBe(true);
    expect(isDate("2026-02-30")).toBe(false);
    expect(isDate("2026-13-01")).toBe(false);
    expect(isDate("2026/02/03")).toBe(false);
    expect(isDate("2026-2-3")).toBe(false);
  });
});

describe("showNumber", () => {
  it("formats in the reader's locale", () => {
    expect(showNumber(1234.5, "en")).toBe("1,234.5");
    expect(showNumber(1234.5, "he")).not.toBe("");
  });
});

describe("name and text limits", () => {
  it("require a name and cap it at 255 characters", () => {
    expect(nameProblem("  ")).toBe("empty");
    expect(nameProblem("Status")).toBeNull();
    expect(nameProblem("x".repeat(256))).toBe("long");
  });

  it("require text and cap it at 500 characters, counted as characters", () => {
    expect(textProblem("   ")).toBe("empty");
    expect(textProblem("é".repeat(500))).toBeNull();
    expect(textProblem("é".repeat(501))).toBe("long");
  });
});

describe("choiceProblem", () => {
  it("needs a name for every choice, and no two with the same name", () => {
    expect(choiceProblem([{ name: "Open", archived: false }, { name: "Done", archived: false }])).toBeNull();
    expect(choiceProblem([{ name: " ", archived: false }])).toBe("empty");
    expect(choiceProblem([{ name: "Open", archived: false }, { name: " open ", archived: false }])).toBe("duplicate");
  });

  it("stops at the server's limit of 100 choices", () => {
    const many = Array.from({ length: 101 }, (_, i) => ({ name: `c${i}`, archived: false }));
    expect(choiceProblem(many)).toBe("limit");
  });
});

describe("value kinds", () => {
  it("tells a choice from a user by its shape", () => {
    expect(isChoice({ id: "lbc_1", name: "Open", archived: false })).toBe(true);
    expect(isUser({ id: "usr_1", name: "Ann" })).toBe(true);
    expect(isUser({ id: "lbc_1", name: "Open", archived: false })).toBe(false);
    expect(isChoice("text")).toBe(false);
  });
});

describe("visibleFields", () => {
  const fields: LabelField[] = [
    { id: "lbf_a", name: "Note", type: "text", restricted: false, choices: [] },
    { id: "lbf_b", name: "Lead", type: "user", restricted: true, choices: [] },
  ];

  it("keeps a restricted field only when the server sent a value for it", () => {
    const sent: NodeLabel[] = [{ field_id: "lbf_a", name: "Note", type: "text", restricted: false, value: "hi" }];
    const rows = visibleFields(fields, sent);
    expect(rows.map((r) => r.field_id)).toEqual(["lbf_a"]);
    const manager: NodeLabel[] = [...sent, { field_id: "lbf_b", name: "Lead", type: "user", restricted: true, value: null }];
    expect(visibleFields(fields, manager).map((r) => r.field_id)).toEqual(["lbf_a", "lbf_b"]);
  });
});

describe("labelFailure", () => {
  it("names the reasons the label pages show a sentence for, and nothing else", () => {
    expect(labelFailure(new ApiError(409, "conflict", { reason: "type_locked" }, 0))).toBe("type_locked");
    expect(labelFailure(new ApiError(403, "forbidden", { reason: "restricted_label" }, 0))).toBe("restricted_label");
    expect(labelFailure(new ApiError(409, "conflict", { reason: "last_owner" }, 0))).toBeNull();
    expect(labelFailure(new Error("network"))).toBeNull();
  });
});
