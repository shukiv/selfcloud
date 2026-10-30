// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../../lib/session";

const api = vi.hoisted(() => ({ searchDirectory: vi.fn() }));
vi.mock("../../lib/sharing/api", async (orig) => ({ ...(await orig<typeof import("../../lib/sharing/api")>()), ...api }));

import PeopleCombobox from "./PeopleCombobox";

describe("the people combobox", () => {
  beforeEach(() => {
    session.set({ user: { id: "usr_ann" } } as unknown as Session);
    api.searchDirectory.mockResolvedValue({ items: [{ id: "usr_mate", kind: "user", name: "Mateo Member", email: "mateo@example.com" }] });
  });
  afterEach(() => {
    vi.clearAllMocks();
    session.set(null);
  });

  it("searches only from two characters, after the pause", async () => {
    const user = userEvent.setup();
    render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" onpick={vi.fn()} />);
    await user.type(screen.getByRole("combobox"), "m");
    await new Promise((r) => setTimeout(r, 400));
    expect(api.searchDirectory).not.toHaveBeenCalled();
    await user.type(screen.getByRole("combobox"), "a");
    await waitFor(() => expect(api.searchDirectory).toHaveBeenCalledWith("ma", "all"));
  });

  it("Escape closes the list and does not reach the dialog around it", async () => {
    const user = userEvent.setup();
    const outer = vi.fn();
    render(
      <div onKeyDown={outer}>
        <PeopleCombobox id="share-who" label="Who" hint="Name or email" onpick={vi.fn()} />
      </div>,
    );
    await user.type(screen.getByRole("combobox"), "ma");
    expect(await screen.findByText("Mateo Member")).toBeTruthy();
    outer.mockClear();
    fireEvent.keyDown(screen.getByRole("combobox"), { key: "Escape", keyCode: 27 });
    await waitFor(() => expect(screen.queryByText("Mateo Member")).toBeNull());
    expect(outer).not.toHaveBeenCalled();
  });

  it("typing again after a choice tells the parent the choice is gone", async () => {
    const user = userEvent.setup();
    const onpick = vi.fn();
    render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" onpick={onpick} />);
    await user.type(screen.getByRole("combobox"), "ma");
    await user.click(await screen.findByText("Mateo Member"));
    expect(onpick).toHaveBeenLastCalledWith(expect.objectContaining({ id: "usr_mate" }));
    await user.type(screen.getByRole("combobox"), "x");
    expect(onpick).toHaveBeenLastCalledWith(null);
  });
});
