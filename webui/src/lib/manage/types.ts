// SPDX-License-Identifier: AGPL-3.0-or-later

// Shapes of the management API (owner and installation admin pages). Only the fields the pages read.
import type { AuditEntry } from '../audit/sentence';

export interface Account {
  id: string;
  external_id: string | null;
  name: string;
  status: 'active' | 'suspended' | 'deleting' | string;
  quota_bytes: number;
  used_bytes: number;
  used_files: number;
  created_at: string;
  updated_at: string;
}

export interface Usage {
  quota_bytes: number | null;
  used_bytes: number;
  reserved_bytes: number;
  available_bytes: number | null;
  files: number;
  folders: number;
  users_active: number;
  users_pending: number;
}

// A person of an account. role is owner or member; an installation admin has no account.
export interface Person {
  id: string;
  external_id: string | null;
  email: string;
  display_name: string;
  locale: string;
  role: 'owner' | 'member' | 'admin' | string;
  status: 'pending' | 'active' | 'disabled' | string;
  verified_at: string | null;
  created_at: string;
  updated_at: string;
  quota_bytes?: number | null;
  effective_quota_bytes?: number | null;
}

// The one-time link a verification gives (method link). url is shown once and never stored.
export interface Verification {
  method: 'email' | 'link' | string;
  url?: string;
  token?: string;
  expires_at?: string;
  sent?: boolean;
}

export interface PersonReply extends Person {
  verification?: Verification;
}

export interface Group {
  id: string;
  external_id: string | null;
  name: string;
  created_at: string;
  updated_at: string;
}

export interface GroupMember {
  user_id: string;
  created_at: string;
}

// A team space as the management list returns it. root_node_id is the node its permissions are set on.
export interface Space {
  id: string;
  name: string;
  kind: 'team' | 'personal';
  root_node_id: string;
  members_only: boolean;
  sharing_managers_only: boolean;
  no_download_for_viewers: boolean;
  allow_links: boolean;
  created_at: string;
  updated_at: string;
}

// SpaceDetail is one team space with the totals of its tree (the single-space answer only).
export interface SpaceDetail extends Space {
  files: number;
  bytes: number;
}

export interface ApiKey {
  id: string;
  name: string;
  prefix: string;
  created_at: string;
  expires_at: string | null;
  revoked_at: string | null;
  last_used_at: string | null;
}

export interface NewKey extends ApiKey {
  key: string;
}

export interface Job {
  id: string;
  kind: string;
  target: string;
  status: 'queued' | 'running' | 'succeeded' | 'failed' | string;
  created_at: string;
  finished_at: string | null;
}

export interface Info {
  name: string;
  version: string;
  public_url: string;
  web_interface: boolean;
  limits: { max_file_size: number; max_chunk_size: number };
}

export type { AuditEntry };
