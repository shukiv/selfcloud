// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { App } from "antd";
import { QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../../lib/query";
import NameDialog from "./NameDialog";

// A save that never answers keeps the dialog busy. Cancel must still close it, and abort the save's request.
describe("the name dialog", () => {
  it("Cancel on a busy dialog aborts the save and closes the dialog (guard: busy dialogs can be cancelled)", async () => {
    const signals: AbortSignal[] = [];
    const onsave = vi.fn(
      (_name: string, signal: AbortSignal) =>
        new Promise<void>((_resolve, reject) => {
          signals.push(signal);
          signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
        }),
    );
    const onclose = vi.fn();
    const qc = createQueryClient();
    const refresh = vi.spyOn(qc, "invalidateQueries");
    render(
      <App>
        <QueryClientProvider client={qc}>
          <NameDialog id="group-new" title="New group" label="Name" action="Create" onsave={onsave} onclose={onclose} />
        </QueryClientProvider>
      </App>,
    );
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Finance" } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(signals).toHaveLength(1));
    expect(signals[0].aborted).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(signals[0].aborted).toBe(true);
    expect(onclose).toHaveBeenCalledTimes(1);
    expect(refresh).toHaveBeenCalled();
    expect(await screen.findByText("Cancelled. The change may or may not have been saved; check the list before you try again.")).toBeTruthy();
  });
});
