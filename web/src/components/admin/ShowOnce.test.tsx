// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import ShowOnce from "./ShowOnce";
import { LINK } from "../../test/adminFixtures";

function renderOnce(onClosed = vi.fn()) {
  render(<ShowOnce id="t" title="Once" label="Invitation link" value={LINK} warning="Copy it now." onClosed={onClosed} />);
  return onClosed;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("a secret shown once", () => {
  it("has no close control, and Escape does not dismiss it (guard: closable false, keyboard false)", () => {
    renderOnce();
    expect(screen.queryByRole("button", { name: "Close" })).toBeNull();
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape", code: "Escape" });
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect((document.getElementById("t-value") as HTMLInputElement).value).toBe(LINK);
  });

  it("closes only by its button, which tells the parent (guard: onClosed)", () => {
    const onClosed = renderOnce();
    fireEvent.click(screen.getByRole("button", { name: "I have copied it" }));
    expect(onClosed).toHaveBeenCalledTimes(1);
  });

  it("copies the value and says so", async () => {
    const writeText = vi.fn(async () => undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    renderOnce();
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    await waitFor(() => expect(screen.getByText("Copied.")).toBeTruthy());
    expect(writeText).toHaveBeenCalledWith(LINK);
    vi.unstubAllGlobals();
  });

  it("when the clipboard refuses, the value is selected for a copy by hand and nothing is logged", async () => {
    const log = vi.spyOn(console, "log").mockImplementation(() => undefined);
    const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText: vi.fn(async () => Promise.reject(new Error("denied"))) } });
    renderOnce();
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    await waitFor(() => expect(screen.queryByText("Copied.")).toBeNull());
    expect(log).not.toHaveBeenCalled();
    expect(error).not.toHaveBeenCalled();
    vi.unstubAllGlobals();
  });
});
