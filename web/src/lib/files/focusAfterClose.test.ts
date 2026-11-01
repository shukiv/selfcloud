// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it } from "vitest";
import { focusAfterClose } from "./focusAfterClose";

let listing: HTMLElement;

function rows(ids: string[]): void {
  listing = document.createElement("div");
  for (const id of ids) {
    const row = document.createElement("div");
    row.setAttribute("data-id", id);
    row.setAttribute("tabindex", "0");
    listing.appendChild(row);
  }
  document.body.appendChild(listing);
}

afterEach(() => {
  listing?.remove();
});

describe("focusAfterClose", () => {
  it("takes the first candidate that is still listed: the file playing at close, then the opening file", () => {
    rows(["a", "b", "c"]);
    expect(focusAfterClose(listing, ["c", "a"])).toBe(listing.querySelector('[data-id="c"]'));
  });

  it("skips a row that has gone, and takes the opening row instead", () => {
    rows(["a", "b"]);
    // The file playing at close was deleted while the viewer was open: its row is no longer in the listing.
    expect(focusAfterClose(listing, ["gone", "b"])).toBe(listing.querySelector('[data-id="b"]'));
  });

  it("takes the listing itself when no candidate row is left, never the body", () => {
    rows(["a"]);
    const target = focusAfterClose(listing, ["gone", null, undefined]);
    expect(target).toBe(listing);
    target?.focus();
    expect(document.activeElement).toBe(listing);
    expect(document.activeElement).not.toBe(document.body);
  });

  it("gives the listing a tab stop so it can take the focus", () => {
    rows([]);
    listing.removeAttribute("tabindex");
    focusAfterClose(listing, []);
    expect(listing.getAttribute("tabindex")).toBe("-1");
  });

  it("returns null only when there is no listing", () => {
    expect(focusAfterClose(null, ["a"])).toBeNull();
  });
});
