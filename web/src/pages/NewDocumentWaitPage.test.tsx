// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, render as rtlRender, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const createMock = vi.hoisted(() => vi.fn());
const apiMock = vi.hoisted(() => vi.fn());
vi.mock("../lib/files/api", () => ({ createDocument: createMock, statNode: vi.fn() }));
vi.mock("../lib/api", async (importOriginal) => ({ ...(await importOriginal<typeof import("../lib/api")>()), api: apiMock }));

import NewDocumentWaitPage from "./NewDocumentWaitPage";
import { finishHandoff, startHandoff } from "../lib/files/newDocumentHandoff";

// FakeChannel delivers each post to every other channel of the same name, as the browser does.
const listeners = new Set<(e: { data: unknown }) => void>();
class FakeChannel {
  onmessage: ((e: { data: unknown }) => void) | null = null;
  constructor() {
    listeners.add((e) => this.onmessage?.(e));
  }
  postMessage(data: unknown) {
    for (const l of listeners) l({ data });
  }
  close() {}
}

const K = "AAAAAAAAAAAAAAAAAAAAAA";

// Where looks at the address the page left, so a test sees the editor's address after the replace.
function Where() {
  const loc = useLocation();
  return <div data-testid="where">{loc.pathname + loc.search}</div>;
}

function render(path: string) {
  return rtlRender(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="*" element={<><NewDocumentWaitPage /><Where /></>} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  localStorage.clear();
  createMock.mockReset();
  apiMock.mockReset();
  listeners.clear();
  vi.stubGlobal("BroadcastChannel", FakeChannel);
});

describe("the wait page (decision 102, round 2 R5): it never makes a file", () => {
  it("a key nobody started shows nothing to open, and creates nothing", async () => {
    render(`/edit/wait?k=${K}`);
    await screen.findByText("Nothing to open here");
    expect(screen.getByRole("button", { name: /close/i })).toBeTruthy();
    expect(createMock).not.toHaveBeenCalled();
    expect(apiMock).not.toHaveBeenCalled();
  });

  it("a malformed key shows nothing to open, and creates nothing", async () => {
    render("/edit/wait?k=../kind=docx&parent=nod_root");
    await screen.findByText("Nothing to open here");
    expect(createMock).not.toHaveBeenCalled();
  });

  it("a result stored before the tab loaded replaces the address with the editor's, and the entry is taken", async () => {
    startHandoff(K, Date.now());
    finishHandoff(K, { node: "nod_a1", from: "/files/fld_b", tab: true });
    render(`/edit/wait?k=${K}`);
    await waitFor(() => expect(screen.getByTestId("where").textContent).toBe("/edit/nod_a1?mode=edit&from=%2Ffiles%2Ffld_b&tab=1"));
    expect(localStorage.getItem(`selfcloud.newdoc.${K}`)).toBeNull();
    expect(createMock).not.toHaveBeenCalled();
  });

  it("a result that comes while the tab waits is taken from the channel", async () => {
    startHandoff(K, Date.now());
    render(`/edit/wait?k=${K}`);
    expect(screen.getByText("Creating the file…")).toBeTruthy();
    await act(async () => {
      finishHandoff(K, { node: "nod_a2", from: null, tab: false });
    });
    await waitFor(() => expect(screen.getByTestId("where").textContent).toBe("/edit/nod_a2?mode=edit"));
    expect(createMock).not.toHaveBeenCalled();
  });

  it("a refused creation shows its error with a close button, and opens no editor", async () => {
    startHandoff(K, Date.now());
    finishHandoff(K, { error: "The file could not be made: over quota" });
    render(`/edit/wait?k=${K}`);
    await screen.findByRole("alert");
    expect(screen.getByText("The file could not be made: over quota")).toBeTruthy();
    expect(screen.getByRole("button", { name: /close/i })).toBeTruthy();
    expect(screen.getByTestId("where").textContent).toBe(`/edit/wait?k=${K}`);
  });

  it("an entry that expired before the tab loaded is refused", async () => {
    startHandoff(K, 0);
    finishHandoff(K, { node: "nod_a1", from: null, tab: true }, 1000);
    render(`/edit/wait?k=${K}`);
    await screen.findByText("Nothing to open here");
    expect(createMock).not.toHaveBeenCalled();
  });
});
