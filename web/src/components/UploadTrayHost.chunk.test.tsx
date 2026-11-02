// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { uploads, type UploadItem } from "../lib/files/uploads";

// A tray whose chunk is gone (an open tab after a deploy has new file names) throws where it renders. The host shows a
// message and a reload in its place and the rest of the page stays; the page's own error boundary is not reached.
vi.mock("./UploadTray", () => ({
  default: function BrokenTray(): never {
    throw new Error("chunk did not load");
  },
}));

import UploadTrayHost from "./UploadTrayHost";

describe("the upload tray's host when its chunk does not load", () => {
  beforeEach(() => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
  });
  afterEach(() => {
    uploads.set([]);
    vi.restoreAllMocks();
  });

  it("keeps the rest of the page and says so where the tray was, with a reload", async () => {
    const queued: UploadItem = {
      id: 3,
      name: "a.pdf",
      size: 1,
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
      suggested: "a (1).pdf",
      head: "a".repeat(64),
    };
    uploads.set([queued]);
    render(
      <div>
        <span>page</span>
        <UploadTrayHost />
        <span>after</span>
      </div>,
    );
    // The tray loads after the first render, so its failure is seen once the load settles.
    await waitFor(() => expect(console.error).toHaveBeenCalled());
    expect(screen.getByText("page")).toBeTruthy();
    expect(screen.getByText("after")).toBeTruthy();
    expect(screen.queryByText("Waiting in line")).toBeNull();
    // Without the tray nobody could answer a conflict question or follow an upload, so the failure is shown.
    expect((await screen.findByRole("alert")).textContent).toContain("Something went wrong");
    expect(screen.getByRole("button", { name: "Reload" })).toBeTruthy();
  });
});
