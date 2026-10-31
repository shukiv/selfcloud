// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type CSSProperties } from "react";
import { Alert, Button, Input, Select } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useLang, useT } from "../../lib/i18n";
import { failureText } from "../../lib/manage/failure";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import { can } from "../../lib/files/roles";
import type { Node } from "../../lib/files/types";
import { clearNodeLabel, listLabelFields, listNodeLabels, setNodeLabel, type LabelField, type NodeLabel } from "../../lib/labels/api";
import {
  activeChoices,
  isChoice,
  isDate,
  isUser,
  labelFailure,
  parseNumber,
  showNumber,
  textProblem,
  visibleFields,
} from "../../lib/labels/logic";
import PeopleCombobox from "../share/PeopleCombobox";
import type { DirectoryEntry } from "../../lib/files/types";

// Labels shows an item's label values in its details. Every reader sees them; an editor sets or clears them. A
// restricted field is in the answer only for a manager of the item, so this shows what it is given. The server
// checks each value against its field; the controls here only offer what it accepts.
export default function Labels({ node }: { node: Node }) {
  const { t } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  // A shortcut carries no labels in this interface; the server would accept them on its own row.
  const shown = node.kind !== "shortcut";
  const fields = useQuery({
    queryKey: queryKey("labels", "fields"),
    queryFn: async () => (await listLabelFields()).items,
    enabled: shown,
    refetchOnMount: "always",
  });
  const values = useQuery({
    queryKey: queryKey("labels", "node", node.id),
    queryFn: async () => (await listNodeLabels(node.id)).items,
    enabled: shown,
    refetchOnMount: "always",
  });
  if (!shown) return null;
  const failure = fields.isError || values.isError ? failureText(fields.error ?? values.error, lang) : null;
  if (values.data === undefined || fields.data === undefined) {
    return failure !== null ? (
      <Alert type="error" showIcon role="alert" message={failure} style={{ marginBlockStart: 16 }} />
    ) : null;
  }
  const byField = new Map<string, LabelField>(fields.data.map((f) => [f.id, f]));
  const rows = visibleFields(fields.data, values.data);
  if (rows.length === 0) return null;
  const canSet = can.edit(node.role);
  return (
    <section aria-label={t("files.labels.heading")} style={{ marginBlockStart: 16, display: "grid", gap: 12 }}>
      <h3 style={{ margin: 0, fontSize: 15, fontWeight: 600 }}>{t("files.labels.heading")}</h3>
      {failure !== null && <Alert type="warning" showIcon role="alert" message={failure} />}
      {rows.map((row) => (
        <LabelRow
          key={row.field_id}
          node={node}
          row={row}
          field={byField.get(row.field_id)}
          canSet={canSet}
          onChanged={() => void qc.invalidateQueries({ queryKey: queryKey("labels", "node", node.id) })}
        />
      ))}
    </section>
  );
}

// LabelRow is one field: its name and its value, and, for an editor, the control that sets it.
function LabelRow({
  node,
  row,
  field,
  canSet,
  onChanged,
}: {
  node: Node;
  row: NodeLabel;
  field: LabelField | undefined;
  canSet: boolean;
  onChanged: () => void;
}) {
  const { t } = useT();
  const lang = useLang();
  const [draft, setDraft] = useState<string>(() => draftOf(row));
  const [pick, setPick] = useState<DirectoryEntry | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const id = `label-${row.field_id}`;
  const choices = field ? activeChoices(field.choices) : [];
  // A second press while a save is out does nothing, so one press sends one request.
  const once = (fn: () => Promise<void>) => (busy ? Promise.resolve() : fn());

  async function save(value: string | number | null) {
    const stale = sessionChanged();
    setBusy(true);
    setError(null);
    try {
      await setNodeLabel(node.id, row.field_id, value);
      if (stale()) return;
      onChanged();
    } catch (err) {
      if (stale()) return;
      const reason = labelFailure(err);
      setError(reason !== null ? t(`labels.err.${reason}`) : failureText(err, lang));
    } finally {
      setBusy(false);
    }
  }

  async function clear() {
    const stale = sessionChanged();
    setBusy(true);
    setError(null);
    try {
      await clearNodeLabel(node.id, row.field_id);
      if (stale()) return;
      setDraft("");
      setPick(null);
      onChanged();
    } catch (err) {
      if (stale()) return;
      setError(failureText(err, lang));
    } finally {
      setBusy(false);
    }
  }

  const submit = () =>
    once(async () => {
      if (row.type === "text") {
        const p = textProblem(draft);
        if (p !== null) return setError(t(p === "empty" ? "labels.err.emptyText" : "labels.err.longText"));
        return save(draft);
      }
      if (row.type === "number") {
        const n = parseNumber(draft);
        if (n === null) return setError(t("labels.err.number"));
        return save(n);
      }
      if (row.type === "date") {
        if (!isDate(draft)) return setError(t("labels.err.date"));
        return save(draft);
      }
      if (row.type === "user") {
        if (pick === null) return setError(t("labels.err.person"));
        return save(pick.id);
      }
      if (draft === "") return setError(t("labels.err.choice"));
      return save(draft);
    });

  return (
    <div style={ROW}>
      <label htmlFor={id} style={{ fontWeight: 600 }}>
        <bdi>{row.name}</bdi>
        {row.restricted && <span style={{ fontWeight: 400, marginInlineStart: 8, fontSize: 13 }}>{t("labels.restrictedBadge")}</span>}
      </label>
      {!canSet && <ReadValue row={row} lang={lang} />}
      {canSet && (
        <>
          {row.type === "text" && (
            <Input id={id} maxLength={500} value={draft} disabled={busy} onChange={(e) => setDraft(e.target.value)} aria-label={t("labels.valueFor", { name: row.name })} />
          )}
          {row.type === "number" && (
            <Input id={id} inputMode="decimal" value={draft} disabled={busy} onChange={(e) => setDraft(e.target.value)} aria-label={t("labels.valueFor", { name: row.name })} />
          )}
          {row.type === "date" && (
            <input
              id={id}
              type="date"
              value={draft}
              disabled={busy}
              onChange={(e) => setDraft(e.target.value)}
              aria-label={t("labels.valueFor", { name: row.name })}
              style={DATE}
            />
          )}
          {row.type === "user" && (
            <div style={{ display: "grid", gap: 8 }}>
              <span>{pick ? <bdi>{pick.name}</bdi> : currentUser(row, t)}</span>
              <PeopleCombobox id={id} kind="user" label={t("labels.choose")} hint={t("groups.addHint")} onpick={(entry) => setPick(entry)} />
            </div>
          )}
          {row.type === "choice" && (
            <Select
              id={id}
              aria-label={t("labels.valueFor", { name: row.name })}
              value={draft === "" ? undefined : draft}
              placeholder={t("labels.choose")}
              disabled={busy}
              onChange={(v: string) => setDraft(v)}
              options={[
                ...choices.map((c) => ({ value: c.id, label: <bdi>{c.name}</bdi> })),
                ...(isChoice(row.value) && row.value.archived
                  ? [{ value: row.value.id, label: <bdi>{`${row.value.name} ${t("labels.archivedTag")}`}</bdi>, disabled: true }]
                  : []),
              ]}
              style={{ width: "100%" }}
            />
          )}
          <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
            <Button type="primary" size="small" disabled={busy} onClick={() => void submit()}>
              {t("labels.save")}
            </Button>
            <Button size="small" disabled={busy || row.value === null} onClick={() => void clear()}>
              {t("labels.clear")}
            </Button>
          </div>
          {error !== null && <Alert type="error" showIcon role="alert" message={error} />}
        </>
      )}
    </div>
  );
}

// ReadValue shows a value as text for a reader. A missing value says so, and a name never shows an ID.
function ReadValue({ row, lang }: { row: NodeLabel; lang: ReturnType<typeof useLang> }) {
  const { t } = useT();
  const v = row.value;
  if (v === null) return <p style={TEXT_VALUE}>{t("labels.noValue")}</p>;
  if (row.type === "number" && typeof v === "number") return <p style={TEXT_VALUE}>{showNumber(v, lang)}</p>;
  if (row.type === "date" && typeof v === "string") return <p style={TEXT_VALUE}>{dateText(v, lang)}</p>;
  if (row.type === "user" && isUser(v)) {
    return <p style={TEXT_VALUE}>{v.name ? <bdi>{v.name}</bdi> : t("labels.unnamed")}</p>;
  }
  if (isChoice(v)) {
    return (
      <p style={TEXT_VALUE}>
        <bdi>{v.name}</bdi>
        {v.archived && <span> {t("labels.archivedTag")}</span>}
      </p>
    );
  }
  return (
    <p dir="auto" style={TEXT_VALUE}>
      {String(v)}
    </p>
  );
}

// draftOf is the control's first value: the stored text, number or date, or the choice's ID.
function draftOf(row: NodeLabel): string {
  const v = row.value;
  if (v === null) return "";
  if (typeof v === "string") return v;
  if (typeof v === "number") return String(v);
  if (isChoice(v)) return v.id;
  return "";
}

// currentUser shows the member a user value names, or that there is none.
function currentUser(row: NodeLabel, t: (k: string) => string): string {
  const v = row.value;
  if (isUser(v)) return v.name ?? t("labels.unnamed");
  return t("labels.noValue");
}

// dateText shows a stored YYYY-MM-DD in the reader's locale. It is built in UTC, so no time zone moves the day.
function dateText(s: string, lang: ReturnType<typeof useLang>): string {
  const [y, m, d] = s.split("-").map(Number);
  return new Intl.DateTimeFormat(lang, { timeZone: "UTC", dateStyle: "medium" }).format(Date.UTC(y, m - 1, d));
}

const ROW: CSSProperties = { display: "grid", gap: 6 };
const TEXT_VALUE: CSSProperties = { margin: 0, whiteSpace: "pre-wrap", overflowWrap: "anywhere" };
const DATE: CSSProperties = { font: "inherit", padding: "4px 11px", border: "1px solid var(--ant-color-border)", borderRadius: 6, maxWidth: 220 };

