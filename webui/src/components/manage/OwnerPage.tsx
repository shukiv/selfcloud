// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, type ReactNode } from "react";
import { useStore } from "../../lib/store";
import { session } from "../../lib/session";
import { brand } from "../../lib/brand";
import { mayOpen, type ManageSection } from "../../lib/routes";
import { useT } from "../../lib/i18n";
import NotFoundPage from "../../pages/NotFoundPage";

// OwnerOnly renders its children only for an account owner, and the not-found view for anyone else. The check is
// made before the children mount, so a refused person sends no request. App.tsx makes the same check for the address.
export function OwnerOnly({ section, children }: { section: ManageSection; children: ReactNode }) {
  const user = useStore(session)?.user;
  if (!mayOpen({ area: "manage", section }, user)) return <NotFoundPage />;
  return <>{children}</>;
}

// useManageTitle sets the document title to the page's name and the brand, as the Svelte pages do.
export function useManageTitle(key: string): void {
  const { t } = useT();
  const name = useStore(brand).name;
  useEffect(() => {
    document.title = `${t(key)} · ${name}`;
  }, [t, key, name]);
}

// useOnce runs an action at most once at a time: a second press while the first request is out does nothing, so a
// double click sends one request.
export function useOnce(): (fn: () => Promise<void>) => Promise<void> {
  const running = useRef(false);
  return async (fn) => {
    if (running.current) return;
    running.current = true;
    try {
      await fn();
    } finally {
      running.current = false;
    }
  };
}

// useAccountID is the account the signed-in owner manages, or "" with no session.
export function useAccountID(): string {
  return useStore(session)?.account?.id ?? "";
}
