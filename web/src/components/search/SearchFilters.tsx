// SPDX-License-Identifier: AGPL-3.0-or-later

// The search filters: a choice for each kind of filter, a label filter with its value, and a chip for every filter that is
// set. A chip's close button removes that filter alone. The controls only write the state; the page writes the address.
import { useState } from "react";
import { Button, Input, Select, theme as antdTheme } from "antd";
import { Eraser, X } from "lucide-react";
import IconButton from "../IconButton";
import type { LabelField } from "../../lib/labels/api";
import { useT, type Params } from "../../lib/i18n";
import {
  EXT_CHOICES,
  MAX_EXTS,
  MAX_LABELS,
  labelValueProblem,
  type LabelFilter,
  type Modified,
  type Size,
  type SearchState,
} from "../../lib/search/query";

interface Props {
  state: SearchState;
  // change returns the state with one change made, and the page writes it to the address.
  change: (next: SearchState) => void;
  // fields is the account's label fields, null while they are read, and fieldsOff when they could not be read.
  fields: LabelField[] | null;
  fieldsOff: boolean;
  coarse: boolean;
}

const touch = (coarse: boolean) => (coarse ? { minHeight: 44 } : {});
type Translate = (key: string, params?: Params) => string;

export default function SearchFilters({ state, change, fields, fieldsOff, coarse }: Props) {
  const { t } = useT();
  const { token } = antdTheme.useToken();
  const kindChoice = (value: string) => t(`search.kind.${value}`);
  const modifiedChoice = (value: Modified) => t(value === "" ? "search.modified.any" : `search.modified.${value}`);
  const sizeChoice = (value: Size) => t(value === "" ? "search.size.any" : `search.size.${value}`);
  const chips = chipsFor(state, fields, t);

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      <div style={{ display: "flex", flexWrap: "wrap", gap: 8, alignItems: "center" }}>
        {!state.inText && (
          <Select
            aria-label={t("search.kind")}
            value={state.kind}
            onChange={(v: string) => change({ ...state, kind: v as SearchState["kind"] })}
            className="search-filter" style={{ flex: "1 1 160px", minWidth: 0 }}
            options={["", "file", "dir", "shortcut"].map((v) => ({ value: v, label: v === "" ? t("search.kind.any") : kindChoice(v) }))}
          />
        )}
        <Select
          aria-label={t("search.ext")}
          mode="multiple"
          maxCount={MAX_EXTS}
          placeholder={t("search.ext.any")}
          value={state.exts}
          onChange={(v: string[]) => change({ ...state, exts: v })}
          className="search-filter" style={{ flex: "1 1 180px", minWidth: 0 }}
          options={EXT_CHOICES.map((e) => ({ value: e, label: e }))}
        />
        <Select
          aria-label={t("search.modified")}
          value={state.modified}
          onChange={(v: Modified) => change({ ...state, modified: v })}
          className="search-filter" style={{ flex: "1 1 160px", minWidth: 0 }}
          options={(["", "7", "30", "365"] as Modified[]).map((v) => ({ value: v, label: modifiedChoice(v) }))}
        />
        <Select
          aria-label={t("search.size")}
          value={state.size}
          onChange={(v: Size) => change({ ...state, size: v })}
          className="search-filter" style={{ flex: "1 1 160px", minWidth: 0 }}
          options={(["", "small", "medium", "large"] as Size[]).map((v) => ({ value: v, label: sizeChoice(v) }))}
        />
      </div>

      {fields !== null && fields.length > 0 && (
        <LabelAdder fields={fields} count={state.labels.length} coarse={coarse} onAdd={(l) => change({ ...state, labels: [...state.labels, l] })} />
      )}
      {fieldsOff && <p style={{ margin: 0, color: token.colorTextSecondary }}>{t("search.labelsOff")}</p>}

      {chips.length > 0 && (
        <div role="group" aria-label={t("search.clear")} style={{ display: "flex", flexWrap: "wrap", gap: 8, alignItems: "center" }}>
          {chips.map((c) => (
            <span
              key={c.key}
              data-testid="search-chip"
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: 4,
                minHeight: coarse ? 44 : 30,
                paddingInlineStart: 10,
                paddingInlineEnd: 2,
                borderRadius: token.borderRadius,
                background: token.colorPrimaryBg,
                color: token.colorText,
                maxWidth: "100%",
              }}
            >
              <span style={{ overflowWrap: "anywhere" }}>
                <bdi>{c.text}</bdi>
              </span>
              <IconButton label={t("search.chip.remove", { name: c.text })} onClick={() => change(c.remove(state))} icon={<X size={16} aria-hidden="true" focusable="false" />} />
            </span>
          ))}
          <IconButton label={t("search.clear")} onClick={() => change({ ...state, kind: "", exts: [], modified: "", size: "", labels: [] })} icon={<Eraser size={18} aria-hidden="true" focusable="false" />} />
        </div>
      )}
    </div>
  );
}

interface Chip {
  key: string;
  text: string;
  remove: (s: SearchState) => SearchState;
}

// chipsFor lists the filters that are set, each with the change that removes it. A label chip names the field and shows a choice
// by its name, never its ID.
function chipsFor(s: SearchState, fields: LabelField[] | null, t: Translate): Chip[] {
  const out: Chip[] = [];
  const chip = (label: string, value: string) => t("search.chip", { label, value });
  if (s.kind !== "") out.push({ key: "kind", text: chip(t("search.kind"), t(`search.kind.${s.kind}`)), remove: (x) => ({ ...x, kind: "" }) });
  if (s.exts.length > 0) out.push({ key: "ext", text: chip(t("search.ext"), s.exts.join(", ")), remove: (x) => ({ ...x, exts: [] }) });
  if (s.modified !== "") out.push({ key: "modified", text: chip(t("search.modified"), t(`search.modified.${s.modified}`)), remove: (x) => ({ ...x, modified: "" }) });
  if (s.size !== "") out.push({ key: "size", text: chip(t("search.size"), t(`search.size.${s.size}`)), remove: (x) => ({ ...x, size: "" }) });
  s.labels.forEach((l, i) => {
    // Until the fields are read, or for a field they do not have, the chip is neutral: no value and no ID can be shown.
    const f = fields?.find((x) => x.id === l.field);
    const choice = f?.type === "choice" ? f.choices.find((c) => c.id === l.value) : undefined;
    const text = !f || (f.type === "choice" && !choice) ? t("search.label") : chip(f.name, f.type === "choice" ? (choice?.name ?? "") : l.value);
    out.push({
      key: `label-${i}`,
      text,
      remove: (x) => ({ ...x, labels: x.labels.filter((_, j) => j !== i) }),
    });
  });
  return out;
}

// LabelAdder chooses a label field and a value, and adds the filter. A value is checked by its field's type before it is added,
// so a bad value is said in a sentence, not sent to the server.
function LabelAdder({
  fields,
  count,
  coarse,
  onAdd,
}: {
  fields: LabelField[];
  count: number;
  coarse: boolean;
  onAdd: (l: LabelFilter) => void;
}) {
  const { t } = useT();
  const { token } = antdTheme.useToken();
  const choices = fields.filter((f) => f.type !== "user");
  const [fieldID, setFieldID] = useState<string | undefined>(undefined);
  const [value, setValue] = useState("");
  const [problem, setProblem] = useState<string | null>(null);
  const field = choices.find((f) => f.id === fieldID);
  const full = count >= MAX_LABELS;

  const add = () => {
    if (!field) return;
    const bad = labelValueProblem(field.type, value);
    if (bad) {
      setProblem(t("search.err.labelValue"));
      return;
    }
    setProblem(null);
    onAdd({ field: field.id, value });
    setValue("");
  };

  const hint =
    field?.type === "text"
      ? t("search.label.hint.text")
      : field?.type === "number"
        ? t("search.label.hint.number")
        : field?.type === "date"
          ? t("search.label.hint.date")
          : "";

  return (
    <div style={{ display: "flex", flexWrap: "wrap", gap: 8, alignItems: "flex-start" }}>
      <Select
        aria-label={t("search.label.field")}
        placeholder={t("search.label")}
        value={fieldID}
        onChange={(v: string) => {
          setFieldID(v);
          setValue("");
          setProblem(null);
        }}
        className="search-filter" style={{ flex: "1 1 180px", minWidth: 0 }}
        options={choices.map((f) => ({ value: f.id, label: f.name }))}
      />
      {field?.type === "choice" ? (
        <Select
          aria-label={t("search.label.choice")}
          placeholder={t("search.label.choice")}
          value={value || undefined}
          onChange={(v: string) => setValue(v)}
          className="search-filter" style={{ flex: "1 1 180px", minWidth: 0 }}
          options={field.choices.filter((c) => !c.archived).map((c) => ({ value: c.id, label: c.name }))}
        />
      ) : (
        <Input
          aria-label={t("search.label.value")}
          placeholder={t("search.label.value")}
          value={value}
          disabled={!field}
          onChange={(e) => setValue(e.target.value)}
          onPressEnter={add}
          className="search-filter"
          style={{ minWidth: 180, flex: "1 1 180px" }}
        />
      )}
      <Button onClick={add} disabled={!field || full} aria-label={t("search.label.add")} style={touch(coarse)}>
        {t("search.label.add")}
      </Button>
      <div style={{ flexBasis: "100%", fontSize: token.fontSizeSM, color: token.colorTextSecondary }}>
        {hint}
        {field?.restricted && <div>{t("search.label.restricted")}</div>}
        {problem && (
          <div role="alert" style={{ color: token.colorError }}>
            {problem}
          </div>
        )}
      </div>
    </div>
  );
}
