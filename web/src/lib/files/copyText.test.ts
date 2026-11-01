// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { copyText, nodeAddress } from "./copyText";

const setClipboard = (value: unknown) => Object.defineProperty(navigator, "clipboard", { value, configurable: true });
const setExec = (fn: unknown) => Object.defineProperty(document, "execCommand", { value: fn, configurable: true, writable: true });

describe("copyText", () => {
  let focused: HTMLButtonElement;
  beforeEach(() => {
    focused = document.createElement("button");
    document.body.appendChild(focused);
    focused.focus();
  });
  afterEach(() => {
    focused.remove();
    setClipboard(undefined);
    setExec(undefined);
  });

  it("uses the clipboard API when the page has one", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    setClipboard({ writeText });
    setExec(vi.fn());
    expect(await copyText("abc")).toBe(true);
    expect(writeText).toHaveBeenCalledWith("abc");
    expect(document.execCommand).not.toHaveBeenCalled();
  });

  it("falls back to the copy command when the API is refused, and leaves the page as it was", async () => {
    setClipboard({ writeText: vi.fn().mockRejectedValue(new Error("denied")) });
    const exec = vi.fn(() => true);
    setExec(exec);
    expect(await copyText("abc")).toBe(true);
    expect(exec).toHaveBeenCalledWith("copy");
    expect(document.querySelector("textarea")).toBeNull();
    expect(document.activeElement).toBe(focused);
  });

  it("falls back to the copy command on a page with no clipboard API (plain http)", async () => {
    setClipboard(undefined);
    const exec = vi.fn(() => true);
    setExec(exec);
    expect(await copyText("abc")).toBe(true);
    expect(exec).toHaveBeenCalledTimes(1);
  });

  it("reports a failure as a failure when the copy command says no", async () => {
    setClipboard(undefined);
    setExec(vi.fn(() => false));
    expect(await copyText("abc")).toBe(false);
    expect(document.querySelector("textarea")).toBeNull();
  });

  it("reports a failure as a failure when both ways throw, and still cleans up", async () => {
    setClipboard({ writeText: vi.fn().mockRejectedValue(new Error("denied")) });
    setExec(
      vi.fn(() => {
        throw new Error("not allowed");
      }),
    );
    expect(await copyText("abc")).toBe(false);
    expect(document.querySelector("textarea")).toBeNull();
    expect(document.activeElement).toBe(focused);
  });

  it("reports a failure when there is no copy command at all", async () => {
    setClipboard(undefined);
    setExec(undefined);
    expect(await copyText("abc")).toBe(false);
  });
});

describe("nodeAddress", () => {
  it("is this page's own address for the item, not a share link", () => {
    expect(nodeAddress("nod_x")).toBe(`${window.location.origin}/n/nod_x`);
  });
});
