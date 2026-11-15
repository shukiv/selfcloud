// SPDX-License-Identifier: AGPL-3.0-or-later

// Download all on the share page of a link that anyone can open (pz1b): the folder on screen is zipped by the link,
// one zip for every visitor of it. The button asks once, waits on the server's Retry-After while the zip is made, and
// downloads through the signed link. A refusal is named by its code, never by the server's text, and leaving the
// folder or the page stops the wait.

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/apiError";
import { translate } from "../../lib/i18n";
import type { Node } from "../../lib/files/types";

const sharingApi = vi.hoisted(() => ({
  visitorNode: vi.fn(),
  visitorChildren: vi.fn(),
  visitorLink: vi.fn(),
  visitorZip: vi.fn(),
  visitorZipLink: vi.fn(),
}));
vi.mock("../../lib/sharing/api", async (orig) => ({ ...(await orig<typeof import("../../lib/sharing/api")>()), ...sharingApi }));

const filesApi = vi.hoisted(() => ({
  statNode: vi.fn(),
  childrenPage: vi.fn(),
  signedLink: vi.fn(),
}));
vi.mock("../../lib/files/api", async (orig) => ({ ...(await orig<typeof import("../../lib/files/api")>()), ...filesApi }));

import PublicBrowse, { sessionSource, visitorSource } from "./PublicBrowse";

function dirNode(id: string, name: string): Node {
  return {
    id,
    folder_id: "fld_a",
    parent_id: null,
    name,
    kind: "dir",
    size: 0,
    mime_type: "",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "viewer",
    no_download: false,
    editors_can_share: false,
  } as unknown as Node;
}

function textFile(id: string, name: string): Node {
  return { ...dirNode(id, name), kind: "file", mime_type: "text/plain", size: 10 } as unknown as Node;
}

// captureClicks records the addresses the page's browser links start, in place of a real download.
function captureClicks(): { urls: string[]; restore: () => void } {
  const urls: string[] = [];
  const real = HTMLAnchorElement.prototype.click;
  HTMLAnchorElement.prototype.click = function () {
    urls.push(this.href);
  };
  return { urls, restore: () => { HTMLAnchorElement.prototype.click = real; } };
}

const ZIP_URL = "https://content.example/zip";
const notReady = (retryAfter: number) => new ApiError(409, "conflict", { reason: "zip_not_ready" }, retryAfter);
const readyLink = () => ({ url: ZIP_URL, expires_at: new Date(Date.now() + 600_000).toISOString() });

beforeEach(() => {
  sharingApi.visitorNode.mockImplementation(async (_t: string, id: string) => (id === "nod_root" ? dirNode("nod_root", "Plans") : dirNode(id, "Notes")));
  sharingApi.visitorChildren.mockImplementation(async (_t: string, id: string) => ({
    items: id === "nod_root" ? [dirNode("nod_child", "Notes")] : [],
    next_cursor: null,
  }));
  sharingApi.visitorZip.mockResolvedValue("zip_1");
  sharingApi.visitorZipLink.mockResolvedValue(readyLink());
});

afterEach(() => {
  vi.clearAllMocks();
  vi.useRealTimers();
});

describe("the Download all button", () => {
  it("shows on the folder the visitor opens, and only the visitor source has it", async () => {
    expect(visitorSource("tok_v").zip).toBeTypeOf("function");
    expect(sessionSource().zip).toBeUndefined();
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    expect(await screen.findByRole("button", { name: "Download all" })).toBeTruthy();
    expect(sharingApi.visitorZip).not.toHaveBeenCalled();
  });

  it("is absent on a file link", async () => {
    sharingApi.visitorNode.mockResolvedValue(textFile("nod_file", "notes.txt"));
    sharingApi.visitorLink.mockResolvedValue({ url: "https://content.example/notes", expires_at: new Date(Date.now() + 300_000).toISOString() });
    render(<PublicBrowse token="tok_v" root="nod_file" onlost={vi.fn()} />);
    expect(await screen.findByText("Download")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Download all" })).toBeNull();
  });

  it("is absent with the signed-in source", async () => {
    filesApi.statNode.mockResolvedValue(dirNode("nod_root", "Plans"));
    filesApi.childrenPage.mockResolvedValue({ items: [], next_cursor: null });
    render(<PublicBrowse token="" root="nod_root" source={sessionSource()} onlost={vi.fn()} />);
    expect(await screen.findByRole("heading", { name: "Plans" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Download all" })).toBeNull();
  });
});

describe("making the zip", () => {
  it("one click creates one zip, and a double click no more, then downloads the signed link", async () => {
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    const button = await screen.findByRole("button", { name: "Download all" });
    const clicks = captureClicks();
    vi.useFakeTimers();
    try {
      fireEvent.click(button);
      fireEvent.click(button);
      await act(async () => { await vi.advanceTimersByTimeAsync(0); });
      expect(sharingApi.visitorZip).toHaveBeenCalledTimes(1);
      expect(sharingApi.visitorZip.mock.calls[0][0]).toBe("tok_v");
      expect(sharingApi.visitorZip.mock.calls[0][1]).toBe("nod_root");
      expect(sharingApi.visitorZipLink).toHaveBeenCalledTimes(1);
      expect(sharingApi.visitorZipLink.mock.calls[0][0]).toBe("tok_v");
      expect(sharingApi.visitorZipLink.mock.calls[0][1]).toBe("zip_1");
      expect(clicks.urls).toEqual([ZIP_URL]);
      expect(screen.getByRole("status").textContent).toContain(translate("en", "share.zipReady"));
      expect(screen.getByRole("button", { name: "Download again" })).toBeTruthy();
    } finally {
      clicks.restore();
    }
  });

  it("waits the server's Retry-After between asks, clamped to 1..10 seconds, and shows the making line meanwhile", async () => {
    sharingApi.visitorZipLink
      .mockRejectedValueOnce(notReady(2))
      .mockRejectedValueOnce(notReady(30))
      .mockResolvedValueOnce(readyLink());
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    const button = await screen.findByRole("button", { name: "Download all" });
    const clicks = captureClicks();
    vi.useFakeTimers();
    try {
      fireEvent.click(button);
      await act(async () => { await vi.advanceTimersByTimeAsync(0); });
      expect(sharingApi.visitorZipLink).toHaveBeenCalledTimes(1);
      expect(screen.getByRole("status").textContent).toContain(translate("en", "share.zipMaking"));
      expect(screen.getByRole("button", { name: "Download all" })).toBeDisabled();
      expect(clicks.urls).toEqual([]);
      // The first wait is the server's two seconds.
      await act(async () => { await vi.advanceTimersByTimeAsync(1999); });
      expect(sharingApi.visitorZipLink).toHaveBeenCalledTimes(1);
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(sharingApi.visitorZipLink).toHaveBeenCalledTimes(2);
      // The second wait is thirty seconds clamped to ten.
      await act(async () => { await vi.advanceTimersByTimeAsync(9999); });
      expect(sharingApi.visitorZipLink).toHaveBeenCalledTimes(2);
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(sharingApi.visitorZipLink).toHaveBeenCalledTimes(3);
      expect(clicks.urls).toEqual([ZIP_URL]);
      expect(screen.getByRole("status").textContent).toContain(translate("en", "share.zipReady"));
    } finally {
      clicks.restore();
    }
  });

  it("Download again reuses the signed link until it expires, then asks for a new one", async () => {
    sharingApi.visitorZipLink.mockResolvedValueOnce(readyLink()).mockResolvedValueOnce({ url: "https://content.example/zip2", expires_at: new Date(Date.now() + 1_200_000).toISOString() });
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    const button = await screen.findByRole("button", { name: "Download all" });
    const clicks = captureClicks();
    vi.useFakeTimers();
    try {
      fireEvent.click(button);
      await act(async () => { await vi.advanceTimersByTimeAsync(0); });
      expect(clicks.urls).toEqual([ZIP_URL]);
      fireEvent.click(screen.getByRole("button", { name: "Download again" }));
      expect(clicks.urls).toEqual([ZIP_URL, ZIP_URL]);
      expect(sharingApi.visitorZip).toHaveBeenCalledTimes(1);
      // Past the link's end, the next ask makes a new link (the server reuses the link's zip).
      vi.setSystemTime(Date.now() + 601_000);
      fireEvent.click(screen.getByRole("button", { name: "Download again" }));
      await act(async () => { await vi.advanceTimersByTimeAsync(0); });
      expect(sharingApi.visitorZip).toHaveBeenCalledTimes(2);
      expect(clicks.urls).toEqual([ZIP_URL, ZIP_URL, "https://content.example/zip2"]);
    } finally {
      clicks.restore();
    }
  });
});

describe("a refused zip", () => {
  const refusals: Array<[string, ApiError, string]> = [
    ["download_restricted", new ApiError(403, "download_restricted", {}, 0), "files.err.downloadRestricted"],
    ["too_many_zips_link", new ApiError(409, "conflict", { reason: "too_many_zips_link" }, 0), "files.zip.linkBusy"],
    ["the visitor caps", new ApiError(409, "conflict", { reason: "too_many_zips_account" }, 0), "files.zip.visitorLimit"],
    ["the address rate", new ApiError(429, "rate_limited", {}, 30), "files.zip.slowDown"],
    ["too_large", new ApiError(413, "too_large", {}, 0), "files.zip.visitorLimit"],
    ["storage_full", new ApiError(507, "storage_full", { reason: "zip_disk" }, 0), "files.zip.storage"],
  ];
  it.each(refusals)("%s shows its own text, and no download starts", async (_name, err, key) => {
    sharingApi.visitorZip.mockRejectedValueOnce(err);
    const clicks = captureClicks();
    try {
      render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
      fireEvent.click(await screen.findByRole("button", { name: "Download all" }));
      await waitFor(() => expect(screen.getByRole("alert").textContent).toBe(translate("en", key)));
      expect(clicks.urls).toEqual([]);
      expect(sharingApi.visitorZipLink).not.toHaveBeenCalled();
      if (err.code === "download_restricted") {
        // Downloads are off for this link now: the button goes with the refusal.
        expect(screen.queryByRole("button", { name: "Download all" })).toBeNull();
      }
    } finally {
      clicks.restore();
    }
  });

  it("the refusal alert can be closed", async () => {
    sharingApi.visitorZip.mockRejectedValueOnce(new ApiError(409, "conflict", { reason: "too_many_zips_link" }, 0));
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "Download all" }));
    await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  });

  it("zip_failed stops the wait with the failed text", async () => {
    sharingApi.visitorZipLink.mockRejectedValue(new ApiError(409, "conflict", { reason: "zip_failed" }, 0));
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    const button = await screen.findByRole("button", { name: "Download all" });
    vi.useFakeTimers();
    fireEvent.click(button);
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(screen.getByRole("alert").textContent).toBe(translate("en", "files.zip.failed"));
    await act(async () => { await vi.advanceTimersByTimeAsync(60 * 60_000); });
    expect(sharingApi.visitorZipLink).toHaveBeenCalledTimes(1);
  });

  it.each([
    [401, "expired"],
    [404, "gone"],
  ] as Array<[number, "expired" | "gone"]>)("a %s goes to the page's lost handling", async (status, why) => {
    const onlost = vi.fn();
    sharingApi.visitorZip.mockRejectedValueOnce(new ApiError(status, status === 401 ? "unauthenticated" : "not_found", {}, 0));
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={onlost} />);
    fireEvent.click(await screen.findByRole("button", { name: "Download all" }));
    await waitFor(() => expect(onlost).toHaveBeenCalledWith(why));
    expect(screen.queryByRole("alert")).toBeNull();
  });
});

describe("leaving while the zip is made", () => {
  it("another folder stops the asks, and shows no status of the old one", async () => {
    sharingApi.visitorZipLink.mockRejectedValue(notReady(2));
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    const button = await screen.findByRole("button", { name: "Download all" });
    vi.useFakeTimers();
    fireEvent.click(button);
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(sharingApi.visitorZipLink).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("row", { name: "Notes" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(screen.getByRole("heading", { name: "Notes" })).toBeTruthy();
    await act(async () => { await vi.advanceTimersByTimeAsync(60 * 60_000); });
    expect(sharingApi.visitorZipLink).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("leaving the page stops the asks", async () => {
    sharingApi.visitorZipLink.mockRejectedValue(notReady(2));
    const view = render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    const button = await screen.findByRole("button", { name: "Download all" });
    vi.useFakeTimers();
    fireEvent.click(button);
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(sharingApi.visitorZipLink).toHaveBeenCalledTimes(1);
    view.unmount();
    await vi.advanceTimersByTimeAsync(60 * 60_000);
    expect(sharingApi.visitorZipLink).toHaveBeenCalledTimes(1);
  });
});
