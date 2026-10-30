// SPDX-License-Identifier: AGPL-3.0-or-later

import type { ReactNode } from "react";

// stubPage is a page replaced in a test: it names itself, so the test can see which page the gate showed.
export function stubPage(name: string) {
  return { default: () => <p>stub {name}</p> };
}

// stubShell is the frame around the pages, replaced by a plain box.
export function stubShell() {
  return { default: ({ children }: { children: ReactNode }) => <div>{children}</div> };
}
