// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../lib/session";
import NotFoundPage from "./NotFoundPage";

// The access request is replaced by a stub that counts its mounts and shows the item it was given.
const stub = vi.hoisted(() => ({ mounts: 0 }));
vi.mock("../components/share/RequestAccess", async () => {
  const React = await import("react");
  return {
    default: ({ nodeID }: { nodeID: string }) => {
      React.useEffect(() => {
        stub.mounts++;
      }, []);
      return React.createElement("p", { "data-testid": "request-stub" }, `request for ${nodeID}`);
    },
  };
});

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en" } as Session["user"],
    account: null,
    csrf: `t-${id}`,
    publicLinks: false,
  };
}

describe("NotFoundPage asks for access only where it may", () => {
  beforeEach(() => {
    stub.mounts = 0;
  });
  afterEach(() => session.set(null));

  it("offers the access request to a signed-in person on an item", async () => {
    session.set(signedAs("u1"));
    render(<NotFoundPage nodeID="nod_aaaaaaaa" />);
    expect(await screen.findByTestId("request-stub")).toBeTruthy();
    expect(screen.getByText("request for nod_aaaaaaaa")).toBeTruthy();
  });

  it("offers no access request with no item", () => {
    session.set(signedAs("u1"));
    render(<NotFoundPage />);
    expect(screen.queryByTestId("request-stub")).toBeNull();
  });

  it("offers no access request to a signed-out visitor, even on an item", () => {
    session.set(null);
    render(<NotFoundPage nodeID="nod_aaaaaaaa" />);
    expect(screen.queryByTestId("request-stub")).toBeNull();
  });

  it("keys the request on the item: another item is a new form, not the old one", async () => {
    session.set(signedAs("u1"));
    const view = render(<NotFoundPage nodeID="nod_aaaaaaaa" />);
    await screen.findByText("request for nod_aaaaaaaa");
    view.rerender(<NotFoundPage nodeID="nod_bbbbbbbb" />);
    expect(await screen.findByText("request for nod_bbbbbbbb")).toBeTruthy();
    expect(stub.mounts).toBe(2);
  });

  it("removes the request when the session ends", async () => {
    session.set(signedAs("u1"));
    render(<NotFoundPage nodeID="nod_aaaaaaaa" />);
    await screen.findByTestId("request-stub");
    act(() => session.set(null));
    expect(screen.queryByTestId("request-stub")).toBeNull();
  });
});
