// SPDX-License-Identifier: AGPL-3.0-or-later

// A revisit of a share's address must not bring its files back once the person has cancelled, uploaded, or signed out and in
// again (decision 82). The share module and the session are real; the worker is a fake that holds shares by id.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { FolderAccess } from "../lib/files/types";
import { clearSession, session, type Session } from "../lib/session";

let held: Record<string, File[]> = {};

// The answer port delivers the files unchanged, as in share.test.ts: a real port's copy of a test File loses its name here.
class SameProcessChannel {
  port1: { onmessage: ((e: { data: unknown }) => void) | null; close(): void } = { onmessage: null, close() {} };
  port2 = {
    postMessage: (data: unknown) => this.port1.onmessage?.({ data }),
  };
}
(globalThis as unknown as { MessageChannel: unknown }).MessageChannel = SameProcessChannel;

Object.defineProperty(navigator, "serviceWorker", {
  configurable: true,
  value: {
    controller: {
      postMessage(msg: { type: string; id: string }, ports: MessagePort[]) {
        if (msg.type === "SHARE_TAKE") {
          const files = held[msg.id] ?? [];
          delete held[msg.id];
          ports[0].postMessage({ files });
        }
        if (msg.type === "SHARE_DROP") delete held[msg.id];
      },
    },
  },
});

const add = vi.fn();
vi.mock("../lib/files/uploads", () => ({
  manager: { add: (...args: unknown[]) => add(...args) },
}));

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
vi.mock("../lib/files/api", () => ({
  myFolders: async () => [mine],
  getMe: async () => ({ personal_folder_id: PERSONAL }),
  childrenPage: async () => ({ items: [] }),
}));

import SharePage from "./SharePage";

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

const signedIn = (name: string) =>
  session.set({ user: { id: name, role: "user" }, impersonation: null } as unknown as Session);

beforeEach(() => {
  add.mockReset();
  held = {};
  signedIn("usr_a");
});

describe("a revisit of a share's address", () => {
  it("does not list the files again after Cancel", async () => {
    held = { r1: [new File(["a"], "cancel-me.txt", { type: "text/plain" })] };
    const first = renderAt("r1");
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByTestId("where")).not.toBeNull());
    first.unmount();
    renderAt("r1");
    expect(screen.queryByText("cancel-me.txt")).toBeNull();
    await screen.findByText(/Share the files again/);
    expect(screen.queryByText("cancel-me.txt")).toBeNull();
  });

  it("does not list the files again after Upload", async () => {
    held = { r2: [new File(["b"], "uploaded.txt", { type: "text/plain" })] };
    const first = renderAt("r2");
    fireEvent.click(await screen.findByRole("button", { name: "Upload" }));
    await waitFor(() => expect(add).toHaveBeenCalledTimes(1));
    first.unmount();
    renderAt("r2");
    expect(screen.queryByText("uploaded.txt")).toBeNull();
    await screen.findByText(/Share the files again/);
    expect(screen.queryByText("uploaded.txt")).toBeNull();
  });

  it("does not list the files again after a sign-out and a new sign-in", async () => {
    held = { r3: [new File(["c"], "signed-out.txt", { type: "text/plain" })] };
    const first = renderAt("r3");
    await screen.findByText("signed-out.txt");
    first.unmount();
    clearSession();
    signedIn("usr_b");
    renderAt("r3");
    expect(screen.queryByText("signed-out.txt")).toBeNull();
    await screen.findByText(/Share the files again/);
    expect(screen.queryByText("signed-out.txt")).toBeNull();
  });
});
