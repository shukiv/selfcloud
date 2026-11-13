// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { decodeText, sniff, sniffWhole, SNIFF_BYTES } from "./encoding";

const bytes = (...xs: number[]) => new Uint8Array(xs);
const utf8 = (s: string) => new TextEncoder().encode(s);

// The editor reads the whole file, so its check reads all of it: a NUL past the first 8 KiB makes the file binary.
describe("the whole-file sniff at /edit/", () => {
  it("refuses a NUL byte past the first 8 KiB as binary", () => {
    const late = new Uint8Array(SNIFF_BYTES + 10).fill(0x61);
    late[SNIFF_BYTES + 2] = 0;
    expect(sniffWhole(late)).toBe("binary");
  });

  it("refuses a NUL byte at the very end of a file", () => {
    const end = new Uint8Array(3 * SNIFF_BYTES).fill(0x61);
    end[end.length - 1] = 0;
    expect(sniffWhole(end)).toBe("binary");
  });

  it("calls a whole file with no NUL text, and still refuses UTF-16 by its mark", () => {
    expect(sniffWhole(new Uint8Array(3 * SNIFF_BYTES).fill(0x61))).toBe("text");
    expect(sniffWhole(bytes(0xff, 0xfe, 0x61, 0x00))).toBe("utf16");
  });
});

// The read side of the text rule (decision 115 and 4.8). The write side comes with the editor.
describe("the sniff", () => {
  it("reads 8 KiB", () => {
    expect(SNIFF_BYTES).toBe(8192);
  });

  it("calls UTF-8 and ASCII text, and an empty file, text", () => {
    expect(sniff(utf8("package main\n"))).toBe("text");
    expect(sniff(utf8("שלום, мир, 世界"))).toBe("text");
    expect(sniff(new Uint8Array(0))).toBe("text");
  });

  it("refuses a NUL byte as binary", () => {
    expect(sniff(bytes(0x61, 0x00, 0x62))).toBe("binary");
    expect(sniff(bytes(0x00))).toBe("binary");
  });

  it("reads no further than the first 8 KiB", () => {
    const late = new Uint8Array(SNIFF_BYTES + 10).fill(0x61);
    late[SNIFF_BYTES + 2] = 0;
    expect(sniff(late)).toBe("text");
    late[SNIFF_BYTES - 1] = 0;
    expect(sniff(late)).toBe("binary");
  });

  it("refuses UTF-16 in either byte order by its mark", () => {
    expect(sniff(bytes(0xff, 0xfe, 0x61, 0x00))).toBe("utf16");
    expect(sniff(bytes(0xfe, 0xff, 0x00, 0x61))).toBe("utf16");
  });

  it("takes a UTF-8 byte order mark as text", () => {
    expect(sniff(bytes(0xef, 0xbb, 0xbf, 0x61))).toBe("text");
  });
});

describe("decoding a read", () => {
  it("decodes UTF-8 and says it is", () => {
    expect(decodeText(utf8("a = 'ש'\n"), false)).toEqual({ text: "a = 'ש'\n", bom: false, utf8: true });
  });

  it("records a byte order mark and leaves it out of the text the viewer shows", () => {
    // CodeMirror would draw U+FEFF as a placeholder on the first line. The flag keeps it for the save (decision 130).
    expect(decodeText(bytes(0xef, 0xbb, 0xbf, 0x61), false)).toEqual({ text: "a", bom: true, utf8: true });
    expect(decodeText(bytes(0xef, 0xbb, 0xbf), false)).toEqual({ text: "", bom: true, utf8: true });
    // Bytes that are not UTF-8 after the mark: the mark is still left out.
    expect(decodeText(bytes(0xef, 0xbb, 0xbf, 0xe9), false)).toEqual({ text: "�", bom: true, utf8: false });
  });

  it("keeps a U+FEFF that is not at the start: it is part of the text", () => {
    expect(decodeText(utf8("a﻿b"), false)).toEqual({ text: "a﻿b", bom: false, utf8: true });
  });

  it("says bytes that are not UTF-8 are not, and shows each bad sequence as U+FFFD without throwing", () => {
    // Latin-1 "café".
    const got = decodeText(bytes(0x63, 0x61, 0x66, 0xe9), false);
    expect(got.utf8).toBe(false);
    expect(got.text).toBe("caf�");
  });

  it("leaves out a character a cut split, and keeps the rest valid", () => {
    // "aש" is 61 D7 A9; a cut after D7 splits the second character.
    expect(decodeText(bytes(0x61, 0xd7), true)).toEqual({ text: "a", bom: false, utf8: true });
    // A four-byte character cut after its third byte.
    expect(decodeText(bytes(0x61, 0xf0, 0x9f, 0x98), true)).toEqual({ text: "a", bom: false, utf8: true });
    // A whole character at the cut stays.
    expect(decodeText(bytes(0x61, 0xd7, 0xa9), true)).toEqual({ text: "aש", bom: false, utf8: true });
  });

  it("does not trim the end of a file that was read whole: a broken last character is not UTF-8", () => {
    expect(decodeText(bytes(0x61, 0xd7), false).utf8).toBe(false);
  });
});
