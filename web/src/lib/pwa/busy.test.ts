// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { isBusy, registerBusy } from "./busy";

describe("the busy registry", () => {
  it("is busy when any registered check says so, and not after it is removed", () => {
    expect(isBusy()).toBe(false);
    let on = false;
    const off = registerBusy(() => on);
    on = true;
    expect(isBusy()).toBe(true);
    off();
    expect(isBusy()).toBe(false);
  });
});
