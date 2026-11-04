// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { CHIPS, freeUpCards, kindOfChip, meterSegments, type Breakdown } from "./breakdown";
import type { MeStorage } from "./logic";

const MB = 1_000_000;
const b = (over: Partial<Breakdown> = {}): Breakdown => ({ versions_bytes: 0, versions_files: 0, trash_bytes: 0, trash_files: 0, ...over });
const me = (over: Partial<MeStorage> = {}): MeStorage => ({ used_bytes: 0, reserved_bytes: 0, quota_bytes: null, available_bytes: null, ...over });

// The meter's parts, as the Storage page draws them: files, old versions, the trash, the reservations and what is free.
// The parts add up to what the meter holds (used plus reserved), so the bar and its legend never disagree with the figure.
describe("meterSegments (decision 79, one meter)", () => {
  it("splits the used bytes into files, old versions and the trash, and leaves the rest free against the limit", () => {
    const s = meterSegments(me({ used_bytes: 153 * MB, quota_bytes: 1000 * MB, available_bytes: 847 * MB }),
      b({ versions_bytes: 4 * MB, versions_files: 6, trash_bytes: 8 * MB, trash_files: 3 }));
    expect(s).toEqual({ files: 141 * MB, versions: 4 * MB, trash: 8 * MB, reserved: 0, free: 847 * MB });
  });

  it("gives reserved bytes their own part, and takes them out of the free part", () => {
    const s = meterSegments(me({ used_bytes: 100 * MB, reserved_bytes: 20 * MB, quota_bytes: 200 * MB }), b());
    expect(s).toEqual({ files: 100 * MB, versions: 0, trash: 0, reserved: 20 * MB, free: 80 * MB });
  });

  it("has no free part when there is no limit", () => {
    const s = meterSegments(me({ used_bytes: 153 * MB }), b({ versions_bytes: 4 * MB, trash_bytes: 8 * MB }));
    expect(s.free).toBeNull();
    expect(s.files).toBe(141 * MB);
  });

  it("never gives a negative part, even when the figures disagree", () => {
    const s = meterSegments(me({ used_bytes: 5 * MB, quota_bytes: 10 * MB }), b({ versions_bytes: 4 * MB, trash_bytes: 4 * MB }));
    expect(s.files).toBe(0);
  });

  it("the parts add up to the held bytes, with the free part only when there is a limit", () => {
    const s = meterSegments(me({ used_bytes: 90 * MB, reserved_bytes: 10 * MB, quota_bytes: 500 * MB }),
      b({ versions_bytes: 30 * MB, trash_bytes: 10 * MB }));
    expect(s.files + s.versions + s.trash + s.reserved).toBe(100 * MB);
    expect((s.free ?? 0) + s.files + s.versions + s.trash + s.reserved).toBe(500 * MB);
  });
});

// "Free up space": a card is shown only when it has something to free.
describe("freeUpCards (decision 79, a card with nothing to free is not shown)", () => {
  it("shows neither card when the trash and the old versions are empty", () => {
    expect(freeUpCards(b())).toEqual({ trash: false, versions: false });
  });

  it("shows the trash card alone when only the trash has files", () => {
    expect(freeUpCards(b({ trash_bytes: 8 * MB, trash_files: 3 }))).toEqual({ trash: true, versions: false });
  });

  it("shows the old versions card alone when only the versions have bytes", () => {
    expect(freeUpCards(b({ versions_bytes: 4 * MB, versions_files: 6 }))).toEqual({ trash: false, versions: true });
  });
});

// The filter chips of "Largest files", and the kind each one asks the server for.
describe("kindOfChip (decision 79, the chips filter the largest files)", () => {
  it("lists the six chips in the mock-up's order", () => {
    expect(CHIPS.map((c) => c.id)).toEqual(["all", "audio", "video", "pictures", "documents", "trash"]);
  });

  it("asks the server for the matching kind, and for every file under All files", () => {
    expect(kindOfChip("all")).toBe("");
    expect(kindOfChip("audio")).toBe("audio");
    expect(kindOfChip("video")).toBe("video");
    expect(kindOfChip("pictures")).toBe("image");
    expect(kindOfChip("documents")).toBe("document");
    expect(kindOfChip("trash")).toBe("trash");
  });
});
