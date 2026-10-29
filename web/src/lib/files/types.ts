// SPDX-License-Identifier: AGPL-3.0-or-later

// Shapes of the file API the browser reads. Only the fields it uses are listed.

// Role is the caller's role on a node, lowest first.
export type Role = 'viewer' | 'commenter' | 'contributor' | 'editor' | 'manager';

export interface Node {
  id: string;
  folder_id: string;
  parent_id: string | null;
  name: string;
  kind: 'dir' | 'file';
  size: number;
  mime_type?: string;
  etag?: string;
  sha256?: string;
  created_at: string;
  modified_at: string;
  role?: Role;
  no_download: boolean;
  editors_can_share: boolean;
  created_by?: string;
  owner_id?: string;
}

export interface TrashItem extends Node {
  trashed_at: string;
  trashed_by: string;
  original_parent_id: string;
  purge_at: string;
}

export interface Version {
  id: string;
  current: boolean;
  size: number;
  mime_type?: string;
  etag: string;
  sha256?: string;
  created_at: string;
  created_by?: string;
  replaced_at?: string;
  replaced_by?: string;
  label: string | null;
  keep_forever: boolean;
}

export interface Page<T> {
  items: T[];
  next_cursor?: string;
}

export interface FolderAccess {
  id: string;
  name: string;
  kind: 'team' | 'personal';
  owner_user_id?: string;
  hidden?: boolean;
  access: 'read' | 'write';
  role: Role;
}

export interface Job {
  id: string;
  kind: string;
  target: string;
  status: 'queued' | 'running' | 'succeeded' | 'failed';
  error?: string;
  finished_at: string | null;
}

export interface SignedLink {
  url: string;
  disposition: 'attachment' | 'inline';
  expires_at: string;
}
