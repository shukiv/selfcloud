// SPDX-License-Identifier: AGPL-3.0-or-later

import { File, FileArchive, FileAudio, FileImage, FileText, FileVideo, Folder, FileSpreadsheet, SquareArrowOutUpRight } from "lucide-react";
import type { Kind } from "../../lib/files/kind";
import { colourVar, isColour } from "../../lib/files/palette";

// Icon draws the kind of a node. It is decorative: the name beside it says what it is. A folder with a palette colour
// is tinted with it; the colour's name is said in text beside the row, for screen readers, by the listing.
const ICONS = {
  folder: Folder,
  file: File,
  image: FileImage,
  pdf: FileText,
  video: FileVideo,
  audio: FileAudio,
  text: FileText,
  office: FileSpreadsheet,
  archive: FileArchive,
  shortcut: SquareArrowOutUpRight,
} as const;

export default function Icon({ kind, size = 24, colour }: { kind: Kind; size?: number; colour?: string }) {
  const Cmp = ICONS[kind];
  const tint = isColour(colour) ? { color: colourVar(colour) } : undefined;
  return (
    <Cmp size={size} strokeWidth={1.6} aria-hidden="true" focusable="false" className={`icon kind-${kind}`} style={tint} />
  );
}
