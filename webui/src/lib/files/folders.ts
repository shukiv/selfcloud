// SPDX-License-Identifier: AGPL-3.0-or-later

import { get, userWritable } from '../store';
import { session, sessionChanged } from '../session';
import { myFolders } from './api';
import type { FolderAccess } from './types';

// teamFolders is the list of folders the caller reaches, shared by the navigation and the browser. It is
// read once a session starts, and again when the browser needs a folder the list does not have.
export const folders = userWritable<FolderAccess[]>([]);

// Nothing is read without a session: a request sent just after a sign-out would be refused, and it is not wanted.
export async function loadFolders(): Promise<FolderAccess[]> {
  if (get(session) === null) return get(folders);
  const changed = sessionChanged();
  const list = await myFolders();
  // An answer that arrives after the session changed is the last person's; it must not fill the next one's list.
  if (changed()) return [];
  folders.set(list);
  return list;
}

// hiddenFolders are the team spaces the caller has hidden from its own list. They are kept apart from folders,
// so the move and copy pickers list only what the caller shows, and they are read only when asked for.
export const hiddenFolders = userWritable<FolderAccess[]>([]);

export async function loadHiddenFolders(): Promise<FolderAccess[]> {
  if (get(session) === null) return get(hiddenFolders);
  const changed = sessionChanged();
  const list = await myFolders(true);
  if (changed()) return [];
  hiddenFolders.set(list);
  return list;
}
