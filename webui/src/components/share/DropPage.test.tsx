// SPDX-License-Identifier: AGPL-3.0-or-later

import { StrictMode } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import DropPage from "./DropPage";

const DROP_INFO = {
  title: "Send the report",
  message: "Files for the audit",
  ask_name: false,
  expires_at: new Date(Date.now() + 86_400_000).toISOString(),
  max_file_size: 1024 * 1024,
};

// A visitor's page: the credential is a plain string, and renewal always succeeds.
function props(over: Partial<Parameters<typeof DropPage>[0]> = {}) {
  return {
    bearer: () => "tok_drop_1",
    expiresAt: () => new Date(Date.now() + 3_600_000).toISOString(),
    onrenew: vi.fn(async () => true),
    onlost: vi.fn(),
    ...over,
  };
}

// dropAnswers answers the drop link's info, and refuses every upload so the rows stay out of the server's reach.
function dropAnswers(c: Call) {
  if (c.url === "/api/v1/share/info") return { status: 200, body: DROP_INFO };
  return fail(401, "unauthenticated");
}

beforeEach(() => {
  session.set(null);
});

afterEach(() => {
  session.set(null);
  vi.unstubAllGlobals();
});

describe("a drop page never lists the folder", () => {
  it("reads the drop's info and no node or children of the folder (guard: upload-only branch in OpenLinkPage)", async () => {
    const { calls } = stubFetch(dropAnswers);
    render(<DropPage {...props()} />);
    await screen.findByRole("heading", { name: "Send the report" });
    expect(calls.map((c) => c.url)).toEqual(["/api/v1/share/info"]);
    expect(calls.every((c) => !c.url.includes("/nodes/") && !c.url.includes("/children"))).toBe(true);
  });

  it("sends the visitor's bearer and no session cookie or CSRF header with the info read", async () => {
    const { calls } = stubFetch(dropAnswers);
    render(<DropPage {...props()} />);
    await screen.findByRole("heading", { name: "Send the report" });
    expect(calls[0].auth).toBe("Bearer tok_drop_1");
  });

  it("shows only the files this visit sent, from its own manager (a file row appears for the visit)", async () => {
    stubFetch(dropAnswers);
    const { container } = render(<DropPage {...props()} />);
    await screen.findByRole("heading", { name: "Send the report" });
    expect(container.querySelector("ul.rows")).toBeNull();
    const input = container.querySelector<HTMLInputElement>('input[type="file"]');
    expect(input).not.toBeNull();
    fireEvent.change(input as HTMLInputElement, {
      target: { files: [new File(["the brief"], "brief.txt", { type: "text/plain" })] },
    });
    await waitFor(() => expect(container.querySelector("ul.rows .name")?.textContent).toBe("brief.txt"));
  });

  it("keeps working under StrictMode, which mounts, unmounts and mounts the page (its manager is made again)", async () => {
    stubFetch(dropAnswers);
    const { container } = render(
      <StrictMode>
        <DropPage {...props()} />
      </StrictMode>,
    );
    await screen.findByRole("heading", { name: "Send the report" });
    const input = container.querySelector<HTMLInputElement>('input[type="file"]');
    fireEvent.change(input as HTMLInputElement, {
      target: { files: [new File(["x"], "after-strict.txt", { type: "text/plain" })] },
    });
    await waitFor(() => expect(container.querySelector("ul.rows .name")?.textContent).toBe("after-strict.txt"));
  });
});

describe("a drop page's name and email", () => {
  it("asks for both as required when the link asks for a name", async () => {
    stubFetch((c) => (c.url === "/api/v1/share/info" ? { status: 200, body: { ...DROP_INFO, ask_name: true } } : fail(401, "unauthenticated")));
    render(<DropPage {...props()} />);
    await screen.findByRole("heading", { name: "Send the report" });
    expect(screen.getByLabelText("Your name").hasAttribute("required")).toBe(true);
    expect(screen.getByLabelText("Your email").hasAttribute("required")).toBe(true);
  });
});

describe("a file the server cannot find", () => {
  it("ends the link: the page is told it is gone (guard: sink onlost on not_found)", async () => {
    const onlost = vi.fn();
    stubFetch((c) => (c.url === "/api/v1/uploads" ? fail(404, "not_found") : dropAnswers(c)));
    const { container } = render(<DropPage {...props({ onlost })} />);
    await screen.findByRole("heading", { name: "Send the report" });
    fireEvent.change(container.querySelector<HTMLInputElement>('input[type="file"]') as HTMLInputElement, {
      target: { files: [new File(["x"], "gone.txt", { type: "text/plain" })] },
    });
    await waitFor(() => expect(onlost).toHaveBeenCalledWith("gone"), { timeout: 4000 });
  });
});

describe("a dead drop link", () => {
  it("is lost when the info answers 404, and the page does not list anything", async () => {
    const onlost = vi.fn();
    stubFetch(() => fail(404, "not_found"));
    render(<DropPage {...props({ onlost })} />);
    await waitFor(() => expect(onlost).toHaveBeenCalledWith("gone"));
  });
});
