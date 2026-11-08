// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import LargestFilesCard from "./LargestFiles";
import type { LargestFile } from "../../lib/storage/api";
import { officeEdit } from "../../lib/files/editor";

const DOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document";

const files: LargestFile[] = [
  { id: "nod_office1", name: "letter.docx", size: 10, versions_bytes: 0, in_trash: false, modified_at: "2026-10-01T00:00:00Z", mime_type: DOCX },
  { id: "nod_pdf1", name: "scan.pdf", size: 20, versions_bytes: 0, in_trash: false, modified_at: "2026-10-01T00:00:00Z", mime_type: "application/pdf" },
];

function renderCard() {
  return render(
    <LargestFilesCard
      files={files}
      chip={"all" as never}
      onChip={() => undefined}
      hasMore={false}
      loading={false}
      onMore={() => undefined}
      onTrash={() => undefined}
      onRestore={() => undefined}
    />,
  );
}

// The name's link is the anchor whose text is the file's name.
function nameLink(name: string): HTMLAnchorElement {
  const found = screen.getByText(name).closest("a");
  if (found === null) throw new Error(`no link for ${name}`);
  return found;
}

// pointer sets the emulated pointer: a phone's is coarse.
function pointer(coarse: boolean) {
  window.matchMedia = ((query: string) => ({
    matches: coarse && query.includes("pointer: coarse"),
    media: query,
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
}

describe("the Largest files links (decision 102, round 2 R6, and the rules of the editor's open)", () => {
  beforeEach(() => {
    officeEdit.set(true);
    pointer(false);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    officeEdit.set(false);
  });

  it("on a desktop, an office file's link opens the editor's address in a new tab with noopener, marked tab=1 so Close ends the tab", () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    renderCard();
    const notPrevented = fireEvent.click(nameLink("letter.docx"));
    expect(open).toHaveBeenCalledWith("/n/nod_office1?tab=1", "_blank", "noopener");
    expect(notPrevented).toBe(false);
  });

  it("on a phone (a coarse pointer) an office file's link keeps its address and opens in this tab, as the editor's open does", () => {
    pointer(true);
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    renderCard();
    const notPrevented = fireEvent.click(nameLink("letter.docx"));
    expect(open).not.toHaveBeenCalled();
    expect(notPrevented).toBe(true);
  });

  it("with office editing off, an office file keeps its link and opens no editor", () => {
    officeEdit.set(false);
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    renderCard();
    const notPrevented = fireEvent.click(nameLink("letter.docx"));
    expect(open).not.toHaveBeenCalled();
    expect(notPrevented).toBe(true);
  });

  it("a file that is not an office document keeps its link", () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    renderCard();
    const notPrevented = fireEvent.click(nameLink("scan.pdf"));
    expect(open).not.toHaveBeenCalled();
    expect(notPrevented).toBe(true);
  });

  it("a modified click on an office file's link is left to the browser's own choice", () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    renderCard();
    const notPrevented = fireEvent.click(nameLink("letter.docx"), { ctrlKey: true });
    expect(open).not.toHaveBeenCalled();
    expect(notPrevented).toBe(true);
  });
});
