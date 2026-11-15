// SPDX-License-Identifier: AGPL-3.0-or-later

import { vi, type Mock } from "vitest";

// stubLocationAssign replaces location.assign with a mock the test reads: jsdom's own is not configurable and goes
// nowhere. The address itself stays the document's, so the History API's changes still show through it.
export function stubLocationAssign(): Mock<(url: string) => void> {
  const real = window.location;
  const assign = vi.fn<(url: string) => void>();
  vi.stubGlobal("location", {
    assign,
    get href() {
      return real.href;
    },
    get pathname() {
      return real.pathname;
    },
    get search() {
      return real.search;
    },
    get hash() {
      return real.hash;
    },
    get origin() {
      return real.origin;
    },
  });
  return assign;
}