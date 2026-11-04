// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { App } from "antd";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({ deleteAllPublicLinks: vi.fn() }));
vi.mock("../../lib/manage/api", async (orig) => ({ ...(await orig<typeof import("../../lib/manage/api")>()), ...api }));

import DeletePublicLinks from "./DeletePublicLinks";
import { session, type Session } from "../../lib/session";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: `csrf-${id}`,
    publicLinks: true,
  } as unknown as Session;
}

describe("delete all public links", () => {
  beforeEach(() => api.deleteAllPublicLinks.mockResolvedValue({ deleted: 2 }));
  afterEach(() => vi.clearAllMocks());

  it("asks first, names the count, and deletes only on the confirmation", async () => {
    const user = userEvent.setup();
    const ondone = vi.fn();
    render(<DeletePublicLinks account="acc_1" count={2} ondone={ondone} />);
    await user.click(screen.getByRole("button", { name: "Delete all public links" }));
    expect(await screen.findByText(/This deletes 2 links that anyone can open/)).toBeTruthy();
    expect(api.deleteAllPublicLinks).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog");
    await user.click(dialog.querySelector(".ant-btn-primary") as HTMLElement);
    await waitFor(() => expect(api.deleteAllPublicLinks).toHaveBeenCalledWith("acc_1", expect.anything()));
    await waitFor(() => expect(ondone).toHaveBeenCalledWith(2));
  });

  it("a click on the mask does not answer the confirmation", async () => {
    const user = userEvent.setup();
    render(<DeletePublicLinks account="acc_1" count={2} ondone={vi.fn()} />);
    await user.click(screen.getByRole("button", { name: "Delete all public links" }));
    await screen.findByRole("dialog");
    (document.querySelector(".ant-modal-wrap") as HTMLElement).click();
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(api.deleteAllPublicLinks).not.toHaveBeenCalled();
  });

  it("an answer that arrives after the person changed is not shown to the next person (guard: the stale check after the delete)", async () => {
    const user = userEvent.setup();
    session.set(signedAs("olivia"));
    let release!: (v: { deleted: number }) => void;
    api.deleteAllPublicLinks.mockReturnValueOnce(new Promise((resolve) => (release = resolve)));
    const ondone = vi.fn();
    render(<DeletePublicLinks account="acc_1" count={2} ondone={ondone} />);
    await user.click(screen.getByRole("button", { name: "Delete all public links" }));
    await screen.findByRole("dialog");
    await user.click(screen.getByRole("dialog").querySelector(".ant-btn-primary") as HTMLElement);
    await waitFor(() => expect(api.deleteAllPublicLinks).toHaveBeenCalled());
    session.set(signedAs("bob"));
    release({ deleted: 2 });
    await new Promise((r) => setTimeout(r, 50));
    expect(ondone).not.toHaveBeenCalled();
    session.set(null);
  });

  it("a failure that arrives after the person changed shows nothing to the next person (guard: the stale check in catch)", async () => {
    const user = userEvent.setup();
    session.set(signedAs("olivia"));
    let fail!: (e: Error) => void;
    api.deleteAllPublicLinks.mockReturnValueOnce(new Promise((_resolve, reject) => (fail = reject)));
    render(<DeletePublicLinks account="acc_1" count={2} ondone={vi.fn()} />);
    await user.click(screen.getByRole("button", { name: "Delete all public links" }));
    await screen.findByRole("dialog");
    await user.click(screen.getByRole("dialog").querySelector(".ant-btn-primary") as HTMLElement);
    await waitFor(() => expect(api.deleteAllPublicLinks).toHaveBeenCalled());
    session.set(signedAs("bob"));
    fail(new Error("boom"));
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByRole("alert")).toBeNull();
    session.set(null);
  });

  // Escape during the delete would close the dialog while the outcome is still out: the close waits for the answer.
  // antd's motion does not end in jsdom, so a closed dialog stays in its leave state (ant-zoom-leave) for the whole test.
  const leaving = () => {
    const modal = document.querySelector(".ant-modal");
    return modal === null || modal.classList.contains("ant-zoom-leave");
  };

  // fu4, item 2: a busy dialog can be cancelled. Escape and Cancel close it at once and abort the request.
  it("Escape and Cancel close the dialog while the delete is in flight, and abort it (fu4, item 2)", async () => {
    const user = userEvent.setup();
    api.deleteAllPublicLinks.mockImplementationOnce(() => new Promise(() => undefined));
    const ondone = vi.fn();
    render(<App><DeletePublicLinks account="acc_1" count={2} ondone={ondone} /></App>);
    await user.click(screen.getByRole("button", { name: "Delete all public links" }));
    await screen.findByRole("dialog");
    await user.click(screen.getByRole("dialog").querySelector(".ant-btn-primary") as HTMLElement);
    await waitFor(() => expect(api.deleteAllPublicLinks).toHaveBeenCalled());
    const signal = api.deleteAllPublicLinks.mock.calls[0][1] as AbortSignal;
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape", code: "Escape", keyCode: 27 });
    await waitFor(() => expect(leaving()).toBe(true));
    expect(signal.aborted).toBe(true);
    expect(ondone).not.toHaveBeenCalled();
  });

  // fu4 review 3: the delete may have gone through, so the page is asked to read the links again.
  it("a cancel after the delete was sent asks the page to read the links again (fu4 review 3)", async () => {
    const user = userEvent.setup();
    const onunsure = vi.fn();
    api.deleteAllPublicLinks.mockImplementationOnce(() => new Promise(() => undefined));
    render(<App><DeletePublicLinks account="acc_1" count={2} ondone={vi.fn()} onunsure={onunsure} /></App>);
    await user.click(screen.getByRole("button", { name: "Delete all public links" }));
    await screen.findByRole("dialog");
    await user.click(screen.getByRole("dialog").querySelector(".ant-btn-primary") as HTMLElement);
    await waitFor(() => expect(api.deleteAllPublicLinks).toHaveBeenCalled());
    const open = screen.getByRole("dialog");
    await user.click([...open.querySelectorAll("button")].find((b) => b.textContent === "Cancel") as HTMLElement);
    expect(onunsure).toHaveBeenCalledTimes(1);
  });

  it("Cancel closes the dialog while the delete is in flight, and aborts it (fu4, item 2)", async () => {
    const user = userEvent.setup();
    api.deleteAllPublicLinks.mockImplementationOnce(() => new Promise(() => undefined));
    render(<App><DeletePublicLinks account="acc_1" count={2} ondone={vi.fn()} /></App>);
    await user.click(screen.getByRole("button", { name: "Delete all public links" }));
    await screen.findByRole("dialog");
    await user.click(screen.getByRole("dialog").querySelector(".ant-btn-primary") as HTMLElement);
    await waitFor(() => expect(api.deleteAllPublicLinks).toHaveBeenCalled());
    const signal = api.deleteAllPublicLinks.mock.calls[0][1] as AbortSignal;
    const open = screen.getByRole("dialog");
    await user.click([...open.querySelectorAll("button")].find((b) => b.textContent === "Cancel") as HTMLElement);
    await waitFor(() => expect(leaving()).toBe(true));
    expect(signal.aborted).toBe(true);
  });
});
