// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { translate } from "../i18n";
import { loadCliText } from "./cliText";

// This file loads its catalogues itself: the test setup loads only the main English. A language's cli strings come with
// English, as the fallback, so the pages can read their keys in English until the language's own text is there.
describe("loadCliText", () => {
  it("loads English with a language, so the English keys read before the language's own are loaded", async () => {
    expect(translate("en", "cliApprove.title")).toBe("cliApprove.title");
    await loadCliText("he");
    expect(translate("en", "cliApprove.title")).toBe("Approve a terminal sign-in");
    expect(translate("he", "cliApprove.title")).not.toBe("cliApprove.title");
    expect(translate("he", "cliApprove.title")).not.toBe("Approve a terminal sign-in");
  });

  it("is idempotent: a second call for the same language resolves with the same strings", async () => {
    await loadCliText("he");
    await loadCliText("he");
    expect(translate("he", "devices.title")).not.toBe("devices.title");
  });
});
