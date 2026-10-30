// SPDX-License-Identifier: AGPL-3.0-or-later

import { ApiError } from '../apiError';
import { childrenPage, statOrNull } from './api';
import { reachableAncestors } from './breadcrumbs';
import type { FolderAccess, Node, Role } from './types';

// Here is where the browser is: a folder, and a directory in it (null for the folder's root). trail is the
// reachable ancestors of the directory below the root, root first; it is empty when the root itself cannot
// be reached, in which case path is null and nothing can be created by path there.
export interface Here {
  folder: string;
  folderName: string | null;
  folderRole: Role;
  dir: Node | null;
  dirRole: Role;
  trail: Node[];
  rootReachable: boolean;
  // spaceNoDownload is the team space's no-download rule for viewers. It binds the browser's own downloads.
  spaceNoDownload: boolean;
  // path is the directory's path from the folder's root, "/" for the root, or null when unknown.
  path: string | null;
}

// joinPath appends a name to a directory path.
export function joinPath(base: string | null, name: string): string | null {
  if (base === null) return null;
  return base === '/' ? `/${name}` : `${base}/${name}`;
}

// locate reads where the browser is. It answers null when the folder or the directory is not reachable by
// the caller, so the page shows the not-found view. Other errors are thrown.
export async function locate(
  folder: string,
  dirID: string | null,
  folders: FolderAccess[],
): Promise<Here | null> {
  const listed = folders.find((f) => f.id === folder);
  const folderName = listed?.name ?? null;
  if (!dirID) {
    try {
      // The folder's listing answers not_found when the caller cannot reach its root.
      await childrenPage(folder, null);
    } catch (err) {
      if (err instanceof ApiError && err.code === 'not_found') return null;
      throw err;
    }
    return {
      folder,
      folderName,
      folderRole: listed?.role ?? 'viewer',
      dir: null,
      dirRole: listed?.role ?? 'viewer',
      trail: [],
      rootReachable: true,
      spaceNoDownload: listed?.no_download_for_viewers ?? false,
      path: '/',
    };
  }

  const dir = await statOrNull(dirID);
  if (!dir || dir.kind !== 'dir' || dir.folder_id !== folder) return null;
  const chain = await reachableAncestors(dir.parent_id, (id) => statOrNull(id));
  const rootReachable = chain.length > 0 && chain[0].parent_id === null;
  // The chain's first node is the folder root when it is reachable; the rest is the trail below it.
  const trail = rootReachable ? chain.slice(1) : [];
  const path = rootReachable ? [...trail.map((n) => n.name), dir.name].join('/') : null;
  return {
    folder,
    folderName: rootReachable ? folderName : null,
    folderRole: listed?.role ?? 'viewer',
    dir,
    dirRole: dir.role ?? 'viewer',
    trail,
    rootReachable,
    spaceNoDownload: listed?.no_download_for_viewers ?? false,
    path: path === null ? null : `/${path}`,
  };
}
