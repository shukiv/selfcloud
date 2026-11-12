// SPDX-License-Identifier: AGPL-3.0-or-later

import { ColorPicker } from "antd";

interface Props {
  // value is the colour set, or an empty text when the colour follows the accent; fallback is the colour then drawn.
  value: string;
  fallback: string;
  label: string;
  disabled: boolean;
  onChange: (hex: string) => void;
}

// ColourInput is antd's colour picker for one brand colour. It is its own file so the picker, which is large, loads only when a
// colour is drawn. A pick is sent as #RRGGBB in capitals, the form the server writes. The picker has no alpha.
export default function ColourInput({ value, fallback, label, disabled, onChange }: Props) {
  return (
    <span role="group" aria-label={label} style={{ display: "inline-flex" }}>
      <ColorPicker value={value !== "" ? value : fallback} disabled={disabled} disabledAlpha showText onChangeComplete={(c) => onChange(c.toHexString().toUpperCase())} />
    </span>
  );
}
