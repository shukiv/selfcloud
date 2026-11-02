// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../apiError";
import type { ActivityEntry, Comment, Page, Viewer } from "./api";
import {
  canComment,
  canDelete,
  canEdit,
  canReply,
  canResolve,
  charCount,
  checkBody,
  commentFault,
  followEmpty,
  formatWhen,
  insertMention,
  MAX_CHARS,
  MAX_LINES,
  MAX_MENTIONS,
  activitySentence,
  mentionRefs,
  uniquePeople,
  viewerName,
} from "./logic";

const base: Comment = { id: "cmt_1", created_at: "2026-11-01T10:00:00Z", mentions: [], resolved: false, body: "hi" };

describe("who may do what, as the panel offers it", () => {
  it("a viewer may not write, a commenter may", () => {
    expect(canComment("viewer")).toBe(false);
    expect(canComment("commenter")).toBe(true);
    expect(canComment("manager")).toBe(true);
    expect(canComment(undefined)).toBe(false);
  });

  it("only the author edits, and only while still a commenter", () => {
    expect(canEdit({ ...base, mine: true }, "commenter")).toBe(true);
    expect(canEdit({ ...base, mine: false }, "manager")).toBe(false);
    expect(canEdit({ ...base, mine: true }, "viewer")).toBe(false);
    expect(canEdit({ ...base, mine: true, deleted: true }, "commenter")).toBe(false);
  });

  it("the author or a manager deletes, and a deleted comment is not deleted twice", () => {
    expect(canDelete({ ...base, mine: true }, "viewer")).toBe(true);
    expect(canDelete({ ...base, mine: false }, "manager")).toBe(true);
    expect(canDelete({ ...base, mine: false }, "editor")).toBe(false);
    expect(canDelete({ ...base, mine: true, deleted: true }, "manager")).toBe(false);
  });

  it("only a top-level comment is replied to and resolved (a reply has no replies)", () => {
    const reply = { ...base, parent_id: "cmt_0" };
    expect(canReply(base, "commenter")).toBe(true);
    expect(canReply(reply, "commenter")).toBe(false);
    expect(canResolve(base, "commenter")).toBe(true);
    expect(canResolve(reply, "commenter")).toBe(false);
    expect(canResolve(base, "viewer")).toBe(false);
    expect(canResolve({ ...base, deleted: true }, "commenter")).toBe(false);
  });
});

describe("mentions in the text", () => {
  it("adds @name at the end with one space between words", () => {
    expect(insertMention("", "Ann")).toBe("@Ann ");
    expect(insertMention("see", "Ann")).toBe("see @Ann ");
    expect(insertMention("see ", "Ann")).toBe("see @Ann ");
  });

  it("sends a reference only while its name is still in the text, each once", () => {
    const chosen = [
      { ref: "mnt_a", name: "Ann" },
      { ref: "mnt_b", name: "Bo" },
      { ref: "mnt_a", name: "Ann" },
    ];
    expect(mentionRefs("hi @Ann and @Bo", chosen)).toEqual(["mnt_a", "mnt_b"]);
    // The author deleted Bo from the text: Bo is not sent.
    expect(mentionRefs("hi @Ann", chosen)).toEqual(["mnt_a"]);
  });

  it("a mutation that sends every chosen reference would fail the removed-name case", () => {
    const chosen = [{ ref: "mnt_b", name: "Bo" }];
    expect(mentionRefs("no name here", chosen)).toEqual([]);
  });
});

describe("the body rules the panel checks before a send", () => {
  it("refuses a blank body, and a body of spaces", () => {
    expect(checkBody("", [])?.key).toBe("comments.blank");
    expect(checkBody("  \n\t ", [])?.key).toBe("comments.blank");
    expect(checkBody("a", [])).toBeNull();
  });

  it("allows exactly 2,000 characters and refuses one more, counting code points", () => {
    expect(checkBody("a".repeat(MAX_CHARS), [])).toBeNull();
    expect(checkBody("a".repeat(MAX_CHARS + 1), [])).toEqual({ key: "comments.tooLong", params: { max: MAX_CHARS } });
    // An emoji is one character, as the server counts it, not two UTF-16 units.
    expect(charCount("😀".repeat(MAX_CHARS))).toBe(MAX_CHARS);
    expect(checkBody("😀".repeat(MAX_CHARS), [])).toBeNull();
  });

  it("allows 50 lines, and refuses 51, whatever the line break", () => {
    expect(checkBody(Array(MAX_LINES).fill("x").join("\n"), [])).toBeNull();
    expect(checkBody(Array(MAX_LINES + 1).fill("x").join("\r\n"), [])?.key).toBe("comments.tooLines");
  });

  it("allows 10 people and refuses 11", () => {
    const ten = Array.from({ length: MAX_MENTIONS }, (_, i) => `mnt_${i}`);
    expect(checkBody("hi", ten)).toBeNull();
    expect(checkBody("hi", [...ten, "mnt_x"])?.key).toBe("comments.tooMentions");
  });
});

describe("paging through a page that is empty and has a cursor", () => {
  const page = (items: number[], next?: string): Page<number> => ({ items, next_cursor: next });

  it("reads on through empty pages until one has entries", async () => {
    const answers = [page([], "c1"), page([], "c2"), page([7, 8], "c3")];
    const fetchPage = vi.fn(async () => answers.shift() as Page<number>);
    const got = await followEmpty(fetchPage);
    expect(got.items).toEqual([7, 8]);
    expect(got.next_cursor).toBe("c3");
    expect(fetchPage).toHaveBeenCalledTimes(3);
    expect(fetchPage.mock.calls[1]).toEqual(["c1"]);
  });

  it("stops at the last page: an empty page with no cursor is the end", async () => {
    const fetchPage = vi.fn(async () => page([]));
    expect((await followEmpty(fetchPage)).items).toEqual([]);
    expect(fetchPage).toHaveBeenCalledTimes(1);
  });

  it("stops after the bounded rounds and keeps the cursor for a show-more press", async () => {
    const fetchPage = vi.fn(async (c?: string) => page([], `${c ?? "start"}+`));
    const got = await followEmpty(fetchPage, undefined, 3);
    expect(got.items).toEqual([]);
    expect(got.next_cursor).toBe("start++++");
    expect(fetchPage).toHaveBeenCalledTimes(4);
  });
});

describe("the activity sentence", () => {
  const entry = (over: Partial<ActivityEntry>): ActivityEntry => ({
    id: "h1",
    time: "2026-11-01T10:00:00Z",
    kind: "comment",
    action: "comment.create",
    actor: { kind: "user", name: "Ann" },
    details: { reply: true },
    ...over,
  });

  it("names a person who reaches the item, and says a reply", () => {
    const got = activitySentence(entry({}), "You", "Former member");
    expect(got.sentence.key).toBe("audit.comment.create.reply");
    expect(got.sentence.params.actor).toEqual({ text: "Ann" });
    expect(got.gone).toBe(false);
  });

  it("shows the viewer's own action as You", () => {
    const got = activitySentence(entry({ actor: { kind: "user", name: "You", mine: true } }), "You", "Former member");
    expect(got.sentence.params.actor).toEqual({ text: "You" });
  });

  it("replaces the actor of a gone person with the former-member text, and never a name", () => {
    const got = activitySentence(entry({ actor: { kind: "user", gone: true } }), "You", "Former member");
    expect(got.gone).toBe(true);
    expect(got.sentence.params.actor).toEqual({ text: "Former member" });
  });

  it("gives an installation admin's action no person's name", () => {
    const got = activitySentence(entry({ actor: { kind: "admin" } }), "You", "Former member");
    expect(got.sentence.params.actor).toEqual({ key: "audit.anAdmin" });
  });

  it("gives an account key's action the key's word, not a name", () => {
    const got = activitySentence(entry({ actor: { kind: "account_key" } }), "You", "Former member");
    expect(got.sentence.params.actor).toEqual({ key: "audit.apiKey" });
  });
});

describe("who viewed: three states, and no ID", () => {
  it("a name while the person reaches the item", () => {
    const v: Viewer = { user_id: "usr_1", name: "Ann", viewed_at: "2026-11-01T10:00:00Z" };
    expect(viewerName(v)).toEqual({ state: "named", name: "Ann" });
  });

  it("former member when the person no longer reaches it", () => {
    const v: Viewer = { user_id: "usr_2", gone: true, viewed_at: "2026-11-01T10:00:00Z" };
    expect(viewerName(v)).toEqual({ state: "gone" });
  });

  it("neither name nor gone past the server's bound: nobody checked, so nothing is said", () => {
    const v: Viewer = { user_id: "usr_3", viewed_at: "2026-11-01T10:00:00Z" };
    expect(viewerName(v)).toEqual({ state: "hidden" });
  });

  it("a mutation that calls an unchecked person gone would fail the hidden case", () => {
    const v: Viewer = { user_id: "usr_3", viewed_at: "2026-11-01T10:00:00Z" };
    expect(viewerName(v).state).not.toBe("gone");
  });
});

describe("the people search answer", () => {
  it("keeps each person once, by reference", () => {
    const got = uniquePeople([
      { ref: "mnt_1", name: "Ann" },
      { ref: "mnt_1", name: "Ann" },
      { ref: "mnt_2", name: "Ann" },
    ]);
    expect(got.map((p) => p.ref)).toEqual(["mnt_1", "mnt_2"]);
  });
});

describe("a failed comment call is named by its rule", () => {
  it("maps the switch, the role, the rate limit and a gone comment", () => {
    expect(commentFault(new ApiError(403, "forbidden", { reason: "comments_off" }, 0))).toBe("off");
    expect(commentFault(new ApiError(403, "forbidden", { reason: "role" }, 0))).toBe("role");
    expect(commentFault(new ApiError(429, "rate_limited", {}, 60))).toBe("rate");
    expect(commentFault(new ApiError(404, "not_found", {}, 0))).toBe("gone");
    expect(commentFault(new ApiError(403, "forbidden", { reason: "who_viewed_off" }, 0))).toBe("viewers_off");
    expect(commentFault(new ApiError(500, "internal", {}, 0))).toBe("other");
    expect(commentFault(new Error("network"))).toBe("other");
  });

  it("a switch that is off is not a rate limit and not a role", () => {
    expect(commentFault(new ApiError(403, "forbidden", { reason: "comments_off" }, 0))).not.toBe("rate");
  });
});

describe("times", () => {
  it("writes a time, and nothing for a value that is not a time", () => {
    expect(formatWhen("2026-11-01T10:00:00Z", "en")).not.toBe("");
    expect(formatWhen("not a time", "en")).toBe("");
    expect(formatWhen(null, "en")).toBe("");
  });
});

describe("the refusals that name their rule", () => {
  it("names the item bound, the author and a reply under a reply, and leaves the rest as before", () => {
    expect(commentFault(new ApiError(409, "conflict", { reason: "limit" }, 0))).toBe("limit");
    expect(commentFault(new ApiError(403, "forbidden", { reason: "author" }, 0))).toBe("author");
    expect(commentFault(new ApiError(400, "invalid_request", { reason: "reply_to_reply" }, 0))).toBe("reply");
    // A conflict with another reason, or a bad request with another reason, is not one of these.
    expect(commentFault(new ApiError(409, "conflict", {}, 0))).toBe("other");
    expect(commentFault(new ApiError(400, "invalid_request", { reason: "reply" }, 0))).toBe("other");
  });
});
