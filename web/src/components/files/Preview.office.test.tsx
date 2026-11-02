// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { officeConvert } from "../../lib/files/convert";
import type { Node } from "../../lib/files/types";
import Preview from "./Preview";

// The office card of the viewer (decision 59): the Preview button and the Export menu appear only while the server offers
// conversion, and every state of the preview shows its own text. The browser's fetch is replaced, so the answers are the test's.

const DOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document";

function file(name: string, mime: string, extra: Partial<Node> = {}): Node {
  return {
    id: `n-${name}`,
    folder_id: "f1",
    parent_id: null,
    name,
    kind: "file",
    size: 10,
    mime_type: mime,
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "editor",
    no_download: false,
    editors_can_share: false,
    ...extra,
  } as Node;
}

// answer makes a Response-like object for the watch: the status, the Retry-After header and, for a refusal, the reason.
function answer(status: number, extra: { retryAfter?: string; reason?: string; type?: string } = {}) {
  const headers = new Headers();
  if (extra.retryAfter) headers.set("Retry-After", extra.retryAfter);
  const body = extra.reason ? { error: { code: "unsupported_media_type", details: { reason: extra.reason } } } : {};
  return {
    status,
    type: extra.type ?? "basic",
    headers,
    body: null,
    json: async () => body,
  } as unknown as Response;
}

describe("Preview, an office document", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    officeConvert.set(false);
  });

  afterEach(() => {
    officeConvert.set(false);
    vi.unstubAllGlobals();
  });

  it("shows no preview button and no export menu while the server does not offer conversion", () => {
    render(<Preview items={[file("letter.docx", DOCX)]} index={0} onClose={() => {}} onIndex={() => {}} />);
    expect(screen.getByText("No preview for this file. Download it to open it.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Preview" })).toBeNull();
    expect(screen.queryByRole("button", { name: /Export/ })).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("offers the preview and asks for nothing until the person asks", () => {
    officeConvert.set(true);
    render(<Preview items={[file("letter.docx", DOCX)]} index={0} onClose={() => {}} onIndex={() => {}} />);
    expect(screen.getByRole("button", { name: "Preview" })).toBeTruthy();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("says the conversion failed on the first answer, and asks once", async () => {
    officeConvert.set(true);
    fetchMock.mockResolvedValueOnce(answer(415, { reason: "failed" }));
    render(<Preview items={[file("letter.docx", DOCX)]} index={0} onClose={() => {}} onIndex={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    expect(await screen.findByText("The conversion failed, so this copy cannot be made.")).toBeTruthy();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0][1]).toMatchObject({ credentials: "same-origin", redirect: "manual" });
  });

  it("says the file type cannot be converted", async () => {
    officeConvert.set(true);
    fetchMock.mockResolvedValueOnce(answer(415, { reason: "not_convertible" }));
    render(<Preview items={[file("old.doc", "application/msword")]} index={0} onClose={() => {}} onIndex={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    expect(await screen.findByText("This type of file cannot be converted.")).toBeTruthy();
  });

  it("shows the PDF in a frame at the preview's own address once the server has it", async () => {
    officeConvert.set(true);
    fetchMock.mockResolvedValueOnce(answer(202, { retryAfter: "1" })).mockResolvedValueOnce(answer(200));
    render(<Preview items={[file("letter.docx", DOCX)]} index={0} onClose={() => {}} onIndex={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    await waitFor(() => expect(document.querySelector("iframe")).not.toBeNull(), { timeout: 4000 });
    expect(document.querySelector("iframe")?.getAttribute("src")).toBe("/api/v1/nodes/n-letter.docx/preview?format=pdf");
    expect(fetchMock.mock.calls.map((c) => (c[1] as RequestInit).method ?? "GET")).toEqual(["GET", "HEAD"]);
  });

  // Returning to a file that was left while its preview was being made: the watch was stopped when the viewer left it, so the
  // card must offer the Preview button again, not show a wait that nothing is making.
  it("returns to a file left mid-conversion with the Preview button, not a stale wait", async () => {
    officeConvert.set(true);
    fetchMock.mockResolvedValue(answer(202));
    const a = file("letter.docx", DOCX);
    const b = file("other.docx", DOCX);
    const { rerender } = render(<Preview items={[a, b]} index={0} onClose={() => {}} onIndex={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    rerender(<Preview items={[a, b]} index={1} onClose={() => {}} onIndex={() => {}} />);
    rerender(<Preview items={[a, b]} index={0} onClose={() => {}} onIndex={() => {}} />);
    expect(await screen.findByRole("button", { name: "Preview" })).toBeTruthy();
    expect(screen.queryByText("Making the copy. A long document can take a minute.")).toBeNull();
  });

  it("offers the export formats of the document's family, and starts the chosen one", async () => {
    officeConvert.set(true);
    render(<Preview items={[file("letter.docx", DOCX)]} index={0} onClose={() => {}} onIndex={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: /Export/ }));
    const items = await screen.findAllByRole("menuitem");
    expect(items.map((i) => i.textContent?.trim())).toEqual(["PDF", "DOCX", "ODT"]);
    fetchMock.mockResolvedValueOnce({ ok: true, status: 200, json: async () => ({ id: "drv_9", state: "pending" }) });
    fetchMock.mockResolvedValue(answer(202));
    await act(async () => {
      fireEvent.click(items[1]);
    });
    expect(await screen.findByText("Export as DOCX")).toBeTruthy();
    const post = fetchMock.mock.calls[0];
    expect(post[0]).toBe("/api/v1/nodes/n-letter.docx/exports");
    expect((post[1] as RequestInit).method).toBe("POST");
    expect((post[1] as RequestInit).body).toBe(JSON.stringify({ format: "docx" }));
  });

  it("offers no preview and no export to a viewer whose downloads are off", () => {
    officeConvert.set(true);
    render(<Preview items={[file("letter.docx", DOCX, { role: "viewer", no_download: true })]} index={0} onClose={() => {}} onIndex={() => {}} />);
    expect(screen.queryByRole("button", { name: "Preview" })).toBeNull();
    expect(screen.queryByRole("button", { name: /Export/ })).toBeNull();
    expect(screen.getByText("Downloads are off for this file.")).toBeTruthy();
  });
});
