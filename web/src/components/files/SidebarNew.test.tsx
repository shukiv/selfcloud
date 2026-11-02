// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setLang } from "../../lib/i18n";
import { myFilesPlace, type NewPlace } from "../../lib/files/newPlace";
import { SIDEBAR } from "../../theme/tokens";
import SidebarNew from "./SidebarNew";

const readOnly: NewPlace = { where: { folder: "fld_t", dirID: null, path: "/" }, canFolder: false, canUpload: false, canUploadFolder: false, readOnly: true };

function renderNew(place: NewPlace | null, collapsed = false) {
  const onPick = vi.fn();
  const onFolder = vi.fn();
  const utils = render(<SidebarNew place={place} collapsed={collapsed} coarse={false} sidebar={SIDEBAR} onPick={onPick} onFolder={onFolder} />);
  return { ...utils, onPick, onFolder };
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
