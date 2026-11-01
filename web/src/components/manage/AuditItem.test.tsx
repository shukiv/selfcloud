// SPDX-License-Identifier: AGPL-3.0-or-later

import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { setLang } from "../../lib/i18n";
import AuditItem from "./AuditItem";

const names = {
  person: (id: string) => (id === "usr_ada" ? "Ada Admin" : undefined),
  space: () => undefined,
};

// The page renders a name with markup in it as text. The hostile name is a file name, so it is the one a user can choose.
const HOSTILE = "<img src=x onerror=alert(1)>";

describe("AuditItem", () => {
  beforeEach(async () => {
    await setLang("en");
  });

  it("shows a file name with markup in it literally, and adds no element", () => {
    const { container } = render(
      <ul>
        <AuditItem
          entry={{
            id: "1",
            time: "2026-10-31T10:00:00Z",
            action: "node.create",
            actor: { kind: "user", id: "usr_ada" },
            target: "nod_1",
            details: { kind: "file", name: HOSTILE },
          }}
          names={names}
        />
      </ul>,
    );
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain(HOSTILE);
    expect(container.textContent).toContain("added");
  });

  it("shows a sentence for a row with no details, without a raw ID or a placeholder", () => {
    const { container } = render(
      <ul>
        <AuditItem
          entry={{
            id: "2",
            time: "2026-10-31T10:00:00Z",
            action: "node.lock",
            actor: { kind: "user", id: "usr_ada" },
            target: "nod_1",
          }}
          names={names}
        />
      </ul>,
    );
    expect(container.textContent).toContain("Ada Admin");
    expect(container.textContent).toContain("locked an item for editing in the office editor.");
    expect(container.textContent).not.toMatch(/nod_|undefined|\{/);
  });
});
