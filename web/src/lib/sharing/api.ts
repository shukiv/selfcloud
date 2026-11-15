// SPDX-License-Identifier: AGPL-3.0-or-later

// The sharing calls the browser makes. Each returns what the server answers, or throws an ApiError.
import { api } from '../api';
import type { Page, Job, Node, Role } from '../files/types';
import type { AccessRequest, DirectoryEntry, Permission, SetPermissionReply } from '../files/types';
import type { ShareLink, ShareLinkCreated } from './links';
import type { DropInfo } from './drop';

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

// ExternalPermission is a share with a person in another account, named by their address. The role is viewer only.
export interface ExternalPermission {
  email: string;
  role: 'viewer';
  created_at: string;
  expires_at: string | null;
  created_by: string;
  created_by_name: string;
}

export function listExternalPermissions(nod: string): Promise<{ items: ExternalPermission[] }> {
  return api<{ items: ExternalPermission[] }>('GET', `/api/v1/nodes/${nod}/external-permissions`);
}

// addExternalPermission shares a node with an address outside the account, as a viewer. expires_at is an RFC 3339 time.
export function addExternalPermission(
  nod: string,
  body: { email: string; role?: 'viewer'; expires_at?: string },
): Promise<ExternalPermission> {
  return api<ExternalPermission>('POST', `/api/v1/nodes/${nod}/external-permissions`, { body });
}

// removeExternalPermission ends a share with an address. The address is encoded, so one with a plus never breaks the route.
export function removeExternalPermission(nod: string, email: string): Promise<void> {
  return api<void>('DELETE', `/api/v1/nodes/${nod}/external-permissions/${encodeURIComponent(email)}`);
}

// searchDirectory finds people and groups by q, 0 to 100 characters after trimming. An empty q lists the account
// (decision 96). more is true when more matched than items holds.
export function searchDirectory(
  q: string,
  kind: 'user' | 'group' | 'all',
): Promise<{ items: DirectoryEntry[]; more?: boolean }> {
  const params = new URLSearchParams({ q, kind });
  return api<{ items: DirectoryEntry[]; more?: boolean }>('GET', `/api/v1/directory?${params.toString()}`);
}

// SharedNode is an item shared with the caller: shared_at is the later of the share and the caller's last visit to it (tl4a,
// decision 136), sent in every order of the list.
export type SharedNode = Node & { shared_at?: string };

export function sharedWithMe(cursor?: string): Promise<Page<SharedNode>> {
  const c = cursor ? `&cursor=${encodeURIComponent(cursor)}` : '';
  return api<Page<SharedNode>>('GET', `/api/v1/shared?limit=50${c}`);
}

// ExternalShared is an item a person in another account shared with the caller's proved address. It names the sharer
// and their account, and carries no folder of the caller's drive.
export interface ExternalShared {
  id: string;
  name: string;
  kind: Node['kind'];
  size?: number;
  modified_at: string;
  mime?: string;
  account_name: string;
  shared_by_name: string;
  role: 'viewer';
  expires_at: string | null;
}

// sharedExternal reads the items shared with the caller from other accounts, and whether their address is proved.
export function sharedExternal(): Promise<{ items: ExternalShared[]; email_proved: boolean }> {
  return api<{ items: ExternalShared[]; email_proved: boolean }>('GET', '/api/v1/shared/external');
}

// proveEmail asks the server to send the proof mail to the session's address. 200 means the address is already proved;
// 202 means the ask was taken, and sent says whether a mail could be sent.
export function proveEmail(): Promise<{ proved: boolean; sent: boolean }> {
  return api<{ proved: boolean; sent: boolean }>('POST', '/api/v1/me/email-proof');
}

// confirmEmailProof redeems the mailed token. A bad token is an answer the page reads, not a redirect to sign-in.
export function confirmEmailProof(token: string): Promise<void> {
  return api<void>('POST', '/api/v1/auth/email-proof', { body: { token }, noRedirect: true });
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

// createShareLink makes the node's link. An organisation link takes an optional expires_at and no password.
// An anyone link takes a viewer role only, an expires_at (the server makes one when it is absent) and an
// optional password. The answer carries the URL, which is shown once.
export function createShareLink(
  nod: string,
  body: ShareLinkBody,
): Promise<ShareLinkCreated> {
  return api<ShareLinkCreated>('POST', `/api/v1/nodes/${nod}/share-links`, { body });
}

// ShareLinkBody is what a new link takes. audience defaults to organisation on the server, and is sent here.
export interface ShareLinkBody {
  audience: 'organisation' | 'anyone';
  role?: Role;
  expires_at?: string;
  password?: string;
}

// createDropLink makes the drop link of a directory (Request files): the link that anyone can send files into. Its
// body is built by dropCreateBody, and the answer carries the URL, which is shown once.
export function createDropLink(nod: string, body: Record<string, unknown>): Promise<ShareLinkCreated> {
  return api<ShareLinkCreated>('POST', `/api/v1/nodes/${nod}/share-links`, { body });
}

// updateShareLink changes a link. An absent field keeps its value; expires_at null clears it (refused for an
// anyone link); password "" removes the password of an anyone link. Switching audience keeps the secret.
export function updateShareLink(shl: string, body: ShareLinkUpdate): Promise<ShareLink> {
  return api<ShareLink>('PATCH', `/api/v1/share-links/${shl}`, { body });
}

export interface ShareLinkUpdate {
  audience?: 'organisation' | 'anyone';
  role?: Role;
  expires_at?: string | null;
  password?: string;
}

// updateDropLink changes a drop link: its end, its text, its two switches and, when given, its password.
export function updateDropLink(shl: string, body: Record<string, unknown>): Promise<ShareLink> {
  return api<ShareLink>('PATCH', `/api/v1/share-links/${shl}`, { body });
}

// resetShareLink gives a link a new secret. The old URL stops working.
export function resetShareLink(shl: string): Promise<ShareLinkCreated> {
  return api<ShareLinkCreated>('POST', `/api/v1/share-links/${shl}/reset`);
}

export function deleteShareLink(shl: string, signal?: AbortSignal): Promise<void> {
  return api<void>('DELETE', `/api/v1/share-links/${shl}`, { signal });
}

// OpenedLink is what a link opens to. An organisation link, opened by a signed-in member, names its node.
// An anyone link answers a bearer credential for the visit and the node it opens.
export type OpenedLink =
  | { node_id: string }
  | { token: string; expires_at: string; upload_only?: boolean; link?: ShareLink; node?: string };

// openShareLink opens a link from its secret. A password is sent only when the visitor gave one.
export function openShareLink(secret: string, password?: string): Promise<OpenedLink> {
  const body = password ? { secret, password } : { secret };
  return api<OpenedLink>('POST', '/api/v1/share/open', { body, noRedirect: true });
}

// renewShareLink swaps a live visitor credential for a new one. The bearer is sent in the header only.
export function renewShareLink(token: string): Promise<{ token: string; expires_at: string }> {
  return api<{ token: string; expires_at: string }>('POST', '/api/v1/share/renew', {
    headers: { Authorization: `Bearer ${token}` },
    noRedirect: true,
  });
}

// The calls a visitor makes with the bearer. A 401 means the credential has ended, and the page asks again;
// it must never send the visitor to sign-in, so every one passes noRedirect.
export function visitorNode(token: string, nod: string): Promise<Node> {
  return api<Node>('GET', `/api/v1/nodes/${nod}`, visitorOpts(token));
}

export function visitorChildren(token: string, nod: string, cursor?: string): Promise<Page<Node>> {
  const c = cursor ? `&cursor=${encodeURIComponent(cursor)}` : '';
  return api<Page<Node>>('GET', `/api/v1/nodes/${nod}/children?limit=200${c}`, visitorOpts(token));
}

// visitorLink makes a signed link to a file the visitor reaches. Inline gives a URL for a preview; attachment
// gives one for a download, which the server refuses when no-download applies.
export function visitorLink(
  token: string,
  nod: string,
  disposition: 'inline' | 'attachment',
): Promise<{ url: string; expires_at: string }> {
  return api<{ url: string; expires_at: string }>('POST', `/api/v1/nodes/${nod}/links`, {
    ...visitorOpts(token),
    // No ttl: the server gives the longest life the visitor's credential has left.
    body: { disposition },
  });
}

// visitorZip asks for a zip of a folder the visitor reaches, as the signed-in create asks (lib/files/api.ts): the
// body names one root, and the job's target is the zip. The zip is the link's, shared by every visitor of it, so the
// same folder asked again answers the same zip. signal ends the call when the visitor leaves.
export async function visitorZip(token: string, folderId: string, signal?: AbortSignal): Promise<string> {
  const job = await api<Job>('POST', '/api/v1/zips', { ...visitorOpts(token), body: { nodes: [folderId] }, signal });
  return job.target;
}

// visitorZipLink asks for a signed download link to the zip the visitor's link made: the archive downloads through it
// with no credential. While the zip is made the server answers 409 zip_not_ready with Retry-After; a failed zip is
// 409 zip_failed, and one past its expiry 404. The error carries its status, code, reason and Retry-After, as the
// other calls do.
export function visitorZipLink(token: string, zip: string, signal?: AbortSignal): Promise<{ url: string; expires_at: string }> {
  return api<{ url: string; expires_at: string }>('POST', `/api/v1/zips/${zip}/links`, { ...visitorOpts(token), signal });
}

// visitorInfo reads what a drop link says to its visitor: its title and message, whether it asks for a name, when it
// ends and the largest file it takes. Only a drop credential reaches it.
export function visitorInfo(token: string): Promise<DropInfo> {
  return api<DropInfo>('GET', '/api/v1/share/info', visitorOpts(token));
}

function visitorOpts(token: string): { headers: Record<string, string>; noRedirect: true } {
  return { headers: { Authorization: `Bearer ${token}` }, noRedirect: true };
}

// SharingPolicy is the account's switch for links that anyone can open, and how many such links it has.
export interface SharingPolicy {
  public_links: boolean;
  anyone_links: number;
  // comments and who_viewed are the account's comment switch and who-viewed switch (decisions 39 and 51). Absent
  // in an answer from an older server.
  comments?: boolean;
  who_viewed?: boolean;
  // external_sharing is the account's switch for sharing with people in other accounts. Absent in an answer from
  // an older server.
  external_sharing?: boolean;
}

// SharingSwitches is the part of the sharing policy a change may name. The server wants at least one of them.
export interface SharingSwitches {
  public_links?: boolean;
  comments?: boolean;
  who_viewed?: boolean;
}

// setSharingSwitches changes one or more of the account's switches. Each one is applied on its own by the server.
export function setSharingSwitches(acc: string, change: SharingSwitches): Promise<SharingPolicy> {
  return api<SharingPolicy>('PATCH', `/api/v1/accounts/${acc}/sharing-policy`, { body: change });
}

// getSharingPolicy reads the account's switch for links that anyone can open.
export function getSharingPolicy(acc: string): Promise<SharingPolicy> {
  return api<SharingPolicy>('GET', `/api/v1/accounts/${acc}/sharing-policy`);
}

// setSharingPolicy changes the account's switches for anyone links and for sharing with other accounts. Each switch
// named is applied on its own; turning anyone links off pauses those links at once.
export function setSharingPolicy(
  acc: string,
  patch: { public_links?: boolean; external_sharing?: boolean },
): Promise<SharingPolicy> {
  return api<SharingPolicy>('PATCH', `/api/v1/accounts/${acc}/sharing-policy`, { body: patch });
}
