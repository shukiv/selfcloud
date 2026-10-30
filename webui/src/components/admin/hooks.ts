// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef } from "react";
import { session, sessionKey } from "../../lib/session";
import { useStore } from "../../lib/store";

// useOnSessionChange runs clear when the signed-in person changes (a sign-out, or a sign-in as someone else). A secret a
// page holds is cleared this way, so the next person on the tab never sees it.
export function useOnSessionChange(clear: () => void): void {
  const key = sessionKey(useStore(session));
  const latest = useRef(clear);
  latest.current = clear;
  useEffect(() => {
    latest.current();
  }, [key]);
}

// useGuard returns run, which starts fn unless an earlier call is still out. A second click while a request is in
// flight does nothing, even before the page has drawn the disabled button.
export function useGuard(): (fn: () => Promise<void>) => Promise<void> {
  const busy = useRef(false);
  return (fn) => {
    if (busy.current) return Promise.resolve();
    busy.current = true;
    return fn().finally(() => {
      busy.current = false;
    });
  };
}
