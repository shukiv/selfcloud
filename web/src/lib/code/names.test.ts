// SPDX-License-Identifier: AGPL-3.0-or-later

import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { ARCHIVE, OFFICE, TEXT } from "../files/kind";
import { CODE_EXTENSIONS, CODE_NAMES } from "./names";

// The drift guard (decision 115): the server's copies of the lists, in internal/api/textnames.go (decision 113), must equal
// the web's. The Go file is read as text: each list is a map literal of "name": true entries.
// vitest runs from web/, so the repository root is one level up.
const GO = readFileSync(resolve(process.cwd(), "../internal/api/textnames.go"), "utf8");

// goKeys reads the keys of the Go map literal assigned to the variable name. The literals hold no braces of their own, so the
// first closing brace after the opening one ends it.
function goKeys(name: string): string[] {
  const m = new RegExp(`var ${name} = map\\[string\\]bool\\{`).exec(GO);
  if (m === null) throw new Error(`textnames.go has no map ${name}`);
  const open = m.index + m[0].length;
  const body = GO.slice(open, GO.indexOf("}", open));
  const keys = [...body.matchAll(/"([^"]+)":\s*true/g)].map((m) => m[1]);
  if (keys.length === 0) throw new Error(`the map ${name} in textnames.go has no entries`);
  return keys.sort();
}

const sorted = (xs: Iterable<string>) => [...xs].sort();

describe("the code list matches the server's copy", () => {
  it("has the same extensions", () => {
    expect(sorted(Object.keys(CODE_EXTENSIONS))).toEqual(goKeys("codeExtensions"));
  });

  it("has the same whole names", () => {
    expect(sorted(Object.keys(CODE_NAMES))).toEqual(goKeys("codeNames"));
  });

  it("has the same text, office and archive types", () => {
    expect(sorted(TEXT)).toEqual(goKeys("textTypes"));
    expect(sorted(OFFICE)).toEqual(goKeys("officeTypes"));
    expect(sorted(ARCHIVE)).toEqual(goKeys("archiveTypes"));
  });

  it("keeps every name and extension in lower case, as the server compares them", () => {
    for (const key of [...Object.keys(CODE_EXTENSIONS), ...Object.keys(CODE_NAMES)]) expect(key).toBe(key.toLowerCase());
  });
});
