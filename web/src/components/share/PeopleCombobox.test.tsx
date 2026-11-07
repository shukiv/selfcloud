// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
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
    // Focus asks for the account's list (decision 96); a single character is not a search.
    expect(api.searchDirectory).not.toHaveBeenCalledWith("m", "all");
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

  // Decision 96: the field lists the account's members before anything is typed.
  describe("the focus list", () => {
    const mate = { id: "usr_mate", kind: "user", name: "Mateo Member", email: "mateo@example.com" };
    const kim = { id: "usr_kim", kind: "user", name: "Kim Kept", email: "kim@example.com" };

    it("focus opens the list with the account's answer to an empty query", async () => {
      api.searchDirectory.mockImplementation(async (q: string) =>
        q === "" ? { items: [mate, kim], more: false } : { items: [mate], more: false },
      );
      render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" onpick={vi.fn()} />);
      // A focus alone, with no click, opens the list.
      fireEvent.focus(screen.getByRole("combobox"));
      expect(await screen.findByText("Mateo Member")).toBeTruthy();
      expect(screen.getByText("Kim Kept")).toBeTruthy();
      expect(api.searchDirectory).toHaveBeenCalledWith("", "all");
      expect(screen.getByRole("combobox").getAttribute("aria-expanded")).toBe("true");
      expect(screen.getByRole("status").textContent).toBe("2 people and groups");
    });

    it("ArrowDown on an empty, closed field opens the list", async () => {
      api.searchDirectory.mockResolvedValue({ items: [mate], more: false });
      const user = userEvent.setup();
      render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" onpick={vi.fn()} />);
      const box = screen.getByRole("combobox");
      await user.click(box);
      await user.keyboard("{Escape}");
      await waitFor(() => expect(screen.queryByText("Mateo Member")).toBeNull());
      fireEvent.keyDown(box, { key: "ArrowDown", keyCode: 40 });
      expect(await screen.findByText("Mateo Member")).toBeTruthy();
      expect(api.searchDirectory).toHaveBeenCalledTimes(1);
    });

    it("the list comes from one answer: clearing back to empty reuses it", async () => {
      api.searchDirectory.mockImplementation(async (q: string) =>
        q === "" ? { items: [mate], more: false } : { items: [kim], more: false },
      );
      const user = userEvent.setup();
      render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" onpick={vi.fn()} />);
      const box = screen.getByRole("combobox");
      await user.click(box);
      expect(await screen.findByText("Mateo Member")).toBeTruthy();
      await user.type(box, "ki");
      expect(await screen.findByText("Kim Kept")).toBeTruthy();
      await user.clear(box);
      expect(await screen.findByText("Mateo Member")).toBeTruthy();
      expect(api.searchDirectory.mock.calls.filter((c) => c[0] === "")).toHaveLength(1);
    });

    it("says there are more people, as text and not as an option", async () => {
      api.searchDirectory.mockResolvedValue({ items: [mate, kim], more: true });
      const user = userEvent.setup();
      render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" onpick={vi.fn()} />);
      await user.click(screen.getByRole("combobox"));
      expect(await screen.findByText("Type a name to find more people")).toBeTruthy();
      expect(screen.getAllByRole("option")).toHaveLength(2);
    });

    it("Escape closes the list and does not reach the dialog", async () => {
      api.searchDirectory.mockResolvedValue({ items: [mate], more: false });
      const user = userEvent.setup();
      const outer = vi.fn();
      render(
        <div onKeyDown={outer}>
          <PeopleCombobox id="share-who" label="Who" hint="Name or email" onpick={vi.fn()} />
        </div>,
      );
      const box = screen.getByRole("combobox");
      await user.click(box);
      expect(await screen.findByText("Mateo Member")).toBeTruthy();
      outer.mockClear();
      fireEvent.keyDown(box, { key: "Escape", keyCode: 27 });
      await waitFor(() => expect(screen.queryByText("Mateo Member")).toBeNull());
      expect(outer).not.toHaveBeenCalled();
    });

    it("a late focus answer is dropped once two characters are typed", async () => {
      let answerEmpty: (v: { items: unknown[]; more: boolean }) => void = () => {};
      api.searchDirectory.mockImplementation((q: string) =>
        q === ""
          ? new Promise((resolve) => {
              answerEmpty = resolve;
            })
          : Promise.resolve({ items: [mate], more: false }),
      );
      const user = userEvent.setup();
      render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" onpick={vi.fn()} />);
      const box = screen.getByRole("combobox");
      await user.click(box);
      await user.type(box, "ma");
      expect(await screen.findByText("Mateo Member")).toBeTruthy();
      answerEmpty({ items: [{ id: "usr_late", kind: "user", name: "Late Arrival", email: "late@example.com" }], more: false });
      await new Promise((r) => setTimeout(r, 20));
      expect(screen.queryByText("Late Arrival")).toBeNull();
    });

    it("a change of session drops the cached list", async () => {
      api.searchDirectory.mockResolvedValue({ items: [mate], more: false });
      const user = userEvent.setup();
      render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" onpick={vi.fn()} />);
      await user.click(screen.getByRole("combobox"));
      expect(await screen.findByText("Mateo Member")).toBeTruthy();
      act(() => session.set({ user: { id: "usr_kim" } } as unknown as Session));
      await user.click(screen.getByRole("combobox"));
      await waitFor(() => expect(api.searchDirectory.mock.calls.filter((c) => c[0] === "")).toHaveLength(2));
    });

    it("Enter right after the focus picks nothing, and no option is active", async () => {
      api.searchDirectory.mockResolvedValue({ items: [mate, kim], more: false });
      const onpick = vi.fn();
      render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" onpick={onpick} />);
      const box = screen.getByRole("combobox");
      fireEvent.focus(box);
      expect(await screen.findByText("Mateo Member")).toBeTruthy();
      expect(box.getAttribute("aria-activedescendant")).toBeNull();
      fireEvent.keyDown(box, { key: "Enter", keyCode: 13 });
      expect(onpick).not.toHaveBeenCalled();
      // The form's submit is checked in the browser (the fu10 e2e section): jsdom does not submit a form from a keydown.
    });

    it("an arrow after the focus, then Enter, picks the first option", async () => {
      api.searchDirectory.mockResolvedValue({ items: [mate, kim], more: false });
      const onpick = vi.fn();
      render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" onpick={onpick} />);
      const box = screen.getByRole("combobox");
      fireEvent.focus(box);
      expect(await screen.findByText("Mateo Member")).toBeTruthy();
      fireEvent.keyDown(box, { key: "ArrowDown", keyCode: 40 });
      fireEvent.keyDown(box, { key: "Enter", keyCode: 13 });
      expect(onpick).toHaveBeenLastCalledWith(expect.objectContaining({ id: "usr_mate" }));
    });

    it("a people-only field counts people, not people and groups", async () => {
      api.searchDirectory.mockResolvedValue({ items: [mate, kim], more: false });
      render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" onpick={vi.fn()} kind="user" />);
      fireEvent.focus(screen.getByRole("combobox"));
      expect(await screen.findByText("Mateo Member")).toBeTruthy();
      expect(screen.getByRole("status").textContent).toBe("2 people");
    });

    it("says Loading while the list loads, and Searching for a search", async () => {
      api.searchDirectory.mockReturnValue(new Promise(() => {}));
      const user = userEvent.setup();
      render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" onpick={vi.fn()} />);
      fireEvent.focus(screen.getByRole("combobox"));
      expect(await screen.findByText("Loading…")).toBeTruthy();
      await user.type(screen.getByRole("combobox"), "ma");
      expect(await screen.findByText("Searching…")).toBeTruthy();
    });

    it("Escape closes the list, and a click on the field reopens it", async () => {
      api.searchDirectory.mockResolvedValue({ items: [mate], more: false });
      render(<PeopleCombobox id="share-who" label="Who" hint="Name or email" onpick={vi.fn()} />);
      const box = screen.getByRole("combobox");
      fireEvent.focus(box);
      expect(await screen.findByText("Mateo Member")).toBeTruthy();
      fireEvent.keyDown(box, { key: "Escape", keyCode: 27 });
      await waitFor(() => expect(screen.queryByText("Mateo Member")).toBeNull());
      // A bare click event, not user.click: the field keeps its focus, so only the click handler can reopen the list.
      fireEvent.click(box);
      expect(await screen.findByText("Mateo Member")).toBeTruthy();
      expect(api.searchDirectory.mock.calls.filter((c) => c[0] === "")).toHaveLength(1);
    });
  });
});
