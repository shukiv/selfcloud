// SPDX-License-Identifier: AGPL-3.0-or-later

// The iPhone instructions (decision 65): the steps name the browser's own Share button, and a visible Close button closes them.
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import IosInstallDialog from "./IosInstallDialog";

describe("IosInstallDialog", () => {
  it("names the browser's Share button, and closes with its Close button", () => {
    const onClose = vi.fn();
    render(<IosInstallDialog onClose={onClose} />);
    expect(screen.getByText("Tap the browser's Share button.")).toBeTruthy();
    // antd's corner X is also named Close; the visible footer button is the one with the word.
    const footer = screen.getAllByRole("button", { name: "Close" }).find((b) => b.textContent === "Close")!;
    fireEvent.click(footer);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
