// SPDX-License-Identifier: AGPL-3.0-or-later

// The sharing calls the browser makes. Each returns what the server answers, or throws an ApiError.
import { api } from '../api';
import type { Page, Node, Role } from '../files/types';
import type { AccessRequest, DirectoryEntry, Permission, SetPermissionReply } from '../files/types';
import type { ShareLink, ShareLinkCreated } from './links';

export function listPermissions(nod: string): Promise<{ items: Permission[] }> {
  return api<{ items: Permission[] }>('GET', `/api/v1/nodes/${nod}/permissions`);
}

// setPermission gives a user or a group a role on a node. expires_at is an RFC 3339 time.
export function setPermission(
  nod: string,
  subject: string,
  body: { role: Role; expires_at?: string; notify?: boolean; message?: string },
): Promise<SetPermissionReply> {
  return api<SetPermissionReply>('PUT', `/api/v1/nodes/${nod}/permissions/${subject}`, { body });
}

export function removePermission(nod: string, subject: string): Promise<void> {
  return api<void>('DELETE', `/api/v1/nodes/${nod}/permissions/${subject}`);
}

// searchDirectory finds people and groups by q, at least two characters after trimming.
export function searchDirectory(q: string, kind: 'user' | 'group' | 'all'): Promise<{ items: DirectoryEntry[] }> {
  const params = new URLSearchParams({ q, kind });
  return api<{ items: DirectoryEntry[] }>('GET', `/api/v1/directory?${params.toString()}`);
}

export function sharedWithMe(cursor?: string): Promise<Page<Node>> {
  const c = cursor ? `&cursor=${encodeURIComponent(cursor)}` : '';
  return api<Page<Node>>('GET', `/api/v1/shared?limit=50${c}`);
}

// createAccessRequest asks for a role on a node. The server answers 202 with no body whether or not the
// node exists, so the answer carries no information.
export function createAccessRequest(nod: string, role: Role, message: string): Promise<void> {
  return api<void>('POST', `/api/v1/nodes/${nod}/access-requests`, { body: { role, message } });
}

// listAccessRequests reads the pending requests the caller may decide.
export function listAccessRequests(): Promise<{ items: AccessRequest[] }> {
  return api<{ items: AccessRequest[] }>('GET', '/api/v1/access-requests');
}

export function approveAccessRequest(arq: string, role?: Role): Promise<AccessRequest> {
  return api<AccessRequest>('POST', `/api/v1/access-requests/${arq}/approve`, {
    body: role ? { role } : {},
  });
}

export function denyAccessRequest(arq: string): Promise<AccessRequest> {
  return api<AccessRequest>('POST', `/api/v1/access-requests/${arq}/deny`);
}

export function setHidden(fld: string, hidden: boolean): Promise<void> {
  return hidden
    ? api<void>('PUT', `/api/v1/folders/${fld}/hidden`)
    : api<void>('DELETE', `/api/v1/folders/${fld}/hidden`);
}

export interface SpaceSettings {
  members_only?: boolean;
  sharing_managers_only?: boolean;
  no_download_for_viewers?: boolean;
  allow_links?: boolean;
}

export function updateSpaceSettings(fld: string, body: SpaceSettings): Promise<unknown> {
  return api<unknown>('PATCH', `/api/v1/folders/${fld}/settings`, { body });
}

// updateNodeFlags sets a node's own flags: editors_can_share, and no_download.
export function updateNodeFlags(
  nod: string,
  body: { editors_can_share?: boolean; no_download?: boolean },
): Promise<Node> {
  return api<Node>('PATCH', `/api/v1/nodes/${nod}`, { body });
}

export function transferOwnership(nod: string, to: string): Promise<void> {
  return api<void>('POST', `/api/v1/nodes/${nod}/transfer-ownership`, { body: { to } });
}

// sharingMaxExpiryDays reads the sharing limit from GET /info: whole days, 0 for no cap.
export function sharingMaxExpiryDays(info: { sharing_max_expiry_days?: unknown }): number {
  const n = info.sharing_max_expiry_days;
  return typeof n === 'number' && Number.isInteger(n) && n >= 0 ? n : 0;
}

// listShareLinks reads the links on a node and on its ancestors, the node's own first.
export function listShareLinks(nod: string): Promise<{ items: ShareLink[] }> {
  return api<{ items: ShareLink[] }>('GET', `/api/v1/nodes/${nod}/share-links`);
}

// createShareLink makes the node's organisation link. expires_at is optional, an RFC 3339 time. The
// answer carries the URL, which is shown once.
export function createShareLink(
  nod: string,
  body: { role: Role; expires_at?: string },
): Promise<ShareLinkCreated> {
  return api<ShareLinkCreated>('POST', `/api/v1/nodes/${nod}/share-links`, {
    body: { audience: 'organisation', ...body },
  });
}

// updateShareLink changes a link's role, or its expiry: null clears it, an absent field keeps it.
export function updateShareLink(
  shl: string,
  body: { role?: Role; expires_at?: string | null },
): Promise<ShareLink> {
  return api<ShareLink>('PATCH', `/api/v1/share-links/${shl}`, { body });
}

// resetShareLink gives a link a new secret. The old URL stops working.
export function resetShareLink(shl: string): Promise<ShareLinkCreated> {
  return api<ShareLinkCreated>('POST', `/api/v1/share-links/${shl}/reset`);
}

export function deleteShareLink(shl: string): Promise<void> {
  return api<void>('DELETE', `/api/v1/share-links/${shl}`);
}

// openShareLink opens a link for the signed-in member, and answers the node it opens.
export function openShareLink(secret: string): Promise<{ node_id: string }> {
  return api<{ node_id: string }>('POST', '/api/v1/share/open', { body: { secret } });
}
