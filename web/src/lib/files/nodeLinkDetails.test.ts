// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { linkQuery } from "./nodeLink";

// A notification opens /n/{nod}?details=comments. The address keeps the request, so the file's listing opens its
// comments; without it the address is the one a link has always given.
describe("the query of a /n/ address", () => {
  const inFolder = { kind: "in-folder" as const, folder: "fld_12345678", dir: null, preview: "nod_file0001" };
  const dirPlan = { kind: "in-folder" as const, folder: "fld_12345678", dir: null, select: "nod_dir00001" };

  it("previews a file as before when no comments are asked for", () => {
    expect(linkQuery(inFolder, false)).toEqual({ preview: "nod_file0001" });
  });

  it("selects a file and opens its details when the comments are asked for", () => {
    expect(linkQuery(inFolder, true)).toEqual({ select: "nod_file0001", details: "comments" });
  });

  it("selects a directory as before, and carries the request too", () => {
    expect(linkQuery(dirPlan, false)).toEqual({ select: "nod_dir00001" });
    expect(linkQuery(dirPlan, true)).toEqual({ select: "nod_dir00001", details: "comments" });
  });
});
