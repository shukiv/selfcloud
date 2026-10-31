// SPDX-License-Identifier: AGPL-3.0-or-later

// Shapes of the file API the browser reads. Only the fields it uses are listed.

// Role is the caller's role on a node, lowest first.
export type Role = 'viewer' | 'commenter' | 'contributor' | 'editor' | 'manager';

export interface Node {
  id: string;
  folder_id: string;
  parent_id: string | null;
  name: string;
  kind: 'dir' | 'file' | 'shortcut';
  size: number;
  // color is a directory's palette name, and description the item's plain text. Every reader sees them; a link visitor
  // gets neither (the API leaves them out).
  color?: string;
  description?: string;
  // target is what a shortcut points to, for a caller who reaches it. unavailable is set on a shortcut whose target the
  // caller cannot reach or that was purged. The target's ID is never sent to a caller who cannot reach it.
  target?: { id: string; kind: 'dir' | 'file' | 'shortcut' };
  unavailable?: boolean;
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
  // created_by_name and owner_name are the display names of those two people, sent to a caller of the account that
  // reaches the node (a stat or a listing). Absent when the API gives no name.
  created_by_name?: string;
  owner_name?: string;
  // has_link is set when the item carries a share link the caller may know of (the children listing only).
  has_link?: boolean;
  // starred is set for a signed-in caller (the caller's own star); absent for keys, admins and link visitors.
  starred?: boolean;
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
  // The team-space settings (false on a personal folder). Only a manager of the space sees them change.
  members_only: boolean;
  sharing_managers_only: boolean;
  no_download_for_viewers: boolean;
  allow_links: boolean;
}

// Permission is a role a user or a group holds on a node, or above it. inherited_from is the ancestor it
// sits on, null when it sits on the node. subject_name and subject_status come only to a manager.
export interface Permission {
  subject_kind: 'user' | 'group';
  subject_id: string;
  role: Role;
  inherited_from: string | null;
  expires_at: string | null;
  created_at: string;
  created_by: string;
  subject_name?: string;
  subject_status?: 'pending' | 'active' | 'disabled' | '';
}

// SetPermissionReply is a permission as it now stands, and whether the subject was mailed.
export interface SetPermissionReply extends Permission {
  notified: boolean;
  reason?: string;
}

// DirectoryEntry is a person or a group found by the directory search.
export interface DirectoryEntry {
  id: string;
  kind: 'user' | 'group';
  name: string;
  email?: string;
}

// AccessRequest is a request for a role on a node. user_name is shown to those who may decide it.
export interface AccessRequest {
  id: string;
  node_id: string;
  user_id: string;
  user_name?: string;
  role: Role;
  message: string;
  status: 'pending' | 'approved' | 'denied';
  created_at: string;
  decided_at: string | null;
  decided_by: string;
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
