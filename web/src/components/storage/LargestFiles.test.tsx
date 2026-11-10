// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import LargestFilesCard from "./LargestFiles";
import type { LargestFile } from "../../lib/storage/api";
import { officeEdit } from "../../lib/files/editor";
import { contentPath } from "../../lib/files/api";
import { phoneViewport } from "../../test/viewport";

const DOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document";

const files: LargestFile[] = [
  { id: "nod_office1", name: "letter.docx", size: 10, versions_bytes: 0, in_trash: false, modified_at: "2026-10-01T00:00:00Z", mime_type: DOCX },
  { id: "nod_pdf1", name: "scan.pdf", size: 20, versions_bytes: 0, in_trash: false, modified_at: "2026-10-01T00:00:00Z", mime_type: "application/pdf" },
];

function renderCard(over: { files?: LargestFile[]; loaded?: boolean } = {}) {
  return render(
    <LargestFilesCard
      files={over.files ?? files}
      loaded={over.loaded ?? true}
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

// Decision 122 (tl3): the largest files are one table. On a desktop it has five headers (Old versions and Modified among them);
// its empty body keeps the headers and says No files yet, and the first load shows no empty message at all.
describe("the largest files are one table (decision 122)", () => {
  beforeEach(() => {
    officeEdit.set(false);
    window.matchMedia = ((query: string) => ({
      matches: /min-width: (\d+)px/.exec(query) !== null,
      media: query,
      onchange: null,
      addListener: () => undefined,
      removeListener: () => undefined,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      dispatchEvent: () => false,
    })) as unknown as typeof window.matchMedia;
  });

  afterEach(() => {
    officeEdit.set(false);
  });

  it("a desktop table keeps its five headers and names each row by its file", () => {
    const { container } = renderCard();
    const region = screen.getByRole("region", { name: "Largest files" });
    expect(within(region).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Size", "Old versions", "Modified", "Actions"]);
    expect(region.querySelector('tbody tr[data-id="nod_pdf1"]')?.getAttribute("aria-label")).toBe("scan.pdf");
    expect(container.querySelectorAll("tbody td .item-name").length).toBe(2);
  });

  // Round 2 (M2): Download is a real link to the file's own address, so a middle click, a new tab and Copy link work as before.
  it("Download is a link to the file's own address (round 2, M2)", () => {
    renderCard();
    const link = screen.getByRole("link", { name: "Download: scan.pdf" });
    expect(link.getAttribute("href")).toBe(contentPath("nod_pdf1"));
  });

  // Round 2 (M6): on a phone every action is in the row's ⋮ menu, and the menu keeps all three, Download among them.
  it("on a phone the ⋮ menu keeps every action, Download among them (round 2, M6)", async () => {
    const undo = phoneViewport();
    try {
      renderCard();
      const row = screen.getByRole("row", { name: "scan.pdf" });
      fireEvent.click(row.querySelector('button[aria-haspopup="menu"]') as HTMLElement);
      const items = (await screen.findAllByRole("menuitem")).map((m) => m.textContent);
      expect(items).toEqual(["Download: scan.pdf", "Move to trash: scan.pdf", "Open"]);
    } finally {
      undo();
    }
  });

  it("an empty card keeps its headers, and its title is in the table body", () => {
    renderCard({ files: [] });
    const region = screen.getByRole("region", { name: "Largest files" });
    // No row, so no row icons and no Actions column: the four column headers stay.
    expect(within(region).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Size", "Old versions", "Modified"]);
    expect(region.querySelector("tbody .empty-title")?.textContent).toBe("No files yet");
  });

  it("before the first load answers, no empty message shows", () => {
    renderCard({ files: [], loaded: false });
    expect(screen.queryByText("No files yet")).toBeNull();
    expect(screen.queryByRole("region", { name: "Largest files" })).toBeNull();
  });
});

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
