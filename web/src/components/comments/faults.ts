// SPDX-License-Identifier: AGPL-3.0-or-later

import { commentFault } from "../../lib/comments/logic";

// faultMessage is the text for a refused comment call: the rule that refused it when the server names one (the comments
// switch, the role, the rate limit, a comment that is gone, the item's bound, the author, a reply under a reply), and the
// fallback text otherwise. The server's own message
// is never shown, and an error code is never shown as a word.
export function faultMessage(err: unknown, t: (key: string) => string, fallback: string): string {
  switch (commentFault(err)) {
    case "off":
      return t("comments.off");
    case "role":
      return t("comments.role");
    case "rate":
      return t("comments.rateLimited");
    case "gone":
      return t("comments.gone");
    case "limit":
      return t("comments.limit");
    case "author":
      return t("comments.notAuthor");
    case "reply":
      return t("comments.replyToReply");
    default:
      return t(fallback);
  }
}
