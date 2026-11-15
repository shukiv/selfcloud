// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, type CSSProperties } from "react";
import { Download } from "lucide-react";
import { useAT } from "../../lib/audit/i18n";
import { auditRange, downloadAuditExport, rangeInvalid, type AuditRange } from "../../lib/manage/auditRange";
import { useCoarsePointer } from "../../lib/pointer";
import { dateField, dateInput, fieldError } from "../admin/adminStyles";
import IconButton from "../IconButton";

const EXPORT_HOLD_MS = 5000;

// AuditRangeBar is the date range and CSV export shared by the owner's audit log and the tenant's admin page. It keeps
// the dates it shows and reports each valid range, so a contradicting pair leaves the parent's last range in place.
export default function AuditRangeBar({
  account,
  action,
  idPrefix,
  loaded,
  onRange,
}: {
  account: string;
  action: string;
  idPrefix: string;
  loaded: boolean;
  onRange: (range: AuditRange) => void;
}) {
  // The bar's labels are the audit catalogue's: they draw nothing until it is there (a failed load is the page's alert).
  const { ready, t: at } = useAT();
  const coarse = useCoarsePointer();
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [range, setRange] = useState<AuditRange>({});
  const [held, setHeld] = useState(false);
  const hold = useRef<number | undefined>(undefined);
  const invalid = rangeInvalid(from, to);
  const fromID = `${idPrefix}-from`;
  const toID = `${idPrefix}-to`;
  const errorID = `${idPrefix}-range-error`;
  const height = coarse ? 44 : 32;
  const inputStyle: CSSProperties = { ...dateInput, height, boxSizing: "border-box" };

  useEffect(
    () => () => {
      if (hold.current !== undefined) window.clearTimeout(hold.current);
    },
    [],
  );

  const chooseFrom = (next: string) => {
    setFrom(next);
    if (!rangeInvalid(next, to)) {
      const nextRange = auditRange(next, to);
      setRange(nextRange);
      onRange(nextRange);
    }
  };
  const chooseTo = (next: string) => {
    setTo(next);
    if (!rangeInvalid(from, next)) {
      const nextRange = auditRange(from, next);
      setRange(nextRange);
      onRange(nextRange);
    }
  };
  const startExport = () => {
    if (invalid || held) return;
    downloadAuditExport(account, { ...range, action: action || undefined });
    setHeld(true);
    hold.current = window.setTimeout(() => {
      hold.current = undefined;
      setHeld(false);
    }, EXPORT_HOLD_MS);
  };

  return (
    <>
      <div style={dateField}>
        <label htmlFor={fromID}>{ready ? at("audit.from") : null}</label>
        <input
          id={fromID}
          type="date"
          value={from}
          onChange={(e) => chooseFrom(e.target.value)}
          aria-invalid={invalid || undefined}
          aria-describedby={invalid ? errorID : undefined}
          style={inputStyle}
        />
      </div>
      <div style={dateField}>
        <label htmlFor={toID}>{ready ? at("audit.to") : null}</label>
        <input
          id={toID}
          type="date"
          value={to}
          onChange={(e) => chooseTo(e.target.value)}
          aria-invalid={invalid || undefined}
          aria-describedby={invalid ? errorID : undefined}
          style={inputStyle}
        />
      </div>
      {loaded && ready && (
        <IconButton
          label={at("audit.export")}
          icon={<Download size={18} />}
          disabled={invalid || held}
          onClick={startExport}
        />
      )}
      {invalid && ready && (
        <p id={errorID} role="alert" style={fieldError}>
          {at("audit.rangeInvalid")}
        </p>
      )}
    </>
  );
}
