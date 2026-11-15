// SPDX-License-Identifier: AGPL-3.0-or-later

// The activity list waits for the audit catalogue as it waits for its entries: a skeleton until the catalogue is there,
// then the sentence's words; a failed load shows the main catalogue's error with Retry, and Retry loads it again.
import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

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
import { loadComments } from "../../lib/comments/i18n";
import { setLang } from "../../lib/i18n";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import ActivityList from "./ActivityList";

beforeAll(async () => {
  await loadComments("en");
});

const entry = {
  id: "h1",
  time: "2026-11-01T10:00:00Z",
  kind: "comment",
  action: "comment.create",
  actor: { kind: "user", name: "Ann" },
  details: { reply: false },
};

function renderList() {
  stubFetch((c: Call) =>
    c.url.startsWith("/api/v1/nodes/nod_1/activity") ? { status: 200, body: { items: [entry] } } : fail(404, "not_found"),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ActivityList nodeID="nod_1" />
    </QueryClientProvider>,
  );
}

afterEach(async () => {
  await setLang("en");
});

describe("ActivityList while the audit catalogue loads", () => {
  it("draws a skeleton until the catalogue is there, then the sentence's words", async () => {
    await setLang("fr");
    const { container } = renderList();
    expect(container.querySelector(".ant-skeleton")).not.toBeNull();
    expect(container.textContent).not.toContain("audit.");
    frGate.release();
    const row = await screen.findByTestId("activity-entry");
    expect(row.textContent).toContain("Ann");
    expect(row.textContent).toContain("a commenté");
  });

  it("a failed load shows the error with Retry, and Retry loads the catalogue again", async () => {
    await setLang("he");
    renderList();
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain(heMain["app.crash.title"]);
    heFail.on = false;
    fireEvent.click(screen.getByRole("button", { name: heMain["upload.retry"] }));
    const row = await screen.findByTestId("activity-entry");
    expect(row.textContent).toContain("הגיב");
  });
});
