// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Node } from "../../lib/files/types";

const signedLink = vi.fn();
const startDownload = vi.fn();
vi.mock("../../lib/files/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/files/api")>()),
  signedLink: (...args: unknown[]) => signedLink(...args),
}));
vi.mock("../../lib/files/ops", async (orig) => ({
  ...(await orig<typeof import("../../lib/files/ops")>()),
  startDownload: (...args: unknown[]) => startDownload(...args),
}));

import Preview from "./Preview";

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
  };
}

const a = file("a.png", "image/png");
const notes = file("notes.txt", "text/plain");
const b = file("b.png", "image/png");
const c = file("c.png", "image/png");

const stage = () => document.querySelector(".lightbox-stage") as HTMLElement;

describe("the image lightbox", () => {
  beforeEach(() => {
    signedLink.mockReset();
    startDownload.mockReset();
    signedLink.mockResolvedValue({ url: "https://content.example/x?sig=1", disposition: "inline", expires_at: "" });
    startDownload.mockResolvedValue(true);
  });
  afterEach(() => {
    document.documentElement.dir = "ltr";
  });

  it("shows an image in the lightbox, with its name, and no page chrome", async () => {
    render(<Preview items={[a]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await screen.findByRole("img", { name: "a.png" });
    expect(stage()).not.toBeNull();
    expect(stage().textContent).toContain("a.png");
    expect(screen.getByRole("button", { name: "Close" })).toBeTruthy();
  });

  it("counts and steps through the images only, passing over other files", async () => {
    const onIndex = vi.fn();
    render(<Preview items={[a, notes, b, c]} index={0} onClose={() => {}} onIndex={onIndex} />);
    await screen.findByRole("img", { name: "a.png" });
    expect(stage().textContent).toContain("1 of 3");
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    // The next image is the third item: the text file between them is not a step.
    expect(onIndex).toHaveBeenLastCalledWith(2);
  });

  it("disables the step that leads nowhere", async () => {
    render(<Preview items={[a, b]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await screen.findByRole("img", { name: "a.png" });
    expect((screen.getByRole("button", { name: "Previous" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Next" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("has no count and no steps for a single image", async () => {
    render(<Preview items={[a, notes]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await screen.findByRole("img", { name: "a.png" });
    expect(screen.queryByRole("button", { name: "Next" })).toBeNull();
    expect(stage().textContent).not.toContain("1 of 1");
  });

  it("steps with the arrow keys, mirrored in right to left", async () => {
    const onIndex = vi.fn();
    const first = render(<Preview items={[a, notes, b]} index={0} onClose={() => {}} onIndex={onIndex} />);
    await screen.findByRole("img", { name: "a.png" });
    fireEvent.keyDown(stage(), { key: "ArrowRight" });
    expect(onIndex).toHaveBeenLastCalledWith(2);
    first.unmount();
    onIndex.mockClear();
    document.documentElement.dir = "rtl";
    render(<Preview items={[a, notes, b]} index={0} onClose={() => {}} onIndex={onIndex} />);
    await screen.findByRole("img", { name: "a.png" });
    fireEvent.keyDown(stage(), { key: "ArrowRight" });
    expect(onIndex).not.toHaveBeenCalled();
    fireEvent.keyDown(stage(), { key: "ArrowLeft" });
    expect(onIndex).toHaveBeenLastCalledWith(2);
  });

  it("steps with a swipe, mirrored in right to left, and ignores a short one", async () => {
    const onIndex = vi.fn();
    render(<Preview items={[a, notes, b]} index={0} onClose={() => {}} onIndex={onIndex} />);
    await screen.findByRole("img", { name: "a.png" });
    const swipe = (from: number, to: number) => {
      fireEvent.touchStart(stage(), { touches: [{ clientX: from, clientY: 100 }] });
      fireEvent.touchEnd(stage(), { changedTouches: [{ clientX: to, clientY: 100 }] });
    };
    swipe(200, 190);
    expect(onIndex).not.toHaveBeenCalled();
    swipe(200, 100);
    expect(onIndex).toHaveBeenLastCalledWith(2);
    onIndex.mockClear();
    document.documentElement.dir = "rtl";
    swipe(100, 200);
    expect(onIndex).toHaveBeenLastCalledWith(2);
  });

  it("closes with the X and with Escape, telling the page which file was on screen", async () => {
    const onClose = vi.fn();
    render(<Preview items={[a, b]} index={1} onClose={onClose} onIndex={() => {}} />);
    await screen.findByRole("img", { name: "b.png" });
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenLastCalledWith(b);
    onClose.mockClear();
    fireEvent.keyDown(stage(), { key: "Escape", keyCode: 27 });
    await waitFor(() => expect(onClose).toHaveBeenLastCalledWith(b));
  });

  it("does not close on a click outside the image, as no dialog does", async () => {
    const onClose = vi.fn();
    render(<Preview items={[a]} index={0} onClose={onClose} onIndex={() => {}} />);
    await screen.findByRole("img", { name: "a.png" });
    fireEvent.click(stage());
    expect(onClose).not.toHaveBeenCalled();
  });

  it("offers Download only when the file allows it, and a refusal shows the notice", async () => {
    startDownload.mockResolvedValue(false);
    const first = render(<Preview items={[a]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await screen.findByRole("img", { name: "a.png" });
    fireEvent.click(screen.getByRole("button", { name: "Download" }));
    expect(await screen.findByRole("status")).toBeTruthy();
    expect(screen.queryByRole("img", { name: "a.png" })).toBeNull();
    first.unmount();
    render(<Preview items={[file("locked.png", "image/png", { no_download: true, role: "viewer" })]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await waitFor(() => expect(stage()).not.toBeNull());
    expect(screen.queryByRole("button", { name: "Download" })).toBeNull();
  });

  it("says so when the picture does not load, and tries the next file afresh", async () => {
    const { rerender } = render(<Preview items={[a, b]} index={0} onClose={() => {}} onIndex={() => {}} />);
    fireEvent.error(await screen.findByRole("img", { name: "a.png" }));
    expect(await screen.findByRole("alert")).toBeTruthy();
    rerender(<Preview items={[a, b]} index={1} onClose={() => {}} onIndex={() => {}} />);
    expect(await screen.findByRole("img", { name: "b.png" })).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("keeps the keys working when a step leads to the end and its own button turns off", async () => {
    const { rerender } = render(<Preview items={[a, b]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await screen.findByRole("img", { name: "a.png" });
    const next = screen.getByRole("button", { name: "Next" }) as HTMLButtonElement;
    next.focus();
    rerender(<Preview items={[a, b]} index={1} onClose={() => {}} onIndex={() => {}} />);
    await screen.findByRole("img", { name: "b.png" });
    expect(next.disabled).toBe(true);
    await waitFor(() => expect(document.activeElement).toBe(stage()));
  });

  it("keeps the keys working in the text and PDF viewer too", async () => {
    const t1 = file("one.pdf", "application/pdf");
    const t2 = file("two.pdf", "application/pdf");
    const { rerender } = render(<Preview items={[t1, t2]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await screen.findByTitle("one.pdf");
    const next = screen.getByRole("button", { name: /Next/ }) as HTMLButtonElement;
    next.focus();
    rerender(<Preview items={[t1, t2]} index={1} onClose={() => {}} onIndex={() => {}} />);
    await screen.findByTitle("two.pdf");
    expect(next.disabled).toBe(true);
    await waitFor(() => expect(document.activeElement).toBe(next.closest("[data-preview-stage]")));
  });

  it("leaves a PDF in the full viewer", async () => {
    render(<Preview items={[file("doc.pdf", "application/pdf")]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await screen.findByTitle("doc.pdf");
    expect(stage()).toBeNull();
  });
});
