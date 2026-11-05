// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import {
  buildEditorForm,
  editorEntry,
  editorMessage,
  editorOrigin,
  editorPath,
  isTrustedEditorMessage,
  parseEditorMessage,
  readEditorPath,
  safeFrom,
  saveAsFilename,
  saveAsNameOk,
  saveAsSuggestion,
} from "./editor";

describe("Save As: the copy's name", () => {
  it("starts from the file's name without its extension, and keeps the extension", () => {
    expect(saveAsSuggestion("letter.docx")).toEqual({ base: "letter", ext: ".docx" });
    expect(saveAsSuggestion("archive.tar.gz")).toEqual({ base: "archive.tar", ext: ".gz" });
    expect(saveAsSuggestion("README")).toEqual({ base: "README", ext: "" });
    expect(saveAsSuggestion(".hidden")).toEqual({ base: ".hidden", ext: "" });
  });

  it("refuses an empty name and a name with a slash or a backslash", () => {
    expect(saveAsNameOk("copy")).toBe(true);
    expect(saveAsNameOk("  copy  ")).toBe(true);
    expect(saveAsNameOk("")).toBe(false);
    expect(saveAsNameOk("   ")).toBe(false);
    expect(saveAsNameOk("a/b")).toBe(false);
    expect(saveAsNameOk("a\\b")).toBe(false);
  });

  it("adds the original extension unless the typed name already ends with it", () => {
    expect(saveAsFilename("copy", ".docx")).toBe("copy.docx");
    expect(saveAsFilename("  copy  ", ".docx")).toBe("copy.docx");
    expect(saveAsFilename("copy.DOCX", ".docx")).toBe("copy.DOCX");
    expect(saveAsFilename("copy.pdf", ".docx")).toBe("copy.pdf.docx");
    expect(saveAsFilename("copy", "")).toBe("copy");
  });

  it("sends Action_SaveAs as JSON text with the id and the values", () => {
    const msg = JSON.parse(editorMessage("Action_SaveAs", { Filename: "copy.docx" }));
    expect(msg.MessageId).toBe("Action_SaveAs");
    expect(msg.Values).toEqual({ Filename: "copy.docx" });
    expect(typeof msg.SendTime).toBe("number");
    expect(JSON.parse(editorMessage("Host_PostmessageReady")).Values).toBeUndefined();
  });
});

describe("the editor's address", () => {
  it("names the file and the mode, and carries the folder the file was opened from", () => {
    expect(editorPath("nod_a1", "edit", "/files/fld_b")).toBe("/edit/nod_a1?mode=edit&from=%2Ffiles%2Ffld_b");
    expect(editorPath("nod_a1", "view", null)).toBe("/edit/nod_a1?mode=view");
  });

  it("reads back the mode and the place to return to, and never a token", () => {
    expect(readEditorPath("?mode=view&from=%2Ffiles%2Ffld_b")).toEqual({ mode: "view", from: "/files/fld_b" });
    expect(readEditorPath("?mode=edit")).toEqual({ mode: "edit", from: null });
    expect(readEditorPath("?access_token=sct_x")).toEqual({ mode: "edit", from: null });
  });

  it("accepts only a path of this app as the place to return to", () => {
    expect(safeFrom("/files/fld_b")).toBe("/files/fld_b");
    expect(safeFrom("//evil.example/x")).toBeNull();
    expect(safeFrom("https://evil.example/")).toBeNull();
    expect(safeFrom("/\\evil.example")).toBeNull();
    expect(safeFrom("files")).toBeNull();
    expect(safeFrom(null)).toBeNull();
  });
});

describe("the editor's origin", () => {
  it("is the scheme and host of the action URL, which is the only origin the frame may message from", () => {
    expect(editorOrigin("https://office.example.test/hosting/x?WOPISrc=a%2Fb")).toBe("https://office.example.test");
    expect(editorOrigin("not a url")).toBeNull();
    expect(editorOrigin("javascript:alert(1)")).toBeNull();
  });
});

describe("messages from the editor", () => {
  it("reads a message whether it arrives as JSON text or as an object", () => {
    expect(parseEditorMessage('{"MessageId":"App_LoadingStatus","Values":{"Status":"Document_Loaded"}}')).toEqual({
      id: "App_LoadingStatus",
      values: { Status: "Document_Loaded" },
    });
    expect(parseEditorMessage({ MessageId: "UI_Close" })).toEqual({ id: "UI_Close", values: undefined });
  });

  it("refuses what is not a message", () => {
    expect(parseEditorMessage("{not json")).toBeNull();
    expect(parseEditorMessage("[1,2]")).toBeNull();
    expect(parseEditorMessage(42)).toBeNull();
    expect(parseEditorMessage({ Values: {} })).toBeNull();
  });

  it("trusts a message only from the editor's origin and the editor's own window", () => {
    const frame = {} as Window;
    const other = {} as Window;
    const origin = "https://office.example.test";
    expect(isTrustedEditorMessage({ origin, source: frame }, origin, frame)).toBe(true);
    expect(isTrustedEditorMessage({ origin: "https://evil.example", source: frame }, origin, frame)).toBe(false);
    expect(isTrustedEditorMessage({ origin, source: other }, origin, frame)).toBe(false);
    expect(isTrustedEditorMessage({ origin, source: frame }, null, frame)).toBe(false);
    expect(isTrustedEditorMessage({ origin, source: null }, origin, frame)).toBe(false);
  });
});

describe("the form that posts the token into the editor", () => {
  const url = "https://office.example.test/hosting/x?WOPISrc=https%3A%2F%2Fselfcloud.example.test%2Fwopi%2Ffiles%2Fnod_a1";
  const token = "sct_secret.value";

  it("carries the token and its expiry in the body, and nothing secret in the address", () => {
    const form = buildEditorForm(document, url, token, 1700000000000, "selfcloud-office-editor");
    expect(form.method.toLowerCase()).toBe("post");
    expect(form.target).toBe("selfcloud-office-editor");
    expect(form.action).toBe(url);
    expect(form.action).not.toContain(token);
    expect(form.querySelector('input[name="access_token"]')?.getAttribute("value")).toBe(token);
    expect(form.querySelector('input[name="access_token_ttl"]')?.getAttribute("value")).toBe("1700000000000");
    expect(form.querySelectorAll("input").length).toBe(2);
  });
});

describe("which editor entry a file gets", () => {
  const docx = {
    id: "nod_a1",
    folder_id: "fld_b",
    parent_id: null,
    name: "letter.docx",
    kind: "file" as const,
    size: 5,
    mime_type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    no_download: false,
    editors_can_share: false,
  };

  it("offers Open in editor to an editor, and Open in viewer to a viewer", () => {
    expect(editorEntry({ ...docx, role: "editor" }, true)).toBe("edit");
    expect(editorEntry({ ...docx, role: "manager" }, true)).toBe("edit");
    expect(editorEntry({ ...docx, role: "viewer" }, true)).toBe("view");
    expect(editorEntry({ ...docx, role: "commenter" }, true)).toBe("view");
  });

  it("offers nothing while the feature is off, for a folder, and for a file that is not an office document", () => {
    expect(editorEntry({ ...docx, role: "editor" }, false)).toBeNull();
    expect(editorEntry({ ...docx, kind: "dir", role: "editor" }, true)).toBeNull();
    expect(editorEntry({ ...docx, name: "photo.png", mime_type: "image/png", role: "editor" }, true)).toBeNull();
  });
});
