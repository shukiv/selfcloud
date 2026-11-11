// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { durationSeconds, durationText, sizeBytes, sizeText, splitDuration, splitSize } from "./units";

// The settings API writes a size as the server's normal form ("64MiB", "100GiB") and a duration as Go's ("168h0m0s"). The page
// shows them in a unit a person picks, and sends them back in the same string form.
describe("sizes", () => {
  it.each([
    ["64MiB", 64 * 1048576],
    ["100GiB", 100 * 1073741824],
    ["2TiB", 2 * 1099511627776],
    ["256KiB", 262144],
    ["95000000", 95000000],
    ["1.5GiB", 1610612736],
  ])("reads %s as %d bytes", (text, bytes) => {
    expect(sizeBytes(text)).toBe(bytes);
  });

  it("reads a whole number of bytes sent as a number", () => {
    expect(sizeBytes(1048576)).toBe(1048576);
  });

  it("shows a multiple of a gibibyte in GB and anything else in MB", () => {
    expect(splitSize("100GiB")).toEqual({ value: 100, unit: "GB" });
    expect(splitSize("2TiB")).toEqual({ value: 2048, unit: "GB" });
    expect(splitSize("64MiB")).toEqual({ value: 64, unit: "MB" });
    expect(splitSize("1536MiB")).toEqual({ value: 1536, unit: "MB" });
    expect(splitSize("64KiB")).toEqual({ value: 0.0625, unit: "MB" });
  });

  it("sends a size in the server's own units", () => {
    expect(sizeText({ value: 64, unit: "MB" })).toBe("64MiB");
    expect(sizeText({ value: 2, unit: "GB" })).toBe("2GiB");
    expect(sizeText({ value: 0.5, unit: "GB" })).toBe("0.5GiB");
  });

  it("sends nothing for an empty box", () => {
    expect(sizeText({ value: null, unit: "MB" })).toBeNull();
  });

  it("gives back the size it was shown, in either unit", () => {
    for (const text of ["64MiB", "100GiB", "1536MiB", "256MiB"]) {
      expect(sizeBytes(sizeText(splitSize(text))!)).toBe(sizeBytes(text));
    }
  });
});

describe("durations", () => {
  it.each([
    ["168h0m0s", 604800],
    ["5m0s", 300],
    ["24h0m0s", 86400],
    ["0s", 0],
    ["1h30m0s", 5400],
    ["720h0m0s", 2592000],
    ["90s", 90],
  ])("reads %s as %d seconds", (text, seconds) => {
    expect(durationSeconds(text)).toBe(seconds);
  });

  it("shows whole days in days, whole hours in hours, and the rest in minutes", () => {
    expect(splitDuration("168h0m0s")).toEqual({ value: 7, unit: "days" });
    expect(splitDuration("720h0m0s")).toEqual({ value: 30, unit: "days" });
    expect(splitDuration("12h0m0s")).toEqual({ value: 12, unit: "hours" });
    expect(splitDuration("72h0m0s")).toEqual({ value: 3, unit: "days" });
    expect(splitDuration("5m0s")).toEqual({ value: 5, unit: "minutes" });
    expect(splitDuration("1h30m0s")).toEqual({ value: 90, unit: "minutes" });
    expect(splitDuration("0s")).toEqual({ value: 0, unit: "days" });
  });

  it("sends a duration as hours, minutes or seconds, since the server has no day", () => {
    expect(durationText({ value: 7, unit: "days" })).toBe("168h");
    expect(durationText({ value: 12, unit: "hours" })).toBe("12h");
    expect(durationText({ value: 5, unit: "minutes" })).toBe("5m");
    expect(durationText({ value: 90, unit: "minutes" })).toBe("90m");
    expect(durationText({ value: 0, unit: "days" })).toBe("0h");
    expect(durationText({ value: 1.5, unit: "hours" })).toBe("90m");
    expect(durationText({ value: 0.5, unit: "minutes" })).toBe("30s");
  });

  it("sends nothing for an empty box", () => {
    expect(durationText({ value: null, unit: "hours" })).toBeNull();
  });

  it("gives back the duration it was shown", () => {
    for (const text of ["168h0m0s", "5m0s", "1h30m0s", "72h0m0s", "0s", "12h0m0s"]) {
      expect(durationSeconds(durationText(splitDuration(text))!)).toBe(durationSeconds(text));
    }
  });
});
