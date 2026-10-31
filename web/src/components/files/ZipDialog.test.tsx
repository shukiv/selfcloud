// SPDX-License-Identifier: AGPL-3.0-or-later

import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { LANGUAGES, setLang, translate, translatePlural, type Lang } from "../../lib/i18n";
import ZipDialog from "./ZipDialog";

// Counts that reach every plural form of the languages in use: zero, one, two, few, many and other.
const COUNTS = [1, 2, 3, 5, 11, 21, 100];

beforeAll(async () => {
  for (const l of LANGUAGES) await setLang(l);
});

afterEach(async () => {
  cleanup();
  await setLang("en");
});

describe("ZipDialog", () => {
  it("shows the left-out notice with the plural form of its count, in every language", async () => {
    for (const l of LANGUAGES as readonly Lang[]) {
      await setLang(l);
      for (const n of COUNTS) {
        const onClose = vi.fn();
        const { unmount } = render(<ZipDialog phase={{ kind: "ready", skipped: n, zip: "zip_1" }} onClose={onClose} />);
        const expected = translatePlural(l, "files.zip.skipped", n);
        expect(expected, `${l} ${n}`).not.toBe("");
        expect(expected, `${l} ${n}`).not.toContain("files.zip");
        const notice = await screen.findByText(expected);
        expect(notice, `${l} ${n}`).toBeTruthy();
        unmount();
      }
    }
  });

  it("shows no notice when nothing was left out", async () => {
    render(<ZipDialog phase={{ kind: "ready", skipped: 0, zip: "zip_1" }} onClose={vi.fn()} />);
    await screen.findByText(translate("en", "files.zip.ready"));
    expect(screen.queryByText(translatePlural("en", "files.zip.skipped", 1))).toBeNull();
  });

  it("keeps the ready notice and the left-out notice in the status region, and not as alerts", async () => {
    await setLang("en");
    render(<ZipDialog phase={{ kind: "ready", skipped: 2, zip: "zip_1" }} onClose={vi.fn()} />);
    const status = await waitFor(() => {
      const el = screen.getByRole("status");
      expect(el.textContent).toContain(translate("en", "files.zip.ready"));
      return el;
    });
    expect(status.textContent).toContain(translatePlural("en", "files.zip.skipped", 2));
    expect(within(status).queryByRole("alert")).toBeNull();
  });

  it("adds the status text after the live region is in the page, so the change is announced", async () => {
    await setLang("en");
    const seen = new MutationObserver(() => undefined);
    seen.observe(document.body, { childList: true, subtree: true });
    render(<ZipDialog phase={{ kind: "making" }} onClose={vi.fn()} />);
    const records = seen.takeRecords();
    seen.disconnect();
    // A change made inside a status region that is already there: the text was not in the region when it was added.
    const inRegion = records.some((r) => {
      const el = r.target instanceof Element ? r.target : r.target.parentElement;
      return !!el?.closest('[role="status"]');
    });
    expect(inRegion).toBe(true);
  });

  it("labels the spinner, and keeps the status text in a live region that exists before the text", async () => {
    await setLang("en");
    render(<ZipDialog phase={{ kind: "making" }} onClose={vi.fn()} />);
    expect(screen.getByRole("img", { name: translate("en", "files.zip.making") })).toBeTruthy();
    const status = await waitFor(() => {
      const el = screen.getByRole("status");
      expect(el.textContent).toContain(translate("en", "files.zip.making"));
      return el;
    });
    expect(status.textContent).toContain(translate("en", "files.zip.closeNote"));
  });
});
