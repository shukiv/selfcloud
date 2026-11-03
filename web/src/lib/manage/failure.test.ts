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

  // The link reasons are named by the share dialog and the drop dialog (GeneralAccess, RequestFiles), which do not use
  // failureText; on a management page they fall back.
  it("the link reasons fall back on a management page", () => {
    for (const reason of ["exists", "links_off", "public_links_off", "manager_required", "members_only"]) {
      const text = failureText(new ApiError(409, "conflict", { reason }, 0), "en");
      expect(text).toBe(translate("en", "error.conflict"));
    }
  });

  // pending 113-114: a limit (label fields and choices, comments, playlists) says the number the server sends.
  it("a limit says its number, in every language", () => {
    const err = new ApiError(409, "conflict", { reason: "limit", limit: 50 }, 0);
    expect(failureText(err, "en")).toBe("This would go over the limit of 50.");
    for (const lang of ["he", "ar", "es", "fr", "pt", "ru"] as const) {
      const text = failureText(err, lang);
      expect(text).not.toBe(translate(lang, "error.conflict"));
      expect(text).toContain("50");
    }
  });

  it("a limit with no number falls back", () => {
    const err = new ApiError(409, "conflict", { reason: "limit" }, 0);
    expect(failureText(err, "en")).toBe(translate("en", "error.conflict"));
  });
});
