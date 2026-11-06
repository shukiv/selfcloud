// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setLang } from "../../lib/i18n";
import { myFilesPlace, placeOfHere, type DocKind, type NewPlace } from "../../lib/files/newPlace";
import { SIDEBAR } from "../../theme/tokens";
import SidebarNew from "./SidebarNew";

const readOnly: NewPlace = { where: { folder: "fld_t", dirID: null, path: "/" }, canFolder: false, canUpload: false, canUploadFolder: false, readOnly: true, canEdit: false };

function renderNew(place: NewPlace | null, collapsed = false, coarse = false, documents: DocKind[] = []) {
  const onPick = vi.fn();
  const onFolder = vi.fn();
  const onDocument = vi.fn();
  const utils = render(
    <SidebarNew place={place} collapsed={collapsed} coarse={coarse} sidebar={SIDEBAR} onPick={onPick} onFolder={onFolder} documents={documents} onDocument={onDocument} />,
  );
  return { ...utils, onPick, onFolder, onDocument };
}

describe("the sidebar's New button", () => {
  beforeEach(async () => {
    await setLang("en");
  });

  it("is a big New button whose menu holds the three entries, each acting on the place it was given", async () => {
    const place = myFilesPlace("fld_mine");
    const { onPick, onFolder } = renderNew(place);
    const button = screen.getByRole("button", { name: "New" });
    expect(button.getAttribute("aria-haspopup")).toBe("menu");
    fireEvent.click(button);
    fireEvent.click(await screen.findByRole("menuitem", { name: "New folder" }));
    expect(onFolder).toHaveBeenCalledWith(place.where);
    fireEvent.click(button);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Upload files" }));
    expect(onPick).toHaveBeenLastCalledWith("files", place.where);
    fireEvent.click(button);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Upload folder" }));
    expect(onPick).toHaveBeenLastCalledWith("folders", place.where);
  });

  it("in a folder the person cannot add to, shows the reason and every entry disabled, and calls nothing", async () => {
    const { onPick, onFolder } = renderNew(readOnly);
    fireEvent.click(screen.getByRole("button", { name: "New" }));
    expect(await screen.findByText("You cannot add to this folder.")).toBeTruthy();
    const folder = await screen.findByRole("menuitem", { name: "New folder" });
    expect(folder.getAttribute("aria-disabled")).toBe("true");
    fireEvent.click(folder);
    expect(onFolder).not.toHaveBeenCalled();
    expect(onPick).not.toHaveBeenCalled();
  });

  // Decision 91: the blank documents come after the uploads, only when the place offers them (documentKinds). A pick calls
  // onDocument with its kind, and nothing else.
  it("adds Document, Spreadsheet and Presentation after the uploads when the place offers them, and a pick names its kind", async () => {
    const place = placeOfHere({ folder: "fld_t", folderName: "Team", folderRole: "manager", dir: null, dirRole: "manager", trail: [],
      rootReachable: true, reach: [], spaceNoDownload: false, path: "/", rootNodeID: "nod_root" } as unknown as Parameters<typeof placeOfHere>[0], true, true);
    const { onDocument } = renderNew(place, false, false, ["docx", "xlsx", "pptx"]);
    fireEvent.click(screen.getByRole("button", { name: "New" }));
    expect(await screen.findByRole("menuitem", { name: "Document" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Spreadsheet" })).toBeTruthy();
    fireEvent.click(screen.getByRole("menuitem", { name: "Presentation" }));
    expect(onDocument).toHaveBeenCalledWith("pptx");
  });

  it("shows none of the three when the place offers none", async () => {
    renderNew(myFilesPlace("fld_mine"), false, false, []);
    fireEvent.click(screen.getByRole("button", { name: "New" }));
    expect(await screen.findByRole("menuitem", { name: "New folder" })).toBeTruthy();
    expect(screen.queryByRole("menuitem", { name: "Document" })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: "Spreadsheet" })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: "Presentation" })).toBeNull();
  });

  // Decision 69: the button is as wide as its text, not the sidebar's width, and it is capped at the sidebar's width so a long
  // translation is cut with an ellipsis rather than pushing the sidebar.
  it("is as wide as its text, and no wider than the sidebar", () => {
    renderNew(myFilesPlace("fld_mine"));
    const button = screen.getByTestId("new-button");
    expect(button.style.inlineSize).toBe("auto");
    expect(button.style.maxInlineSize).toBe("100%");
    expect(button.style.minInlineSize).toBe("0px");
  });

  // Decision 69, review fix 8: a long translation is cut with an ellipsis inside the cap, not pushed past the sidebar.
  it("cuts a long word with an ellipsis, inside the sidebar's width", () => {
    renderNew(myFilesPlace("fld_mine"));
    const word = screen.getByTestId("new-button").querySelector<HTMLElement>("span");
    expect(word?.style.textOverflow).toBe("ellipsis");
    expect(word?.style.overflow).toBe("hidden");
    expect(word?.style.whiteSpace).toBe("nowrap");
    expect(screen.getByTestId("new-button").style.maxInlineSize).toBe("100%");
  });

  // Decision 69, review fix 8: New's top edge is 10 px down with a mouse and 6 px on a finger (the search box's top edge).
  it("sits 10 px from the top with a mouse, and 6 px on a finger", () => {
    const mouse = renderNew(myFilesPlace("fld_mine"));
    expect(screen.getByTestId("new-button").parentElement?.style.marginBlockStart).toBe("10px");
    mouse.unmount();
    renderNew(myFilesPlace("fld_mine"), false, true);
    expect(screen.getByTestId("new-button").parentElement?.style.marginBlockStart).toBe("6px");
  });

  it("waits, disabled, while the folder shown is not read", () => {
    renderNew(null);
    expect((screen.getByRole("button", { name: "New" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("folded to the rail it is a round button with its name as the accessible name", async () => {
    const place = myFilesPlace("fld_mine");
    const { onFolder } = renderNew(place, true);
    const round = screen.getByRole("button", { name: "New" });
    fireEvent.click(round);
    fireEvent.click(await screen.findByRole("menuitem", { name: "New folder" }));
    expect(onFolder).toHaveBeenCalledWith(place.where);
  });
});
