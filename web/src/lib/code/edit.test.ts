// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import type { Node } from "../files/types";
import { codeEditEntry, codeMenuEntry, EDIT_LIMIT, encodeDraft, lineBreakOf, normalizeText } from "./edit";
import type { Viewer } from "./kind";

const MiB = 1024 * 1024;

function file(name: string, mime: string, role: Node["role"], size = 10): Node {
  return {
    id: "nod_a1",
    folder_id: "fld_b",
    parent_id: null,
    name,
    kind: "file",
    size,
    mime_type: mime,
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role,
    no_download: false,
    editors_can_share: false,
  };
}

const CODE: Viewer = { kind: "code", pack: "json" };
const PLAIN: Viewer = { kind: "plain", pack: "text" };

describe("the editor's entry for a file", () => {
  it("is the editor's only for an editor, a file the code viewer shows as code, up to 2 MiB", () => {
    expect(codeEditEntry(file("a.json", "application/json", "editor"), CODE)).toBe("edit");
    expect(codeEditEntry(file("a.ts", "video/mp2t", "manager"), { kind: "code", pack: "typescript" })).toBe("edit");
    expect(codeEditEntry(file("a.json", "application/json", "editor", EDIT_LIMIT), CODE)).toBe("edit");
  });

  it("is none for a contributor, a commenter and a viewer: they read, they do not get the pencil", () => {
    for (const role of ["contributor", "commenter", "viewer"] as const) {
      expect(codeEditEntry(file("a.json", "application/json", role), CODE), role).toBeNull();
    }
    expect(codeEditEntry(file("a.json", "application/json", undefined), CODE)).toBeNull();
  });

  it("is none for a file the viewer shows as plain text (rule 5): no editing of what is not a code file", () => {
    expect(codeEditEntry(file("notes.bin", "application/octet-stream", "editor"), PLAIN)).toBeNull();
  });

  it("is none for a file that does not open in the code viewer, or is larger than 2 MiB", () => {
    expect(codeEditEntry(file("a.json", "application/json", "editor"), null)).toBeNull();
    expect(codeEditEntry(file("a.json", "application/json", "editor", EDIT_LIMIT + 1), CODE)).toBeNull();
    expect(codeEditEntry(file("a.json", "application/json", "editor", 3 * MiB), CODE)).toBeNull();
  });

  it("is none for a folder, and for an office file, which keeps its own editor", () => {
    const folder = { ...file("dir", "", "editor"), kind: "dir" as const };
    expect(codeEditEntry(folder, CODE)).toBeNull();
    expect(codeEditEntry(file("a.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "editor"), CODE)).toBeNull();
  });
});

describe("the editor's menu entry, before the file is sniffed", () => {
  it("is for an editor, a listed name or a text type, and up to 2 MiB", () => {
    expect(codeMenuEntry(file("a.go", "application/octet-stream", "editor"))).toBe(true);
    expect(codeMenuEntry(file("a.md", "text/markdown", "editor"))).toBe(true);
    expect(codeMenuEntry(file("a.json", "application/json", "editor", EDIT_LIMIT))).toBe(true);
  });

  it("is not for a viewer, an unlisted octet-stream name, an image, or a file over 2 MiB", () => {
    expect(codeMenuEntry(file("a.json", "application/json", "viewer"))).toBe(false);
    expect(codeMenuEntry(file("notes.bin", "application/octet-stream", "editor"))).toBe(false);
    expect(codeMenuEntry(file("a.json", "image/png", "editor"))).toBe(false);
    expect(codeMenuEntry(file("a.json", "application/json", "editor", EDIT_LIMIT + 1))).toBe(false);
  });
});

describe("the line break of a file", () => {
  it("is the first break's style: LF, CRLF or CR; LF when a file has none", () => {
    expect(lineBreakOf("a\nb\r\nc")).toBe("\n");
    expect(lineBreakOf("a\r\nb\nc")).toBe("\r\n");
    expect(lineBreakOf("a\rb")).toBe("\r");
    expect(lineBreakOf("one line")).toBe("\n");
    expect(lineBreakOf("")).toBe("\n");
  });

  it("is taken out of the text the editor holds, which uses LF only", () => {
    expect(normalizeText("a\r\nb\rc\nd")).toBe("a\nb\nc\nd");
  });
});

describe("encoding a draft for a save", () => {
  it("writes UTF-8 with the file's line break, and puts a byte order mark back when the file had one", () => {
    expect([...encodeDraft("a\nb", false, "\n")]).toEqual([0x61, 0x0a, 0x62]);
    expect([...encodeDraft("a\nb", false, "\r\n")]).toEqual([0x61, 0x0d, 0x0a, 0x62]);
    expect([...encodeDraft("a", true, "\n")]).toEqual([0xef, 0xbb, 0xbf, 0x61]);
  });

  it("gives back the bytes of a file it read, unchanged: a CRLF file with a byte order mark", () => {
    const original = new Uint8Array([0xef, 0xbb, 0xbf, 0x7b, 0x0d, 0x0a, 0x20, 0x22, 0x61, 0x22, 0x0d, 0x0a, 0x7d]);
    const text = new TextDecoder("utf-8").decode(original.subarray(3));
    const back = encodeDraft(normalizeText(text), true, lineBreakOf(text));
    expect([...back]).toEqual([...original]);
  });

  it("counts the byte order mark in the bytes, so the size gate sees what is sent", () => {
    expect(encodeDraft("", true, "\n").length).toBe(3);
    expect(encodeDraft("é", false, "\n").length).toBe(2);
  });
});
