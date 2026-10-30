// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, within } from "@testing-library/react";
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
    fireEvent.click(screen.getByRole("button", { name: "Pause" }));
    expect(manager.pause).toHaveBeenCalledWith(7);
  });

  it("shows a failed upload's error by its code, never server text, and offers retry", () => {
    uploads.set([item({ id: 2, status: "failed", error: new ApiError(413, "too_large", { message: "server text" }, 0) })]);
    render(<UploadTray />);
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toBe("The file is too large.");
    expect(screen.queryByText("server text")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
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
    expect(screen.queryByRole("button", { name: "Pause" })).toBeNull();
  });

  it("a done upload has no progress bar and can be dismissed", () => {
    uploads.set([item({ id: 5, status: "done", offset: 1000 })]);
    const { container } = render(<UploadTray />);
    expect(container.querySelector("progress")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
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
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
  });
});
