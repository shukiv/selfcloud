// SPDX-License-Identifier: AGPL-3.0-or-later

import "../../test/rangeRects";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import CodeView, { type FindAction } from "./CodeView";

// The page's nonce, as the server writes it. It is set before the first editor is drawn: CodeMirror writes its style sheet
// once per document, with the nonce it is given then.
const meta = document.createElement("meta");
meta.setAttribute("property", "csp-nonce");
meta.setAttribute("nonce", "nonce-for-tests");
document.head.appendChild(meta);

afterEach(() => {
  document.documentElement.dir = "ltr";
});

const editor = () => document.querySelector<HTMLElement>(".cm-editor");

describe("CodeView", () => {
  it("writes CodeMirror's style sheet with the page's nonce", () => {
    render(<CodeView text={"const a = 1;\n"} pack="javascript" name="a.js" dark={false} />);
    expect(editor()).not.toBeNull();
    const sheets = [...document.querySelectorAll("style")].filter((s) => (s.textContent ?? "").includes(".cm-scroller"));
    expect(sheets.length).toBeGreaterThan(0);
    for (const s of sheets) expect(s.getAttribute("nonce")).toBe("nonce-for-tests");
  });

  it("shows the text read-only, with line numbers, and no keyboard on a phone", () => {
    render(<CodeView text={"one\ntwo\nthree"} pack="text" name="notes.txt" dark={false} />);
    const content = document.querySelector(".cm-content")!;
    expect([...content.querySelectorAll(".cm-line")].map((l) => l.textContent)).toEqual(["one", "two", "three"]);
    expect(content.getAttribute("aria-readonly")).toBe("true");
    expect(content.getAttribute("aria-label")).toBe("notes.txt");
    expect(content.getAttribute("inputmode")).toBe("none");
    expect(document.querySelector(".cm-lineNumbers")).not.toBeNull();
  });

  it("never reads the text as markup", () => {
    render(<CodeView text={"<b>not html</b><script>x()</script>"} pack="html" name="a.html" dark={false} />);
    const content = document.querySelector(".cm-content")!;
    expect(content.textContent).toBe("<b>not html</b><script>x()</script>");
    expect(content.querySelector("b, script")).toBeNull();
  });

  it("keeps the code area left to right in a right-to-left page, with the gutter before the text", () => {
    document.documentElement.dir = "rtl";
    render(<CodeView text={"x = 1"} pack="python" name="a.py" dark={false} />);
    const host = screen.getByTestId("code-view");
    expect(host.getAttribute("dir")).toBe("ltr");
    expect(host.contains(editor())).toBe(true);
    const scroller = document.querySelector(".cm-scroller")!;
    const order = [...scroller.children].map((c) => c.className);
    expect(order.findIndex((c) => c.includes("cm-gutters"))).toBeLessThan(order.findIndex((c) => c.includes("cm-content")));
  });

  it("follows the theme without drawing the editor again", () => {
    const { rerender } = render(<CodeView text={"x"} pack="text" name="a.txt" dark={false} />);
    const first = editor()!;
    expect(first.classList.contains("sc-code-light")).toBe(true);
    rerender(<CodeView text={"x"} pack="text" name="a.txt" dark />);
    expect(editor()).toBe(first);
    expect(first.classList.contains("sc-code-dark")).toBe(true);
    expect(first.classList.contains("sc-code-light")).toBe(false);
    rerender(<CodeView text={"x"} pack="text" name="a.txt" dark={false} />);
    expect(editor()).toBe(first);
    expect(first.classList.contains("sc-code-light")).toBe(true);
  });

  it("wraps lines, unless one is longer than 2,000 characters", () => {
    const { unmount } = render(<CodeView text={"short line"} pack="text" name="a.txt" dark={false} />);
    expect(document.querySelector(".cm-content")!.classList.contains("cm-lineWrapping")).toBe(true);
    unmount();
    render(<CodeView text={`short\n${"m".repeat(2001)}`} pack="text" name="a.min.js" dark={false} />);
    expect(document.querySelector(".cm-content")!.classList.contains("cm-lineWrapping")).toBe(false);
  });

  it("colours the text once its language pack has loaded", async () => {
    render(<CodeView text={'{"name": "value", "n": 1}'} pack="json" name="a.json" dark={false} />);
    await waitFor(() => expect(document.querySelector(".cm-content .tok-propertyName")).not.toBeNull());
    expect(document.querySelector(".cm-content .tok-string")).not.toBeNull();
  });

  it("offers Find, and Escape closes the find panel without reaching the viewer around it", async () => {
    const got: { find: FindAction | null } = { find: null };
    const outer = vi.fn();
    // The panel is gone by the time the key bubbles on, so React finds no handler for it either way; a listener of the page's
    // own (the listing's shortcuts are one) still hears it unless CodeMirror stops it.
    const page = vi.fn();
    const { container } = render(
      <div onKeyDown={outer}>
        <CodeView text={"alpha\nbeta"} pack="text" name="a.txt" dark={false} onFind={(f) => (got.find = f)} />
      </div>,
    );
    container.addEventListener("keydown", page);
    const find = got.find as FindAction | null;
    expect(find).not.toBeNull();
    expect(find!.label).toBe("Find");
    act(() => find!.run());
    const field = await screen.findByRole("textbox", { name: "Find" });
    expect(document.querySelector(".cm-search")).not.toBeNull();
    fireEvent.keyDown(field, { key: "Escape", code: "Escape", keyCode: 27 });
    expect(document.querySelector(".cm-search")).toBeNull();
    expect(outer).not.toHaveBeenCalled();
    expect(page).not.toHaveBeenCalled();
    // With no panel open, Escape goes on to the viewer, which closes.
    fireEvent.keyDown(document.querySelector(".cm-content")!, { key: "Escape", code: "Escape", keyCode: 27 });
    expect(outer).toHaveBeenCalledTimes(1);
    expect(page).toHaveBeenCalledTimes(1);
  });

  it("tells the viewer the find action is gone when it unmounts", () => {
    const onFind = vi.fn();
    const { unmount } = render(<CodeView text={"x"} pack="text" name="a.txt" dark={false} onFind={onFind} />);
    expect(onFind).toHaveBeenLastCalledWith(expect.objectContaining({ label: "Find" }));
    unmount();
    expect(onFind).toHaveBeenLastCalledWith(null);
  });

  it("shows an empty file as antd's Empty, not as an empty editor", () => {
    render(<CodeView text="" pack="text" name="empty.txt" dark={false} />);
    expect(screen.getByText("This file is empty.")).toBeTruthy();
    expect(document.querySelector(".ant-empty")).not.toBeNull();
    expect(editor()).toBeNull();
  });
});
