// SPDX-License-Identifier: AGPL-3.0-or-later

import { File, FileArchive, FileAudio, FileImage, FileText, FileVideo, Folder, FileSpreadsheet } from "lucide-react";
import type { Kind } from "../../lib/files/kind";

// Icon draws the kind of a node. It is decorative: the name beside it says what it is.
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
} as const;

export default function Icon({ kind, size = 24 }: { kind: Kind; size?: number }) {
  const Cmp = ICONS[kind];
  return <Cmp size={size} strokeWidth={1.6} aria-hidden="true" focusable="false" className={`icon kind-${kind}`} />;
}
