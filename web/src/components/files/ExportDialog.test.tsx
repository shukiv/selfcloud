// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ExportDialog from "./ExportDialog";

// The export's requests and the download are replaced: each test says what the server answers, and whether the download
// starts. The dialog's own logic is what is tested.
const post = vi.fn();
const head = vi.fn();
const download = vi.fn();
vi.mock("../../lib/files/convertLive", () => ({
  liveExportDeps: () => ({
    post: (...args: unknown[]) => post(...args),
    head: (...args: unknown[]) => head(...args),
    schedule: (fn: () => void) => setTimeout(fn, 0),
    cancel: () => undefined,
  }),
  downloadExport: (...args: unknown[]) => download(...args),
}));

describe("ExportDialog", () => {
  beforeEach(() => {
    post.mockReset();
    head.mockReset();
    download.mockReset();
    post.mockResolvedValue({ id: "drv_1" });
  });

  // A download that cannot start is not a success: the dialog says so, and offers to try again.
  it("shows an error, not the ready text, when the download cannot start, and retries it", async () => {
    head.mockResolvedValue({ status: 200 });
    download.mockRejectedValueOnce(new TypeError("offline")).mockResolvedValueOnce(true);
    render(<ExportDialog nod="nod_1" format="odt" onClose={() => {}} />);
    expect(await screen.findByText("Something went wrong. Try again.")).toBeTruthy();
    expect(screen.queryByText("The copy is ready and the download has started. If it did not start, use Download again.")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Download again" }));
    await waitFor(() => expect(download).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByText("Something went wrong. Try again.")).toBeNull());
  });

  // An outage the server names with Retry-After is shown with a retry, as the viewer's preview shows one.
  it("gives an outage a retry that asks the server again", async () => {
    head.mockResolvedValueOnce({ status: 503, retryAfter: 60 }).mockResolvedValue({ status: 200 });
    download.mockResolvedValue(true);
    render(<ExportDialog nod="nod_1" format="odt" onClose={() => {}} />);
    expect(await screen.findByText("The converter is not available right now. Try again in a minute.")).toBeTruthy();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    });
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(download).toHaveBeenCalledTimes(1));
  });
});
