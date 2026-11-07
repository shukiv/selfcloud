// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import type { Node } from "./types";
import { editorPath, officeLinkClick, openEditorTab, readEditorPath, saveStateAfter, type EditorMessage } from "./editor";

const DOC: Node = {
  id: "nod_a1",
  folder_id: "fld_b",
  parent_id: null,
  name: "letter.docx",
  kind: "file",
  mime_type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  size: 5,
  created_at: "2026-10-01T00:00:00Z",
  modified_at: "2026-10-01T00:00:00Z",
  role: "editor",
} as Node;

const PDF: Node = { ...DOC, id: "nod_p1", name: "scan.pdf", mime_type: "application/pdf" } as Node;

describe("opening an office file (decision 102)", () => {
  it("a fine pointer opens a new tab with window.open and noopener, in edit mode for an editor", () => {
    const open = vi.fn();
    const goTo = vi.fn();
    openEditorTab("nod_a1", "edit", "/files/fld_b", false, goTo, open);
    expect(open).toHaveBeenCalledWith(editorPath("nod_a1", "edit", null, true), "_blank", "noopener");
    expect(goTo).not.toHaveBeenCalled();
    expect(readEditorPath(editorPath("nod_a1", "edit", null, true).split("?")[1] ?? "")).toEqual({ mode: "edit", from: null, tab: true });
  });

  it("a fine pointer opens view mode for a viewer", () => {
    const open = vi.fn();
    openEditorTab("nod_a1", "view", "/files/fld_b", false, vi.fn(), open);
    const url = open.mock.calls[0]?.[0] as string;
    expect(new URLSearchParams(url.split("?")[1]).get("mode")).toBe("view");
  });

  it("a coarse pointer (a phone or a tablet) opens the editor in the same tab, with from set to here", () => {
    const open = vi.fn();
    const goTo = vi.fn();
    openEditorTab("nod_a1", "edit", "/files/fld_b?x=1", true, goTo, open);
    expect(open).not.toHaveBeenCalled();
    expect(goTo).toHaveBeenCalledWith(editorPath("nod_a1", "edit", "/files/fld_b?x=1"));
    expect(readEditorPath(goTo.mock.calls[0]?.[0].split("?")[1] ?? "").from).toBe("/files/fld_b?x=1");
  });

  it("a plain click on an office link opens the editor tab and stops the link", () => {
    const winOpen = vi.spyOn(window, "open").mockImplementation(() => null);
    const preventDefault = vi.fn();
    const goTo = vi.fn();
    officeLinkClick({ button: 0, metaKey: false, ctrlKey: false, shiftKey: false, altKey: false, preventDefault }, DOC, true, "/recent", goTo);
    expect(preventDefault).toHaveBeenCalledTimes(1);
    expect(goTo).not.toHaveBeenCalled();
    expect(winOpen).toHaveBeenCalledWith(editorPath("nod_a1", "edit", null, true), "_blank", "noopener");
    winOpen.mockRestore();
  });

  it("a modified or middle click keeps the browser's own choice", () => {
    for (const e of [
      { button: 0, metaKey: true, ctrlKey: false, shiftKey: false, altKey: false },
      { button: 0, metaKey: false, ctrlKey: true, shiftKey: false, altKey: false },
      { button: 0, metaKey: false, ctrlKey: false, shiftKey: true, altKey: false },
      { button: 1, metaKey: false, ctrlKey: false, shiftKey: false, altKey: false },
    ]) {
      const preventDefault = vi.fn();
      officeLinkClick({ ...e, altKey: e.altKey ?? false, preventDefault }, DOC, true, "/recent", vi.fn());
      expect(preventDefault).not.toHaveBeenCalled();
    }
  });

  it("with the feature off, or for a file that is not an office document, a link is left alone", () => {
    const preventDefault = vi.fn();
    const click = { button: 0, metaKey: false, ctrlKey: false, shiftKey: false, altKey: false, preventDefault };
    officeLinkClick(click, DOC, false, "/recent", vi.fn());
    officeLinkClick(click, PDF, true, "/recent", vi.fn());
    expect(preventDefault).not.toHaveBeenCalled();
  });
});

describe("the save state (decision 102)", () => {
  const msg = (id: string, values?: Record<string, unknown>): EditorMessage => ({ id, values });

  it("Doc_ModifiedStatus: Modified true is unsaved changes, false is saved", () => {
    expect(saveStateAfter(null, msg("Doc_ModifiedStatus", { Modified: true }))).toBe("unsaved");
    expect(saveStateAfter("unsaved", msg("Doc_ModifiedStatus", { Modified: false }))).toBe("saved");
    expect(saveStateAfter("saved", msg("Doc_ModifiedStatus", {}))).toBe("saved");
  });

  it("Action_Save_Resp: success is saved, a refusal is unsaved changes; other messages change nothing", () => {
    expect(saveStateAfter("unsaved", msg("Action_Save_Resp", { success: true }))).toBe("saved");
    expect(saveStateAfter("saved", msg("Action_Save_Resp", { success: false }))).toBe("unsaved");
    expect(saveStateAfter("saved", msg("Action_Save_Resp"))).toBe("saved");
    expect(saveStateAfter("unsaved", msg("UI_Close"))).toBe("unsaved");
    expect(saveStateAfter(null, msg("App_LoadingStatus", { Status: "Document_Loaded" }))).toBeNull();
  });
});
