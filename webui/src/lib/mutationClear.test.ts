// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { QueryClient } from "@tanstack/react-query";

// Question for the review of slice v3: when the query client is cleared while a mutation is in flight (the
// sign-out case, lib/query.ts clearOnSessionChange), does the mutation's onSuccess still run?
describe("a mutation in flight when the client is cleared", () => {
  it("still runs onSuccess when its answer arrives after the clear", async () => {
    const client = new QueryClient();
    let answer!: () => void;
    const mutationFn = () =>
      new Promise<string>((resolve) => {
        answer = () => resolve("done");
      });
    const ran: string[] = [];
    const mutation = client.getMutationCache().build(client, {
      mutationFn,
      onSuccess: () => {
        ran.push("onSuccess");
      },
    });
    const pending = mutation.execute(undefined);
    await new Promise((r) => setTimeout(r, 0));
    client.clear();
    answer();
    await pending;
    expect(ran).toEqual(["onSuccess"]);
  });
});
