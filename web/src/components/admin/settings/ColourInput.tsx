// SPDX-License-Identifier: AGPL-3.0-or-later

interface Props {
  // value is the colour set, or an empty text when the colour follows the accent; fallback is the colour then drawn.
  value: string;
  fallback: string;
  label: string;
  disabled: boolean;
  onChange: (hex: string) => void;
}

// ColourInput is the browser's own colour input for one brand colour, with its #RRGGBB beside it. It costs the page no script: antd's
// colour picker added 4.4 KB gzip to the first load, which is over its budget. A pick is sent as #RRGGBB in capitals, the form the
// server writes. The browser's input has no alpha.
export default function ColourInput({ value, fallback, label, disabled, onChange }: Props) {
  const shown = (value !== "" ? value : fallback).toUpperCase();
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
      <input
        type="color"
        aria-label={label}
        disabled={disabled}
        value={shown.toLowerCase()}
        onChange={(e) => onChange(e.target.value.toUpperCase())}
        style={{ width: 40, height: 32, padding: 2, border: "1px solid var(--ant-color-border)", borderRadius: 6, background: "transparent", cursor: disabled ? "not-allowed" : "pointer" }}
      />
      <code style={{ fontSize: 13 }}>{shown}</code>
    </span>
  );
}
