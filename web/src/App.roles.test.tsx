// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, waitFor } from "@testing-library/react";
import { RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createAppRouter } from "./lib/router";
import { session, type Session } from "./lib/session";
import { setLang } from "./lib/i18n";
import { ThemeProvider } from "./theme/theme";
import type { User } from "./lib/types";

// Every page is replaced by a stub that names itself, and the shell by a plain frame: this file is about which page the
// gate shows for each role and address, not about what the pages do.
vi.mock("./components/Shell", async () => (await import("./test/stubPage")).stubShell());
vi.mock("./pages/FilesPage", async () => (await import("./test/stubPage")).stubPage("files"));
vi.mock("./pages/NodeLinkPage", async () => (await import("./test/stubPage")).stubPage("node"));
vi.mock("./pages/TrashPage", async () => (await import("./test/stubPage")).stubPage("trash"));
vi.mock("./pages/SharedPage", async () => (await import("./test/stubPage")).stubPage("shared"));
vi.mock("./pages/RequestsPage", async () => (await import("./test/stubPage")).stubPage("requests"));
vi.mock("./pages/HomePage", async () => (await import("./test/stubPage")).stubPage("home"));
vi.mock("./pages/StarredPage", async () => (await import("./test/stubPage")).stubPage("starred"));
vi.mock("./pages/RecentPage", async () => (await import("./test/stubPage")).stubPage("recent"));
vi.mock("./pages/StoragePage", async () => (await import("./test/stubPage")).stubPage("storage"));
vi.mock("./pages/ChangePasswordPage", async () => (await import("./test/stubPage")).stubPage("change-password"));
vi.mock("./pages/manage/OverviewPage", async () => (await import("./test/stubPage")).stubPage("manage overview"));
vi.mock("./pages/manage/PeoplePage", async () => (await import("./test/stubPage")).stubPage("manage people"));
vi.mock("./pages/manage/GroupsPage", async () => (await import("./test/stubPage")).stubPage("manage groups"));
vi.mock("./pages/manage/SpacesPage", async () => (await import("./test/stubPage")).stubPage("manage spaces"));
vi.mock("./pages/manage/StoragePage", async () => (await import("./test/stubPage")).stubPage("manage storage"));
vi.mock("./pages/manage/LinksPage", async () => (await import("./test/stubPage")).stubPage("manage links"));
vi.mock("./pages/manage/AuditPage", async () => (await import("./test/stubPage")).stubPage("manage audit"));
vi.mock("./pages/admin/TenantsPage", async () => (await import("./test/stubPage")).stubPage("admin tenants"));
vi.mock("./pages/admin/TenantPage", async () => (await import("./test/stubPage")).stubPage("admin tenant"));
vi.mock("./pages/admin/AdminsPage", async () => (await import("./test/stubPage")).stubPage("admin admins"));
vi.mock("./pages/admin/InstallationPage", async () => (await import("./test/stubPage")).stubPage("admin installation"));

import { Gate } from "./App";

function person(role: User["role"]): Session {
  return {
    user: { id: `usr_${role}`, email: `${role}@example.com`, display_name: role, locale: "en", role, status: "active" } as unknown as User,
    account: role === "admin" ? null : { id: "acc_1", name: "Acme" },
    csrf: `csrf-${role}`,
    publicLinks: false,
  } as unknown as Session;
}

// reach renders the gate at an address, signed in as a role, and says what the page shows.
async function reach(role: User["role"], path: string): Promise<{ text: string; address: string }> {
  session.set(person(role));
  window.history.replaceState(null, "", path);
  const router = createAppRouter([{ path: "*", element: <Gate /> }]);
  const view = render(
    <ThemeProvider>
      <RouterProvider router={router} />
    </ThemeProvider>,
  );
  const text = await waitFor(() => {
    const region = document.getElementById("main") ?? view.container;
    const said = region.textContent?.trim() ?? "";
    if (said === "" || said.includes("Loading")) throw new Error("no page yet");
    return said;
  });
  const address = window.location.pathname;
  view.unmount();
  return { text, address };
}

const NOT_FOUND = "Page not found";

beforeEach(async () => {
  window.localStorage.clear();
  await setLang("en");
});
afterEach(() => {
  session.set(null);
  window.history.replaceState(null, "", "/");
});

describe("an owner reaches only the owner pages", () => {
  it.each(["/admin/tenants", "/admin/tenants/acc_1", "/admin/admins", "/admin/installation"])("%s is the not-found view", async (path) => {
    const got = await reach("owner", path);
    expect(got.text).toContain(NOT_FOUND);
    expect(got.text).not.toContain("admin");
  });

  it("an owner's own pages open", async () => {
    expect((await reach("owner", "/manage/people")).text).toBe("stub manage people");
  });
});

describe("a member reaches neither the owner pages nor the admin pages", () => {
  it.each(["/manage", "/manage/people", "/admin/tenants", "/admin/installation"])("%s is the not-found view", async (path) => {
    expect((await reach("member", path)).text).toContain(NOT_FOUND);
  });
});

describe("an installation admin reaches the admin pages and no drive page", () => {
  it.each(["/manage", "/manage/people", "/manage/storage"])("%s is the not-found view", async (path) => {
    expect((await reach("admin", path)).text).toContain(NOT_FOUND);
  });

  it("the admin pages open", async () => {
    expect((await reach("admin", "/admin/tenants")).text).toBe("stub admin tenants");
    expect((await reach("admin", "/admin/tenants/acc_1")).text).toBe("stub admin tenant");
    expect((await reach("admin", "/admin/admins")).text).toBe("stub admin admins");
    expect((await reach("admin", "/admin/installation")).text).toBe("stub admin installation");
  });

  it.each(["/", "/files", "/files/fld_a", "/n/nod_a", "/starred", "/recent", "/shared", "/requests", "/trash", "/storage"])(
    "%s, a drive page, is the not-found view, or the admin home for the root",
    async (path) => {
      const got = await reach("admin", path);
      if (path === "/") {
        // The root is the admin's home, the tenants list, not Home.
        expect(got.address).toBe("/admin/tenants");
        expect(got.text).toBe("stub admin tenants");
      } else {
        expect(got.text).toContain(NOT_FOUND);
      }
    },
  );

  it("the change-password page stays open to an admin", async () => {
    expect((await reach("admin", "/change-password")).text).toBe("stub change-password");
  });
});

describe("a member and an owner reach their drive pages", () => {
  it.each(["member", "owner"] as const)("%s opens Home, Starred, Recent and Storage", async (role) => {
    expect((await reach(role, "/")).text).toBe("stub home");
    expect((await reach(role, "/starred")).text).toBe("stub starred");
    expect((await reach(role, "/recent")).text).toBe("stub recent");
    expect((await reach(role, "/storage")).text).toBe("stub storage");
  });
});
