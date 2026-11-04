// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement } from "react";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { ctranslate, loadComments } from "../../lib/comments/i18n";
import { ApiError } from "../../lib/apiError";
import { faultMessage } from "./faults";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import type { Comment } from "../../lib/comments/api";
import CommentsPanel from "./CommentsPanel";
import CommentsTabs from "./CommentsTabs";
import SharingSwitches from "./SharingSwitches";
import Composer from "./Composer";
import { useInfiniteQuery } from "@tanstack/react-query";
import { listComments } from "../../lib/comments/api";
import { queryKey } from "../../lib/query";
import { nextPage } from "../../lib/comments/logic";

beforeAll(async () => {
  await loadComments("en");
});

function signedIn(): Session {
  return {
    user: { id: "usr_olivia", email: "olivia@example.com", display_name: "Olivia", role: "member", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf",
    publicLinks: false,
  } as unknown as Session;
}

beforeEach(() => session.set(signedIn()));
afterEach(() => {
  session.set(null);
});

// A client that does not retry, so a failed answer shows at once.
function client(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
}

function wrap(ui: ReactElement, qc = client()) {
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

function comment(over: Partial<Comment> = {}): Comment {
  return {
    id: "cmt_1",
    created_at: "2026-11-01T10:00:00Z",
    body: "A plain comment",
    author_name: "Ann",
    mine: false,
    mentions: [],
    resolved: false,
    ...over,
  };
}

// The list route's first page, with its answer for each cursor.
function page(items: unknown[], next?: string) {
  return { status: 200, body: { items, next_cursor: next } };
}

// plain drops the bidi isolation marks that wrap a name in a sentence, so a test reads the words.
const plain = (s: string | null) => (s ?? "").replace(/[\u2066-\u2069]/g, "");

// A deferred answer, for the tests that answer late.
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("comment icon buttons name their author", () => {
  it("each comment's Reply, Resolve and Delete name its author as the row shows it, so the rows can be told apart", async () => {
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) {
        return page([comment({ id: "cmt_ann", author_name: "Ann Lee", body: "ann" }), comment({ id: "cmt_ben", author_name: "Ben Ko", body: "ben" })]);
      }
      return fail(404, "not_found");
    });
    wrap(<ListHost nodeID="nod_1" role="manager" />);
    await screen.findByText("ann");
    expect(screen.getByRole("button", { name: "Reply: Ann Lee" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Delete: Ann Lee" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Resolve: Ann Lee" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Reply: Ben Ko" })).toBeTruthy();
  });

  // The reply and the reopen glyphs are arrows: they carry icon-dir, so a right-to-left page mirrors them (index.css).
  it("the reply and the reopen arrows carry icon-dir, so they mirror in a right-to-left page", async () => {
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) {
        return page([comment({ id: "cmt_ann", author_name: "Ann Lee", body: "ann" }), comment({ id: "cmt_ben", author_name: "Ben Ko", body: "ben", resolved: true })]);
      }
      return fail(404, "not_found");
    });
    wrap(<ListHost nodeID="nod_1" role="manager" />);
    await screen.findByText("ann");
    expect(screen.getByRole("button", { name: "Reply: Ann Lee" }).querySelector("svg")?.classList.contains("icon-dir")).toBe(true);
    expect(screen.getByRole("button", { name: "Reopen: Ben Ko" }).querySelector("svg")?.classList.contains("icon-dir")).toBe(true);
  });
});

describe("the comments panel for a commenter", () => {
  it("the composer sends the typed text and the picked mention, and clears only after the server takes it", async () => {
    const posted: Call[] = [];
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) return page([]);
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/people?q=An")) {
        return { status: 200, body: { items: [{ ref: "mnt_ann", name: "Ann" }] } };
      }
      if (c.method === "POST" && c.url === "/api/v1/nodes/nod_1/comments") {
        posted.push(c);
        return { status: 201, body: comment({ id: "cmt_new", body: "Hi @Ann " }) };
      }
      return fail(404, "not_found");
    });
    const { container } = wrap(
      <Composer nodeID="nod_1" label="New comment" submit="Post" />,
    );
    const area = container.querySelector("textarea") as HTMLTextAreaElement;
    fireEvent.change(area, { target: { value: "Hi" } });
    const pick = screen.getByRole("combobox");
    fireEvent.change(pick, { target: { value: "An" } });
    const option = await screen.findByRole("option", { name: "Ann" });
    fireEvent.click(option);
    expect(area.value).toBe("Hi @Ann ");

    fireEvent.click(screen.getByRole("button", { name: "Post" }));
    await waitFor(() => expect(posted.length).toBe(1));
    expect(posted[0].body).toEqual({ body: "Hi @Ann ", mentions: ["mnt_ann"] });
    await waitFor(() => expect(area.value).toBe(""));
  });

  it("a send that fails keeps the typed text and says so", async () => {
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET") return page([]);
      if (c.method === "POST") return fail(500, "internal");
      return fail(404, "not_found");
    });
    const { container } = wrap(<Composer nodeID="nod_1" label="New comment" submit="Post" />);
    const area = container.querySelector("textarea") as HTMLTextAreaElement;
    fireEvent.change(area, { target: { value: "keep me" } });
    fireEvent.click(screen.getByRole("button", { name: "Post" }));
    expect((await screen.findByRole("alert")).textContent).toContain("The comment was not saved. Your text is still here.");
    expect(area.value).toBe("keep me");
  });

  it("refuses a body over the limit before any send", async () => {
    const calls: Call[] = [];
    stubFetch((c: Call) => {
      calls.push(c);
      return page([]);
    });
    const { container } = wrap(<Composer nodeID="nod_1" label="New comment" submit="Post" />);
    fireEvent.change(container.querySelector("textarea") as HTMLTextAreaElement, { target: { value: "a".repeat(2001) } });
    fireEvent.click(screen.getByRole("button", { name: "Post" }));
    expect((await screen.findByRole("alert")).textContent).toContain("A comment may be at most 2000 characters.");
    expect(calls.filter((c) => c.method === "POST")).toEqual([]);
  });

  it("a hostile body is shown as the text it is: no element is made from it", async () => {
    const hostile = `<img src=x onerror="window.__owned=1"> and <script>window.__owned=2</script>`;
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) return page([comment({ body: hostile })]);
      return fail(404, "not_found");
    });
    const { container } = wrap(<ListHost nodeID="nod_1" role="commenter" />);
    expect(await screen.findByText((text) => text.includes("<img src=x"))).toBeTruthy();
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("script")).toBeNull();
    expect(container.textContent?.includes("<img src=x")).toBe(true);
    expect((window as unknown as { __owned?: number }).__owned).toBeUndefined();
  });

  it("the author edits, a manager deletes, and a viewer sees no action on another person's comment", async () => {
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) {
        return page([comment({ id: "cmt_mine", mine: true, body: "mine" }), comment({ id: "cmt_theirs", body: "theirs" })]);
      }
      return fail(404, "not_found");
    });
    const { unmount } = wrap(<ListHost nodeID="nod_1" role="viewer" />);
    await screen.findByText("mine");
    // A viewer may not reply or edit (editing needs a commenter), and deletes only her own comment.
    expect(screen.queryByRole("button", { name: /^Reply/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Edit/ })).toBeNull();
    expect(screen.getAllByRole("button", { name: /^Delete/ })).toHaveLength(1);
    unmount();

    wrap(<ListHost nodeID="nod_1" role="manager" />);
    await screen.findByText("theirs");
    // A manager deletes any comment; a commenter edits only her own, and a manager of the item is not an editor of others.
    expect(screen.getAllByRole("button", { name: /^Delete/ })).toHaveLength(2);
    expect(screen.getAllByRole("button", { name: /^Edit/ })).toHaveLength(1);
  });

  it("delete asks first, then sends the delete and refreshes", async () => {
    const calls: Call[] = [];
    let gone = false;
    stubFetch(async (c: Call): Promise<Answer> => {
      calls.push(c);
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) {
        return page(gone ? [] : [comment({ id: "cmt_mine", mine: true, body: "to go" })]);
      }
      if (c.method === "DELETE" && c.url === "/api/v1/comments/cmt_mine") {
        gone = true;
        return { status: 204 };
      }
      return fail(404, "not_found");
    });
    wrap(<ListHost nodeID="nod_1" role="commenter" />);
    await screen.findByText("to go");
    fireEvent.click(screen.getByRole("button", { name: /^Delete/ }));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete comment" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    expect(await screen.findByText("No comments yet.")).toBeTruthy();
  });

  it("resolve sends the new state for a top-level comment", async () => {
    const bodies: unknown[] = [];
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) return page([comment({ body: "topic" })]);
      if (c.method === "POST" && c.url === "/api/v1/comments/cmt_1/resolve") {
        bodies.push(c.body);
        return { status: 200, body: comment({ resolved: true, body: "topic" }) };
      }
      return fail(404, "not_found");
    });
    wrap(<ListHost nodeID="nod_1" role="commenter" />);
    await screen.findByText("topic");
    fireEvent.click(screen.getByRole("button", { name: /^Resolve/ }));
    await waitFor(() => expect(bodies).toEqual([{ resolved: true }]));
  });

  it("a failed first load shows an error and a try-again button", async () => {
    stubFetch(() => fail(500, "internal"));
    wrap(<ListHost nodeID="nod_1" role="commenter" />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("The comments could not be loaded.");
    expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy();
  });

  it("a failed refetch keeps the list on the screen and says it is the last one loaded", async () => {
    let fail500 = false;
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) {
        return fail500 ? fail(500, "internal") : page([comment({ body: "still here" })]);
      }
      return fail(404, "not_found");
    });
    const qc = client();
    wrap(<ListHost nodeID="nod_1" role="commenter" qc={qc} />, qc);
    await screen.findByText("still here");
    fail500 = true;
    await act(async () => {
      await qc.invalidateQueries({ queryKey: queryKey("comments", "nod_1") });
    });
    expect(await screen.findByText("The comments could not be refreshed. The list below is the last one loaded.")).toBeTruthy();
    expect(screen.getByText("still here")).toBeTruthy();
  });

  it("show more replies reads the rest of the thread, oldest first, and a reply has no reply button", async () => {
    const preview = [comment({ id: "cmt_r1", parent_id: "cmt_1", body: "first reply", author_name: "Bo", created_at: "2026-11-01T10:01:00Z" })];
    const rest = [
      comment({ id: "cmt_r1", parent_id: "cmt_1", body: "first reply", created_at: "2026-11-01T10:01:00Z" }),
      comment({ id: "cmt_r2", parent_id: "cmt_1", body: "second reply", created_at: "2026-11-01T10:02:00Z" }),
    ];
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET" && c.url.includes("parent=cmt_1")) return page(rest);
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) {
        return page([comment({ replies: preview, reply_count: 2, replies_more: true })]);
      }
      return fail(404, "not_found");
    });
    wrap(<ListHost nodeID="nod_1" role="commenter" />);
    await screen.findByText("first reply");
    expect(screen.queryByText("second reply")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Show more replies" }));
    expect(await screen.findByText("second reply")).toBeTruthy();
    // Replies are not replied to: the reply rows have no Reply button, only the top comment does.
    const replies = screen.getAllByTestId("comment-reply");
    expect(replies).toHaveLength(2);
    for (const r of replies) expect(within(r).queryByRole("button", { name: /^Reply/ })).toBeNull();
  });
});

describe("the tabs: comments, activity and who viewed", () => {
  it("hides the comments tab when the account's switch is off, and says why", async () => {
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.url.startsWith("/api/v1/nodes/nod_1/comments")) return fail(403, "forbidden", { reason: "comments_off" });
      if (c.url.startsWith("/api/v1/nodes/nod_1/activity")) return page([]);
      return fail(404, "not_found");
    });
    wrap(<CommentsTabs nodeID="nod_1" role="viewer" />);
    expect(await screen.findByText("Comments are turned off for this account.")).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("tab", { name: "Comments" })).toBeNull());
    expect(screen.getByRole("tab", { name: "Activity" })).toBeTruthy();
  });

  it("the who-viewed tab is shown to a manager only, and only while the switch is on", async () => {
    const seen: string[] = [];
    stubFetch(async (c: Call): Promise<Answer> => {
      seen.push(c.url);
      if (c.url.startsWith("/api/v1/nodes/nod_1/comments")) return page([]);
      if (c.url.startsWith("/api/v1/nodes/nod_1/activity")) return page([]);
      if (c.url.startsWith("/api/v1/nodes/nod_1/viewers")) return fail(403, "forbidden", { reason: "who_viewed_off" });
      return fail(404, "not_found");
    });
    wrap(<CommentsTabs nodeID="nod_1" role="manager" />);
    await waitFor(() => expect(seen.some((u) => u.includes("/viewers"))).toBe(true));
    await waitFor(() => expect(screen.queryByRole("tab", { name: "Who viewed" })).toBeNull());
  });

  it("a viewer does not ask for the who-viewed list at all", async () => {
    const seen: string[] = [];
    stubFetch(async (c: Call): Promise<Answer> => {
      seen.push(c.url);
      if (c.url.startsWith("/api/v1/nodes/nod_1/comments")) return page([]);
      if (c.url.startsWith("/api/v1/nodes/nod_1/activity")) return page([]);
      return fail(404, "not_found");
    });
    wrap(<CommentsTabs nodeID="nod_1" role="viewer" />);
    await screen.findByRole("tab", { name: "Activity" });
    expect(seen.some((u) => u.includes("/viewers"))).toBe(false);
    expect(screen.queryByRole("tab", { name: "Who viewed" })).toBeNull();
  });

  it("a manager with the switch on sees the list, its convenience note, and three kinds of name", async () => {
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.url.startsWith("/api/v1/nodes/nod_1/comments")) return page([]);
      if (c.url.startsWith("/api/v1/nodes/nod_1/activity")) return page([]);
      if (c.url.startsWith("/api/v1/nodes/nod_1/viewers")) {
        return page([
          { user_id: "usr_a", name: "Ann", viewed_at: "2026-11-01T10:00:00Z" },
          { user_id: "usr_b", gone: true, viewed_at: "2026-11-01T09:00:00Z" },
          { user_id: "usr_c", viewed_at: "2026-11-01T08:00:00Z" },
        ]);
      }
      return fail(404, "not_found");
    });
    wrap(<CommentsTabs nodeID="nod_1" role="manager" />);
    fireEvent.mouseDown(await screen.findByRole("tab", { name: "Who viewed" }));
    fireEvent.click(screen.getByRole("tab", { name: "Who viewed" }));
    const rows = await screen.findAllByTestId("viewer");
    expect(rows.map((r) => r.dataset.state)).toEqual(["named", "gone", "hidden"]);
    expect(rows[0].textContent).toContain("Ann");
    expect(rows[1].textContent).toContain("Former member");
    expect(rows[2].textContent).toContain("Name not shown");
    expect(screen.getByRole("note").textContent).toContain("A convenience, not evidence.");
    // No ID is shown to the manager.
    for (const r of rows) expect(r.textContent).not.toContain("usr_");
  });

  it("an activity page that is empty but has a cursor is followed to the entries behind it", async () => {
    const calls: string[] = [];
    stubFetch(async (c: Call): Promise<Answer> => {
      calls.push(c.url);
      if (c.url.startsWith("/api/v1/nodes/nod_1/comments")) return page([]);
      if (c.url === "/api/v1/nodes/nod_1/activity?limit=20") return { status: 200, body: { items: [], next_cursor: "c1" } };
      if (c.url === "/api/v1/nodes/nod_1/activity?limit=20&cursor=c1") {
        return {
          status: 200,
          body: {
            items: [
              {
                id: "h1",
                time: "2026-11-01T10:00:00Z",
                kind: "comment",
                action: "comment.create",
                actor: { kind: "user", name: "Ann" },
                details: { reply: false },
              },
            ],
          },
        };
      }
      return fail(404, "not_found");
    });
    wrap(<CommentsTabs nodeID="nod_1" role="viewer" />);
    fireEvent.click(await screen.findByRole("tab", { name: "Activity" }));
    expect(plain((await screen.findByTestId("activity-entry")).textContent)).toContain("Ann commented on an item.");
    expect(calls).toContain("/api/v1/nodes/nod_1/activity?limit=20&cursor=c1");
    expect(screen.queryByText("Nothing has been recorded for this item yet.")).toBeNull();
  });

  it("a gone actor is shown as former member, and no name is left in the sentence", async () => {
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.url.startsWith("/api/v1/nodes/nod_1/comments")) return page([]);
      if (c.url.startsWith("/api/v1/nodes/nod_1/activity")) {
        return page([
          {
            id: "h2",
            time: "2026-11-01T10:00:00Z",
            kind: "comment",
            action: "comment.delete",
            actor: { kind: "user", gone: true },
            details: {},
          },
        ]);
      }
      return fail(404, "not_found");
    });
    wrap(<CommentsTabs nodeID="nod_1" role="viewer" />);
    fireEvent.click(await screen.findByRole("tab", { name: "Activity" }));
    const entry = await screen.findByTestId("activity-entry");
    expect(plain(entry.textContent)).toContain("Former member deleted a comment on an item.");
    expect(entry.textContent).not.toContain("Someone");
  });
});

describe("the mention search", () => {
  it("drops an answer for an old query that arrives after the new one", async () => {
    const answers: Record<string, ReturnType<typeof deferred<Answer>>> = {};
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.url.startsWith("/api/v1/nodes/nod_1/people")) {
        const q = decodeURIComponent(c.url.split("q=")[1]);
        answers[q] = deferred<Answer>();
        return answers[q].promise;
      }
      return fail(404, "not_found");
    });
    wrap(<Composer nodeID="nod_1" label="New comment" submit="Post" />);
    const box = screen.getByRole("combobox");
    fireEvent.change(box, { target: { value: "An" } });
    await waitFor(() => expect(answers["An"]).toBeDefined());
    fireEvent.change(box, { target: { value: "Ann" } });
    await waitFor(() => expect(answers["Ann"]).toBeDefined());
    await act(async () => {
      answers["Ann"].resolve({ status: 200, body: { items: [{ ref: "mnt_ann", name: "Ann" }] } });
    });
    expect(await screen.findByRole("option", { name: "Ann" })).toBeTruthy();
    await act(async () => {
      answers["An"].resolve({ status: 200, body: { items: [{ ref: "mnt_anna", name: "Anna" }] } });
    });
    await new Promise((r) => setTimeout(r, 30));
    expect(screen.queryByRole("option", { name: "Anna" })).toBeNull();
    expect(screen.getByRole("option", { name: "Ann" })).toBeTruthy();
  });

  it("a rate-limited search says so in words", async () => {
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.url.startsWith("/api/v1/nodes/nod_1/people")) return fail(429, "rate_limited");
      return fail(404, "not_found");
    });
    wrap(<Composer nodeID="nod_1" label="New comment" submit="Post" />);
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "Ann" } });
    expect(await screen.findByText("Too many searches in a minute. Wait a minute, then try again.")).toBeTruthy();
  });

  it("waits for the typing to stop before it asks the server", async () => {
    const asked: string[] = [];
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.url.startsWith("/api/v1/nodes/nod_1/people")) {
        asked.push(c.url);
        return { status: 200, body: { items: [] } };
      }
      return fail(404, "not_found");
    });
    wrap(<Composer nodeID="nod_1" label="New comment" submit="Post" />);
    const box = screen.getByRole("combobox");
    fireEvent.change(box, { target: { value: "Al" } });
    fireEvent.change(box, { target: { value: "Ali" } });
    fireEvent.change(box, { target: { value: "Alic" } });
    await waitFor(() => expect(asked.length).toBe(1));
    expect(asked[0]).toBe("/api/v1/nodes/nod_1/people?q=Alic");
  });
});

describe("the owner's switches for comments and who viewed", () => {
  it("each switch sends only its own part of the change", async () => {
    const sent: unknown[] = [];
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET") return { status: 200, body: { public_links: true, anyone_links: 0, comments: true, who_viewed: false } };
      if (c.method === "PATCH") {
        sent.push(c.body);
        const b = c.body as Record<string, boolean>;
        return { status: 200, body: { public_links: true, anyone_links: 0, comments: true, who_viewed: false, ...b } };
      }
      return fail(404, "not_found");
    });
    wrap(<SharingSwitches account="acc_1" />);
    const comments = (await waitFor(() => {
      const el = document.getElementById("comments-switch") as HTMLInputElement | null;
      expect(el).not.toBeNull();
      return el as HTMLInputElement;
    })) as HTMLInputElement;
    expect(comments.checked).toBe(true);
    const whoViewed = document.getElementById("who-viewed-switch") as HTMLInputElement;
    expect(whoViewed.checked).toBe(false);
    fireEvent.click(whoViewed);
    await waitFor(() => expect(sent).toEqual([{ who_viewed: true }]));
    fireEvent.click(comments);
    await waitFor(() => expect(sent).toEqual([{ who_viewed: true }, { comments: false }]));
  });

  it("the who-viewed hint says it is a convenience and that turning it on again starts a new period", async () => {
    stubFetch(() => ({ status: 200, body: { public_links: true, anyone_links: 0, comments: true, who_viewed: true } }));
    wrap(<SharingSwitches account="acc_1" />);
    await screen.findByText(/a convenience, not evidence/);
    expect(screen.getByText(/starts a new period/)).toBeTruthy();
  });
});

describe("a refused write leaves the screen true to the server", () => {
  it("a failed edit keeps the typed text and says why", async () => {
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) {
        return page([comment({ id: "cmt_mine", mine: true, body: "mine" })]);
      }
      if (c.method === "PATCH" && c.url === "/api/v1/comments/cmt_mine") return fail(500, "internal");
      return fail(404, "not_found");
    });
    wrap(<ListHost nodeID="nod_1" role="commenter" />);
    await screen.findByText("mine");
    fireEvent.click(screen.getByRole("button", { name: /^Edit/ }));
    const box = screen.getByRole("textbox", { name: "Edit comment" }) as HTMLTextAreaElement;
    fireEvent.change(box, { target: { value: "changed" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect((await screen.findByRole("alert")).textContent).toContain("The comment was not saved. Your text is still here.");
    expect(box.value).toBe("changed");
  });

  it("a failed resolve says so, and the thread keeps its state", async () => {
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) return page([comment({ body: "topic" })]);
      if (c.method === "POST" && c.url === "/api/v1/comments/cmt_1/resolve") return fail(500, "internal");
      return fail(404, "not_found");
    });
    wrap(<ListHost nodeID="nod_1" role="commenter" />);
    await screen.findByText("topic");
    fireEvent.click(screen.getByRole("button", { name: /^Resolve/ }));
    expect((await screen.findByRole("alert")).textContent).toContain("The change was not saved.");
    expect(screen.getByRole("button", { name: /^Resolve/ })).toBeTruthy();
  });

  it("a failed delete keeps the confirm open with the reason", async () => {
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) {
        return page([comment({ id: "cmt_mine", mine: true, body: "to go" })]);
      }
      if (c.method === "DELETE") return fail(500, "internal");
      return fail(404, "not_found");
    });
    wrap(<ListHost nodeID="nod_1" role="commenter" />);
    await screen.findByText("to go");
    fireEvent.click(screen.getByRole("button", { name: /^Delete/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete comment" }));
    expect(await within(dialog).findByRole("alert")).toBeTruthy();
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(screen.getByText("to go")).toBeTruthy();
  });

  it("a delete the server says is gone closes the confirm and reads the list again", async () => {
    let gone = false;
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) {
        return page(gone ? [] : [comment({ id: "cmt_mine", mine: true, body: "to go" })]);
      }
      if (c.method === "DELETE") {
        gone = true;
        return fail(404, "not_found");
      }
      return fail(404, "not_found");
    });
    wrap(<ListHost nodeID="nod_1" role="commenter" />);
    await screen.findByText("to go");
    fireEvent.click(screen.getByRole("button", { name: /^Delete/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete comment" }));
    expect(await screen.findByText("No comments yet.")).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByText("to go")).toBeNull();
  });

  it("an edit the server says is gone reads the list again, and the row goes", async () => {
    let gone = false;
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) {
        return page(gone ? [] : [comment({ id: "cmt_mine", mine: true, body: "mine" })]);
      }
      if (c.method === "PATCH") {
        gone = true;
        return fail(404, "not_found");
      }
      return fail(404, "not_found");
    });
    wrap(<ListHost nodeID="nod_1" role="commenter" />);
    await screen.findByText("mine");
    fireEvent.click(screen.getByRole("button", { name: /^Edit/ }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("No comments yet.")).toBeTruthy();
    expect(screen.queryByText("mine")).toBeNull();
  });

  it("a switch turned off during a resolve hides the comments tab in the same session", async () => {
    let off = false;
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) {
        return off ? fail(403, "forbidden", { reason: "comments_off" }) : page([comment({ body: "topic" })]);
      }
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/activity")) return page([]);
      if (c.method === "POST" && c.url === "/api/v1/comments/cmt_1/resolve") {
        off = true;
        return fail(403, "forbidden", { reason: "comments_off" });
      }
      return fail(404, "not_found");
    });
    wrap(<CommentsTabs nodeID="nod_1" role="commenter" />);
    await screen.findByText("topic");
    fireEvent.click(screen.getByRole("button", { name: /^Resolve/ }));
    expect(await screen.findByText("Comments are turned off for this account.")).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("tab", { name: "Comments" })).toBeNull());
  });

  it("a post refreshes the activity that was already visited, not only the comments", async () => {
    const seen: string[] = [];
    stubFetch(async (c: Call): Promise<Answer> => {
      seen.push(`${c.method} ${c.url}`);
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) return page([]);
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/activity")) return page([]);
      if (c.method === "POST" && c.url === "/api/v1/nodes/nod_1/comments") return { status: 201, body: comment({ id: "cmt_new", body: "new" }) };
      return fail(404, "not_found");
    });
    wrap(<CommentsTabs nodeID="nod_1" role="commenter" />);
    fireEvent.click(await screen.findByRole("tab", { name: "Activity" }));
    await screen.findByText("Nothing has been recorded for this item yet.");
    fireEvent.click(screen.getByRole("tab", { name: "Comments" }));
    const activityBefore = seen.filter((s) => s.includes("/activity")).length;
    fireEvent.change(document.querySelector("textarea") as HTMLTextAreaElement, { target: { value: "new" } });
    fireEvent.click(screen.getByRole("button", { name: "Post" }));
    await waitFor(() => expect(seen.filter((s) => s.includes("/activity")).length).toBeGreaterThan(activityBefore));
  });

  it("a failed send focuses the box, and the box stays editable while the send is in flight", async () => {
    const post = deferred<Answer>();
    let posts = 0;
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET") return page([]);
      if (c.method === "POST") {
        posts += 1;
        return post.promise;
      }
      return fail(404, "not_found");
    });
    const { container } = wrap(<Composer nodeID="nod_1" label="New comment" submit="Post" />);
    const area = container.querySelector("textarea") as HTMLTextAreaElement;
    fireEvent.change(area, { target: { value: "keep me" } });
    fireEvent.click(screen.getByRole("button", { name: "Post" }));
    await waitFor(() => expect(posts).toBe(1));
    expect(area.disabled).toBe(false);
    await act(async () => {
      post.resolve(fail(500, "internal"));
    });
    expect((await screen.findByRole("alert")).textContent).toContain("Your text is still here.");
    expect(area.value).toBe("keep me");
    expect(document.activeElement).toBe(area);
  });

  it("text typed while a send is in flight is kept when the send succeeds", async () => {
    const post = deferred<Answer>();
    let posts = 0;
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET") return page([]);
      if (c.method === "POST") {
        posts += 1;
        return post.promise;
      }
      return fail(404, "not_found");
    });
    const { container } = wrap(<Composer nodeID="nod_1" label="New comment" submit="Post" />);
    const area = container.querySelector("textarea") as HTMLTextAreaElement;
    fireEvent.change(area, { target: { value: "first" } });
    fireEvent.click(screen.getByRole("button", { name: "Post" }));
    await waitFor(() => expect(posts).toBe(1));
    fireEvent.change(area, { target: { value: "first and more" } });
    await act(async () => {
      post.resolve({ status: 201, body: comment({ id: "cmt_new", body: "first" }) });
    });
    // A standalone composer has no list to read again, so the end of the send is waited for by its own timer.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 30));
    });
    expect(area.value).toBe("first and more");
  });

  it("a second send while one is in flight is refused", async () => {
    const post = deferred<Answer>();
    let posts = 0;
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET") return page([]);
      if (c.method === "POST") {
        posts += 1;
        return post.promise;
      }
      return fail(404, "not_found");
    });
    const { container } = wrap(<Composer nodeID="nod_1" label="New comment" submit="Post" />);
    const area = container.querySelector("textarea") as HTMLTextAreaElement;
    fireEvent.change(area, { target: { value: "once" } });
    fireEvent.keyDown(area, { key: "Enter", ctrlKey: true });
    fireEvent.keyDown(area, { key: "Enter", ctrlKey: true });
    await waitFor(() => expect(posts).toBe(1));
    await act(async () => {
      post.resolve({ status: 201, body: comment({ id: "cmt_new", body: "once" }) });
    });
    expect(posts).toBe(1);
  });

  it("show more replies shows that the rest is being read", async () => {
    const rest = deferred<Answer>();
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET" && c.url.includes("parent=cmt_1")) return rest.promise;
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) {
        return page([comment({ replies: [], reply_count: 2, replies_more: true })]);
      }
      return fail(404, "not_found");
    });
    wrap(<ListHost nodeID="nod_1" role="commenter" />);
    fireEvent.click(await screen.findByRole("button", { name: "Show more replies" }));
    expect(await screen.findByText("Loading…")).toBeTruthy();
    await act(async () => {
      rest.resolve(page([comment({ id: "cmt_r1", parent_id: "cmt_1", body: "a reply" })]));
    });
    expect(await screen.findByText("a reply")).toBeTruthy();
  });

  it("a failed read of the rest of the replies is shown even when the first page had none", async () => {
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.method === "GET" && c.url.includes("parent=cmt_1")) return fail(500, "internal");
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) {
        return page([comment({ replies: [], reply_count: 2, replies_more: true })]);
      }
      return fail(404, "not_found");
    });
    wrap(<ListHost nodeID="nod_1" role="commenter" />);
    fireEvent.click(await screen.findByRole("button", { name: "Show more replies" }));
    expect((await screen.findByRole("alert")).textContent).toContain("The comments could not be loaded.");
    expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy();
  });

  it("a refusal for the item's bound, an edit by someone else, or a reply under a reply has its own words", () => {
    const t = (key: string) => ctranslate("en", key);
    expect(faultMessage(new ApiError(409, "conflict", { reason: "limit" }, 0), t, "comments.saveFailed")).toBe(
      "This item has as many comments as it can hold. Nothing was saved.",
    );
    expect(faultMessage(new ApiError(403, "forbidden", { reason: "author" }, 0), t, "comments.saveFailed")).toBe(
      "Only the person who wrote a comment can change its text.",
    );
    expect(faultMessage(new ApiError(400, "invalid_request", { reason: "reply_to_reply" }, 0), t, "comments.saveFailed")).toBe(
      "A reply goes under a top-level comment, not under another reply. The list has been refreshed.",
    );
  });
});

describe("the keyboard in the name picker and the composer", () => {
  it("ArrowDown moves the active option, Enter picks it, and the box gets the focus back", async () => {
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.url.startsWith("/api/v1/nodes/nod_1/people")) {
        return { status: 200, body: { items: [{ ref: "mnt_ann", name: "Ann" }, { ref: "mnt_anna", name: "Anna" }] } };
      }
      if (c.method === "GET" && c.url.startsWith("/api/v1/nodes/nod_1/comments")) return page([]);
      return fail(404, "not_found");
    });
    const { container } = wrap(<Composer nodeID="nod_1" label="New comment" submit="Post" />);
    const box = screen.getByRole("combobox");
    expect(box.getAttribute("aria-expanded")).toBe("false");
    expect(box.hasAttribute("aria-controls")).toBe(false);
    fireEvent.change(box, { target: { value: "An" } });
    const first = await screen.findByRole("option", { name: "Ann" });
    expect(box.getAttribute("aria-expanded")).toBe("true");
    expect(box.getAttribute("aria-controls")).toBe(screen.getByRole("listbox").id);
    expect(box.getAttribute("aria-activedescendant")).toBe(first.id);
    fireEvent.keyDown(box, { key: "ArrowDown" });
    const second = screen.getByRole("option", { name: "Anna" });
    expect(box.getAttribute("aria-activedescendant")).toBe(second.id);
    expect(second.getAttribute("aria-selected")).toBe("true");
    fireEvent.keyDown(box, { key: "Enter" });
    const area = container.querySelector("textarea") as HTMLTextAreaElement;
    expect(area.value).toContain("Anna");
    expect(document.activeElement).toBe(area);
    expect(box.getAttribute("aria-expanded")).toBe("false");
    expect(box.hasAttribute("aria-controls")).toBe(false);
    expect(box.hasAttribute("aria-activedescendant")).toBe(false);
  });

  it("Escape closes the open list and keeps the name typed", async () => {
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.url.startsWith("/api/v1/nodes/nod_1/people")) return { status: 200, body: { items: [{ ref: "mnt_ann", name: "Ann" }] } };
      return page([]);
    });
    wrap(<Composer nodeID="nod_1" label="New comment" submit="Post" />);
    const box = screen.getByRole("combobox") as HTMLInputElement;
    fireEvent.change(box, { target: { value: "An" } });
    await screen.findByRole("option", { name: "Ann" });
    fireEvent.keyDown(box, { key: "Escape" });
    expect(screen.queryByRole("option", { name: "Ann" })).toBeNull();
    expect(box.getAttribute("aria-expanded")).toBe("false");
    expect(box.value).toBe("An");
  });

  it("Escape in the name picker with a name typed does not reach the drawer", async () => {
    const outer = vi.fn();
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.url.startsWith("/api/v1/nodes/nod_1/people")) return { status: 200, body: { items: [] } };
      return page([]);
    });
    wrap(
      <div onKeyDown={outer}>
        <Composer nodeID="nod_1" label="New comment" submit="Post" />
      </div>,
    );
    const box = screen.getByRole("combobox") as HTMLInputElement;
    fireEvent.change(box, { target: { value: "An" } });
    fireEvent.keyDown(box, { key: "Escape" });
    expect(outer).not.toHaveBeenCalled();
    expect(box.value).toBe("An");
  });

  it("Escape with text in the composer does not reach the drawer, and an empty composer lets it through", () => {
    const outer = vi.fn();
    stubFetch(() => page([]));
    wrap(
      <div onKeyDown={outer}>
        <Composer nodeID="nod_1" label="New comment" submit="Post" />
      </div>,
    );
    const area = document.querySelector("textarea") as HTMLTextAreaElement;
    fireEvent.change(area, { target: { value: "draft" } });
    fireEvent.keyDown(area, { key: "Escape" });
    expect(outer).not.toHaveBeenCalled();
    expect(area.value).toBe("draft");
    fireEvent.change(area, { target: { value: "" } });
    fireEvent.keyDown(area, { key: "Escape" });
    expect(outer).toHaveBeenCalledTimes(1);
  });

  it("a refusal for an old search does not put its note on the newer search", async () => {
    const answers: Record<string, ReturnType<typeof deferred<Answer>>> = {};
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.url.startsWith("/api/v1/nodes/nod_1/people")) {
        const q = decodeURIComponent(c.url.split("q=")[1]);
        answers[q] = deferred<Answer>();
        return answers[q].promise;
      }
      return fail(404, "not_found");
    });
    wrap(<Composer nodeID="nod_1" label="New comment" submit="Post" />);
    const box = screen.getByRole("combobox");
    fireEvent.change(box, { target: { value: "An" } });
    await waitFor(() => expect(answers["An"]).toBeDefined());
    fireEvent.change(box, { target: { value: "Ann" } });
    await waitFor(() => expect(answers["Ann"]).toBeDefined());
    await act(async () => {
      answers["An"].resolve(fail(429, "rate_limited"));
    });
    await new Promise((r) => setTimeout(r, 30));
    expect(screen.queryByText("Too many searches in a minute. Wait a minute, then try again.")).toBeNull();
    await act(async () => {
      answers["Ann"].resolve({ status: 200, body: { items: [{ ref: "mnt_ann", name: "Ann" }] } });
    });
    expect(await screen.findByRole("option", { name: "Ann" })).toBeTruthy();
    expect(screen.queryByText("Too many searches in a minute. Wait a minute, then try again.")).toBeNull();
  });

  it("a refusal for the current search says so in words", async () => {
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.url.startsWith("/api/v1/nodes/nod_1/people")) return fail(429, "rate_limited");
      return page([]);
    });
    wrap(<Composer nodeID="nod_1" label="New comment" submit="Post" />);
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "Ann" } });
    expect(await screen.findByText("Too many searches in a minute. Wait a minute, then try again.")).toBeTruthy();
  });
});

describe("who viewed mid-session", () => {
  it("the who-viewed tab leaves when the switch is turned off during the session", async () => {
    let off = false;
    stubFetch(async (c: Call): Promise<Answer> => {
      if (c.url.startsWith("/api/v1/nodes/nod_1/comments")) return page([]);
      if (c.url.startsWith("/api/v1/nodes/nod_1/activity")) return page([]);
      if (c.url.startsWith("/api/v1/nodes/nod_1/viewers")) {
        return off
          ? fail(403, "forbidden", { reason: "who_viewed_off" })
          : page([{ user_id: "usr_a", name: "Ann", viewed_at: "2026-11-01T10:00:00Z" }]);
      }
      return fail(404, "not_found");
    });
    const qc = client();
    wrap(<CommentsTabs nodeID="nod_1" role="manager" />, qc);
    expect(await screen.findByRole("tab", { name: "Who viewed" })).toBeTruthy();
    off = true;
    await act(async () => {
      await qc.invalidateQueries({ queryKey: queryKey("viewers", "nod_1") });
    });
    await waitFor(() => expect(screen.queryByRole("tab", { name: "Who viewed" })).toBeNull());
  });
});

// ListHost makes the comments query the tabs make, and renders the panel on it, as the tabs do.
function ListHost({ nodeID, role, qc }: { nodeID: string; role: string; qc?: QueryClient }) {
  void qc;
  const q = useInfiniteQuery({
    queryKey: queryKey("comments", nodeID),
    queryFn: ({ pageParam }) => listComments(nodeID, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => nextPage(last),
  });
  return <CommentsPanel nodeID={nodeID} role={role} q={q} />;
}
