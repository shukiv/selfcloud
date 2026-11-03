// SPDX-License-Identifier: AGPL-3.0-or-later

// With no cover, a playlist row and the mini player show the file's coloured type icon, as the file list does (ic2, decision 72),
// not the grey box with a music note.
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import TagCover from "./TagCover";
import type { Node } from "../../lib/files/types";

const audio = { kind: "file", mime_type: "audio/ogg", name: "song.ogg" } as Pick<Node, "kind" | "mime_type" | "name">;

describe("TagCover placeholder (decision 72)", () => {
  it("shows the coloured type icon of an audio file when it has no cover", () => {
    const { container } = render(<TagCover nod="n1" kind="audio" size={28} enabled={false} node={audio} />);
    const tile = container.querySelector(".icon-tile");
    expect(tile).not.toBeNull();
    expect(container.querySelector("svg.lucide-music")).toBeNull();
  });

  it("keeps the outline note when no file is given", () => {
    const { container } = render(<TagCover nod="n1" kind="audio" size={28} enabled={false} />);
    expect(container.querySelector(".icon-tile")).toBeNull();
    expect(container.querySelector("svg")).not.toBeNull();
  });
});
