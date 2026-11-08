// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import LargestFilesCard from "./LargestFiles";
import type { LargestFile } from "../../lib/storage/api";

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

describe("the Largest files links (decision 102, round 2 R6)", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("an office file's link opens its address in a new tab with noopener, and does not follow the link here", () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    renderCard();
    const notPrevented = fireEvent.click(nameLink("letter.docx"));
    expect(open).toHaveBeenCalledWith("/n/nod_office1", "_blank", "noopener");
    expect(notPrevented).toBe(false);
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
