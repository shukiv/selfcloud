// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useOncePer } from "./OwnerPage";

// A row action is held per row: an action on one row does not wait for an action on another row, but a second press on
// the same row while its action is out is still dropped (guard: one lock per row, not one per page).
describe("the per-row lock", () => {
  it("runs one row's action while another row's is in flight, and drops a second press on the same row", async () => {
    const { result } = renderHook(() => useOncePer());
    const calls: string[] = [];
    const release: Record<string, () => void> = {};
    const action = (key: string) => () =>
      new Promise<void>((resolve) => {
        calls.push(key);
        release[key] = resolve;
      });
    let a: Promise<void> = Promise.resolve();
    let b: Promise<void> = Promise.resolve();
    act(() => {
      a = result.current("usr_a", action("a"));
      b = result.current("usr_b", action("b"));
    });
    expect(calls).toEqual(["a", "b"]);
    act(() => {
      void result.current("usr_a", action("a-again"));
    });
    expect(calls).toEqual(["a", "b"]);
    await act(async () => {
      release.a();
      release.b();
      await Promise.all([a, b]);
    });
    await act(async () => {
      void result.current("usr_a", action("a-next"));
    });
    expect(calls).toEqual(["a", "b", "a-next"]);
  });
});
