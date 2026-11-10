// SPDX-License-Identifier: AGPL-3.0-or-later

// The spaces a drive list names a location from include the spaces the caller has hidden (tl4 r1, finding 6): the caller reaches
// them, so their names are known, and a top-level item in one shows the space's name. The hidden list is read once per session,
// so a drive list mounted again reads nothing (tl4 r2, N4).
import type { ReactNode } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { FolderAccess } from "../files/types";
import { session, type Session } from "../session";
import { get } from "../store";

const api = vi.hoisted(() => ({ myFolders: vi.fn() }));
vi.mock("../files/api", () => api);

import { folders, hiddenFolders } from "../files/folders";
import { useLocationSpaces } from "./spaces";

const shown = { id: "fld_a", name: "Finance", kind: "team", root_node_id: "root_a" } as unknown as FolderAccess;
const hidden = { id: "fld_h", name: "Archive", kind: "team", root_node_id: "root_h" } as unknown as FolderAccess;

let client: QueryClient;
function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const s: Session = {
    user: { id: "u1", email: "olivia@example.com", display_name: "Olivia", role: "member", locale: "en" } as Session["user"],
    account: null,
    csrf: "csrf",
    publicLinks: false,
  } as Session;
  session.set(s);
  folders.set([shown]);
  hiddenFolders.set([]);
  api.myFolders.mockReset();
  api.myFolders.mockResolvedValue([hidden]);
});

describe("useLocationSpaces", () => {
  it("names a location from the shown spaces and the hidden ones the caller reaches", async () => {
    const { result } = renderHook(() => useLocationSpaces(true), { wrapper });
    await waitFor(() => expect(result.current.map((f) => f.id)).toEqual(expect.arrayContaining(["fld_a", "fld_h"])));
  });

  it("leaves the shown spaces list as it was, so a space picker still lists only what the caller shows", async () => {
    renderHook(() => useLocationSpaces(true), { wrapper });
    await waitFor(() => expect(api.myFolders).toHaveBeenCalledWith(true));
    expect(get(folders).map((f) => f.id)).toEqual(["fld_a"]);
  });

  // tl4 r2, N4: the hidden list is read once per session. A drive list mounted again (another page, or the same one after a
  // visit elsewhere) finds it there and makes no request.
  it("reads the hidden spaces once per session: a drive list mounted again makes no new request", async () => {
    const first = renderHook(() => useLocationSpaces(true), { wrapper });
    await waitFor(() => expect(api.myFolders).toHaveBeenCalledTimes(1));
    first.unmount();
    renderHook(() => useLocationSpaces(true), { wrapper });
    await new Promise((r) => setTimeout(r, 30));
    expect(api.myFolders).toHaveBeenCalledTimes(1);
  });
});
