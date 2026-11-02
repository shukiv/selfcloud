// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { conflictAsk, uploads, type UploadItem } from "../lib/files/uploads";
import UploadTrayHost from "./UploadTrayHost";

// The host loads the tray only once an upload or a conflict question exists. These tests run the real tray.
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

describe("the upload tray's host", () => {
  beforeEach(() => {
    conflictAsk.set(null);
    uploads.set([]);
  });
  afterEach(() => {
    uploads.set([]);
    conflictAsk.set(null);
  });

  it("draws nothing while there is no upload and no conflict", () => {
    const { container } = render(<UploadTrayHost />);
    expect(container.textContent).toBe("");
  });

  it("shows the tray once an upload exists, after its code loads", async () => {
    uploads.set([item({ id: 7, status: "queued" })]);
    render(<UploadTrayHost />);
    expect(await screen.findByText("Waiting in line")).toBeTruthy();
  });

  it("shows the conflict question once it is asked, after its code loads", async () => {
    uploads.set([item({ id: 8, status: "conflict" })]);
    conflictAsk.set("report.pdf");
    render(<UploadTrayHost />);
    expect((await screen.findByRole("dialog")).textContent).toContain("report.pdf");
  });
});
