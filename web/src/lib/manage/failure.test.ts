// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { ApiError } from "../apiError";
import { translate } from "../i18n";
import { failureText } from "./failure";

describe("failureText", () => {
  it("a conflict with a reason the catalogue names gets that reason's sentence", () => {
    const err = new ApiError(409, "conflict", { reason: "last_owner" }, 0);
    expect(failureText(err, "en")).toBe(translate("en", "reason.last_owner"));
  });

  // The server may add a reason before the catalogue names it. The page then shows the generic conflict message, never
  // the key itself (which would read as "reason.<token>").
  it("a reason outside the catalogue gets the generic message, never the key (guard: fall back when the key is unknown)", () => {
    const err = new ApiError(409, "conflict", { reason: "some_new_reason" }, 0);
    const text = failureText(err, "en");
    expect(text).toBe(translate("en", "error.conflict"));
    expect(text).not.toContain("reason.");
    expect(text).not.toContain("some_new_reason");
  });

  it("the reasons without a sentence today fall back as well", () => {
    for (const reason of ["exists", "limit", "links_off", "public_links_off", "manager_required", "members_only"]) {
      const text = failureText(new ApiError(409, "conflict", { reason }, 0), "en");
      expect(text).toBe(translate("en", "error.conflict"));
    }
  });
});
