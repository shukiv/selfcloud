// SPDX-License-Identifier: AGPL-3.0-or-later

import { baseType, kindOf } from './kind';
import type { Node } from './types';

// The colour of a file's square and the glyph inside it (decision 68, the owner's model: "coloured by type"). Each colour is
// at least 3:1 against the page, the card and the selected fill of both modes, and against the tile's preview on those
// (iconType.test.ts), and the white glyph on it is at least 3:1. They sit at one luminance, about 0.236: a darker square
// fails 3:1 on the dark selected tile's preview, and a lighter one fails 3:1 on the light selected tile's preview
// (review fix, decision 68).
export const TYPE_COLOURS = {
  red: '#E15950',
  blue: '#3785EB',
  green: '#3F955A',
  orange: '#C76F14',
  grey: '#81868B',
} as const;
export type TypeColour = keyof typeof TYPE_COLOURS;

// The lucide glyphs a type can show. Icon maps each name to its component.
export type Glyph = 'folder' | 'file' | 'image' | 'pdf' | 'video' | 'audio' | 'document' | 'spreadsheet' | 'presentation' | 'archive';

export type FileType = { colour: TypeColour; glyph: Glyph };

const WORD = ['msword', 'wordprocessingml', 'opendocument.text'];
const SHEET = ['ms-excel', 'spreadsheetml', 'opendocument.spreadsheet', 'text/csv'];
const SLIDES = ['ms-powerpoint', 'presentationml', 'opendocument.presentation'];

// By extension. A file whose media type is generic (application/octet-stream, or none) takes its type from the extension, in
// any letter case; so does a file whose type is not one the interface knows.
const RED_PDF: FileType = { colour: 'red', glyph: 'pdf' };
const RED_IMAGE: FileType = { colour: 'red', glyph: 'image' };
const RED_AUDIO: FileType = { colour: 'red', glyph: 'audio' };
const RED_VIDEO: FileType = { colour: 'red', glyph: 'video' };
const BLUE_DOC: FileType = { colour: 'blue', glyph: 'document' };
const GREEN_SHEET: FileType = { colour: 'green', glyph: 'spreadsheet' };
const ORANGE_SLIDES: FileType = { colour: 'orange', glyph: 'presentation' };
const GREY_ARCHIVE: FileType = { colour: 'grey', glyph: 'archive' };
const EXTENSION: Record<string, FileType> = {
  pdf: RED_PDF,
  png: RED_IMAGE,
  jpg: RED_IMAGE,
  jpeg: RED_IMAGE,
  gif: RED_IMAGE,
  webp: RED_IMAGE,
  avif: RED_IMAGE,
  svg: RED_IMAGE,
  bmp: RED_IMAGE,
  heic: RED_IMAGE,
  mp3: RED_AUDIO,
  m4a: RED_AUDIO,
  aac: RED_AUDIO,
  ogg: RED_AUDIO,
  oga: RED_AUDIO,
  opus: RED_AUDIO,
  wav: RED_AUDIO,
  flac: RED_AUDIO,
  mp4: RED_VIDEO,
  m4v: RED_VIDEO,
  webm: RED_VIDEO,
  mov: RED_VIDEO,
  mkv: RED_VIDEO,
  avi: RED_VIDEO,
  doc: BLUE_DOC,
  docx: BLUE_DOC,
  odt: BLUE_DOC,
  txt: BLUE_DOC,
  md: BLUE_DOC,
  rtf: BLUE_DOC,
  xls: GREEN_SHEET,
  xlsx: GREEN_SHEET,
  ods: GREEN_SHEET,
  csv: GREEN_SHEET,
  tsv: GREEN_SHEET,
  ppt: ORANGE_SLIDES,
  pptx: ORANGE_SLIDES,
  odp: ORANGE_SLIDES,
  zip: GREY_ARCHIVE,
  tar: GREY_ARCHIVE,
  gz: GREY_ARCHIVE,
  '7z': GREY_ARCHIVE,
  rar: GREY_ARCHIVE,
};

// extensionOf is the file name's extension in lower case, or '' when it has none.
function extensionOf(name: string | undefined): string {
  const n = name ?? '';
  const dot = n.lastIndexOf('.');
  return dot >= 0 ? n.slice(dot + 1).toLowerCase() : '';
}

// fileTypeOf says which square a node has: its colour and its glyph. A folder is grey. The kind is read from the media type
// first (kind.ts); an office or unknown file with a known extension takes the extension's type. A shortcut is not a file here:
// it keeps its own outline icon, so it is grey.
export function fileTypeOf(node: Pick<Node, 'kind' | 'mime_type' | 'name'>): FileType {
  if (node.kind === 'dir') return { colour: 'grey', glyph: 'folder' };
  const kind = kindOf(node);
  if (kind === 'shortcut') return { colour: 'grey', glyph: 'file' };
  const ext = extensionOf(node.name);
  const has = (e: string) => Object.prototype.hasOwnProperty.call(EXTENSION, e);
  const mt = baseType(node.mime_type);
  if ((mt === '' || mt === 'application/octet-stream') && has(ext)) return EXTENSION[ext];
  if (kind === 'image') return { colour: 'red', glyph: 'image' };
  if (kind === 'pdf') return { colour: 'red', glyph: 'pdf' };
  if (kind === 'video') return { colour: 'red', glyph: 'video' };
  if (kind === 'audio') return { colour: 'red', glyph: 'audio' };
  if (kind === 'text') return mt === 'text/csv' || mt === 'text/tab-separated-values' || ext === 'csv' || ext === 'tsv' ? GREEN_SHEET : BLUE_DOC;
  if (kind === 'office') {
    if (WORD.some((w) => mt.includes(w))) return BLUE_DOC;
    if (SHEET.some((s) => mt.includes(s))) return GREEN_SHEET;
    if (SLIDES.some((s) => mt.includes(s))) return ORANGE_SLIDES;
  }
  if (kind === 'archive') return GREY_ARCHIVE;
  if (has(ext)) return EXTENSION[ext];
  return { colour: 'grey', glyph: 'file' };
}
