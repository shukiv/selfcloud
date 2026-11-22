// SPDX-License-Identifier: AGPL-3.0-or-later

// The app-password routes the browser calls (decision 236): the credentials of the read-only WebDAV mount, on the
// person's own session only. Each returns what the server answers, or throws an ApiError. The secret is in the create
// answer alone, shown once; the list and the row never carry it.
import { api } from '../api';

// AppPassword is one credential as the list and the create answer carry it (without the secret). A revoked one is
// deleted, so the list holds only live ones and there is no revocation time.
export interface AppPassword {
  id: string;
  name: string;
  access: 'read' | 'write';
  prefix: string;
  created_at: string;
  last_used_at: string | null;
}

// listAppPasswords reads the person's app passwords and whether the drive mount is on. mount_enabled is the live
// dav.enabled, so the page can say the mount is off without a second read of the settings. Listing keeps working while
// the mount is off, so a leaked app password can always be ended.
export function listAppPasswords(): Promise<{ items: AppPassword[]; mount_enabled: boolean }> {
  return api<{ items: AppPassword[]; mount_enabled: boolean }>('GET', '/api/v1/me/app-passwords');
}

// createAppPassword makes an app password and returns it with its secret, shown once. No access field is sent: the mount
// is read-only today, and the server's default is read.
export function createAppPassword(name: string): Promise<{ app_password: AppPassword; secret: string }> {
  return api<{ app_password: AppPassword; secret: string }>('POST', '/api/v1/me/app-passwords', { body: { name } });
}

// revokeAppPassword ends one app password: the row is deleted and the mount stops accepting it at once. Revoking keeps
// working while the mount is off.
export function revokeAppPassword(id: string): Promise<void> {
  return api<void>('DELETE', `/api/v1/me/app-passwords/${encodeURIComponent(id)}`);
}
