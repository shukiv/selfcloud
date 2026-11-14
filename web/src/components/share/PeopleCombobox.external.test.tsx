// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../../lib/session";

const api = vi.hoisted(() => ({ searchDirectory: vi.fn() }));
vi.mock("../../lib/sharing/api", async (orig) => ({ ...(await orig<typeof import("../../lib/sharing/api")>()), ...api }));

import PeopleCombobox from "./PeopleCombobox";

function signedWith(externalSharing: boolean): Session {
  return { user: { id: "usr_ann", email: "ann@example.com" }, accountExternalSharing: externalSharing } as unknown as Session;
}

const OFF_HINT = "To share with people in other accounts, your account's owner can allow it in Manage > Overview.";

describe("the combobox's outside option", () => {
  beforeEach(() => {
    session.set(signedWith(true));
    api.searchDirectory.mockResolvedValue({ items: [] });
  });
  afterEach(() => {
    vi.clearAllMocks();
    session.set(null);
  });

  it("offers a complete unknown address as one option, with its hint and tag", async () => {
    const user = userEvent.setup();
    render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" allowOutside onpick={vi.fn()} onoutside={vi.fn()} />);
    await user.type(screen.getByRole("combobox"), "mia@else.example");
    expect(await screen.findByText("Share with mia@else.example")).toBeTruthy();
    expect(screen.getByText("Someone outside your account")).toBeTruthy();
    expect(screen.getByText("Other account")).toBeTruthy();
    expect(screen.getAllByRole("option")).toHaveLength(1);
  });

  it("choosing it tells the parent the address, never a person of the account", async () => {
    const user = userEvent.setup();
    const onpick = vi.fn();
    const onoutside = vi.fn();
    render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" allowOutside onpick={onpick} onoutside={onoutside} />);
    await user.type(screen.getByRole("combobox"), "mia@else.example");
    await user.click(await screen.findByText("Share with mia@else.example"));
    expect(onoutside).toHaveBeenLastCalledWith("mia@else.example");
    expect(onpick).not.toHaveBeenCalledWith(expect.objectContaining({ id: expect.anything() }));
  });

  it("the arrow keys reach it and Enter chooses it", async () => {
    const onoutside = vi.fn();
    render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" allowOutside onpick={vi.fn()} onoutside={onoutside} />);
    const box = screen.getByRole("combobox");
    await userEvent.setup().type(box, "mia@else.example");
    await screen.findByText("Share with mia@else.example");
    fireEvent.keyDown(box, { key: "ArrowDown", keyCode: 40 });
    expect(box.getAttribute("aria-activedescendant")).toBe("share-who-opt-0");
    fireEvent.keyDown(box, { key: "Enter", keyCode: 13 });
    expect(onoutside).toHaveBeenLastCalledWith("mia@else.example");
  });

  it("an address the directory knows is a person, not an outside option", async () => {
    api.searchDirectory.mockResolvedValue({ items: [{ id: "usr_mia", kind: "user", name: "Mia Member", email: "mia@else.example" }] });
    const user = userEvent.setup();
    render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" allowOutside onpick={vi.fn()} onoutside={vi.fn()} />);
    await user.type(screen.getByRole("combobox"), "mia@else.example");
    expect(await screen.findByText("Mia Member")).toBeTruthy();
    expect(screen.queryByText("Share with mia@else.example")).toBeNull();
  });

  it("a partial address offers nothing", async () => {
    const user = userEvent.setup();
    render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" allowOutside onpick={vi.fn()} onoutside={vi.fn()} />);
    await user.type(screen.getByRole("combobox"), "mia@else");
    await waitFor(() => expect(api.searchDirectory).toHaveBeenCalled());
    expect(screen.queryByText(/Share with/)).toBeNull();
  });

  it("with the account's switch off there is no option, and the hint says where it is turned on", async () => {
    session.set(signedWith(false));
    const user = userEvent.setup();
    render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" allowOutside onpick={vi.fn()} onoutside={vi.fn()} />);
    await user.type(screen.getByRole("combobox"), "mia@else.example");
    await waitFor(() => expect(api.searchDirectory).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByText("Share with mia@else.example")).toBeNull());
    expect(screen.getByText(OFF_HINT)).toBeTruthy();
  });

  it("a field that does not allow outside offers nothing, whatever the switch", async () => {
    const user = userEvent.setup();
    render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" onpick={vi.fn()} />);
    await user.type(screen.getByRole("combobox"), "mia@else.example");
    await waitFor(() => expect(api.searchDirectory).toHaveBeenCalled());
    expect(screen.queryByText(/Share with/)).toBeNull();
    expect(screen.queryByText(OFF_HINT)).toBeNull();
  });

  it("typing again after the choice tells the parent the choice is gone", async () => {
    const user = userEvent.setup();
    const onoutside = vi.fn();
    render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" allowOutside onpick={vi.fn()} onoutside={onoutside} />);
    const box = screen.getByRole("combobox");
    await user.type(box, "mia@else.example");
    await user.click(await screen.findByText("Share with mia@else.example"));
    expect(onoutside).toHaveBeenLastCalledWith("mia@else.example");
    await user.type(box, "x");
    expect(onoutside).toHaveBeenLastCalledWith(null);
  });
});