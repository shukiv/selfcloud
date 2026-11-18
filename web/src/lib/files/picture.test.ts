// SPDX-License-Identifier: AGPL-3.0-or-later

// The picture choice: fit and the three percentages, kept per person in this browser, clamped to 50-150 on the step of
// 5, with the default on any bad stored value. The style is none at the defaults and the three filter functions otherwise.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_PICTURE, pictureStyle, readPicture, writePicture, type Picture } from "./picture";

const KEY = "selfcloud.picture.u";

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  window.localStorage.clear();
  vi.restoreAllMocks();
});

describe("the kept picture", () => {
  it("starts at the defaults: contain, and 100 for each of the three", () => {
    expect(DEFAULT_PICTURE).toEqual({ fit: "contain", brightness: 100, contrast: 100, saturation: 100 });
    expect(readPicture("u")).toEqual(DEFAULT_PICTURE);
  });

  it("keeps what was written under the person's own key, and reads no one else's", () => {
    const p: Picture = { fit: "cover", brightness: 120, contrast: 90, saturation: 110 };
    writePicture("u", p);
    expect(window.localStorage.getItem(KEY)).not.toBeNull();
    expect(readPicture("u")).toEqual(p);
    expect(readPicture("someone-else")).toEqual(DEFAULT_PICTURE);
  });

  it("keeps the visitor's choice under visitor", () => {
    const p: Picture = { fit: "fill", brightness: 80, contrast: 100, saturation: 100 };
    writePicture(null, p);
    expect(window.localStorage.getItem("selfcloud.picture.visitor")).not.toBeNull();
    expect(readPicture(null)).toEqual(p);
    expect(readPicture("u")).toEqual(DEFAULT_PICTURE);
  });

  it("clamps a stored value to 50-150 and rounds it to the step of 5", () => {
    window.localStorage.setItem(KEY, JSON.stringify({ fit: "contain", brightness: 200, contrast: 20, saturation: 123 }));
    expect(readPicture("u")).toEqual({ fit: "contain", brightness: 150, contrast: 50, saturation: 125 });
    window.localStorage.setItem(KEY, JSON.stringify({ fit: "cover", brightness: 122, contrast: 51, saturation: 149 }));
    expect(readPicture("u")).toEqual({ fit: "cover", brightness: 120, contrast: 50, saturation: 150 });
  });

  it("falls back to the default on any bad stored value", () => {
    const bad = [
      "not json",
      "",
      "null",
      "[]",
      "{}",
      JSON.stringify({ fit: "zoom", brightness: 100, contrast: 100, saturation: 100 }),
      JSON.stringify({ fit: "contain", brightness: "x", contrast: 100, saturation: 100 }),
      JSON.stringify({ fit: "contain", brightness: 100, contrast: 100 }),
      JSON.stringify({ fit: "contain", brightness: NaN, contrast: 100, saturation: 100 }),
    ];
    for (const raw of bad) {
      window.localStorage.setItem(KEY, raw);
      expect(readPicture("u"), raw).toEqual(DEFAULT_PICTURE);
    }
  });

  it("writes nothing outside 50-150 or the step of 5, and no unknown fit", () => {
    writePicture("u", { fit: "cover", brightness: 500, contrast: -3, saturation: 101 });
    expect(readPicture("u")).toEqual({ fit: "cover", brightness: 150, contrast: 50, saturation: 100 });
    writePicture("u", { fit: "zoom" as Picture["fit"], brightness: 100, contrast: 100, saturation: 100 });
    expect(readPicture("u")).toEqual(DEFAULT_PICTURE);
  });

  it("reads the default and throws nothing when storage refuses", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(readPicture("u")).toEqual(DEFAULT_PICTURE);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(() => writePicture("u", DEFAULT_PICTURE)).not.toThrow();
  });
});

describe("pictureStyle", () => {
  it("is none at the defaults, with the fit as the objectFit", () => {
    expect(pictureStyle(DEFAULT_PICTURE)).toEqual({ objectFit: "contain", filter: "none" });
  });

  it("writes the three functions when any of the three differs from 100", () => {
    expect(pictureStyle({ fit: "cover", brightness: 120, contrast: 90, saturation: 110 }).filter).toBe(
      "brightness(120%) contrast(90%) saturate(110%)",
    );
    expect(pictureStyle({ fit: "contain", brightness: 105, contrast: 100, saturation: 100 }).filter).toBe(
      "brightness(105%) contrast(100%) saturate(100%)",
    );
  });

  it.each(["contain", "cover", "fill"] as const)("passes the fit %s through", (fit) => {
    expect(pictureStyle({ ...DEFAULT_PICTURE, fit }).objectFit).toBe(fit);
  });
});
