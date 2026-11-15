// SPDX-License-Identifier: AGPL-3.0-or-later

import { formatDate } from "../../lib/files/format";
import type { Lang, Params } from "../../lib/i18n";
import { sentence, type AuditEntry, type Names } from "../../lib/audit/sentence";
import { auditText } from "../../lib/audit/i18n";
import type { ListColumn } from "../files/Listing";

// AuditRow is one audit entry as a table row: its ID is the row's key.
export type AuditRow = AuditEntry & { id: string; time: string };

// auditSaid is an entry's sentence, as text: the Event cell shows it, and a screen reader hears the row by it. The words
// are read from the audit catalogue (at), with the main one (t) under any parameter that names it.
export function auditSaid(
  e: AuditRow,
  names: Names,
  t: (key: string, params?: Params) => string,
  at: (key: string, params?: Params) => string,
): string {
  return auditText(sentence(e, names), t, at);
}

// auditColumns are the two columns of the audit log, shared by the owner's pages and the tenant's admin page (decision 122):
// the sentence, with its action code under it, and When, newest first. When carries aria-sort descending and no button, as
// the order it arrives in has no control. whenSecondary says whether When joins the sentence's cell on a phone (the owner's
// phone rule, design section 4); the admin page keeps When as a column of its own there, as it always did. The titles are
// the audit catalogue's, so the table is drawn only once that catalogue is there.
export function auditColumns(
  at: (key: string) => string,
  lang: Lang,
  said: (e: AuditRow) => string,
  opts: { whenSecondary: boolean },
): ListColumn<AuditRow>[] {
  return [
    {
      key: "event",
      title: at("audit.filter"),
      render: (e) => (
        <span style={{ display: "block", minWidth: 0 }}>
          <span style={{ display: "block", overflowWrap: "anywhere" }}>{said(e)}</span>
          <span style={{ display: "block", fontFamily: "monospace", fontSize: 12, color: "var(--ant-color-text-secondary)" }}>{e.action}</span>
        </span>
      ),
    },
    {
      key: "when",
      title: at("audit.col.when"),
      ariaSort: "descending",
      secondary: opts.whenSecondary,
      width: 180,
      render: (e) => (
        <time dateTime={e.time} style={{ fontSize: 14, color: "var(--ant-color-text-secondary)" }}>
          {formatDate(e.time, lang)}
        </time>
      ),
    },
  ];
}
