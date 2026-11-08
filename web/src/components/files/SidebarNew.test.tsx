// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setLang } from "../../lib/i18n";
import { myFilesPlace, placeOfHere, type DocKind, type NewPlace } from "../../lib/files/newPlace";
import { PALETTE, SIDEBAR } from "../../theme/tokens";
import { ThemeProvider, antdTokens } from "../../theme/theme";
import { contrast } from "../../lib/brand";
import SidebarNew from "./SidebarNew";

const readOnly: NewPlace = { where: { folder: "fld_t", dirID: null, path: "/" }, canFolder: false, canUpload: false, canUploadFolder: false, readOnly: true, canEdit: false };

function renderNew(place: NewPlace | null, collapsed = false, coarse = false, documents: DocKind[] = [], failure: string | null = null) {
  const onPick = vi.fn();
  const onFolder = vi.fn();
  const onDocument = vi.fn();
  const utils = render(
    <SidebarNew place={place} failure={failure} collapsed={collapsed} coarse={coarse} sidebar={SIDEBAR} onPick={onPick} onFolder={onFolder} documents={documents} onDocument={onDocument} />,
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

  // wf1: while the folder shown is not read yet, the button is disabled and its tooltip gives the reason. A disabled button fires
  // no hover events, so the tooltip hangs on a wrapper.
  it("while the place is still read, hovering the disabled button shows the reason", async () => {
    renderNew(null);
    const button = screen.getByTestId("new-button");
    expect(button.hasAttribute("disabled")).toBe(true);
    fireEvent.mouseEnter(button);
    expect(await screen.findByText("Loading the folder")).toBeTruthy();
  });

  it("in the folded rail, while the place is still read, the tooltip gives the reason instead of the name", async () => {
    renderNew(null, true);
    fireEvent.mouseEnter(screen.getByTestId("new-button"));
    expect(await screen.findByText("Loading the folder")).toBeTruthy();
    expect(screen.queryByText("New")).toBeNull();
  });

  // Review item 1 (wf1): the reason is the button's accessible description while it is disabled, with no hover needed, so a
  // keyboard or screen-reader user hears why it waits. It is absent once the button is enabled.
  function description(button: HTMLElement): string | null {
    const id = button.getAttribute("aria-describedby");
    return id ? document.getElementById(id)?.textContent ?? null : null;
  }

  it("while the place is still read, the button's accessible description is the reason, without a hover", () => {
    renderNew(null);
    expect(description(screen.getByTestId("new-button"))).toBe("Loading the folder");
  });

  it("in the folded rail, while the place is still read, the accessible description is the reason too", () => {
    renderNew(null, true);
    expect(description(screen.getByTestId("new-button"))).toBe("Loading the folder");
  });

  // Review item 2 (wf1): the copy is hidden from the reading order, so browse mode does not read the reason twice; the button's
  // description still reads it.
  it("the reason copy is aria-hidden, so it is read once, as the button's description", () => {
    renderNew(null);
    const id = screen.getByTestId("new-button").getAttribute("aria-describedby");
    const copy = id ? document.getElementById(id) : null;
    expect(copy?.getAttribute("aria-hidden")).toBe("true");
    expect(description(screen.getByTestId("new-button"))).toBe("Loading the folder");
  });

  it("when the place is read, the button has no accessible description", () => {
    renderNew(myFilesPlace("fld_mine"));
    const button = screen.getByTestId("new-button");
    expect(button.getAttribute("aria-describedby")).toBeNull();
    expect(screen.queryByText("Loading the folder")).toBeNull();
  });

  // Review item 2 (wf1): a failed read names its error where the wait used to say "Loading the folder".
  it("when the folder could not be read, the reason is the error, as the tooltip and the accessible description", async () => {
    renderNew(null, false, false, [], "Something went wrong.");
    const button = screen.getByTestId("new-button");
    expect(description(button)).toBe("Something went wrong.");
    fireEvent.mouseEnter(button);
    expect(await screen.findByText("Something went wrong.")).toBeTruthy();
    expect(screen.queryByText("Loading the folder")).toBeNull();
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
    expect(screen.getByTestId("new-button").closest("div")?.style.marginBlockStart).toBe("10px");
    mouse.unmount();
    renderNew(myFilesPlace("fld_mine"), false, true);
    expect(screen.getByTestId("new-button").closest("div")?.style.marginBlockStart).toBe("6px");
  });

  // Decision 95: the reason is a header on the menu, not a disabled item. The menu is named by it, so a screen reader hears it once.
  it("in a folder the person cannot add to, the reason is a header, not a disabled menu item, and names the menu", async () => {
    renderNew(readOnly);
    fireEvent.click(screen.getByRole("button", { name: "New" }));
    const menu = await screen.findByRole("menu", { name: "You cannot add to this folder." });
    expect(within(menu).queryByRole("menuitem", { name: "You cannot add to this folder." })).toBeNull();
    expect(menu.querySelector("[aria-disabled='true'][role='menuitem']")).not.toBeNull();
    expect(menu.querySelectorAll("[aria-disabled='true']").length).toBe(within(menu).getAllByRole("menuitem").length);
  });

  // Round 2, M4: in dark the reason is drawn at 4.5:1 or more on the menu's ground, read from the DOM (the palette's colorBgElevated).
  it("in dark, the read-only reason is drawn at 4.5:1 or more on the menu's ground", async () => {
    window.localStorage.setItem("selfcloud.theme", "dark");
    try {
      render(
        <ThemeProvider>
          <SidebarNew place={readOnly} collapsed={false} coarse={false} sidebar={SIDEBAR} onPick={vi.fn()} onFolder={vi.fn()} documents={[]} onDocument={vi.fn()} />
        </ThemeProvider>,
      );
      fireEvent.click(screen.getByRole("button", { name: "New" }));
      const menu = await screen.findByRole("menu", { name: "You cannot add to this folder." });
      const reason = document.getElementById(menu.getAttribute("aria-labelledby") ?? "") as HTMLElement;
      const ground = String(antdTokens("dark", PALETTE.dark, SIDEBAR).token?.colorBgElevated);
      const css = reason.style.color.match(/\d+/g) ?? ["0", "0", "0"];
      const hex = `#${css.slice(0, 3).map((v) => Number(v).toString(16).padStart(2, "0")).join("")}`.toUpperCase();
      expect(contrast(hex, ground)).toBeGreaterThanOrEqual(4.5);
    } finally {
      window.localStorage.removeItem("selfcloud.theme");
    }
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

// Round 4, L4: Escape in the New menu. A parent's keydown stands for the phone drawer's panel, which closes on Escape: while the menu is
// open the drawer must not see the key (the menu takes it and closes), and with the menu closed the key reaches the parent as before.
describe("Escape in the sidebar's New menu (decision 95, round 3 fix)", () => {
  beforeEach(async () => {
    await setLang("en");
  });

  function renderInParent(place: NewPlace) {
    const parentKey = vi.fn();
    const utils = render(
      <div onKeyDown={parentKey}>
        <SidebarNew place={place} collapsed={false} coarse={false} sidebar={SIDEBAR} onPick={vi.fn()} onFolder={vi.fn()} documents={[]} onDocument={vi.fn()} />
      </div>,
    );
    return { ...utils, parentKey };
  }

  it("with the menu open, Escape closes it, focuses the New button, and does not reach the parent", async () => {
    const { parentKey } = renderInParent(myFilesPlace("fld_mine"));
    const button = screen.getByTestId("new-button");
    fireEvent.click(button);
    const item = await screen.findByRole("menuitem", { name: "New folder" });
    fireEvent.keyDown(item, { key: "Escape", code: "Escape", keyCode: 27 });
    // The menu is closed when its leave animation has started (jsdom never ends the animation, so it stays in the leave state).
    await waitFor(() => {
      const dropdown = document.querySelector(".ant-dropdown");
      expect(!dropdown || dropdown.className.includes("slide-up-leave") || dropdown.classList.contains("ant-dropdown-hidden")).toBe(true);
    });
    expect(document.activeElement).toBe(button);
    expect(parentKey).not.toHaveBeenCalled();
  });

  it("with the menu closed, Escape on the New button reaches the parent", () => {
    const { parentKey } = renderInParent(myFilesPlace("fld_mine"));
    const button = screen.getByTestId("new-button");
    button.focus();
    fireEvent.keyDown(button, { key: "Escape", code: "Escape", keyCode: 27 });
    expect(parentKey).toHaveBeenCalledTimes(1);
    expect(parentKey.mock.calls[0][0].key).toBe("Escape");
  });
});
