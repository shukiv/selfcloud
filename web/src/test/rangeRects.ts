// SPDX-License-Identifier: AGPL-3.0-or-later

// jsdom lays nothing out, and its Range has no client rects. CodeMirror measures text through them, so the tests that draw it
// import this file first: each Range then has the empty rects an element has in jsdom.
if (typeof Range !== "undefined") {
  if (!Range.prototype.getClientRects) {
    Range.prototype.getClientRects = function getClientRects() {
      return document.createElement("span").getClientRects();
    };
  }
  if (!Range.prototype.getBoundingClientRect) {
    Range.prototype.getBoundingClientRect = function getBoundingClientRect() {
      return document.createElement("span").getBoundingClientRect();
    };
  }
}

export {};
