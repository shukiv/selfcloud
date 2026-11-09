// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import EmptyState from "./EmptyState";

describe("EmptyState (decision 111: an empty state is antd's Empty)", () => {
  it("renders antd's .ant-empty with the text as its description", () => {
    const { container } = render(<EmptyState text="Nothing is starred yet." />);
    expect(container.querySelector(".ant-empty")).not.toBeNull();
    expect(container.querySelector(".ant-empty .ant-empty-description")?.textContent).toBe("Nothing is starred yet.");
  });

  it("the page size shows antd's default picture, and the compact size the simple one", () => {
    const { container } = render(<EmptyState text="Page." />);
    expect(container.querySelector(".ant-empty-image svg")).not.toBeNull();
    expect(container.querySelector(".ant-empty-normal")).toBeNull();

    const compact = render(<EmptyState text="Compact." size="compact" />);
    expect(compact.container.querySelector(".ant-empty-normal")).not.toBeNull();
    expect(compact.container.querySelector(".ant-empty-image svg")).not.toBeNull();
  });

  it("defaults to the page size", () => {
    const { container } = render(<EmptyState text="Default." />);
    expect(container.querySelector(".ant-empty-normal")).toBeNull();
  });

  it("carries the role and the class the old element had, when asked", () => {
    const { container } = render(<EmptyState text="Live." role="status" className="empty" />);
    const region = container.querySelector('[role="status"]') as HTMLElement;
    expect(region).not.toBeNull();
    expect(region.classList.contains("empty")).toBe(true);
    expect(region.querySelector(".ant-empty .ant-empty-description")?.textContent).toBe("Live.");
  });

  it("has no status role unless one is asked for", () => {
    const { container } = render(<EmptyState text="Quiet." />);
    expect(container.querySelector('[role="status"]')).toBeNull();
  });

  it("keeps an action under the text, where the page offers one", () => {
    render(
      <EmptyState text="Empty folder.">
        <button type="button">Upload files</button>
      </EmptyState>,
    );
    const button = screen.getByRole("button", { name: "Upload files" });
    expect(button.closest(".ant-empty")).not.toBeNull();
  });
});
