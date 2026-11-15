// SPDX-License-Identifier: AGPL-3.0-or-later

import { Skeleton } from "antd";
import { formatDate } from "../../lib/files/format";
import { useLang, useT } from "../../lib/i18n";
import { sentence, type AuditEntry, type Names } from "../../lib/audit/sentence";
import { auditText, useAT } from "../../lib/audit/i18n";
import AuditTextFailure from "./AuditTextFailure";

// AuditItem is one audit entry: the plain sentence, the raw action under it, and when it happened. The sentence waits
// for the audit catalogue: a skeleton until it is there, and the main catalogue's error with Retry when it fails.
export default function AuditItem({ entry, names }: { entry: AuditEntry & { id: string; time: string }; names: Names }) {
  const { t } = useT();
  const { ready, failed, t: at } = useAT();
  const lang = useLang();
  return (
    <li className="row audit-row" style={{ display: "flex", justifyContent: "space-between", gap: 12, padding: "12px 0", borderBlockEnd: "1px solid var(--ant-color-split)" }}>
      {failed ? (
        <AuditTextFailure />
      ) : !ready ? (
        <Skeleton active title={false} paragraph={{ rows: 1 }} />
      ) : (
        <>
          <div style={{ minInlineSize: 0 }}>
            <p style={{ margin: 0, overflowWrap: "anywhere" }}>{auditText(sentence(entry, names), t, at)}</p>
            <p style={{ margin: 0, fontFamily: "monospace", fontSize: 12, color: "var(--ant-color-text-secondary)" }}>{entry.action}</p>
          </div>
          <time dateTime={entry.time} style={{ flex: "none", fontSize: 14, color: "var(--ant-color-text-secondary)" }}>
            {formatDate(entry.time, lang)}
          </time>
        </>
      )}
    </li>
  );
}
