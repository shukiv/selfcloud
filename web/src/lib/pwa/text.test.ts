// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { translate } from "../i18n";
import { loadInstallText } from "./text";

// This file loads its catalogues itself: the test setup loads only the main English. A language's install strings come with
// English, as the fallback, so the card can read its keys in English until the language's own text is there.
describe("loadInstallText", () => {
  it("loads English with a language, so the English keys read before the language's own are loaded", async () => {
    expect(translate("en", "pwaPrompt.title")).toBe("pwaPrompt.title");
    await loadInstallText("he");
    expect(translate("en", "pwaPrompt.title")).toBe("Install {name}");
    expect(translate("he", "pwaPrompt.title")).not.toBe("pwaPrompt.title");
    expect(translate("he", "pwaPrompt.title")).not.toBe("Install {name}");
  });

  it("is idempotent: a second call for the same language resolves with the same strings", async () => {
    await loadInstallText("he");
    await loadInstallText("he");
    expect(translate("he", "pwaPrompt.never")).not.toBe("pwaPrompt.never");
  });
});
