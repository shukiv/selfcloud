// SPDX-License-Identifier: AGPL-3.0-or-later

// The Storage page's breakdown (decision 79): what My files holds, split into files, old versions and the trash, and the
// rules of the "Free up space" cards and of the filter chips. The server sends the old versions and the trash as totals
// (GET /me?storage=breakdown); the rest is worked out here. Pure functions, so they are tested on their own.
import type { MeStorage } from "./logic";

// Breakdown is the old versions and the trash of My files, as GET /me?storage=breakdown gives them.
export interface Breakdown {
  versions_bytes: number;
  versions_files: number;
  trash_bytes: number;
  trash_files: number;
}

// Segments are the parts of the meter, in bytes. free is null when there is no limit (the bar has no free part then).
export interface Segments {
  files: number;
  versions: number;
  trash: number;
  reserved: number;
  free: number | null;
}

// meterSegments splits what the meter holds (used plus reserved) into its parts. The used bytes include the old versions
// and the trash, so the files are what is left of the used bytes; a figure that disagrees gives 0, never a negative part.
export function meterSegments(s: MeStorage, b: Breakdown): Segments {
  const used = Math.max(0, s.used_bytes);
  const reserved = Math.max(0, s.reserved_bytes);
  const versions = Math.max(0, b.versions_bytes);
  const trash = Math.max(0, b.trash_bytes);
  const files = Math.max(0, used - versions - trash);
  const free = s.quota_bytes === null || s.quota_bytes <= 0 ? null : Math.max(0, s.quota_bytes - (used + reserved));
  return { files, versions, trash, reserved, free };
}

// freeUpCards says which "Free up space" cards to show: a card with nothing to free is not shown.
export function freeUpCards(b: Breakdown): { trash: boolean; versions: boolean } {
  return { trash: b.trash_files > 0 || b.trash_bytes > 0, versions: b.versions_files > 0 || b.versions_bytes > 0 };
}

// The filter chips of "Largest files", in the mock-up's order. kind is the server's ?kind= value ("" is every file).
export const CHIPS = [
  { id: "all", kind: "" },
  { id: "audio", kind: "audio" },
  { id: "video", kind: "video" },
  { id: "pictures", kind: "image" },
  { id: "documents", kind: "document" },
  { id: "trash", kind: "trash" },
] as const;

export type ChipId = (typeof CHIPS)[number]["id"];

// kindOfChip is the server's ?kind= value for a chip.
export function kindOfChip(id: ChipId): string {
  return CHIPS.find((c) => c.id === id)?.kind ?? "";
}
