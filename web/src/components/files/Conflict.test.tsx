// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import Conflict from "./Conflict";

describe("Conflict", () => {
  it("names the file and answers replace, keep both or skip at once", () => {
    const onAnswer = vi.fn();
    render(<Conflict name="plan.pdf" onAnswer={onAnswer} onCancel={() => {}} />);
    expect(screen.getByText(/plan\.pdf/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Keep both" }));
    expect(onAnswer).toHaveBeenLastCalledWith("rename", false);
    fireEvent.click(screen.getByRole("button", { name: "Skip it" }));
    expect(onAnswer).toHaveBeenLastCalledWith("skip", false);
    fireEvent.click(screen.getByRole("button", { name: "Replace it" }));
    expect(onAnswer).toHaveBeenLastCalledWith("replace", false);
  });

  it("shows the remember checkbox only in a batch, and passes it with the answer", () => {
    const onAnswer = vi.fn();
    const { unmount } = render(<Conflict name="a" onAnswer={onAnswer} onCancel={() => {}} />);
    expect(screen.queryByRole("checkbox")).toBeNull();
    unmount();
    render(<Conflict name="a" batch onAnswer={onAnswer} onCancel={() => {}} />);
    fireEvent.click(screen.getByRole("checkbox"));
    fireEvent.click(screen.getByRole("button", { name: "Skip it" }));
    expect(onAnswer).toHaveBeenLastCalledWith("skip", true);
  });

  it("gives focus to replace when it opens", async () => {
    render(<Conflict name="a" onAnswer={() => {}} onCancel={() => {}} />);
    const replace = screen.getByRole("button", { name: "Replace it" });
    await waitFor(() => expect(document.activeElement).toBe(replace), { timeout: 3000 });
  });

  it("closing the dialog calls onCancel", () => {
    const onCancel = vi.fn();
    render(<Conflict name="a" onAnswer={() => {}} onCancel={onCancel} />);
    fireEvent.click(document.querySelector(".ant-modal-close") as Element);
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});

describe("Conflict mask", () => {
  it("a click outside the dialog does not answer skip or cancel it", () => {
    const onAnswer = vi.fn();
    const onCancel = vi.fn();
    render(<Conflict name="a" onAnswer={onAnswer} onCancel={onCancel} />);
    const mask = document.querySelector(".ant-modal-wrap") as HTMLElement;
    fireEvent.mouseDown(mask);
    fireEvent.mouseUp(mask);
    fireEvent.click(mask);
    expect(onAnswer).not.toHaveBeenCalled();
    expect(onCancel).not.toHaveBeenCalled();
  });
});
