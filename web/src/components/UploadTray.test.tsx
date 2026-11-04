// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../lib/apiError";
import { conflictAsk, uploads, type UploadItem } from "../lib/files/uploads";

const manager = {
  pause: vi.fn(),
  resume: vi.fn(),
  retry: vi.fn(),
  cancel: vi.fn(async () => {}),
  dismiss: vi.fn(),
  clearFinished: vi.fn(),
  commitName: vi.fn(async () => {}),
  attach: vi.fn(async () => true),
  answerConflict: vi.fn(),
};
vi.mock("../lib/files/uploads", async (orig) => ({
  ...(await orig<typeof import("../lib/files/uploads")>()),
  manager: new Proxy({}, { get: (_t, key: string) => (manager as Record<string, unknown>)[key] }),
}));

import UploadTray from "./UploadTray";

function item(over: Partial<UploadItem>): UploadItem {
  return {
    id: 1,
    name: "report.pdf",
    size: 1000,
    mime: "application/pdf",
    lastModified: 0,
    target: { folder: "f1" },
    status: "queued",
    offset: 0,
    speed: 0,
    onConflict: "fail",
    url: null,
    nodeID: null,
    error: null,
    suggested: "report (1).pdf",
    head: "a".repeat(64),
    ...over,
  };
}

describe("UploadTray icon buttons name their file", () => {
  beforeEach(() => {
    conflictAsk.set(null);
    uploads.set([]);
  });
  afterEach(() => {
    uploads.set([]);
    conflictAsk.set(null);
  });

  it("each row's Pause, Cancel, Retry and Dismiss name the file, so the rows can be told apart", () => {
    uploads.set([
      item({ id: 1, name: "a.bin", status: "queued" }),
      item({ id: 2, name: "b.bin", status: "failed" }),
      item({ id: 3, name: "c.bin", status: "done" }),
    ]);
    render(<UploadTray />);
    expect(screen.getByRole("button", { name: "Pause: a.bin" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Cancel: a.bin" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Retry: b.bin" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Dismiss: c.bin" })).toBeTruthy();
  });
});

describe("UploadTray", () => {
  beforeEach(() => {
    for (const fn of Object.values(manager)) fn.mockClear();
    conflictAsk.set(null);
    uploads.set([]);
  });
  afterEach(() => {
    uploads.set([]);
    conflictAsk.set(null);
  });

  it("shows a queued upload with its status and a pause button that calls the manager", () => {
    uploads.set([item({ id: 7, status: "queued" })]);
    render(<UploadTray />);
    expect(screen.getByText("Waiting in line")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /^Pause/ }));
    expect(manager.pause).toHaveBeenCalledWith(7);
  });

  it("shows a failed upload's error by its code, never server text, and offers retry", () => {
    uploads.set([item({ id: 2, status: "failed", error: new ApiError(413, "too_large", { message: "server text" }, 0) })]);
    render(<UploadTray />);
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toBe("The file is too large.");
    expect(screen.queryByText("server text")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /^Retry/ }));
    expect(manager.retry).toHaveBeenCalledWith(2);
  });

  it("asks for another name when every byte arrived, and saves the typed name", () => {
    uploads.set([item({ id: 3, status: "awaiting_name" })]);
    render(<UploadTray />);
    const input = screen.getByLabelText(/already here/) as HTMLInputElement;
    expect(input.value).toBe("report (1).pdf");
    fireEvent.change(input, { target: { value: "report final.pdf" } });
    fireEvent.click(screen.getByRole("button", { name: "Save name" }));
    expect(manager.commitName).toHaveBeenCalledWith(3, "report final.pdf");
  });

  it("offers to choose the file again for an interrupted upload", () => {
    uploads.set([item({ id: 4, status: "interrupted" })]);
    render(<UploadTray />);
    expect(screen.getByRole("button", { name: "Choose the file to resume" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^Pause/ })).toBeNull();
  });

  it("a done upload has no progress bar and can be dismissed", () => {
    uploads.set([item({ id: 5, status: "done", offset: 1000 })]);
    const { container } = render(<UploadTray />);
    // Every upload has ended, so the tray has folded itself: its list opens again on the person's request.
    fireEvent.click(screen.getByRole("button", { name: "Show" }));
    expect(container.querySelector("progress")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /^Dismiss/ }));
    expect(manager.dismiss).toHaveBeenCalledWith(5);
  });

  it("collapses the list to its header and back", () => {
    uploads.set([item({ id: 6, status: "uploading", offset: 500 })]);
    render(<UploadTray />);
    expect(screen.getByRole("list")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Hide" }));
    expect(screen.queryByRole("list")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Show" }));
    expect(within(screen.getByRole("list")).getByText("report.pdf")).toBeTruthy();
  });

  it("answers a taken name through the conflict dialog, batch only with more than one active upload", () => {
    uploads.set([item({ id: 8, status: "conflict" }), item({ id: 9, status: "queued" })]);
    conflictAsk.set("report.pdf");
    render(<UploadTray />);
    expect(screen.getByRole("dialog").textContent).toContain("report.pdf");
    expect(screen.getByRole("checkbox")).toBeTruthy();
    fireEvent.click(screen.getByRole("checkbox"));
    fireEvent.click(screen.getByRole("button", { name: "Keep both" }));
    expect(manager.answerConflict).toHaveBeenLastCalledWith("rename", true);
  });
});

describe("UploadTray presence and heading", () => {
  beforeEach(() => {
    conflictAsk.set(null);
    uploads.set([]);
  });

  it("renders nothing when there are no uploads and no conflict to answer", () => {
    const { container } = render(<UploadTray />);
    expect(container.firstChild).toBeNull();
  });

  it("names its region by an h2 heading", () => {
    uploads.set([item({ id: 1 })]);
    render(<UploadTray />);
    const heading = screen.getByRole("heading", { level: 2, name: "Uploads" });
    const region = screen.getByRole("region", { name: "Uploads" });
    expect(region.getAttribute("aria-labelledby")).toBe(heading.id);
  });
});

describe("UploadTray cancel", () => {
  beforeEach(() => {
    conflictAsk.set(null);
    uploads.set([]);
  });

  it("offers cancel only for an upload still in progress, never for a finished one", () => {
    uploads.set([item({ id: 1, status: "done", name: "done.pdf" })]);
    render(<UploadTray />);
    expect(screen.queryByRole("button", { name: /^Cancel/ })).toBeNull();
  });
});

// The tray folds itself away when every upload has ended, and an error keeps it open, so a failure is seen.
describe("UploadTray by itself", () => {
  beforeEach(() => {
    conflictAsk.set(null);
    uploads.set([]);
  });

  it("collapses to its header when the last upload ends", () => {
    uploads.set([item({ id: 1, status: "uploading", offset: 500 })]);
    render(<UploadTray />);
    expect(screen.getByRole("list")).toBeTruthy();
    act(() => uploads.set([item({ id: 1, status: "done" })]));
    expect(screen.queryByRole("list")).toBeNull();
    expect(screen.getByRole("button", { name: "Show" })).toBeTruthy();
  });

  it("stays open when an upload failed, and opens again when a failure comes after the person folded it", () => {
    uploads.set([item({ id: 1, status: "uploading", offset: 500 }), item({ id: 2, name: "b.pdf", status: "uploading", offset: 0 })]);
    render(<UploadTray />);
    act(() => uploads.set([item({ id: 1, status: "done" }), item({ id: 2, name: "b.pdf", status: "uploading", offset: 0 })]));
    fireEvent.click(screen.getByRole("button", { name: "Hide" }));
    expect(screen.queryByRole("list")).toBeNull();
    act(() => uploads.set([item({ id: 1, status: "done" }), item({ id: 2, name: "b.pdf", status: "failed", error: null })]));
    expect(screen.getByRole("list")).toBeTruthy();
  });

  it("reopens when a new upload arrives after the tray folded", () => {
    uploads.set([item({ id: 1, status: "done" })]);
    render(<UploadTray />);
    expect(screen.queryByRole("list")).toBeNull();
    act(() => uploads.set([item({ id: 1, status: "done" }), item({ id: 2, name: "c.pdf", status: "queued", offset: 0 })]));
    expect(screen.getByRole("list")).toBeTruthy();
  });

  it.each(["awaiting_name", "paused", "interrupted", "waiting"] as const)("reopens when a running upload becomes %s, after the person folded it", (status) => {
    uploads.set([item({ id: 1, status: "uploading", offset: 500 }), item({ id: 2, name: "b.pdf", status: "uploading", offset: 0 })]);
    render(<UploadTray />);
    fireEvent.click(screen.getByRole("button", { name: "Hide" }));
    expect(screen.queryByRole("list")).toBeNull();
    act(() => uploads.set([item({ id: 1, status: "uploading", offset: 500 }), item({ id: 2, name: "b.pdf", status, offset: 0 })]));
    expect(screen.getByRole("list")).toBeTruthy();
  });

  it("a failure among the uploads keeps the tray open when the others end", () => {
    uploads.set([item({ id: 1, status: "uploading", offset: 500 }), item({ id: 2, name: "b.pdf", status: "uploading", offset: 0 })]);
    render(<UploadTray />);
    act(() => uploads.set([item({ id: 1, status: "failed", error: null }), item({ id: 2, name: "b.pdf", status: "done" })]));
    expect(screen.getByRole("list")).toBeTruthy();
  });
});
