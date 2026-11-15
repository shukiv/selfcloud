// SPDX-License-Identifier: AGPL-3.0-or-later

// The audit catalogue loads with the parts that show sentences: a skeleton until it is there, then the sentence's words;
// a failed load shows the main catalogue's error with Retry, and Retry loads it again. French waits on a gate the test
// opens; Hebrew fails once, then loads on Retry.
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

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
import AuditItem from "./AuditItem";

const names = {
  person: (id: string) => (id === "usr_ada" ? "Ada Admin" : undefined),
  space: () => undefined,
};
const entry = {
  id: "1",
  time: "2026-10-31T10:00:00Z",
  action: "node.create",
  actor: { kind: "user", id: "usr_ada" },
  target: "nod_1",
  details: { kind: "file", name: "Report.pdf" },
};

afterEach(async () => {
  await setLang("en");
});

describe("AuditItem while the audit catalogue loads", () => {
  it("draws a skeleton until the catalogue is there, then the sentence's words", async () => {
    await setLang("fr");
    const { container } = render(
      <ul>
        <AuditItem entry={entry} names={names} />
      </ul>,
    );
    expect(container.querySelector(".ant-skeleton")).not.toBeNull();
    expect(container.textContent).not.toContain("audit.");
    expect(container.textContent).not.toContain("a ajouté");
    frGate.release();
    expect(await screen.findByText(/a ajouté/)).toBeTruthy();
    expect(screen.getByText(/Report\.pdf/)).toBeTruthy();
  });

  it("a failed load shows the error with Retry, and Retry loads the catalogue again", async () => {
    await setLang("he");
    render(
      <ul>
        <AuditItem entry={entry} names={names} />
      </ul>,
    );
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain(heMain["app.crash.title"]);
    heFail.on = false;
    fireEvent.click(screen.getByRole("button", { name: heMain["upload.retry"] }));
    expect(await screen.findByText(/הוסיף/)).toBeTruthy();
  });
});
