// SPDX-License-Identifier: AGPL-3.0-or-later

import { Input, InputNumber, Select, Space, Switch } from "antd";
import ColourInput from "./ColourInput";
import { useT } from "../../../lib/i18n";
import type { Setting } from "../../../lib/settings/api";
import type { FieldSpec } from "../../../lib/settings/fields";
import type { Draft } from "../../../lib/settings/form";
import type { Amount, DurationUnit, SizeUnit } from "../../../lib/settings/units";

const SIZE_UNITS: SizeUnit[] = ["MB", "GB"];
const DURATION_UNITS: DurationUnit[] = ["minutes", "hours", "days"];

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

// FieldControl is the input of one setting: a switch, a text, a count, or a number with a unit picker for a size or a duration.
// A read-only field keeps its value on show and cannot be changed.
export default function FieldControl({ setting, label, value, readOnly, onChange, spec, fallback = "#FFFFFF" }: Props) {
  const { t } = useT();
  switch (setting.type) {
    case "bool":
      return <Switch checked={value === true} disabled={readOnly} onChange={onChange} aria-label={label} />;
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
