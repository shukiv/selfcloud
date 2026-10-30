// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import UndoToast from "./UndoToast";

describe("UndoToast", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("offers undo and runs it once", () => {
    const onUndo = vi.fn();
    render(<UndoToast label="Moved." expiresAt={Date.now() + 8000} onUndo={onUndo} onExpire={() => {}} />);
    expect(screen.getByText("Moved.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    expect(onUndo).toHaveBeenCalledTimes(1);
  });

  it("shows the status in place of the label and hides undo", () => {
    render(<UndoToast label="Moved." status="Undone." expiresAt={Date.now() + 4000} onUndo={() => {}} onExpire={() => {}} />);
    expect(screen.getByText("Undone.")).toBeTruthy();
    expect(screen.queryByText("Moved.")).toBeNull();
    expect(screen.queryByRole("button", { name: "Undo" })).toBeNull();
  });

  it("calls onExpire once the window closes, and dismiss calls it at once", () => {
    const onExpire = vi.fn();
    render(<UndoToast label="Deleted." expiresAt={Date.now() + 1000} onUndo={() => {}} onExpire={onExpire} />);
    act(() => {
      vi.advanceTimersByTime(900);
    });
    expect(onExpire).not.toHaveBeenCalled();
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(onExpire).toHaveBeenCalled();
  });

  it("dismiss button is labelled and calls onExpire", () => {
    const onExpire = vi.fn();
    render(<UndoToast label="Deleted." expiresAt={Date.now() + 8000} onUndo={() => {}} onExpire={onExpire} />);
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(onExpire).toHaveBeenCalledTimes(1);
  });
});
