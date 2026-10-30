// SPDX-License-Identifier: AGPL-3.0-or-later

import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it } from "vitest";
import { clearOnSessionChange, createQueryClient, queryKey } from "./query";
import { session, type Session } from "./session";
import { folders, hiddenFolders } from "./files/folders";
import { myStorage } from "./storage/api";
import { pendingRequests } from "./sharing/requestCount";
import { get } from "./store";

function signedAs(id: string, csrf = "t1"): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: `Name ${id}`, role: "member", locale: "en", status: "active" },
    account: null,
    csrf,
    publicLinks: false,
  };
}

describe("the query cache follows the session", () => {
  let client: QueryClient;
  let stop: () => void;

  beforeEach(() => {
    session.set(null);
    client = new QueryClient();
    stop = clearOnSessionChange(client);
  });

  it("clears the cache when the person changes", () => {
    session.set(signedAs("u1"));
    client.setQueryData(queryKey("children", "f1"), { pages: [] });
    expect(client.getQueryCache().getAll().length).toBe(1);
    session.set(signedAs("u2"));
    expect(client.getQueryCache().getAll().length).toBe(0);
    stop();
  });

  it("prefixes every key with the session it was made under", () => {
    session.set(signedAs("u1"));
    const one = queryKey("children", "f1");
    session.set(signedAs("u2"));
    const two = queryKey("children", "f1");
    expect(one[0]).not.toBe(two[0]);
    expect(two.slice(1)).toEqual(["children", "f1"]);
    stop();
  });

  it("empties the user stores when the person changes, so the next person never sees them", () => {
    session.set(signedAs("u1"));
    folders.set([{ id: "fld_a", name: "Hidden team", kind: "team", role: "manager" } as never]);
    hiddenFolders.set([{ id: "fld_h", name: "Hidden team", kind: "team", role: "viewer" } as never]);
    myStorage.set({ used: 5, limit: 10 } as never);
    pendingRequests.set(3);
    session.set(signedAs("u2"));
    expect(get(folders)).toEqual([]);
    expect(get(hiddenFolders)).toEqual([]);
    expect(get(myStorage)).toBeNull();
    expect(get(pendingRequests)).toBeNull();
    stop();
  });
});

describe("a read still retrying when the person changes is stopped", () => {
  it("does not send the read again after the session has ended", async () => {
    const client = createQueryClient();
    const cleanup = clearOnSessionChange(client);
    session.set(signedAs("u1"));
    let calls = 0;
    const key = queryKey("children", "fld_a", null);
    const pending = client
      .fetchQuery({
        queryKey: key,
        // A dropped connection: the client retries the read after a delay.
        queryFn: async () => {
          calls++;
          throw new TypeError("Failed to fetch");
        },
        retry: 1,
        retryDelay: 150,
      })
      .catch(() => undefined);
    await new Promise((r) => setTimeout(r, 30));
    session.set(null);
    await pending;
    await new Promise((r) => setTimeout(r, 400));
    expect(calls).toBe(1);
    cleanup();
  });
});
