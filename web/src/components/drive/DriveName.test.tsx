// SPDX-License-Identifier: AGPL-3.0-or-later

// A drive name is text in the text colour at weight 500, as the approved mockup draws it (tl4 r1, finding 5). Search keeps the
// link colour, which its contrast check reads (docs/design.md, decision 137).
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import DriveName from "./DriveName";
import type { Node } from "../../lib/files/types";

const node = { id: "nod_1", name: "Budget.pdf", kind: "file" } as unknown as Node;
const LINK_RGB = "rgb(22, 119, 255)"; // antd's default link colour, #1677ff

describe("DriveName", () => {
  it("draws a name in the text colour at weight 500 by default", () => {
    render(<DriveName node={node} />);
    const link = screen.getByRole("link", { name: "Budget.pdf" });
    expect(link.style.fontWeight).toBe("500");
    expect(link.style.color).not.toBe(LINK_RGB);
    expect(link.style.color).not.toBe("");
  });

  it("keeps the link colour at weight 600 when the page asks for it (search)", () => {
    render(<DriveName node={node} linkColour />);
    const link = screen.getByRole("link", { name: "Budget.pdf" });
    expect(link.style.fontWeight).toBe("600");
    expect(link.style.color).toBe(LINK_RGB);
  });
});
