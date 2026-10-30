// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from "vitest";
import { get, resetUserStores } from "../store";
import { maxExpiryDays } from "./limits";

// The installation's cap comes from /info, not from a person, so a change of session must not clear it: the date
// inputs would otherwise lose the cap at sign-in. Boot sets it once the info has loaded.
describe("the installation's expiry cap", () => {
  beforeEach(() => maxExpiryDays.set(0));

  it("survives the reset that runs when the session changes", () => {
    maxExpiryDays.set(90);
    resetUserStores();
    expect(get(maxExpiryDays)).toBe(90);
  });
});
