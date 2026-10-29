// SPDX-License-Identifier: AGPL-3.0-or-later

import { writable } from 'svelte/store';
import { myFolders } from './api';
import type { FolderAccess } from './types';

// teamFolders is the list of folders the caller reaches, shared by the navigation and the browser. It is
// read once a session starts, and again when the browser needs a folder the list does not have.
export const folders = writable<FolderAccess[]>([]);

export async function loadFolders(): Promise<FolderAccess[]> {
  const list = await myFolders();
  folders.set(list);
  return list;
}
