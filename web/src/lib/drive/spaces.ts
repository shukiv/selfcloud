// SPDX-License-Identifier: AGPL-3.0-or-later

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { queryKey } from "../query";
import { folders, hiddenFolders, loadHiddenFolders } from "../files/folders";
import type { FolderAccess } from "../files/types";
import { useStore } from "../store";

// useLocationSpaces is the spaces a drive list names a location from: the spaces the caller shows, and the ones it has hidden,
// which it reaches too (tl4 r1, finding 6). The hidden list is read once per session: its query is keyed by the session and is
// not read again on a mount (tl4 r2, N4). The shown list is left as it is, so a space picker still lists only what the caller shows.
export function useLocationSpaces(signedIn: boolean): FolderAccess[] {
  const shown = useStore(folders);
  const hidden = useStore(hiddenFolders);
  useQuery({
    queryKey: queryKey("hidden-spaces"),
    enabled: signedIn,
    staleTime: Infinity,
    refetchOnMount: false,
    queryFn: async () => {
      await loadHiddenFolders();
      return null;
    },
  });
  return useMemo(() => [...shown, ...hidden], [shown, hidden]);
}
