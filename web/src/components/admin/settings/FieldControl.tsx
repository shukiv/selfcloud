// SPDX-License-Identifier: AGPL-3.0-or-later

import { Button, Input, InputNumber, Select, Space, Switch, Tooltip, type InputRef } from "antd";
import { Plus, Trash2 } from "lucide-react";
import { useEffect, useRef } from "react";
import ColourInput from "./ColourInput";
import IconButton from "../../IconButton";
import { useT } from "../../../lib/i18n";
import type { Setting } from "../../../lib/settings/api";
import type { FieldSpec } from "../../../lib/settings/fields";
import type { Draft } from "../../../lib/settings/form";
import type { Amount, DurationUnit, SizeUnit } from "../../../lib/settings/units";

const SIZE_UNITS: SizeUnit[] = ["MB", "GB"];
const DURATION_UNITS: DurationUnit[] = ["minutes", "hours", "days"];
// The server's own caps for ui.scripts (decision 219): at most ten entries, each at most 2048 bytes. The page states them;
// the server stays the judge of every entry.
const MAX_SCRIPTS = 10;
const SCRIPT_MAX_LENGTH = 2048;

interface Props {
  setting: Setting;
  label: string;
  value: Draft;
  readOnly: boolean;
  onChange: (value: Draft) => void;
  // spec says a text is a colour, or how long it may be.
  spec?: FieldSpec;
  // fallback is the colour drawn for a colour that is empty.
  fallback?: string;
}

// ListControl is a list setting (ui.scripts): one text row per entry, each with a Remove icon, and Add below. Add appends an
// empty row and focuses it; at the server's cap it is disabled and says so. Enter in a row does nothing: the section's own Save
// is the only way a list is sent. A read-only list keeps its rows on show, with no Add or Remove.
function ListControl({ value, readOnly, onChange }: { value: Draft; readOnly: boolean; onChange: (value: Draft) => void }) {
  const { t } = useT();
  const entries = Array.isArray(value) ? value : [];
  const boxes = useRef(new Map<number, InputRef>());
  const focusNext = useRef<number | null>(null);
  useEffect(() => {
    if (focusNext.current !== null) {
      boxes.current.get(focusNext.current)?.focus();
      focusNext.current = null;
    }
  });
  const full = entries.length >= MAX_SCRIPTS;
  const add = (
    <Button
      icon={<Plus size={16} aria-hidden="true" />}
      disabled={full}
      aria-label={t("settings.scripts.add")}
      onClick={() => {
        focusNext.current = entries.length;
        onChange([...entries, ""]);
      }}
    >
      {t("settings.scripts.add")}
    </Button>
  );
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 8, flex: 1, minWidth: 0, maxWidth: "100%" }}>
      {entries.length === 0 && <div style={{ fontSize: 13, color: "var(--ant-color-text-secondary)" }}>{t("settings.scripts.empty")}</div>}
      {entries.map((entry, i) => (
        <div key={i} style={{ display: "flex", alignItems: "center", gap: 6, minWidth: 0 }}>
          <Input
            ref={(el) => {
              if (el) boxes.current.set(i, el);
              else boxes.current.delete(i);
            }}
            value={entry}
            readOnly={readOnly}
            maxLength={SCRIPT_MAX_LENGTH}
            placeholder="https://example.com/menu.js"
            aria-label={t("settings.scripts.entry", { n: i + 1 })}
            onChange={(e) => onChange(entries.map((x, j) => (j === i ? e.target.value : x)))}
            style={{ flex: 1, minWidth: 0 }}
          />
          {!readOnly && <IconButton label={t("settings.scripts.remove", { n: i + 1 })} icon={<Trash2 size={16} aria-hidden="true" />} onClick={() => onChange(entries.filter((_, j) => j !== i))} />}
        </div>
      ))}
      {!readOnly &&
        (full ? (
          <Tooltip title={t("settings.scripts.max")}>
            <span style={{ display: "inline-flex" }}>{add}</span>
          </Tooltip>
        ) : (
          add
        ))}
    </div>
  );
}

// FieldControl is the input of one setting: a switch, a text, a count, a number with a unit picker for a size or a duration, or a
// list of texts. A read-only field keeps its value on show and cannot be changed.
export default function FieldControl({ setting, label, value, readOnly, onChange, spec, fallback = "#FFFFFF" }: Props) {
  const { t } = useT();
  switch (setting.type) {
    case "bool":
      return <Switch checked={value === true} disabled={readOnly} onChange={onChange} aria-label={label} />;
    case "list":
      return <ListControl value={value} readOnly={readOnly} onChange={onChange} />;
    case "string": {
      const text = typeof value === "string" ? value : "";
      if (spec?.colour) {
        return <ColourInput value={text} fallback={fallback} label={label} disabled={readOnly} onChange={onChange} />;
      }
      return <Input value={text} disabled={readOnly} maxLength={spec?.maxLength} showCount={spec?.maxLength !== undefined} onChange={(e) => onChange(e.target.value)} aria-label={label} style={{ maxWidth: "100%" }} />;
    }
    case "int":
      return (
        <span style={{ display: "inline-flex", alignItems: "center", gap: 8, maxWidth: "100%" }}>
          <InputNumber
            min={0}
            precision={0}
            value={typeof value === "number" ? value : null}
            disabled={readOnly}
            onChange={(v) => onChange(typeof v === "number" ? v : null)}
            aria-label={label}
            style={{ width: 120, maxWidth: "100%" }}
          />
          {setting.key === "office.lock_minutes" && <span>{t("settings.unit.minutes")}</span>}
        </span>
      );
    default: {
      const amount = value as Amount<SizeUnit | DurationUnit>;
      const sizes = setting.type === "bytes";
      const units: (SizeUnit | DurationUnit)[] = sizes ? SIZE_UNITS : DURATION_UNITS;
      return (
        <Space.Compact style={{ maxWidth: "100%" }}>
          <InputNumber
            min={0}
            value={amount.value}
            disabled={readOnly}
            onChange={(v) => onChange({ ...amount, value: typeof v === "number" ? v : null })}
            aria-label={label}
            style={{ width: 140 }}
          />
          <Select
            value={amount.unit}
            disabled={readOnly}
            onChange={(unit) => onChange({ ...amount, unit } as Amount<SizeUnit | DurationUnit>)}
            aria-label={t("settings.unitOf", { label })}
            options={units.map((u) => ({ value: u, label: t(`settings.unit.${u.toLowerCase()}`) }))}
            style={{ width: 110 }}
          />
        </Space.Compact>
      );
    }
  }
}
