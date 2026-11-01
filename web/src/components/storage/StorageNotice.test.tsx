// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../../lib/session";
import { myStorage, refreshMyStorage } from "../../lib/storage/api";
import { translate, LANGUAGES, type Lang } from "../../lib/i18n";
import en from "../../i18n/en.json";
import es from "../../i18n/es.json";
import fr from "../../i18n/fr.json";
import pt from "../../i18n/pt.json";
import ru from "../../i18n/ru.json";
import ar from "../../i18n/ar.json";
import he from "../../i18n/he.json";
import StorageNotice from "./StorageNotice";

// The figures are read from the server on mount; the test states them through the store and counts the reads.
vi.mock("../../lib/storage/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/storage/api")>()),
  refreshMyStorage: vi.fn(async () => undefined),
}));

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

const limited = (used: number) => ({ used_bytes: used, reserved_bytes: 0, quota_bytes: 1000, available_bytes: 1000 - used });

const shown = () =>
  render(
    <MemoryRouter initialEntries={["/files"]}>
      <Routes>
        <Route path="/files" element={<StorageNotice />} />
        <Route path="/storage" element={<p>the storage page</p>} />
      </Routes>
    </MemoryRouter>,
  );

beforeEach(() => {
  session.set(signedAs("olivia"));
  vi.mocked(refreshMyStorage).mockClear();
});

afterEach(() => {
  session.set(null);
  myStorage.set(null);
});

describe("the storage notice on My files", () => {
  it("shows from 80% of the limit, with a link to Storage and the meter", () => {
    myStorage.set(limited(900));
    shown();
    expect(screen.getByText(translate("en", "home.storageNotice", { percent: "90" }))).toBeTruthy();
    expect(screen.getByRole("link", { name: translate("en", "home.storageLink") }).getAttribute("href")).toBe("/storage");
    expect(screen.getByRole("meter").getAttribute("aria-valuemax")).toBe("1000");
  });

  // Loading the page again would end a running upload and stop the player.
  it("opens Storage inside the app, without loading the page again", () => {
    myStorage.set(limited(900));
    shown();
    fireEvent.click(screen.getByRole("link", { name: translate("en", "home.storageLink") }));
    expect(screen.getByText("the storage page")).toBeTruthy();
  });

  it("shows nothing below 80% of the limit", () => {
    myStorage.set(limited(799));
    const { container } = shown();
    expect(container.textContent).toBe("");
    expect(screen.queryByRole("meter")).toBeNull();
    expect(screen.queryByRole("link", { name: translate("en", "home.storageLink") })).toBeNull();
  });

  it("shows nothing before the figures are read, and nothing for a person with no limit", () => {
    const { container, unmount } = shown();
    expect(container.textContent).toBe("");
    unmount();
    myStorage.set({ used_bytes: 5000, reserved_bytes: 0, quota_bytes: null, available_bytes: null });
    const again = shown();
    expect(again.container.textContent).toBe("");
  });

  it("reads the figures when it is shown, so the notice is current", () => {
    myStorage.set(limited(100));
    shown();
    expect(refreshMyStorage).toHaveBeenCalledTimes(1);
  });

  it("has its percent in every language", () => {
    const catalogues: Record<Lang, Record<string, string>> = { en, es, fr, pt, ru, ar, he };
    for (const l of LANGUAGES) {
      expect(catalogues[l]["home.storageNotice"], l).toContain("{percent}");
      expect(catalogues[l]["home.storageLink"], l).toBeTypeOf("string");
    }
  });
});
