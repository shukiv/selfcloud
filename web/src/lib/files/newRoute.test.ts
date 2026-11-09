// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { newURL } from "./newRoute";

describe("newURL: the gallery address", () => {
  it("is /new/{fld} for a folder's root, with the kind", () => {
    expect(newURL("fld_a", null, "document")).toBe("/new/fld_a?kind=document");
  });

  it("is /new/{fld}/{nod} for a directory, with the kind", () => {
    expect(newURL("fld_a", "nod_b", "spreadsheet")).toBe("/new/fld_a/nod_b?kind=spreadsheet");
  });

  it("encodes its parts, so an ID is one path segment", () => {
    expect(newURL("a/b", "c d", "presentation")).toBe("/new/a%2Fb/c%20d?kind=presentation");
  });
});
