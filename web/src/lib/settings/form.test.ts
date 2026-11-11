// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { ApiError } from "../apiError";
import { draftOf, isChanged, messagesFor, refusal, sectionChange, type Edits } from "./form";
import type { Setting } from "./api";

function setting(over: Partial<Setting> & { key: string; type: Setting["type"]; value: Setting["value"] }): Setting {
  return { default: over.value, source: "default", locked: false, restart: false, ...over };
}

const piece = setting({ key: "upload.max_chunk_size", type: "bytes", value: "64MiB", default: "64MiB" });
const retention = setting({ key: "trash.retention", type: "duration", value: "720h0m0s", default: "720h0m0s" });
const count = setting({ key: "versions.max_count", type: "int", value: 100, default: 100 });
const search = setting({ key: "search.enabled", type: "bool", value: true, default: true });
const stored = setting({ key: "links.max_ttl", type: "duration", value: "12h0m0s", default: "24h0m0s", source: "settings" });
const locked = setting({ key: "upload.max_file_size", type: "bytes", value: "50GiB", default: "100GiB", source: "file", locked: true });
const all = [piece, retention, count, search, stored, locked];

describe("what a person has changed", () => {
  it("is nothing for a field that was not touched", () => {
    expect(isChanged(piece, undefined)).toBe(false);
  });

  it("is nothing when the same size is written in the other unit", () => {
    expect(isChanged(piece, { value: { value: 0.0625, unit: "GB" }, reset: false })).toBe(false);
    expect(isChanged(retention, { value: { value: 720, unit: "hours" }, reset: false })).toBe(false);
    expect(isChanged(retention, { value: { value: 30, unit: "days" }, reset: false })).toBe(false);
  });

  it("is a different size, duration, count or switch", () => {
    expect(isChanged(piece, { value: { value: 100, unit: "MB" }, reset: false })).toBe(true);
    expect(isChanged(retention, { value: { value: 31, unit: "days" }, reset: false })).toBe(true);
    expect(isChanged(count, { value: 50, reset: false })).toBe(true);
    expect(isChanged(search, { value: false, reset: false })).toBe(true);
  });

  it("counts a reset only where a value is stored", () => {
    expect(isChanged(stored, { value: draftOf(stored), reset: true })).toBe(true);
    expect(isChanged(piece, { value: draftOf(piece), reset: true })).toBe(false);
  });

  it("never counts a locked field", () => {
    expect(isChanged(locked, { value: { value: 1, unit: "GB" }, reset: false })).toBe(false);
  });
});

describe("what one section sends", () => {
  it("sends only the changed keys of the section, in the API's string form", () => {
    const edits: Edits = {
      "upload.max_chunk_size": { value: { value: 100, unit: "MB" }, reset: false },
      "trash.retention": { value: { value: 30, unit: "days" }, reset: false },
      "search.enabled": { value: false, reset: false },
    };
    const keys = ["upload.max_chunk_size", "trash.retention", "versions.max_count"];
    expect(sectionChange(all, keys, edits)).toEqual({ set: { "upload.max_chunk_size": "100MiB" }, reset: [], incomplete: [] });
  });

  it("sends a count as a number and a switch as a boolean", () => {
    const edits: Edits = { "versions.max_count": { value: 50, reset: false }, "search.enabled": { value: false, reset: false } };
    expect(sectionChange(all, ["versions.max_count", "search.enabled"], edits).set).toEqual({ "versions.max_count": 50, "search.enabled": false });
  });

  it("sends a reset for a stored key and no value with it", () => {
    const edits: Edits = { "links.max_ttl": { value: draftOf(stored), reset: true } };
    expect(sectionChange(all, ["links.max_ttl"], edits)).toEqual({ set: {}, reset: ["links.max_ttl"], incomplete: [] });
  });

  it("names a field whose box was emptied, and does not send it", () => {
    const edits: Edits = { "versions.max_count": { value: null, reset: false }, "trash.retention": { value: { value: null, unit: "days" }, reset: false } };
    const got = sectionChange(all, ["versions.max_count", "trash.retention"], edits);
    expect(got.set).toEqual({});
    expect(got.incomplete).toEqual(["versions.max_count", "trash.retention"]);
  });

  it("leaves out a locked key even when its box was edited", () => {
    const edits: Edits = { "upload.max_file_size": { value: { value: 1, unit: "GB" }, reset: false } };
    expect(sectionChange(all, ["upload.max_file_size"], edits)).toEqual({ set: {}, reset: [], incomplete: [] });
  });
});

describe("a refused change", () => {
  const e = (status: number, code: string, details: Record<string, unknown>) => new ApiError(status, code, details, 0);

  it("marks every refused key with the messages that name it", () => {
    const err = e(400, "invalid_request", {
      reason: "invalid_setting",
      keys: ["links.default_ttl", "links.max_ttl"],
      errors: ["config: links.default_ttl and links.max_ttl must be positive, and default_ttl at most max_ttl", "config: trash.retention: too small"],
    });
    const got = refusal(err);
    expect(got.kind).toBe("invalid");
    if (got.kind !== "invalid") return;
    expect(Object.keys(got.fields).sort()).toEqual(["links.default_ttl", "links.max_ttl"]);
    expect(got.fields["links.max_ttl"]).toContain("default_ttl at most max_ttl");
  });

  it("gives a key no message of its own a general one, so it is still marked", () => {
    const got = refusal(e(400, "invalid_request", { reason: "invalid_setting", keys: ["trash.retention"], errors: ["something else"] }));
    expect(got).toEqual({ kind: "invalid", fields: { "trash.retention": null }, unmatched: ["something else"] });
  });

  it("keeps a message that names no key, and a refusal that names no key at all", () => {
    expect(refusal(e(400, "invalid_request", { reason: "invalid_setting", keys: [], errors: ["a rule broke"] }))).toEqual({
      kind: "invalid",
      fields: {},
      unmatched: ["a rule broke"],
    });
    const got = refusal(e(400, "invalid_request", { reason: "invalid_setting", keys: ["trash.retention"], errors: ["trash.retention is low", "a rule broke"] }));
    expect(got).toEqual({ kind: "invalid", fields: { "trash.retention": "trash.retention is low" }, unmatched: ["a rule broke"] });
  });

  it("names the keys a 409 says are locked", () => {
    expect(refusal(e(409, "conflict", { reason: "setting_locked", keys: ["upload.max_file_size"] }))).toEqual({
      kind: "locked",
      keys: ["upload.max_file_size"],
    });
  });

  it("passes any other error through", () => {
    const err = e(500, "internal", {});
    expect(refusal(err)).toEqual({ kind: "other", error: err });
    expect(refusal(new Error("x")).kind).toBe("other");
  });

  it("matches a key as a whole word", () => {
    expect(messagesFor("sessions.idle_timeout", ["config: sessions.admin_idle_timeout is too long", "sessions.idle_timeout must be at most 30 days"])).toEqual([
      "sessions.idle_timeout must be at most 30 days",
    ]);
    expect(messagesFor("sessions.max_age", ["sessions.max_age_extra is odd"])).toEqual([]);
  });
});
