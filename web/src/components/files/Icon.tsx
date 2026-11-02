// SPDX-License-Identifier: AGPL-3.0-or-later

import { File, FileArchive, FileAudio, FileImage, FileText, FileVideo, Folder, FileSpreadsheet, Presentation, SquareArrowOutUpRight, type LucideIcon } from "lucide-react";
import type { Kind } from "../../lib/files/kind";
import { colourVar, isColour } from "../../lib/files/palette";
import { fileTypeOf, TYPE_COLOURS, type Glyph } from "../../lib/files/iconType";
import type { Node } from "../../lib/files/types";

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

// GLYPHS are the glyphs a coloured square carries (decision 68).
const GLYPHS: Record<Glyph, LucideIcon> = {
  folder: Folder,
  file: File,
  image: FileImage,
  pdf: FileText,
  video: FileVideo,
  audio: FileAudio,
  document: FileText,
  spreadsheet: FileSpreadsheet,
  presentation: Presentation,
  archive: FileArchive,
};

// Icon draws a node's kind. Without node it is the outline icon of the kind, in the text colour (the sidebar, Details, the
// trash). With node it is coloured by type, as the owner asked (decision 68): a folder is a solid grey folder (or the palette
// colour it has), and a file is a small rounded square in its type's colour with the white glyph inside.
export default function Icon({ kind, size = 24, colour, node }: { kind: Kind; size?: number; colour?: string; node?: Pick<Node, "kind" | "mime_type" | "name"> }) {
  if (node && kind !== "shortcut") {
    const type = fileTypeOf(node);
    if (type.glyph === "folder") {
      const tint = isColour(colour) ? colourVar(colour) : TYPE_COLOURS.grey;
      return <Folder size={size} strokeWidth={1.6} fill="currentColor" aria-hidden="true" focusable="false" className={`icon kind-${kind}`} style={{ color: tint }} />;
    }
    const Glyph = GLYPHS[type.glyph];
    return (
      <span
        aria-hidden="true"
        className={`icon kind-${kind} icon-tile`}
        style={{
          display: "inline-flex",
          flex: "none",
          alignItems: "center",
          justifyContent: "center",
          width: size,
          height: size,
          borderRadius: Math.max(3, Math.round(size / 6)),
          background: TYPE_COLOURS[type.colour],
          color: "#FFFFFF",
        }}
      >
        <Glyph size={Math.round(size * 0.6)} strokeWidth={2} aria-hidden="true" focusable="false" />
      </span>
    );
  }
  const Cmp = ICONS[kind];
  const tint = isColour(colour) ? { color: colourVar(colour) } : undefined;
  return (
    <Cmp size={size} strokeWidth={1.6} aria-hidden="true" focusable="false" className={`icon kind-${kind}`} style={tint} />
  );
}
