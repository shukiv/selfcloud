// SPDX-License-Identifier: AGPL-3.0-or-later

import { formatDate } from "../../lib/files/format";
import { useLang, translate, useT } from "../../lib/i18n";
import { renderSentence, sentence, type AuditEntry, type Names } from "../../lib/audit/sentence";

// AuditItem is one audit entry: the plain sentence, the raw action under it, and when it happened.
export default function AuditItem({ entry, names }: { entry: AuditEntry & { id: string; time: string }; names: Names }) {
  useT();
  const lang = useLang();
  const text = renderSentence(sentence(entry, names), (key, params) => translate(lang, key, params));
  return (
    <li className="row audit-row" style={{ display: "flex", justifyContent: "space-between", gap: 12, padding: "12px 0", borderBlockEnd: "1px solid var(--ant-color-split)" }}>
      <div style={{ minInlineSize: 0 }}>
        <p style={{ margin: 0, overflowWrap: "anywhere" }}>{text}</p>
        <p style={{ margin: 0, fontFamily: "monospace", fontSize: 12, color: "var(--ant-color-text-secondary)" }}>{entry.action}</p>
      </div>
      <time dateTime={entry.time} style={{ flex: "none", fontSize: 14, color: "var(--ant-color-text-secondary)" }}>
        {formatDate(entry.time, lang)}
      </time>
    </li>
  );
}
