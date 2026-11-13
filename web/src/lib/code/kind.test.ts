// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import type { Node } from "../files/types";
import { needsSniff, opensInViewer, viewerOf } from "./kind";
import { packOf, type Pack } from "./names";

const file = (name: string, mime_type: string): Pick<Node, "kind" | "name" | "mime_type"> => ({ kind: "file", name, mime_type });

// The extension table of decision 115: each name and the pack it gets.
const TABLE: Array<[string, Pack]> = [
  ["a.js", "javascript"],
  ["a.mjs", "javascript"],
  ["a.cjs", "javascript"],
  ["a.jsx", "javascript"],
  ["a.ts", "typescript"],
  ["a.tsx", "typescript"],
  ["a.json", "json"],
  ["a.jsonc", "json"],
  ["a.md", "markdown"],
  ["a.markdown", "markdown"],
  ["a.html", "html"],
  ["a.htm", "html"],
  ["a.xml", "xml"],
  ["a.svg", "xml"],
  ["a.css", "css"],
  ["a.py", "python"],
  ["a.go", "go"],
  ["a.rb", "ruby"],
  ["a.php", "php"],
  ["a.java", "java"],
  ["a.c", "cpp"],
  ["a.h", "cpp"],
  ["a.cc", "cpp"],
  ["a.cpp", "cpp"],
  ["a.cxx", "cpp"],
  ["a.hpp", "cpp"],
  ["a.rs", "rust"],
  ["a.sh", "shell"],
  ["a.bash", "shell"],
  ["a.sql", "sql"],
  ["a.yml", "yaml"],
  ["a.yaml", "yaml"],
  ["a.toml", "toml"],
  ["a.ini", "properties"],
  ["a.conf", "properties"],
  ["a.cfg", "properties"],
  ["a.env", "properties"],
  [".env", "properties"],
  ["a.properties", "properties"],
  ["Dockerfile", "dockerfile"],
  ["a.txt", "text"],
  ["a.csv", "text"],
  ["a.tsv", "text"],
  ["a.log", "text"],
  ["Makefile", "text"],
];

describe("the extension table", () => {
  it.each(TABLE)("%s gets the %s pack", (name, pack) => {
    expect(packOf(name)).toBe(pack);
  });

  it("matches a name and an extension in any case, by the last dot only", () => {
    expect(packOf("README.MD")).toBe("markdown");
    expect(packOf("DOCKERFILE")).toBe("dockerfile");
    expect(packOf("archive.tar.gz")).toBeNull();
    expect(packOf("notes.txt.bak")).toBeNull();
    expect(packOf("Dockerfile.dev")).toBeNull();
    expect(packOf("trailing.")).toBeNull();
    expect(packOf("README")).toBeNull();
  });

  it("takes only the lists' own keys: a name like an object's property is not in the list", () => {
    for (const name of ["constructor", "__proto__", "toString", "a.constructor", "a.__proto__", "a.hasOwnProperty"]) {
      expect(packOf(name), name).toBeNull();
    }
  });
});

describe("the rule order", () => {
  it("rule 1: an office type never reaches the code viewer, whatever its name", () => {
    for (const f of [
      file("report.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"),
      file("data.csv", "application/vnd.ms-excel"),
      file("notes.txt", "application/msword"),
    ]) {
      expect(needsSniff(f)).toBe(false);
      expect(viewerOf(f, "text")).toBeNull();
    }
  });

  it("rule 2: a picture stays with the image viewer, SVG included, whatever its name", () => {
    for (const f of [file("cat.png", "image/png"), file("logo.svg", "image/svg+xml"), file("pic.txt", "image/png")]) {
      expect(needsSniff(f)).toBe(false);
      expect(viewerOf(f, "text")).toBeNull();
    }
  });

  it("rule 3: a .ts file stored as video/mp2t opens as TypeScript when the sniff says text, and not when it says binary", () => {
    const ts = file("app.ts", "video/mp2t");
    expect(needsSniff(ts)).toBe(true);
    expect(viewerOf(ts, "text")).toEqual({ kind: "code", pack: "typescript" });
    expect(viewerOf(ts, "binary")).toBeNull();
  });

  it("rule 3: the name decides over the stored type", () => {
    expect(viewerOf(file("lib.rs", "application/rls-services+xml"), "text")).toEqual({ kind: "code", pack: "rust" });
    expect(viewerOf(file("main.go", "application/octet-stream"), "text")).toEqual({ kind: "code", pack: "go" });
    expect(viewerOf(file("Dockerfile", "application/octet-stream"), "text")).toEqual({ kind: "code", pack: "dockerfile" });
    expect(viewerOf(file("Makefile", "application/octet-stream"), "text")).toEqual({ kind: "code", pack: "text" });
    expect(viewerOf(file("site.env", "application/x-something"), "text")).toEqual({ kind: "code", pack: "properties" });
    expect(viewerOf(file("logo.svg", "application/octet-stream"), "text")).toEqual({ kind: "code", pack: "xml" });
  });

  it("rule 4: a text type opens in the code viewer when the sniff says text, with the type's pack", () => {
    expect(viewerOf(file("notes", "text/plain"), "text")).toEqual({ kind: "code", pack: "text" });
    expect(viewerOf(file("config", "application/json"), "text")).toEqual({ kind: "code", pack: "json" });
    expect(viewerOf(file("page", "text/html; charset=utf-8"), "text")).toEqual({ kind: "code", pack: "html" });
    expect(viewerOf(file("notes", "text/plain"), "binary")).toBeNull();
    expect(viewerOf(file("notes", "text/plain"), "utf16")).toBeNull();
  });

  it("rule 5: a name not in the list, stored as octet-stream, opens as plain text when the sniff says text", () => {
    const f = file("LICENSE", "application/octet-stream");
    expect(needsSniff(f)).toBe(true);
    expect(viewerOf(f, "text")).toEqual({ kind: "plain", pack: "text" });
    expect(viewerOf(f, "binary")).toBeNull();
    expect(viewerOf(f, "utf16")).toBeNull();
  });

  it("rule 6: a file the rules do not take is never sniffed and keeps its viewer", () => {
    for (const f of [file("plan.pdf", "application/pdf"), file("clip.mp4", "video/mp4"), file("song.mp3", "audio/mpeg"), file("a.zip", "application/zip")]) {
      expect(needsSniff(f)).toBe(false);
      expect(viewerOf(f, "text")).toBeNull();
    }
  });

  it("never sniffs a folder or a shortcut", () => {
    expect(needsSniff({ kind: "dir", name: "src.go", mime_type: "" })).toBe(false);
    expect(needsSniff({ kind: "shortcut", name: "notes.txt", mime_type: "text/plain" })).toBe(false);
  });

  it("opens in the viewer what it showed before and what the sniff may find to be text", () => {
    expect(opensInViewer(file("cat.png", "image/png"))).toBe(true);
    expect(opensInViewer(file("plan.pdf", "application/pdf"))).toBe(true);
    expect(opensInViewer(file("main.go", "application/octet-stream"))).toBe(true);
    expect(opensInViewer(file("blob.bin", "application/octet-stream"))).toBe(true);
    expect(opensInViewer(file("a.zip", "application/zip"))).toBe(false);
    expect(opensInViewer(file("sheet.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"))).toBe(false);
  });
});
