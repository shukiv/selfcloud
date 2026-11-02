// SPDX-License-Identifier: AGPL-3.0-or-later

// Two tabs of one browser, in this process: a tab about to apply an update asks the others whether any is busy (decision 65).
import { describe, expect, it } from "vitest";
import { askOtherTabsBusy, startTab } from "./tabs";

describe("asking the other tabs before an update", () => {
  it("is true when another tab is busy", async () => {
    const stop = startTab("tab-a", () => true);
    const stopIdle = startTab("tab-b", () => false);
    expect(await askOtherTabsBusy("tab-c", 200)).toBe(true);
    stop();
    stopIdle();
  });

  it("is false when every other tab is idle", async () => {
    const stop = startTab("tab-a", () => false);
    expect(await askOtherTabsBusy("tab-c", 200)).toBe(false);
    stop();
  });

  it("is false when no other tab answers, and a tab does not ask itself", async () => {
    const stop = startTab("tab-a", () => true);
    expect(await askOtherTabsBusy("tab-a", 200)).toBe(false);
    stop();
    expect(await askOtherTabsBusy("tab-c", 200)).toBe(false);
  });
});
