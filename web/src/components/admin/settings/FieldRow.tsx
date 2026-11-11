// SPDX-License-Identifier: AGPL-3.0-or-later

import { Button, Tag, Tooltip } from "antd";
import { Lock, RotateCcw } from "lucide-react";
import { useLang, useT } from "../../../lib/i18n";
import type { Setting } from "../../../lib/settings/api";
import type { FieldSpec } from "../../../lib/settings/fields";
import { draftOf, isChanged, type Draft, type FieldEdit } from "../../../lib/settings/form";
import { formatBound, formatValue } from "../../../lib/settings/format";
import FieldControl from "./FieldControl";

interface Props {
  spec: FieldSpec;
  setting: Setting;
  edit: FieldEdit | undefined;
  // error is the message of a refused value, or null.
  error: string | null;
  wide: boolean;
  onChange: (value: Draft) => void;
  onReset: () => void;
}

const muted = { margin: "4px 0 0", fontSize: 13, lineHeight: 1.5, color: "var(--ant-color-text-secondary)", maxWidth: "62ch" } as const;

// FieldRow is one setting: its name, its help and rules, where its value comes from, and its input. A field the config file
// writes, or that the server reads only at start, is read-only. A stored value has Reset, an icon that puts the default back.
export default function FieldRow({ spec, setting, edit, error, wide, onChange, onReset }: Props) {
  const { t } = useT();
  const lang = useLang();
  const label = t(`settings.field.${setting.key}.label`);
  const readOnly = setting.locked || setting.restart;
  const changed = isChanged(setting, edit);
  const words = { on: t("settings.on"), off: t("settings.off"), none: t("settings.none") };
  const shown = formatValue(setting.type, setting.default, lang, words);
  const canReset = !readOnly && setting.source === "settings" && edit?.reset !== true;
  const range = spec.range;
  // An emptied box is an edit of null, which must stay empty and not fall back to the saved value.
  const value = edit !== undefined ? edit.value : draftOf(setting);

  return (
    <div
      data-setting={setting.key}
      style={{
        display: "grid",
        gridTemplateColumns: wide ? "minmax(0, 1fr) minmax(0, 340px)" : "minmax(0, 1fr)",
        gap: wide ? "8px 24px" : 8,
        padding: "14px 0",
        borderBlockStart: "1px solid var(--ant-color-split)",
        alignItems: "start",
      }}
    >
      <div style={{ minWidth: 0 }}>
        <div style={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: 8, fontWeight: 500 }}>
          {changed && <span role="img" aria-label={t("settings.changed")} style={{ width: 6, height: 6, borderRadius: "50%", background: "var(--ant-color-primary)", display: "inline-block" }} />}
          <span>{label}</span>
          {setting.source === "file" || setting.source === "env" ? (
            <Tag icon={<Lock size={12} aria-hidden="true" style={{ marginInlineEnd: 4, verticalAlign: "-1px" }} />} style={{ marginInlineEnd: 0 }}>
              {t("settings.locked")}
            </Tag>
          ) : setting.source === "settings" ? (
            <Tag color="blue" style={{ marginInlineEnd: 0 }}>
              {t("settings.source.settings")}
            </Tag>
          ) : (
            <Tag style={{ marginInlineEnd: 0 }}>{t("settings.source.default")}</Tag>
          )}
          {setting.restart && (
            <Tag color="gold" style={{ marginInlineEnd: 0 }}>
              {t("settings.restart")}
            </Tag>
          )}
        </div>
        <p style={muted}>{t(`settings.field.${setting.key}.help`)}</p>
        {(spec.notes ?? []).map((n) => (
          <p key={n} style={muted}>
            {t(n)}
          </p>
        ))}
        {range !== undefined && !readOnly && (
          <p style={muted}>
            {t(range.zero ? "settings.rangeZero" : "settings.range", {
              min: formatBound(setting.type, range.min, lang),
              max: formatBound(setting.type, range.max, lang),
            })}
          </p>
        )}
        {!readOnly && <p style={muted}>{t("settings.defaultIs", { value: shown })}</p>}
      </div>
      <div style={{ minWidth: 0 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 6, justifyContent: wide ? "flex-end" : "flex-start", flexWrap: "wrap" }}>
          <FieldControl setting={setting} label={label} value={value} readOnly={readOnly} onChange={onChange} />
          {canReset ? (
            <Tooltip title={t("settings.reset", { value: shown })}>
              <Button type="text" aria-label={t("settings.reset", { value: shown })} icon={<RotateCcw size={16} aria-hidden="true" />} onClick={onReset} />
            </Tooltip>
          ) : (
            wide && <span style={{ width: 32, flex: "none" }} />
          )}
        </div>
        {error !== null && (
          <div role="alert" style={{ marginBlockStart: 6, color: "var(--ant-color-error)", fontSize: 13, textAlign: wide ? "end" : "start" }}>
            {error}
          </div>
        )}
      </div>
    </div>
  );
}
