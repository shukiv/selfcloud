// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { limitBytes } from "../../lib/quota";
import LimitEditor from "./LimitEditor";

afterEach(() => vi.restoreAllMocks());

const amount = () => screen.getByLabelText("Amount") as HTMLInputElement;

describe("the limit editor", () => {
  it("a person's empty amount means the default, and saves null (guard: parseUserLimit for allowDefault)", async () => {
    const onsave = vi.fn(async () => undefined);
    render(<LimitEditor current={null} allowDefault onsave={onsave} oncancel={() => undefined} />);
    fireEvent.click(screen.getByRole("button", { name: "Save limit" }));
    await waitFor(() => expect(onsave).toHaveBeenCalledWith(null));
  });

  it("the default's empty amount means no limit, 0 (guard: parseAccountLimit for the default)", async () => {
    const onsave = vi.fn(async () => undefined);
    render(<LimitEditor current={null} allowDefault={false} onsave={onsave} oncancel={() => undefined} />);
    fireEvent.click(screen.getByRole("button", { name: "Save limit" }));
    await waitFor(() => expect(onsave).toHaveBeenCalledWith(0));
  });

  it("an amount in the chosen unit is saved as bytes (guard: limitBytes in the unit)", async () => {
    const onsave = vi.fn(async () => undefined);
    render(<LimitEditor current={null} allowDefault onsave={onsave} oncancel={() => undefined} />);
    fireEvent.change(amount(), { target: { value: "2" } });
    fireEvent.click(screen.getByRole("button", { name: "Save limit" }));
    await waitFor(() => expect(onsave).toHaveBeenCalledWith(limitBytes("2", "GB")));
  });

  it("an invalid amount is refused in place, with no save (guard: the invalid check before onsave)", async () => {
    const onsave = vi.fn(async () => undefined);
    render(<LimitEditor current={null} allowDefault onsave={onsave} oncancel={() => undefined} />);
    fireEvent.change(amount(), { target: { value: "lots" } });
    fireEvent.click(screen.getByRole("button", { name: "Save limit" }));
    expect((await screen.findByRole("alert")).textContent).toContain("Enter an amount as a positive number");
    expect(onsave).not.toHaveBeenCalled();
  });

  it("a second press while the save is out makes no second save (guard: the once-at-a-time check)", async () => {
    let release!: () => void;
    const onsave = vi.fn(() => new Promise<void>((r) => (release = r)));
    render(<LimitEditor current={null} allowDefault onsave={onsave} oncancel={() => undefined} />);
    const save = screen.getByRole("button", { name: "Save limit" });
    act(() => {
      fireEvent.click(save);
      fireEvent.click(save);
    });
    await waitFor(() => expect(onsave).toHaveBeenCalledTimes(1));
    release();
  });

  it("a refused save keeps the editor and shows the error by code (guard: the error stays in the editor)", async () => {
    const onsave = vi.fn(async () => {
      throw Object.assign(new Error("forbidden"), { code: "forbidden" });
    });
    render(<LimitEditor current={null} allowDefault onsave={onsave} oncancel={() => undefined} />);
    fireEvent.click(screen.getByRole("button", { name: "Save limit" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).not.toContain("forbidden");
    expect(screen.getByRole("button", { name: /Save limit/ })).toBeTruthy();
  });

  // The stored value is shown rounded (1,234,567,890 bytes as "1.23" GB). Saving it untouched must not write the rounded
  // value back: nothing was changed, so nothing is sent, and the editor closes as a cancel.
  it("saving an untouched stored limit sends nothing, so the rounded text is not written back (guard: untouched save is a cancel)", async () => {
    const onsave = vi.fn(async () => undefined);
    const oncancel = vi.fn();
    render(<LimitEditor current={1_234_567_890} allowDefault onsave={onsave} oncancel={oncancel} />);
    fireEvent.click(screen.getByRole("button", { name: "Save limit" }));
    await waitFor(() => expect(oncancel).toHaveBeenCalled());
    expect(onsave).not.toHaveBeenCalled();
  });
});
