// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, render } from "@testing-library/react";
import { useEffect, useLayoutEffect, useState } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { session, type Session } from "../../lib/session";
import { useOnSessionChange } from "./hooks";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "admin", locale: "en", status: "active" },
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

// Probe holds a secret, as a page does. Each commit records the text on the screen, which is what a person could see.
function Probe({ commits }: { commits: string[] }) {
  const [secret, setSecret] = useState("");
  useOnSessionChange(() => setSecret(""));
  // The secret arrives after the first person's mount, as a shown key does.
  useEffect(() => {
    setSecret("secret-of-first-person");
  }, []);
  useLayoutEffect(() => {
    commits.push(document.body.textContent ?? "");
  });
  return <p>{secret}</p>;
}

afterEach(() => session.set(null));

describe("useOnSessionChange", () => {
  // The clear runs while the change is rendered, not in an effect after the frame is committed: no committed render shows
  // the previous person's secret (guard: clear before paint).
  it("no committed render after a session change shows the previous person's secret", () => {
    session.set(signedAs("ana"));
    const commits: string[] = [];
    render(<Probe commits={commits} />);
    expect(commits.at(-1)).toContain("secret-of-first-person");
    commits.length = 0;
    act(() => {
      session.set(signedAs("ben"));
    });
    expect(commits.length).toBeGreaterThan(0);
    expect(commits.some((c) => c.includes("secret-of-first-person"))).toBe(false);
  });
});
