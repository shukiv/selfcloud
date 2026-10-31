// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Node } from './types';

// The kinds the interface shows an icon and a preview for.
export type Kind = 'folder' | 'image' | 'pdf' | 'video' | 'audio' | 'text' | 'office' | 'archive' | 'file' | 'shortcut';

const IMAGE = new Set(['image/jpeg', 'image/png', 'image/gif', 'image/webp', 'image/avif', 'image/svg+xml']);
const TEXT = new Set([
  'application/json',
  'application/xml',
  'application/javascript',
  'application/x-yaml',
  'application/yaml',
  'application/toml',
  'application/x-sh',
  'application/sql',
  'application/x-httpd-php',
]);
const OFFICE = new Set([
  'application/msword',
  'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  'application/vnd.ms-excel',
  'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
  'application/vnd.ms-powerpoint',
  'application/vnd.openxmlformats-officedocument.presentationml.presentation',
  'application/vnd.oasis.opendocument.text',
  'application/vnd.oasis.opendocument.spreadsheet',
  'application/vnd.oasis.opendocument.presentation',
]);
const ARCHIVE = new Set([
  'application/zip',
  'application/x-tar',
  'application/gzip',
  'application/x-7z-compressed',
  'application/x-rar-compressed',
]);

// baseType is a media type without its parameters, in lower case.
export function baseType(mime: string | undefined): string {
  return (mime ?? '').split(';')[0].trim().toLowerCase();
}

// kindOf classifies a node by its kind and media type.
export function kindOf(node: Pick<Node, 'kind' | 'mime_type'>): Kind {
  if (node.kind === 'dir') return 'folder';
  // A shortcut has its own icon whatever its target is: an unavailable target must not show what it is.
  if (node.kind === 'shortcut') return 'shortcut';
  const mt = baseType(node.mime_type);
  if (IMAGE.has(mt)) return 'image';
  if (mt === 'application/pdf') return 'pdf';
  if (mt.startsWith('video/')) return 'video';
  if (mt.startsWith('audio/')) return 'audio';
  if (mt.startsWith('text/') || TEXT.has(mt)) return 'text';
  if (OFFICE.has(mt)) return 'office';
  if (ARCHIVE.has(mt)) return 'archive';
  return 'file';
}

// previewable is true for the kinds the viewer shows inline. Office files and the rest get a card.
export function previewable(node: Pick<Node, 'kind' | 'mime_type'>): boolean {
  const k = kindOf(node);
  return k === 'image' || k === 'pdf' || k === 'video' || k === 'audio' || k === 'text';
}

// The largest text the viewer reads: the first 1 MiB.
export const TEXT_PREVIEW_BYTES = 1024 * 1024;
