// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../../lib/session";
import type { LabelField } from "../../lib/labels/api";
import LabelDialog from "./LabelDialog";

function signedAs(): Session {
  return {
    user: { id: "usr_owen", email: "owen@example.com", display_name: "Owen", role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: false,
  } as unknown as Session;
}

beforeEach(() => session.set(signedAs()));
afterEach(() => session.set(null));

const STATUS: LabelField = {
  id: "lbf_status",
  name: "Status",
  type: "choice",
  restricted: false,
  choices: [{ id: "lbc_open", name: "Open", archived: false }],
  values: 2,
};

describe("label dialog", () => {
  it("does not save a field with no name, and says so", () => {
    const onsave = vi.fn(async () => {});
    render(<LabelDialog id="f" field={null} onsave={onsave} onclose={() => {}} />);
    expect(screen.getByText("Enter a name.")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Create" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    expect(onsave).not.toHaveBeenCalled();
  });

  it("refuses two choices with one name before anything is sent", () => {
    render(<LabelDialog id="f" field={null} onsave={vi.fn(async () => {})} onclose={() => {}} />);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Stage" } });
    fireEvent.mouseDown(screen.getByRole("combobox"));
    fireEvent.click(screen.getByText("Choice"));
    fireEvent.click(screen.getByRole("button", { name: "Add choice" }));
    fireEvent.click(screen.getByRole("button", { name: "Add choice" }));
    const boxes = screen.getAllByLabelText("Choice name");
    fireEvent.change(boxes[0], { target: { value: "Open" } });
    fireEvent.change(boxes[1], { target: { value: "open" } });
    expect(screen.getByText("Two choices have the same name.")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Create" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("turning off a restriction warns with the number of values before saving", () => {
    const onsave = vi.fn(async () => {});
    const restricted = { ...STATUS, restricted: true, values: 4 };
    render(<LabelDialog id="f" field={restricted} onsave={onsave} onclose={() => {}} />);
    fireEvent.click(screen.getByRole("checkbox", { name: /restricted/i }));
    expect(screen.getByText(/Values now: 4/)).toBeTruthy();
    expect(onsave).not.toHaveBeenCalled();
  });

  it("a field that holds values cannot change its type, and says why", () => {
    render(<LabelDialog id="f" field={STATUS} onsave={vi.fn(async () => {})} onclose={() => {}} />);
    expect(screen.getByText("The type cannot change while items hold values.")).toBeTruthy();
    expect(screen.getByText("Edit Status")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Archive" })).toBeTruthy();
  });

  it("saving an edit sends only what changed, with the choices by ID", () => {
    const onsave = vi.fn(async () => {});
    render(<LabelDialog id="f" field={STATUS} onsave={onsave} onclose={() => {}} />);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "State" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(onsave).toHaveBeenCalledWith({
      name: "State",
      restricted: false,
      choices: [{ id: "lbc_open", name: "Open", archived: false }],
    });
  });
});
