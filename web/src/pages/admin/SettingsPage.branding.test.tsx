// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { brand, brandLoaded } from "../../lib/brand";
import { session } from "../../lib/session";
import { get } from "../../lib/store";
import type { Picture, Setting, SettingsAnswer } from "../../lib/settings/api";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import { info, signedAs } from "../../test/adminFixtures";
import { desktopViewport } from "../../test/viewport";
import { settingsAnswer } from "../../test/settingsFixtures";
import SettingsPage from "./SettingsPage";

const SHA = "a".repeat(64);
const uploaded = (key: Picture["key"], type: Picture["type"] = "image/png"): Picture => ({ key, state: "uploaded", type, sha256: SHA, locked: false });

interface Options {
  over?: Record<string, Partial<Setting>>;
  pictures?: Picture[];
  // put answers an upload: by default the picture is stored, and the answer says so.
  put?: (c: Call) => Answer | undefined;
}

// The server the Branding card talks to: the settings, the info the page reads again after a change, the changes, and the picture
// routes, which keep the pictures in a state of their own as the server does.
function server(o: Options = {}) {
  let state: SettingsAnswer = settingsAnswer(o.over ?? {}, undefined, o.pictures);
  const calls = stubFetch((c) => {
    if (c.url === "/api/v1/info") return { status: 200, body: { ...info, brand: { name: nameOf(state), accent: valueOf(state, "ui.brand.accent"), logo_url: "/brand/logo", artwork: true } } };
    if (c.url === "/api/v1/settings" && c.method === "GET") return { status: 200, body: state };
    if (c.url === "/api/v1/settings" && c.method === "PATCH") {
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
    const m = /^\/api\/v1\/settings\/brand\/(logo|icon)$/.exec(c.url);
    if (m) {
      const key = `ui.brand.${m[1]}` as Picture["key"];
      if (c.method === "PUT") {
        const own = o.put?.(c);
        if (own) return own;
        state = { ...state, pictures: state.pictures.map((p) => (p.key === key ? uploaded(key, c.contentType === "image/svg+xml" ? "image/svg+xml" : "image/png") : p)) };
        return { status: 200, body: state };
      }
      if (c.method === "DELETE") {
        state = { ...state, pictures: state.pictures.map((p) => (p.key === key ? { key, state: "none" as const, locked: false } : p)) };
        return { status: 200, body: state };
      }
    }
    return fail(404, "not_found");
  });
  return calls;
}

const valueOf = (a: SettingsAnswer, key: string) => String(a.settings.find((s) => s.key === key)?.value);
const nameOf = (a: SettingsAnswer) => valueOf(a, "ui.brand.name");

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const router = createMemoryRouter([{ path: "/admin/settings", element: <QueryClientProvider client={qc}><SettingsPage /></QueryClientProvider> }], {
    initialEntries: ["/admin/settings"],
  });
  render(<RouterProvider router={router} />);
}

const region = (name: string) => screen.getByRole("region", { name });
const row = (key: string) => document.querySelector(`[data-setting="${key}"]`) as HTMLElement;
const pictureRow = (kind: "logo" | "icon") => document.querySelector(`[data-picture="ui.brand.${kind}"]`) as HTMLElement;
const save = () => within(region("Branding")).getByRole("button", { name: "Save" }) as HTMLButtonElement;
const writes = (calls: Call[]) => calls.filter((c) => c.method !== "GET");
const preview = (which: "sidebar" | "signin" | "icons") => document.querySelector(`[data-preview="${which}"]`) as HTMLElement;
const text = (key: string) => within(row(key)).getByRole("textbox") as HTMLInputElement;

async function ready() {
  await screen.findByRole("region", { name: "Branding" });
}

// pick sets a colour through the field's colour input, which takes the six digits.
async function pick(key: string, hex: string) {
  const box = row(key).querySelector("input[type='color']") as HTMLInputElement;
  fireEvent.change(box, { target: { value: `#${hex}` } });
}

async function openAdvanced() {
  fireEvent.click(within(region("Branding")).getByText("Advanced: sidebar colours and theme tokens"));
  await waitFor(() => expect(row("ui.brand.sidebar.bg")).toBeTruthy());
}

let undo: () => void;
beforeEach(() => {
  session.set(signedAs("admin"));
  undo = desktopViewport();
  brand.set({ name: "SelfCloud", accent: "#053D8E", logo_url: null, artwork: true });
  brandLoaded.set(true);
});
afterEach(() => {
  undo();
  session.set(null);
});

describe("the Branding card", () => {
  it("is the first section, in the list of sections and in the cards", async () => {
    server();
    renderPage();
    await ready();
    const cards = [...document.querySelectorAll("section[id^='settings-']")].map((s) => s.id);
    expect(cards).toEqual(["settings-branding", "settings-uploads", "settings-sharing", "settings-signin", "settings-about"]);
    const names = within(screen.getByRole("navigation", { name: "Sections" })).getAllByRole("button").map((b) => b.textContent);
    expect(names[0]).toBe("Branding");
    expect(within(region("Branding")).getByText("How the installation looks to everyone: its name, colour, logo and app icon.")).toBeTruthy();
  });

  it("shows the name and the accent colour with their sources and defaults", async () => {
    server();
    renderPage();
    await ready();
    expect(text("ui.brand.name").value).toBe("SelfCloud");
    expect(within(row("ui.brand.name")).getByText("Default", { selector: ".ant-tag" })).toBeTruthy();
    expect(within(row("ui.brand.accent")).getByText("Accent colour")).toBeTruthy();
    expect(within(row("ui.brand.accent")).getByText("Default: #053D8E")).toBeTruthy();
    expect(row("ui.brand.name").querySelector("input")?.getAttribute("maxlength")).toBe("64");
  });

  it("draws every brand key the server serves, once, and folds the sidebar colours and theme tokens away", async () => {
    server();
    renderPage();
    await ready();
    expect(row("ui.brand.sidebar.bg")).toBeNull();
    await openAdvanced();
    const keys = [...document.querySelectorAll("[data-setting^='ui.brand.']")].map((el) => el.getAttribute("data-setting"));
    const served = settingsAnswer().settings.filter((s) => s.key.startsWith("ui.brand.")).map((s) => s.key);
    expect(served.length).toBe(21);
    expect([...keys].sort()).toEqual(served);
    expect(within(row("ui.brand.palette.light.accent_fill")).getByText("Light theme: accent fill")).toBeTruthy();
    expect(within(row("ui.brand.palette.dark.accent_soft")).getByText("Dark theme: soft accent")).toBeTruthy();
  });

  it("marks a value set here with Reset naming the default, and a colour token's default as taken from the accent", async () => {
    server({ over: { "ui.brand.name": { value: "Acme", source: "settings" }, "ui.brand.sidebar.bg": { value: "#112233", source: "settings" } } });
    renderPage();
    await ready();
    expect(within(row("ui.brand.name")).getByRole("button", { name: "Back to the default (SelfCloud)" })).toBeTruthy();
    expect(within(row("ui.brand.name")).getByText("Set here")).toBeTruthy();
    await openAdvanced();
    expect(within(row("ui.brand.sidebar.bg")).getByRole("button", { name: "Back to the default (from the accent)" })).toBeTruthy();
  });

  it("reads a key the config file writes as read-only and marked, with no Reset and a colour input that is off", async () => {
    server({ over: { "ui.brand.name": { value: "Acme", source: "file", locked: true }, "ui.brand.accent": { value: "#0F766E", source: "file", locked: true } } });
    renderPage();
    await ready();
    expect(text("ui.brand.name").disabled).toBe(true);
    expect(within(row("ui.brand.name")).getByText("Set in the server's config file")).toBeTruthy();
    expect(within(row("ui.brand.accent")).getByText("Set in the server's config file")).toBeTruthy();
    expect(within(row("ui.brand.accent")).queryByRole("button", { name: /Back to the default/ })).toBeNull();
    expect((row("ui.brand.accent").querySelector("input[type='color']") as HTMLInputElement).disabled).toBe(true);
  });
});

describe("saving the brand", () => {
  it("sends the name and the accent as the one change of the section, in the capitals the server writes", async () => {
    const { calls } = server();
    renderPage();
    await ready();
    expect(save().disabled).toBe(true);
    fireEvent.change(text("ui.brand.name"), { target: { value: "Acme" } });
    await pick("ui.brand.accent", "0f766e");
    expect(save().disabled).toBe(false);
    fireEvent.click(save());
    await waitFor(() => expect(writes(calls)).toHaveLength(1));
    expect(writes(calls)[0].body).toEqual({ set: { "ui.brand.name": "Acme", "ui.brand.accent": "#0F766E" } });
    await waitFor(() => expect(within(region("Branding")).getByRole("status").textContent).toBe("Saved. The change applies now."));
  });

  it("is no change when the accent is picked again as it already is", async () => {
    server();
    renderPage();
    await ready();
    await pick("ui.brand.accent", "053d8e");
    expect(save().disabled).toBe(true);
  });

  it("reads the brand again after a save, so the page's own sidebar and title follow at once", async () => {
    const { calls } = server();
    renderPage();
    await ready();
    fireEvent.change(text("ui.brand.name"), { target: { value: "Acme" } });
    expect(get(brand).name).toBe("SelfCloud");
    fireEvent.click(save());
    await waitFor(() => expect(get(brand).name).toBe("Acme"));
    expect(calls.filter((c) => c.url === "/api/v1/info").length).toBeGreaterThanOrEqual(2);
    expect(document.title).toContain("Acme");
  });

  it("sends a Reset of a colour token and of the name as reset", async () => {
    const { calls } = server({ over: { "ui.brand.name": { value: "Acme", source: "settings" }, "ui.brand.sidebar.text": { value: "#FFFFFF", source: "settings" } } });
    renderPage();
    await ready();
    await openAdvanced();
    fireEvent.click(within(row("ui.brand.name")).getByRole("button", { name: "Back to the default (SelfCloud)" }));
    fireEvent.click(within(row("ui.brand.sidebar.text")).getByRole("button", { name: "Back to the default (from the accent)" }));
    fireEvent.click(save());
    await waitFor(() => expect(writes(calls)).toHaveLength(1));
    expect(writes(calls)[0].body).toEqual({ reset: ["ui.brand.name", "ui.brand.sidebar.text"] });
  });

  it("shows the server's refusal of a value on the key, and keeps the edit", async () => {
    stubFetch((c) => {
      if (c.url === "/api/v1/settings" && c.method === "GET") return { status: 200, body: settingsAnswer() };
      if (c.url === "/api/v1/info") return { status: 200, body: info };
      if (c.method === "PATCH") return fail(400, "invalid_request", { reason: "invalid_setting", keys: ["ui.brand.name"], errors: ["ui.brand.name must be at most 64 characters"] });
      return fail(404, "not_found");
    });
    renderPage();
    await ready();
    fireEvent.change(text("ui.brand.name"), { target: { value: "Acme" } });
    fireEvent.click(save());
    expect((await within(row("ui.brand.name")).findByRole("alert")).textContent).toBe("ui.brand.name must be at most 64 characters");
    expect(text("ui.brand.name").value).toBe("Acme");
  });
});

describe("the preview", () => {
  it("draws the name and accent being typed, and changes nothing else on the page", async () => {
    server();
    renderPage();
    await ready();
    expect(preview("signin").querySelector("img")?.getAttribute("alt")).toBe("SelfCloud");
    fireEvent.change(text("ui.brand.name"), { target: { value: "Acme" } });
    await pick("ui.brand.accent", "0f766e");
    // With no logo the artwork is drawn, which has the name inside the picture, so the name is its label.
    await waitFor(() => expect(preview("signin").querySelector("img")?.getAttribute("alt")).toBe("Acme"));
    expect((preview("signin").querySelector("[data-preview-button]") as HTMLElement).style.background).toBe("rgb(15, 118, 110)");
    // The icon's tile is the accent too.
    expect((preview("icons").querySelector("[data-preview-tile]") as HTMLElement).style.background).toBe("rgb(15, 118, 110)");
    // The page's own brand, its title and its theme are what they were.
    expect(get(brand)).toEqual({ name: "SelfCloud", accent: "#053D8E", logo_url: null, artwork: true });
    expect(document.title).not.toContain("Acme");
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("Settings");
  });

  it("draws the sidebar colours being typed in Advanced, and the highlight's own text", async () => {
    server();
    renderPage();
    await ready();
    await openAdvanced();
    await pick("ui.brand.sidebar.bg", "112233");
    await pick("ui.brand.sidebar.active", "ffffff");
    const side = preview("sidebar");
    await waitFor(() => expect(side.style.background).toBe("rgb(17, 34, 51)"));
    const current = side.querySelector("[aria-current='page']") as HTMLElement;
    expect(current.style.background).toBe("rgb(255, 255, 255)");
    // The text on a white highlight is black, the one that reads on it.
    expect(current.style.color).toBe("rgb(0, 0, 0)");
  });

  it("draws the picture being used, and the artwork when there is none", async () => {
    server({ pictures: [{ key: "ui.brand.icon", state: "uploaded", type: "image/png", sha256: "b".repeat(64), locked: false }, uploaded("ui.brand.logo")] });
    renderPage();
    await ready();
    expect(preview("sidebar").querySelector("img")?.getAttribute("src")).toBe(`/brand/logo?v=${SHA}`);
    expect(preview("icons").querySelector("img")?.getAttribute("src")).toBe(`/brand/icon-512.png?v=${"b".repeat(64)}`);
  });

  it("warns when the accent is too light for the light theme, or too dark for the dark one", async () => {
    server();
    renderPage();
    await ready();
    expect(within(region("Branding")).queryByText(/so the (light|dark) theme keeps/)).toBeNull();
    await pick("ui.brand.accent", "fde68a");
    expect(await within(region("Branding")).findByText("This colour is too light for the light theme, so the light theme keeps the default blue.")).toBeTruthy();
    await pick("ui.brand.accent", "000000");
    expect(await within(region("Branding")).findByText("This colour is too dark for the dark theme, so the dark theme keeps its default.")).toBeTruthy();
    await pick("ui.brand.accent", "0f766e");
    await waitFor(() => expect(within(region("Branding")).queryByText(/so the (light|dark) theme keeps/)).toBeNull());
  });
});

describe("the pictures", () => {
  const png = () => new File([new Uint8Array([137, 80, 78, 71])], "logo.png", { type: "image/png" });
  const fileBox = (kind: "logo" | "icon") => within(pictureRow(kind)).getByLabelText(kind === "logo" ? "Logo file" : "Icon file") as HTMLInputElement;

  it("shows no picture as an upload button and the default's words, with no Remove", async () => {
    server();
    renderPage();
    await ready();
    const r = pictureRow("logo");
    expect(within(r).getByRole("button", { name: "Upload logo" })).toBeTruthy();
    expect(within(r).queryByRole("button", { name: "Remove logo" })).toBeNull();
    expect(within(r).getByText("Default", { selector: ".ant-tag" })).toBeTruthy();
    expect(within(pictureRow("icon")).getByRole("button", { name: "Upload icon" })).toBeTruthy();
  });

  it("uploads the file as the body, with its own type, and shows the picture in use with Replace and Remove", async () => {
    const { calls } = server();
    renderPage();
    await ready();
    fireEvent.change(fileBox("logo"), { target: { files: [png()] } });
    await waitFor(() => expect(writes(calls)).toHaveLength(1));
    const put = writes(calls)[0];
    expect(put.method).toBe("PUT");
    expect(put.url).toBe("/api/v1/settings/brand/logo");
    expect(put.contentType).toBe("image/png");
    expect(put.body).toBeInstanceOf(Blob);
    await waitFor(() => expect(within(pictureRow("logo")).getByRole("button", { name: "Replace logo" })).toBeTruthy());
    expect(within(pictureRow("logo")).getByRole("button", { name: "Remove logo" })).toBeTruthy();
    expect(within(pictureRow("logo")).getByText("Set here")).toBeTruthy();
    expect(preview("sidebar").querySelector("img")?.getAttribute("src")).toBe(`/brand/logo?v=${SHA}`);
    // The page's own brand is read again, so its sidebar shows the logo.
    expect(calls.filter((c) => c.url === "/api/v1/info").length).toBeGreaterThanOrEqual(2);
  });

  it("is not a change of the section: Save stays off and nothing is asked on leaving", async () => {
    server();
    renderPage();
    await ready();
    fireEvent.change(fileBox("icon"), { target: { files: [png()] } });
    await waitFor(() => expect(within(pictureRow("icon")).getByRole("button", { name: "Remove icon" })).toBeTruthy());
    expect(save().disabled).toBe(true);
  });

  it("removes a picture with DELETE and goes back to the default", async () => {
    const { calls } = server({ pictures: [{ key: "ui.brand.icon", state: "none", locked: false }, uploaded("ui.brand.logo", "image/svg+xml")] });
    renderPage();
    await ready();
    fireEvent.click(within(pictureRow("logo")).getByRole("button", { name: "Remove logo" }));
    await waitFor(() => expect(writes(calls)).toHaveLength(1));
    expect(writes(calls)[0]).toMatchObject({ method: "DELETE", url: "/api/v1/settings/brand/logo" });
    await waitFor(() => expect(within(pictureRow("logo")).getByRole("button", { name: "Upload logo" })).toBeTruthy());
    expect(within(pictureRow("logo")).queryByRole("button", { name: "Remove logo" })).toBeNull();
  });

  it("names Upload and Remove with a tooltip, as icons", async () => {
    server({ pictures: [{ key: "ui.brand.icon", state: "none", locked: false }, uploaded("ui.brand.logo")] });
    renderPage();
    await ready();
    const remove = within(pictureRow("logo")).getByRole("button", { name: "Remove logo" });
    expect(remove.textContent).toBe("");
    expect(remove.querySelector("svg")).toBeTruthy();
    fireEvent.mouseEnter(remove);
    expect(await screen.findByRole("tooltip")).toBeTruthy();
    expect(within(pictureRow("logo")).getByRole("button", { name: "Replace logo" }).textContent).toBe("");
  });

  it("says before sending that a file is not a PNG or an SVG, and sends nothing", async () => {
    const { calls } = server();
    renderPage();
    await ready();
    fireEvent.change(fileBox("logo"), { target: { files: [new File(["x"], "a.txt", { type: "text/plain" })] } });
    expect((await within(pictureRow("logo")).findByRole("alert")).textContent).toBe("A picture is a PNG or an SVG file.");
    expect(writes(calls)).toHaveLength(0);
  });

  it("says before sending that a file is over 256 KB, and sends nothing", async () => {
    const { calls } = server();
    renderPage();
    await ready();
    const big = new File([new Uint8Array(256 * 1024 + 1)], "big.png", { type: "image/png" });
    fireEvent.change(fileBox("icon"), { target: { files: [big] } });
    expect((await within(pictureRow("icon")).findByRole("alert")).textContent).toBe("This file is 257 KB; the most is 256 KB.");
    expect(writes(calls)).toHaveLength(0);
  });

  it("shows the server's refusal of a picture as it comes, and leaves the picture as it was", async () => {
    server({
      put: () => ({ status: 400, body: { error: { code: "invalid_request", message: "the icon must be square, 512 to 2048 pixels a side", details: { reason: "invalid_picture" } } } }),
    });
    renderPage();
    await ready();
    fireEvent.change(fileBox("icon"), { target: { files: [png()] } });
    expect((await within(pictureRow("icon")).findByRole("alert")).textContent).toBe("the icon must be square, 512 to 2048 pixels a side");
    expect(within(pictureRow("icon")).getByRole("button", { name: "Upload icon" })).toBeTruthy();
    // A new choice clears the refusal.
    fireEvent.change(fileBox("icon"), { target: { files: [png()] } });
    await waitFor(() => expect(within(pictureRow("icon")).getByRole("alert")).toBeTruthy());
  });

  it("shows a refusal that carries no message as the page's own sentence for the code", async () => {
    server({ put: () => fail(413, "too_large", { limit_bytes: 262144 }) });
    renderPage();
    await ready();
    fireEvent.change(fileBox("logo"), { target: { files: [png()] } });
    expect((await within(pictureRow("logo")).findByRole("alert")).textContent).toBeTruthy();
  });

  it("shows a picture the start did not use with its reason, and offers Remove", async () => {
    const { calls } = server({ pictures: [{ key: "ui.brand.icon", state: "none", locked: false }, { key: "ui.brand.logo", state: "unusable", reason: "its file is missing", locked: false }] });
    renderPage();
    await ready();
    const r = pictureRow("logo");
    expect(within(r).getByText("The stored picture is not used: its file is missing.")).toBeTruthy();
    expect(within(r).getByRole("button", { name: "Upload logo" })).toBeTruthy();
    fireEvent.click(within(r).getByRole("button", { name: "Remove logo" }));
    await waitFor(() => expect(writes(calls)).toHaveLength(1));
    expect(writes(calls)[0]).toMatchObject({ method: "DELETE", url: "/api/v1/settings/brand/logo" });
    await waitFor(() => expect(within(pictureRow("logo")).queryByText(/is not used/)).toBeNull());
    // An unusable picture is the default in the preview: no logo.
    expect(preview("sidebar").querySelector("img")?.getAttribute("src")).not.toContain("?v=");
  });

  it("reads a picture the config file sets as read-only and marked, with no upload and no Remove", async () => {
    server({ pictures: [{ key: "ui.brand.icon", state: "none", locked: false }, { key: "ui.brand.logo", state: "file", type: "image/png", sha256: SHA, locked: true }] });
    renderPage();
    await ready();
    const r = pictureRow("logo");
    expect(within(r).getByText("Set in the server's config file")).toBeTruthy();
    expect(within(r).queryByRole("button", { name: /logo/ })).toBeNull();
    expect(preview("sidebar").querySelector("img")?.getAttribute("src")).toBe(`/brand/logo?v=${SHA}`);
  });

  it("reads the settings again when a picture turns out to be locked", async () => {
    const { calls } = server({ put: () => fail(409, "conflict", { reason: "setting_locked", keys: ["ui.brand.logo"] }) });
    renderPage();
    await ready();
    const before = calls.filter((c) => c.url === "/api/v1/settings" && c.method === "GET").length;
    fireEvent.change(fileBox("logo"), { target: { files: [png()] } });
    await waitFor(() => expect(calls.filter((c) => c.url === "/api/v1/settings" && c.method === "GET").length).toBe(before + 1));
    expect(await within(pictureRow("logo")).findByRole("alert")).toBeTruthy();
  });
});
