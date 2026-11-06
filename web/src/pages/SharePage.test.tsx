// SPDX-License-Identifier: AGPL-3.0-or-later

// The share page (decision 82): the files the worker holds for a share, a place to save them to that starts at My files, and
// Upload, which queues them into the upload manager like a file picked on the files page.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { FolderAccess } from "../lib/files/types";
import { formatBytes } from "../lib/files/format";
import { session, type Session } from "../lib/session";

const takeShare = vi.fn();
const dropShare = vi.fn();
vi.mock("../lib/pwa/share", () => ({
  takeShare: (...args: unknown[]) => takeShare(...args),
  dropShare: (...args: unknown[]) => dropShare(...args),
}));

const add = vi.fn();
vi.mock("../lib/files/uploads", () => ({
  manager: { add: (...args: unknown[]) => add(...args) },
}));

const myFolders = vi.fn();
const getMe = vi.fn();
const childrenPage = vi.fn();
vi.mock("../lib/files/api", () => ({
  myFolders: (...args: unknown[]) => myFolders(...args),
  getMe: (...args: unknown[]) => getMe(...args),
  childrenPage: (...args: unknown[]) => childrenPage(...args),
}));

import SharePage from "./SharePage";

const PERSONAL = "fld_me";
const mine: FolderAccess = {
  id: PERSONAL,
  name: "My files",
  kind: "personal",
  access: "write",
  role: "manager",
  members_only: false,
  sharing_managers_only: false,
  no_download_for_viewers: false,
  allow_links: false,
};

// Where shows the address the page left for, so a test can see the redirect.
function Where() {
  const loc = useLocation();
  return <p data-testid="where">{loc.pathname}</p>;
}

function renderAt(id: string) {
  return render(
    <MemoryRouter initialEntries={[`/share?id=${id}`]}>
      <Routes>
        <Route path="/share" element={<SharePage />} />
        <Route path="*" element={<Where />} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  takeShare.mockReset();
  dropShare.mockReset();
  add.mockReset();
  myFolders.mockReset();
  getMe.mockReset();
  childrenPage.mockReset();
  myFolders.mockResolvedValue([mine]);
  getMe.mockResolvedValue({ personal_folder_id: PERSONAL });
  session.set({ user: { id: "usr_1", role: "user" }, impersonation: null } as unknown as Session);
});

describe("SharePage", () => {
  it("says so when no files are waiting, and takes nothing with no id", async () => {
    renderAt("");
    await screen.findByText(/Share the files again/);
    expect(takeShare).not.toHaveBeenCalled();
  });

  it("says so when the worker holds nothing for the id", async () => {
    takeShare.mockResolvedValue([]);
    renderAt("abc");
    await screen.findByText(/Share the files again/);
    expect(takeShare).toHaveBeenCalledWith("abc");
  });

  it("lists the files with their names and sizes", async () => {
    takeShare.mockResolvedValue([new File(["abc"], "a.txt"), new File(["x".repeat(2048)], "b.pdf")]);
    renderAt("abc");
    await screen.findByText("a.txt");
    screen.getByText("b.pdf");
    screen.getByText(formatBytes(2048, "en"));
  });

  it("says the files came from another app, shows each one's type, and queues nothing before Upload", async () => {
    takeShare.mockResolvedValue([new File(["abc"], "a.txt", { type: "text/plain" })]);
    renderAt("abc");
    await screen.findByText(/came from another app/);
    screen.getByText("text/plain");
    expect(add).not.toHaveBeenCalled();
  });

  it("starts with My files as the place the files are saved to", async () => {
    takeShare.mockResolvedValue([new File(["abc"], "a.txt")]);
    renderAt("abc");
    await screen.findByText(/Save to/);
    expect(screen.getAllByText("My files").length).toBeGreaterThan(0);
    expect((screen.getByRole("button", { name: "Upload" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("refuses while the person views as another person, and drops the files", async () => {
    session.set({ user: { id: "usr_1", role: "user" }, impersonation: { endsAt: "2026-11-06T12:00:00Z" } } as unknown as Session);
    renderAt("abc");
    await screen.findByText(/viewing as another person/);
    expect(takeShare).not.toHaveBeenCalled();
    expect(dropShare).toHaveBeenCalledWith("abc");
  });

  it("queues each file into the chosen folder and opens it", async () => {
    const a = new File(["abc"], "a.txt");
    const b = new File(["x"], "b.pdf");
    takeShare.mockResolvedValue([a, b]);
    renderAt("abc");
    fireEvent.click(await screen.findByRole("button", { name: "Upload" }));
    await waitFor(() => expect(add).toHaveBeenCalledTimes(1));
    expect(add.mock.calls[0][0]).toEqual([
      { file: a, target: { folder: PERSONAL, path: "/" } },
      { file: b, target: { folder: PERSONAL, path: "/" } },
    ]);
    expect((await screen.findByTestId("where")).textContent).toBe(`/files/${PERSONAL}`);
  });

  it("Cancel drops the files, tells the worker, and goes to My files", async () => {
    takeShare.mockResolvedValue([new File(["abc"], "a.txt")]);
    renderAt("abc");
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    expect(dropShare).toHaveBeenCalledWith("abc");
    expect(add).not.toHaveBeenCalled();
    expect((await screen.findByTestId("where")).textContent).toBe("/files");
  });
});
