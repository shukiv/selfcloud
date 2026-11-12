// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, Link, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../../lib/session";
import type { Setting } from "../../lib/settings/api";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import { info, signedAs } from "../../test/adminFixtures";
import { desktopViewport, phoneViewport } from "../../test/viewport";
import { settingsAnswer } from "../../test/settingsFixtures";
import SettingsPage from "./SettingsPage";

// The server the page talks to: GET /settings and /info answer from state, and PATCH applies the change the way the API
// does for the cases a test needs (a stored value, or a reset), unless the test gives its own answer.
function server(over: Record<string, Partial<Setting>> = {}, patch?: (c: Call) => Answer, infoBody: unknown = info) {
  let state = settingsAnswer(over);
  return stubFetch((c) => {
    if (c.url === "/api/v1/info") return { status: 200, body: infoBody };
    if (c.url === "/api/v1/settings" && c.method === "GET") return { status: 200, body: state };
    if (c.url === "/api/v1/settings" && c.method === "PATCH") {
      if (patch) {
        const a = patch(c);
        if (a.status === 200 && a.body === undefined) return { status: 200, body: state };
        return a;
      }
      const body = c.body as { set?: Record<string, unknown>; reset?: string[] };
      state = {
        ...state,
        settings: state.settings.map((s) => {
          if (body.set && s.key in body.set) return { ...s, value: body.set[s.key] as Setting["value"], source: "settings" as const };
          if (body.reset?.includes(s.key)) return { ...s, value: s.default, source: "default" as const };
          return s;
        }),
      };
      return { status: 200, body: state };
    }
    return fail(404, "not_found");
  });
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const router = createMemoryRouter(
    [
      {
        path: "/admin/settings",
        element: (
          <QueryClientProvider client={qc}>
            <SettingsPage />
            <Link to="/admin/tenants">Tenants</Link>
          </QueryClientProvider>
        ),
      },
      { path: "/admin/tenants", element: <p>the tenants page</p> },
    ],
    { initialEntries: ["/admin/settings"] },
  );
  render(<RouterProvider router={router} />);
  return { qc, router };
}

const region = (name: string) => screen.getByRole("region", { name });
const row = (key: string) => document.querySelector(`[data-setting="${key}"]`) as HTMLElement;
const numberBox = (key: string) => within(row(key)).getByRole("spinbutton") as HTMLInputElement;
const save = (name: string) => within(region(name)).getByRole("button", { name: "Save" });
const writes = (calls: Call[]) => calls.filter((c) => c.method === "PATCH");

async function pickUnit(key: string, label: string, unit: string) {
  fireEvent.mouseDown(within(row(key)).getByRole("combobox", { name: label }));
  const option = await waitFor(() => {
    const el = document.querySelector(`.ant-select-item-option[title="${unit}"]`);
    if (!el) throw new Error("no option");
    return el;
  });
  fireEvent.click(option);
}

let undo: () => void;
beforeEach(() => {
  session.set(signedAs("admin"));
  undo = desktopViewport();
});
afterEach(() => {
  undo();
  session.set(null);
});

describe("who sees the settings", () => {
  it.each(["owner", "member"] as const)("a %s sees the not-found view and no request is sent", async (role) => {
    session.set(signedAs(role));
    const { calls } = server();
    renderPage();
    expect(await screen.findByText("Page not found")).toBeTruthy();
    expect(calls).toEqual([]);
  });

  it("a person signed in as another user sees the not-found view and no request is sent", async () => {
    session.set({ ...signedAs("owner"), impersonation: { endsAt: "2999-01-01T00:00:00Z" } } as Session);
    const { calls } = server();
    renderPage();
    expect(await screen.findByText("Page not found")).toBeTruthy();
    expect(calls).toEqual([]);
  });
});

describe("what a row shows", () => {
  it("shows a default row with its value, its default, and no Reset", async () => {
    server();
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    const r = row("trash.retention");
    expect(numberBox("trash.retention").value).toBe("30");
    expect(within(r).getByText("days")).toBeTruthy();
    expect(within(r).getByText("Default", { selector: ".ant-tag" })).toBeTruthy();
    expect(within(r).getByText("Default: 30 days")).toBeTruthy();
    expect(within(r).queryByRole("button", { name: /Back to the default/ })).toBeNull();
  });

  it("shows a stored row as set here, with Reset naming the default", async () => {
    server({ "trash.retention": { value: "360h0m0s", source: "settings" } });
    renderPage();
    await screen.findByRole("region", { name: "Sharing, trash and versions" });
    const r = row("trash.retention");
    expect(numberBox("trash.retention").value).toBe("15");
    expect(within(r).getByText("Set here")).toBeTruthy();
    expect(within(r).getByRole("button", { name: "Back to the default (30 days)" })).toBeTruthy();
  });

  it("shows a size in the unit that divides it", async () => {
    server({ "upload.max_chunk_size": { value: "2GiB", source: "settings" } });
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    expect(numberBox("upload.max_chunk_size").value).toBe("2");
    expect(within(row("upload.max_chunk_size")).getByText("GB")).toBeTruthy();
    expect(numberBox("upload.max_file_size").value).toBe("100");
    expect(numberBox("upload.max_small_file_size").value).toBe("16");
  });

  it("shows a locked row read-only, marked as set in the config file, with no Reset", async () => {
    server({ "upload.max_file_size": { value: "50GiB", source: "file", locked: true } });
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    const r = row("upload.max_file_size");
    expect(numberBox("upload.max_file_size").value).toBe("50");
    expect(numberBox("upload.max_file_size").disabled).toBe(true);
    expect(within(r).getByRole("combobox").closest(".ant-select")?.className).toContain("ant-select-disabled");
    expect(within(r).getByText("Set in the server's config file")).toBeTruthy();
    expect(within(r).queryByRole("button", { name: /Back to the default/ })).toBeNull();
  });

  it("shows a setting that applies at the next start as read-only", async () => {
    server({ "office.enabled": { value: true, source: "file", locked: true, restart: true } });
    renderPage();
    await screen.findByRole("region", { name: "Sign-in, search, office and administration" });
    const r = row("office.enabled");
    expect((within(r).getByRole("switch") as HTMLButtonElement).disabled).toBe(true);
    expect(within(r).getByRole("switch").getAttribute("aria-checked")).toBe("true");
    expect(within(r).getByText("Applies at the next start")).toBeTruthy();
    expect(within(r).queryByRole("button", { name: /Back to the default/ })).toBeNull();
  });

  it("shows the office pair as the server sends it at its default: read-only, applies at the next start, and not set in the config file", async () => {
    server();
    renderPage();
    await screen.findByRole("region", { name: "Sign-in, search, office and administration" });
    for (const key of ["office.enabled", "office.discovery_url"]) {
      const r = row(key);
      expect(within(r).getByText("Applies at the next start")).toBeTruthy();
      expect(within(r).queryByText("Set in the server's config file")).toBeNull();
      expect(within(r).getByText("Default", { selector: ".ant-tag" })).toBeTruthy();
    }
  });

  it("marks a value the environment writes as set in the server's config file", async () => {
    server({ "search.enabled": { value: false, source: "env", locked: true } });
    renderPage();
    await screen.findByRole("region", { name: "Sign-in, search, office and administration" });
    expect(within(row("search.enabled")).getByText("Set in the server's config file")).toBeTruthy();
  });

  it("keeps a restart setting read-only even when the server does not call it locked", async () => {
    server({ "office.enabled": { value: true, source: "default", locked: false, restart: true } });
    renderPage();
    await screen.findByRole("region", { name: "Sign-in, search, office and administration" });
    const r = row("office.enabled");
    expect((within(r).getByRole("switch") as HTMLButtonElement).disabled).toBe(true);
    expect(within(r).getByText("Applies at the next start")).toBeTruthy();
  });

  it("states the rules: the allowed range, and what lowering a session limit does", async () => {
    server();
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    expect(within(row("storage.min_free")).getByText(/Allowed: 256 MB to 1 TB/)).toBeTruthy();
    expect(within(row("sharing.max_expiry")).getByText(/0 for no limit, or 1 hour to 3,650 days/)).toBeTruthy();
    const idle = row("sessions.idle_timeout");
    expect(within(idle).getByText(/Lowering it signs out/)).toBeTruthy();
    expect(within(idle).getByText(/typing in an office document is signed out when this time passes with no other page request/)).toBeTruthy();
    expect(within(row("sessions.max_age")).getByText(/Lowering it signs out/)).toBeTruthy();
    expect(within(row("sessions.admin_idle_timeout")).getByText(/Lowering it signs out/)).toBeTruthy();
    expect(within(row("sessions.admin_max_age")).getByText(/Lowering it signs out/)).toBeTruthy();
    expect(within(row("trash.retention")).queryByText(/Lowering it signs out/)).toBeNull();
  });
});

describe("editing and saving", () => {
  it("sends a size in the API's string form and only the changed key of its section", async () => {
    const { calls } = server();
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    expect((save("Uploads and storage") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(numberBox("upload.max_file_size"), { target: { value: "200" } });
    expect(within(row("upload.max_file_size")).getByRole("img", { name: "Changed" })).toBeTruthy();
    fireEvent.click(save("Uploads and storage"));
    await waitFor(() => expect(writes(calls)).toHaveLength(1));
    expect(writes(calls)[0].body).toEqual({ set: { "upload.max_file_size": "200GiB" } });
    expect(writes(calls)[0].url).toBe("/api/v1/settings");
  });

  it("converts a duration to hours and sends it in hours", async () => {
    const { calls } = server();
    renderPage();
    await screen.findByRole("region", { name: "Sharing, trash and versions" });
    await pickUnit("trash.retention", "Keep deleted files: unit", "hours");
    fireEvent.change(numberBox("trash.retention"), { target: { value: "36" } });
    fireEvent.click(save("Sharing, trash and versions"));
    await waitFor(() => expect(writes(calls)).toHaveLength(1));
    expect(writes(calls)[0].body).toEqual({ set: { "trash.retention": "36h" } });
  });

  it("is not a change when the same duration is written in another unit", async () => {
    server();
    renderPage();
    await screen.findByRole("region", { name: "Sharing, trash and versions" });
    await pickUnit("trash.retention", "Keep deleted files: unit", "hours");
    fireEvent.change(numberBox("trash.retention"), { target: { value: "720" } });
    expect((save("Sharing, trash and versions") as HTMLButtonElement).disabled).toBe(true);
    expect(within(row("trash.retention")).queryByRole("img", { name: "Changed" })).toBeNull();
  });

  it("sends a count as a number and a switch as a boolean", async () => {
    const { calls } = server();
    renderPage();
    await screen.findByRole("region", { name: "Sharing, trash and versions" });
    fireEvent.change(numberBox("versions.max_count"), { target: { value: "50" } });
    fireEvent.click(save("Sharing, trash and versions"));
    await waitFor(() => expect(writes(calls)).toHaveLength(1));
    expect(writes(calls)[0].body).toEqual({ set: { "versions.max_count": 50 } });
    fireEvent.click(within(row("search.enabled")).getByRole("switch"));
    fireEvent.click(save("Sign-in, search, office and administration"));
    await waitFor(() => expect(writes(calls)).toHaveLength(2));
    expect(writes(calls)[1].body).toEqual({ set: { "search.enabled": false } });
  });

  it("Reset sends reset for that key, on Save, and the row goes back to its default", async () => {
    const { calls } = server({ "links.max_ttl": { value: "12h0m0s", source: "settings" } });
    renderPage();
    await screen.findByRole("region", { name: "Sharing, trash and versions" });
    expect(numberBox("links.max_ttl").value).toBe("12");
    fireEvent.click(within(row("links.max_ttl")).getByRole("button", { name: "Back to the default (1 day)" }));
    expect(numberBox("links.max_ttl").value).toBe("1");
    expect(writes(calls)).toHaveLength(0);
    fireEvent.click(save("Sharing, trash and versions"));
    await waitFor(() => expect(writes(calls)).toHaveLength(1));
    expect(writes(calls)[0].body).toEqual({ reset: ["links.max_ttl"] });
    await waitFor(() => expect(within(row("links.max_ttl")).getByText("Default", { selector: ".ant-tag" })).toBeTruthy());
    expect(within(row("links.max_ttl")).queryByRole("button", { name: /Back to the default/ })).toBeNull();
  });

  it("saves one section and leaves another section's edit unsaved", async () => {
    const { calls } = server();
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    fireEvent.change(numberBox("upload.max_file_size"), { target: { value: "200" } });
    fireEvent.change(numberBox("versions.max_count"), { target: { value: "7" } });
    fireEvent.click(save("Uploads and storage"));
    await waitFor(() => expect(writes(calls)).toHaveLength(1));
    expect(writes(calls)[0].body).toEqual({ set: { "upload.max_file_size": "200GiB" } });
    await waitFor(() => expect((save("Uploads and storage") as HTMLButtonElement).disabled).toBe(true));
    expect(numberBox("upload.max_file_size").value).toBe("200");
    expect(within(row("upload.max_file_size")).getByText("Set here")).toBeTruthy();
    expect(numberBox("versions.max_count").value).toBe("7");
    expect((save("Sharing, trash and versions") as HTMLButtonElement).disabled).toBe(false);
    expect(within(region("Sharing, trash and versions")).getByText("Unsaved changes")).toBeTruthy();
  });

  it("Discard puts the section back as the server has it", async () => {
    server();
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    fireEvent.change(numberBox("upload.max_file_size"), { target: { value: "5" } });
    fireEvent.click(within(region("Uploads and storage")).getByRole("button", { name: "Discard" }));
    expect(numberBox("upload.max_file_size").value).toBe("100");
    expect((save("Uploads and storage") as HTMLButtonElement).disabled).toBe(true);
  });

  it("does not send a box that was emptied, and says so on the field", async () => {
    const { calls } = server();
    renderPage();
    await screen.findByRole("region", { name: "Sharing, trash and versions" });
    fireEvent.change(numberBox("versions.max_count"), { target: { value: "" } });
    fireEvent.click(save("Sharing, trash and versions"));
    expect(await within(row("versions.max_count")).findByText("Enter a number.")).toBeTruthy();
    expect(writes(calls)).toHaveLength(0);
  });

  it("refreshes what About shows from the server after a save", async () => {
    const { calls } = server();
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    const before = calls.filter((c) => c.url === "/api/v1/info").length;
    fireEvent.change(numberBox("upload.max_file_size"), { target: { value: "200" } });
    fireEvent.click(save("Uploads and storage"));
    await waitFor(() => expect(calls.filter((c) => c.url === "/api/v1/info").length).toBeGreaterThan(before));
  });
});

describe("what the server answers to a change", () => {
  it("marks each refused field with its own message, keeps the edits, and marks no other field", async () => {
    server({}, () =>
      fail(400, "invalid_request", {
        reason: "invalid_setting",
        keys: ["links.default_ttl", "versions.max_count"],
        errors: ["config: links.default_ttl is below the least a link may last", "config: versions.max_count must not be negative"],
      }),
    );
    renderPage();
    await screen.findByRole("region", { name: "Sharing, trash and versions" });
    fireEvent.change(numberBox("links.default_ttl"), { target: { value: "1" } });
    fireEvent.change(numberBox("versions.max_count"), { target: { value: "3" } });
    fireEvent.change(numberBox("trash.retention"), { target: { value: "40" } });
    fireEvent.click(save("Sharing, trash and versions"));
    expect(await within(row("links.default_ttl")).findByText(/below the least a link may last/)).toBeTruthy();
    expect(within(row("versions.max_count")).getByText(/must not be negative/)).toBeTruthy();
    expect(within(row("trash.retention")).queryByRole("alert")).toBeNull();
    expect(numberBox("links.default_ttl").value).toBe("1");
    expect(numberBox("versions.max_count").value).toBe("3");
    expect((save("Sharing, trash and versions") as HTMLButtonElement).disabled).toBe(false);
    // Editing the field again clears its mark.
    fireEvent.change(numberBox("links.default_ttl"), { target: { value: "2" } });
    expect(within(row("links.default_ttl")).queryByText(/below the least a link may last/)).toBeNull();
    expect(within(row("versions.max_count")).getByText(/must not be negative/)).toBeTruthy();
  });

  it("shows the messages that name no field in the section's notice", async () => {
    server({}, () => fail(400, "invalid_request", { reason: "invalid_setting", keys: [], errors: ["a rule across the settings broke"] }));
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    fireEvent.change(numberBox("upload.max_file_size"), { target: { value: "200" } });
    fireEvent.click(save("Uploads and storage"));
    expect((await within(region("Uploads and storage")).findByRole("alert")).textContent).toContain("a rule across the settings broke");
  });

  it("disables every section's Save while one Save is out", async () => {
    let release: () => void = () => undefined;
    const held = new Promise<void>((r) => (release = r));
    const { calls } = stubFetch(async (c) => {
      if (c.url === "/api/v1/info") return { status: 200, body: info };
      if (c.method === "PATCH") {
        await held;
        return { status: 200, body: settingsAnswer() };
      }
      return { status: 200, body: settingsAnswer() };
    });
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    fireEvent.change(numberBox("upload.max_file_size"), { target: { value: "200" } });
    fireEvent.change(numberBox("versions.max_count"), { target: { value: "7" } });
    fireEvent.click(save("Uploads and storage"));
    await waitFor(() => expect(writes(calls)).toHaveLength(1));
    expect((save("Sharing, trash and versions") as HTMLButtonElement).disabled).toBe(true);
    release();
    await waitFor(() => expect((save("Sharing, trash and versions") as HTMLButtonElement).disabled).toBe(false));
  });

  it("says which keys are now locked on a 409, and reloads", async () => {
    let locked = false;
    const { calls } = stubFetch((c) => {
      if (c.url === "/api/v1/info") return { status: 200, body: info };
      if (c.method === "PATCH") {
        locked = true;
        return fail(409, "conflict", { reason: "setting_locked", keys: ["upload.max_file_size"] });
      }
      return { status: 200, body: settingsAnswer(locked ? { "upload.max_file_size": { value: "50GiB", source: "file", locked: true } } : {}) };
    });
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    fireEvent.change(numberBox("upload.max_file_size"), { target: { value: "200" } });
    fireEvent.change(numberBox("upload.max_chunk_size"), { target: { value: "32" } });
    fireEvent.click(save("Uploads and storage"));
    const notice = await within(region("Uploads and storage")).findByRole("alert");
    expect(notice.textContent).toContain("upload.max_file_size");
    expect(notice.textContent).toContain("config file");
    await waitFor(() => expect(numberBox("upload.max_file_size").disabled).toBe(true));
    expect(numberBox("upload.max_file_size").value).toBe("50");
    expect(calls.filter((c) => c.url === "/api/v1/settings" && c.method === "GET").length).toBeGreaterThanOrEqual(2);
    // The edit to a key that is not locked is kept.
    expect(numberBox("upload.max_chunk_size").value).toBe("32");
  });

  it("shows a refusal it has no sentence for as the general error", async () => {
    server({}, () => fail(500, "internal"));
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    fireEvent.change(numberBox("upload.max_file_size"), { target: { value: "200" } });
    fireEvent.click(save("Uploads and storage"));
    expect((await within(region("Uploads and storage")).findByRole("alert")).textContent).toContain("The server had a problem");
    expect(numberBox("upload.max_file_size").value).toBe("200");
  });

  it("shows the error and no settings when the first read fails", async () => {
    stubFetch((c) => (c.url === "/api/v1/info" ? { status: 200, body: info } : fail(500, "internal")));
    renderPage();
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.queryByRole("region", { name: "Uploads and storage" })).toBeNull();
  });
});

describe("leaving with unsaved changes", () => {
  it("asks first on a route change, and Stay keeps the page and its edit", async () => {
    server();
    const { router } = renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    fireEvent.change(numberBox("upload.max_file_size"), { target: { value: "5" } });
    fireEvent.click(screen.getByRole("link", { name: "Tenants" }));
    expect(await screen.findByText("Leave without saving?")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Stay" }));
    await waitFor(() => expect(router.state.blockers.size).toBeGreaterThan(0));
    expect(router.state.location.pathname).toBe("/admin/settings");
    expect(numberBox("upload.max_file_size").value).toBe("5");
    // The guard is still in place: the next try to leave asks again.
    fireEvent.click(screen.getByRole("link", { name: "Tenants" }));
    await waitFor(() => expect(screen.getAllByText("Leave without saving?").length).toBeGreaterThan(0));
    expect(router.state.location.pathname).toBe("/admin/settings");
  });

  it("Leave goes on to the page", async () => {
    server();
    const { router } = renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    fireEvent.change(numberBox("upload.max_file_size"), { target: { value: "5" } });
    fireEvent.click(screen.getByRole("link", { name: "Tenants" }));
    fireEvent.click(await screen.findByRole("button", { name: "Leave" }));
    expect(await screen.findByText("the tenants page")).toBeTruthy();
    expect(router.state.location.pathname).toBe("/admin/tenants");
  });

  it("does not ask when nothing is changed, or once the changes are saved", async () => {
    server();
    const { router } = renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    fireEvent.change(numberBox("upload.max_file_size"), { target: { value: "5" } });
    fireEvent.click(save("Uploads and storage"));
    await waitFor(() => expect((save("Uploads and storage") as HTMLButtonElement).disabled).toBe(true));
    fireEvent.click(screen.getByRole("link", { name: "Tenants" }));
    expect(await screen.findByText("the tenants page")).toBeTruthy();
    expect(router.state.location.pathname).toBe("/admin/tenants");
  });

  it("asks through the browser on a reload or a close, only while something is unsaved", async () => {
    server();
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    const clean = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(clean);
    expect(clean.defaultPrevented).toBe(false);
    fireEvent.change(numberBox("upload.max_file_size"), { target: { value: "5" } });
    await waitFor(() => {
      const dirty = new Event("beforeunload", { cancelable: true });
      const prevent = vi.spyOn(dirty, "preventDefault");
      window.dispatchEvent(dirty);
      // A browser asks only when the handler calls preventDefault: an empty returnValue alone does not prompt.
      expect(prevent).toHaveBeenCalled();
    });
  });

  it("a click on the backdrop does not close the leave prompt: Leave still goes on", async () => {
    server();
    const { router } = renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    fireEvent.change(numberBox("upload.max_file_size"), { target: { value: "5" } });
    fireEvent.click(screen.getByRole("link", { name: "Tenants" }));
    await screen.findByText("Leave without saving?");
    const wrap = document.querySelector(".ant-modal-wrap") as HTMLElement;
    fireEvent.click(wrap);
    fireEvent.click(await screen.findByRole("button", { name: "Leave" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/tenants"));
  });
});

// The layout follows the width of the page's content, not the window's: the docked sidebar takes 264 px of a window from 992 px.
describe("the layout follows the content's width", () => {
  const saved = globalThis.ResizeObserver;
  const contentWidth = (width: number) => {
    globalThis.ResizeObserver = class {
      private cb: ResizeObserverCallback;
      constructor(cb: ResizeObserverCallback) {
        this.cb = cb;
      }
      observe() {
        this.cb([{ contentRect: { width } } as ResizeObserverEntry], this as unknown as ResizeObserver);
      }
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;
  };
  afterEach(() => {
    globalThis.ResizeObserver = saved;
  });
  const twoColumns = (key: string) => row(key).style.gridTemplateColumns.includes("340px");

  it("beside the cards from 900 px of content, with two-column rows", async () => {
    contentWidth(968);
    server();
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    expect(screen.getByRole("navigation", { name: "Sections" })).toBeTruthy();
    expect(twoColumns("upload.max_file_size")).toBe(true);
  });

  it("a window of 1000 px with the sidebar docked (about 700 px of content) stacks the cards, with two-column rows", async () => {
    contentWidth(700);
    server();
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    expect(screen.queryByRole("navigation", { name: "Sections" })).toBeNull();
    expect(screen.queryByRole("tablist")).toBeNull();
    expect(screen.getAllByRole("region")).toHaveLength(4);
    expect(twoColumns("upload.max_file_size")).toBe(true);
  });

  it("a narrow card stacks each row's input under its text", async () => {
    contentWidth(620);
    server();
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    expect(screen.queryByRole("tablist")).toBeNull();
    expect(twoColumns("upload.max_file_size")).toBe(false);
  });

  it("tabs below 600 px of content, whatever the window is", async () => {
    contentWidth(560);
    server();
    renderPage();
    expect(await screen.findByRole("tablist")).toBeTruthy();
    expect(screen.queryByRole("navigation", { name: "Sections" })).toBeNull();
  });

  // A scrollbar that appears when the cards stack takes 24 px: the content is 610 px of window less it. Reporting again after
  // each render, as a real scrollbar does, the layout must settle instead of flipping between tabs and stacked cards.
  it("settles when a scrollbar takes the width that stacks the cards", async () => {
    const observers: (() => void)[] = [];
    // Each render reports again, as a real scrollbar does, but only so many times: an unsettled layout would loop for ever.
    let budget = 50;
    let stopped = false;
    globalThis.ResizeObserver = class {
      private cb: ResizeObserverCallback;
      constructor(cb: ResizeObserverCallback) {
        this.cb = cb;
      }
      observe() {
        observers.push(() => {
          if (stopped || budget-- <= 0) return;
          this.cb([{ contentRect: { width: screen.queryByRole("tablist") ? 610 : 595 } } as ResizeObserverEntry], this as unknown as ResizeObserver);
        });
      }
      unobserve() {}
      disconnect() {
        observers.length = 0;
      }
    } as unknown as typeof ResizeObserver;
    const report = () => {
      for (const fn of [...observers]) fn();
    };
    const mo = new MutationObserver(report);
    mo.observe(document.body, { childList: true, subtree: true });
    const seen = { tablist: false, changes: 0 };
    const watch = new MutationObserver(() => {
      const now = !!document.querySelector('[role="tablist"]');
      if (now !== seen.tablist) {
        seen.tablist = now;
        seen.changes += 1;
      }
    });
    watch.observe(document.body, { childList: true, subtree: true });
    server();
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 200));
    });
    const settled = seen.changes;
    stopped = true;
    watch.disconnect();
    mo.disconnect();
    expect(settled).toBeLessThanOrEqual(2);
  });
});

describe("the layout", () => {
  it("lists the four sections beside the cards on a wide window, and no Branding", async () => {
    server();
    renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    const nav = screen.getByRole("navigation", { name: "Sections" });
    expect(within(nav).getAllByRole("button").map((b) => b.textContent)).toEqual([
      "Uploads and storage",
      "Sharing, trash and versions",
      "Sign-in, search, office and administration",
      "About",
    ]);
    expect(screen.getAllByRole("region")).toHaveLength(4);
    expect(screen.queryByText("Branding")).toBeNull();
    expect(screen.queryByRole("tablist")).toBeNull();
  });

  it("shows tabs on a phone, one section at a time, and keeps an edit made in another tab", async () => {
    undo();
    undo = phoneViewport();
    server();
    renderPage();
    const tabs = await screen.findByRole("tablist");
    expect(within(tabs).getAllByRole("tab").map((t) => t.textContent)).toEqual([
      "Uploads and storage",
      "Sharing, trash and versions",
      "Sign-in, search, office and administration",
      "About",
    ]);
    expect(screen.queryByRole("navigation", { name: "Sections" })).toBeNull();
    fireEvent.change(numberBox("upload.max_file_size"), { target: { value: "5" } });
    fireEvent.click(within(tabs).getByRole("tab", { name: "Sharing, trash and versions" }));
    await waitFor(() => expect(row("trash.retention")).toBeTruthy());
    fireEvent.click(within(tabs).getByRole("tab", { name: "Uploads and storage" }));
    await waitFor(() => expect(numberBox("upload.max_file_size").value).toBe("5"));
  });

  it("About gives the version, the addresses, the links and the facts the file owns", async () => {
    server();
    renderPage();
    await screen.findByRole("region", { name: "About" });
    const about = region("About");
    expect(within(about).getByText("1.4.2")).toBeTruthy();
    expect(within(about).getByText("https://drive.example.com", { selector: "bdi" })).toBeTruthy();
    expect(within(about).getByRole("link", { name: "Readiness check" }).getAttribute("href")).toBe("/readyz");
    expect(within(about).getByRole("link", { name: "API description (OpenAPI)" }).getAttribute("href")).toBe("/api/v1/openapi.yaml");
    expect(within(about).getByText("trusted_proxies")).toBeTruthy();
    expect(within(about).getByText("10.0.0.0/8, 192.168.0.0/16")).toBeTruthy();
    expect(within(about).getByText("listen")).toBeTruthy();
  });

  it("About writes the piece size and the largest file in the same units as the cards", async () => {
    server({}, undefined, { ...info, limits: { max_file_size: 100 * 1024 ** 3, max_chunk_size: 64 * 1024 ** 2 } });
    renderPage();
    await screen.findByRole("region", { name: "About" });
    const about = region("About");
    expect(within(about).getByText("100 GB")).toBeTruthy();
    expect(within(about).getByText("64 MB")).toBeTruthy();
    expect(numberBox("upload.max_file_size").value).toBe("100");
  });

  it("keeps the page after a failed refresh of an answer it already has", async () => {
    server();
    const { qc } = renderPage();
    await screen.findByRole("region", { name: "Uploads and storage" });
    fireEvent.change(numberBox("upload.max_file_size"), { target: { value: "5" } });
    stubFetch(() => fail(500, "internal"));
    await act(async () => {
      await qc.invalidateQueries();
    });
    expect(numberBox("upload.max_file_size").value).toBe("5");
  });
});
