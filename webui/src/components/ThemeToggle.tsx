// SPDX-License-Identifier: AGPL-3.0-or-later

import { Segmented } from "antd";
import { useT } from "../lib/i18n";
import { useTheme, type Pref } from "../theme/theme";

const CHOICES: Pref[] = ["system", "light", "dark"];

// The theme choice: the device's, or light, or dark. Choosing one saves it in this browser.
export default function ThemeToggle() {
  const { t } = useT();
  const { pref, setPref } = useTheme();
  return (
    <Segmented
      data-testid="theme"
      aria-label={t("theme.label")}
      value={pref}
      onChange={(value) => setPref(value as Pref)}
      options={CHOICES.map((c) => ({ value: c, label: t(`theme.${c}`) }))}
    />
  );
}
