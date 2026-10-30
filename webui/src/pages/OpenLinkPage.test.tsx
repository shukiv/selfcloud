// SPDX-License-Identifier: AGPL-3.0-or-later

import { StrictMode } from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { session, type Session } from "../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../test/fetchStub";
import { peekSecret, stashSecret } from "../lib/sharing/links";
import OpenLinkPage from "./OpenLinkPage";

// A secret has the shape the server issues: 43 characters of base64url.
const SECRET = "a".repeat(43);
const DEAD = "This link doesn't work. It may have expired or been turned off.";

// A visit that opens: the credential for a root folder with no children. The folder is what the page shows once the
// credential is held, so a test that wants no sign-in or no failure answers the folder reads with it.
const bearer = { status: 200, body: { token: "tok_visitor_2", expires_at: new Date(Date.now() + 3_600_000).toISOString(), node: "nod_root", upload_only: false } };
const folder = (c: Call): Answer | null => {
  if (c.url === "/api/v1/share/open") return bearer;
  if (c.url.startsWith("/api/v1/nodes/nod_root/children")) return { status: 200, body: { items: [], next_cursor: null } };
  if (c.url === "/api/v1/nodes/nod_root") return { status: 200, body: { id: "nod_root", name: "Plans", kind: "dir", role: "viewer", mime_type: "", size: 0, parent_id: null } };
  return null;
};

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

function visitFrom(hash: string): void {
  window.history.replaceState(null, "", `/s${hash}`);
}

// openAnswers gives the answer to an open of the secret: the one the test passes, or an error when the secret is wrong.
function opens(answer: Answer) {
  return (c: Call): Answer => {
    if (c.url === "/api/v1/share/open") return answer;
    return fail(404, "not_found");
  };
}

beforeEach(() => {
  session.set(null);
  sessionStorage.clear();
  localStorage.clear();
  visitFrom("");
});

afterEach(() => {
  session.set(null);
  sessionStorage.clear();
  window.history.replaceState(null, "", "/");
});

describe("the link secret", () => {
  it("is read from the address and sent only in the body, never in a URL", async () => {
    visitFrom(`#${SECRET}`);
    const { calls } = stubFetch(opens({ status: 404, body: { error: { code: "not_found", details: {} } } }));
    render(<OpenLinkPage />);
    await waitFor(() => expect(calls.length).toBeGreaterThan(0));
    const open = calls.find((c) => c.url === "/api/v1/share/open");
    expect(open?.body).toEqual({ secret: SECRET });
    expect(calls.some((c) => c.url.includes(SECRET))).toBe(false);
  });

  it("is opened once when the page mounts under StrictMode (guard: startedRef)", async () => {
    session.set(signedAs("olivia"));
    visitFrom(`#${SECRET}`);
    const { calls } = stubFetch(opens(fail(404, "not_found")));
    render(
      <StrictMode>
        <OpenLinkPage />
      </StrictMode>,
    );
    await waitFor(() => expect(screen.getByText(DEAD)).toBeTruthy());
    expect(calls.filter((c) => c.url === "/api/v1/share/open")).toHaveLength(1);
  });

  it("is kept in no storage after the visit", async () => {
    visitFrom(`#${SECRET}`);
    stubFetch((c) => folder(c) ?? fail(404, "not_found"));
    render(<OpenLinkPage />);
    await waitFor(() => expect(sessionStorage.getItem("selfcloud.share.secret")).toBeNull());
    const stored = JSON.stringify({ ...localStorage, ...sessionStorage });
    expect(stored.includes(SECRET)).toBe(false);
    expect(stored.includes("tok_visitor_1")).toBe(false);
  });
});

describe("a visitor is sent to sign in only when the link is not found", () => {
  it("a signed-out visitor to a dead link goes to sign-in, the secret waits in storage, the address loses it", async () => {
    visitFrom(`#${SECRET}`);
    stubFetch(opens(fail(404, "not_found")));
    render(<OpenLinkPage />);
    await waitFor(() => expect(window.location.pathname).toBe("/login"));
    expect(window.location.hash).toBe("");
    expect(sessionStorage.getItem("selfcloud.share.secret")).toContain(SECRET);
  });

  it("a signed-in visitor to a dead link gets the one message and no sign-in (guard: visitStep's signedIn)", async () => {
    session.set(signedAs("olivia"));
    visitFrom(`#${SECRET}`);
    stubFetch(opens(fail(404, "not_found")));
    render(<OpenLinkPage />);
    await screen.findByText(DEAD);
    expect(window.location.pathname).toBe("/s");
  });

  it("a signed-out visitor who is asked for a password is not sent to sign-in", async () => {
    visitFrom(`#${SECRET}`);
    stubFetch(opens(fail(401, "unauthenticated", { reason: "password_required" })));
    render(<OpenLinkPage />);
    await screen.findByLabelText("Password");
    expect(window.location.pathname).toBe("/s");
    expect(window.location.hash).toBe(`#${SECRET}`);
  });

  it("a wrong password is refused with its message, and the page stays on the password step", async () => {
    visitFrom(`#${SECRET}`);
    stubFetch(opens(fail(401, "unauthenticated", { reason: "password_wrong" })));
    render(<OpenLinkPage />);
    await screen.findByText("That password is not right. Try again.");
    expect(window.location.pathname).toBe("/s");
  });
});

describe("the address loses the secret on every exit", () => {
  it("a dead link strips the fragment (guard: stripAddress on dead)", async () => {
    session.set(signedAs("olivia"));
    visitFrom(`#${SECRET}`);
    stubFetch(opens(fail(404, "not_found")));
    render(<OpenLinkPage />);
    await screen.findByText(DEAD);
    expect(window.location.hash).toBe("");
  });

  it("an organisation link goes to its item, and the old address with the secret is gone (guard: go on node_id)", async () => {
    session.set(signedAs("olivia"));
    visitFrom(`#${SECRET}`);
    stubFetch(opens({ status: 200, body: { node_id: "nod_item00001" } }));
    render(<OpenLinkPage />);
    await waitFor(() => expect(window.location.pathname).toBe("/n/nod_item00001"));
    expect(window.location.hash).toBe("");
  });

  it("the sign-in exit strips the fragment (guard: stripAddress on sign-in)", async () => {
    visitFrom(`#${SECRET}`);
    stubFetch(opens(fail(404, "not_found")));
    render(<OpenLinkPage />);
    await waitFor(() => expect(window.location.pathname).toBe("/login"));
    expect(window.location.hash).toBe("");
  });
});

describe("a malformed address", () => {
  it("is stripped even when a secret waits in storage (guard: stripAddress on a malformed fragment)", async () => {
    session.set(signedAs("olivia"));
    stashSecret(SECRET, window.sessionStorage);
    visitFrom("#not-a-secret");
    stubFetch((c) => (c.url === "/api/v1/share/open" ? bearer : folder(c) ?? fail(404, "not_found")));
    render(<OpenLinkPage />);
    await screen.findByRole("heading", { name: "Plans" });
    expect(window.location.hash).toBe("");
  });

  it("an organisation link with a malformed item id shows the dead-link message and goes nowhere (guard: validID on node_id)", async () => {
    session.set(signedAs("olivia"));
    visitFrom(`#${SECRET}`);
    stubFetch(opens({ status: 200, body: { node_id: "../../admin" } }));
    render(<OpenLinkPage />);
    await screen.findByText(DEAD);
    expect(window.location.pathname).toBe("/s");
    expect(window.location.hash).toBe("");
  });
});

describe("the visit's credential and state end with the session", () => {
  it("a drop link opens the drop page, which reads no folder (guard: uploadOnly branch)", async () => {
    session.set(signedAs("olivia"));
    visitFrom(`#${SECRET}`);
    const dropBearer = { status: 200, body: { token: "tok_drop_9", expires_at: new Date(Date.now() + 3_600_000).toISOString(), node: "nod_root", upload_only: true } };
    const { calls } = stubFetch((c) => {
      if (c.url === "/api/v1/share/open") return dropBearer;
      if (c.url === "/api/v1/share/info") return { status: 200, body: { title: "Send", message: "", ask_name: false, expires_at: new Date(Date.now() + 3_600_000).toISOString(), max_file_size: 1024 } };
      return folder(c) ?? fail(404, "not_found");
    });
    render(<OpenLinkPage />);
    await screen.findByRole("heading", { name: "Send" });
    expect(calls.some((c) => c.url.startsWith("/api/v1/nodes/") || c.url.includes("/children"))).toBe(false);
  });

  it("a session swap clears the credential and shows the one dead-link message (guard: session effect)", async () => {
    session.set(signedAs("olivia"));
    visitFrom(`#${SECRET}`);
    stubFetch((c) => folder(c) ?? fail(404, "not_found"));
    render(<OpenLinkPage />);
    await screen.findByRole("heading", { name: "Plans" });
    await act(async () => {
      session.set(signedAs("mateo"));
    });
    await screen.findByText(DEAD);
    expect(screen.queryByRole("heading", { name: "Plans" })).toBeNull();
    expect(window.location.hash).toBe("");
  });

  it("an open that fails after the session changed clears the stored secret (guard: forgetSecret before the changed return)", async () => {
    session.set(signedAs("olivia"));
    stashSecret(SECRET, window.sessionStorage);
    visitFrom("");
    let release!: (a: Answer) => void;
    const held = new Promise<Answer>((resolve) => {
      release = resolve;
    });
    stubFetch((c) => (c.url === "/api/v1/share/open" ? held : folder(c) ?? fail(404, "not_found")));
    render(<OpenLinkPage />);
    await act(async () => {
      session.set(signedAs("mateo"));
    });
    await act(async () => {
      release(fail(500, "internal_error"));
      await new Promise((r) => setTimeout(r, 0));
    });
    expect(peekSecret(window.sessionStorage)).toBe("");
  });

  it("an answer that arrives after the session changed is dropped (guard: changed() after await)", async () => {
    session.set(signedAs("olivia"));
    visitFrom(`#${SECRET}`);
    let release!: (a: Answer) => void;
    const held = new Promise<Answer>((resolve) => {
      release = resolve;
    });
    const { calls } = stubFetch((c) => (c.url === "/api/v1/share/open" ? held : folder(c) ?? fail(404, "not_found")));
    render(<OpenLinkPage />);
    await act(async () => {
      session.set(signedAs("mateo"));
    });
    await act(async () => {
      release(bearer);
      await new Promise((r) => setTimeout(r, 0));
    });
    expect(calls.some((c) => c.url.startsWith("/api/v1/nodes/"))).toBe(false);
    expect(screen.queryByRole("heading", { name: "Plans" })).toBeNull();
  });
});
