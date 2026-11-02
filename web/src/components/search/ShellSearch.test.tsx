// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useEffect } from "react";
import { Link, MemoryRouter, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { describe, expect, it } from "vitest";
import ShellSearch from "./ShellSearch";

const BOX = "Search names and text in files";

// Where shows the address the test is at, so the test can read what the box wrote.
function Where() {
  const l = useLocation();
  return <p data-testid="where">{`${l.pathname}${l.search}`}</p>;
}

function renderAt(url: string, wide = true) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <ShellSearch wide={wide} />
      <Where />
      <Routes>
        <Route path="*" element={null} />
      </Routes>
    </MemoryRouter>,
  );
}

const where = () => screen.getByTestId("where").textContent;

describe("the search box in the top bar", () => {
  it("a word typed elsewhere opens the search page with the word in its address, after a pause", async () => {
    renderAt("/files");
    const user = userEvent.setup();
    await user.type(screen.getByLabelText(BOX), "budget");
    expect(where()).toBe("/files");
    await waitFor(() => expect(where()).toBe("/search?q=budget"));
  });

  it("Enter opens the search page at once", () => {
    renderAt("/files");
    const box = screen.getByLabelText(BOX);
    fireEvent.change(box, { target: { value: "budget" } });
    fireEvent.keyDown(box, { key: "Enter" });
    expect(where()).toBe("/search?q=budget");
  });

  it("an empty box opens nothing", async () => {
    renderAt("/files");
    fireEvent.keyDown(screen.getByLabelText(BOX), { key: "Enter" });
    await new Promise((r) => setTimeout(r, 400));
    expect(where()).toBe("/files");
  });

  it("on the search page the box keeps the filters in the address and replaces the entry", async () => {
    renderAt("/search?kind=file");
    const user = userEvent.setup();
    await user.type(screen.getByLabelText(BOX), "tax");
    await waitFor(() => expect(where()).toBe("/search?kind=file&q=tax"));
  });

  it("a word typed and then a click on a nav link within the pause does not pull the person to the search page", async () => {
    render(
      <MemoryRouter initialEntries={["/recent"]}>
        <ShellSearch wide />
        <Link to="/files">Go to files</Link>
        <Where />
      </MemoryRouter>,
    );
    const user = userEvent.setup();
    await user.type(screen.getByLabelText(BOX), "budget");
    await user.click(screen.getByRole("link", { name: "Go to files" }));
    expect(where()).toBe("/files");
    await new Promise((r) => setTimeout(r, 400));
    expect(where()).toBe("/files");
  });

  it("a word typed while /files moves on to the first folder still opens the search page", async () => {
    // The files page replaces /files with the first folder's address once the folders are read. That move is the page's,
    // not the person's, so what they typed is not dropped.
    function Redirect() {
      const go = useNavigate();
      const l = useLocation();
      useEffect(() => {
        if (l.pathname !== "/files") return;
        const id = setTimeout(() => go("/files/fld_first", { replace: true }), 60);
        return () => clearTimeout(id);
      }, [l.pathname, go]);
      return null;
    }
    render(
      <MemoryRouter initialEntries={["/files"]}>
        <ShellSearch wide />
        <Redirect />
        <Where />
      </MemoryRouter>,
    );
    const user = userEvent.setup();
    await user.type(screen.getByLabelText(BOX), "budget");
    await waitFor(() => expect(where()).toBe("/search?q=budget"));
  });

  it("on a phone it is one button that opens the search page", () => {
    renderAt("/files", false);
    expect(screen.queryByLabelText(BOX)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Search" }));
    expect(where()).toBe("/search");
  });
});
