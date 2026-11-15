// SPDX-License-Identifier: AGPL-3.0-or-later

// The tenant audit log waits for the audit catalogue: a skeleton where the sentences go and no filter label until it is
// there, then the sentence's words; a failed load shows the main catalogue's error with Retry, and Retry loads it again.
import { fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const frGate = vi.hoisted(() => {
  let release = () => {};
  const promise = new Promise<void>((r) => {
    release = r;
  });
  return { promise, release: () => release() };
});
vi.mock("../../i18n/audit/fr.json", async () => {
  await frGate.promise;
  return await vi.importActual<{ default: Record<string, string> }>("../../i18n/audit/fr.json");
});
const heFail = vi.hoisted(() => ({ on: true }));
vi.mock("../../i18n/audit/he.json", async () => {
  if (heFail.on) throw new Error("chunk failed");
  return await vi.importActual<{ default: Record<string, string> }>("../../i18n/audit/he.json");
});

import heMain from "../../i18n/he.json";
import { setLang } from "../../lib/i18n";
import { session } from "../../lib/session";
import { signedAs } from "../../test/adminFixtures";
import { stubFetch, type Call } from "../../test/fetchStub";
import TenantAudit from "./TenantAudit";

beforeEach(() => session.set(signedAs("admin")));
afterEach(async () => {
  session.set(null);
  await setLang("en");
});

function renderAudit() {
  stubFetch((c: Call) =>
    c.url.includes("/audit")
      ? { status: 200, body: { items: [{ id: "e1", time: "2026-11-01T10:00:00Z", action: "key.revoke", actor: "usr_1", subject: "key_1" }] } }
      : { status: 200, body: { items: [] } },
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <TenantAudit account="acc_1" />
    </QueryClientProvider>,
  );
}

describe("TenantAudit while the audit catalogue loads", () => {
  it("draws a skeleton and no filter label until the catalogue is there, then the sentence's words", async () => {
    await setLang("fr");
    const { container } = renderAudit();
    expect(container.querySelector(".ant-skeleton")).not.toBeNull();
    expect(container.querySelector("tbody")).toBeNull();
    expect(document.querySelector('label[for="tenant-audit-action"]')?.textContent).toBe("");
    frGate.release();
    const region = await screen.findByRole("region");
    expect(await within(region).findByText(/a révoqué/)).toBeTruthy();
    expect(document.querySelector('label[for="tenant-audit-action"]')?.textContent).toBe("Action");
  });

  it("a failed load shows the error with Retry, and Retry loads the catalogue again", async () => {
    await setLang("he");
    renderAudit();
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain(heMain["app.crash.title"]);
    heFail.on = false;
    fireEvent.click(screen.getByRole("button", { name: heMain["upload.retry"] }));
    const region = await screen.findByRole("region");
    expect(await within(region).findByText(/ביטל/)).toBeTruthy();
  });
});
