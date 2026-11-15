// SPDX-License-Identifier: AGPL-3.0-or-later

import { Dropdown } from "antd";
import { Monitor, Moon, Sun } from "lucide-react";
import IconButton from "./IconButton";
import { useT } from "../lib/i18n";
import { useTheme, type Pref } from "../theme/theme";
import { useCoarsePointer } from "../lib/pointer";

const CHOICES: Pref[] = ["system", "light", "dark"];
const GLYPH = { system: Monitor, light: Sun, dark: Moon } as const;

// ThemeMenu is the top bar's theme control (decision 68): one icon that shows the current choice, and a menu of the three
// with their icons and words. The choice is saved in this browser, on the sign-in pages too (AuthLayout).
export default function ThemeMenu() {
  const { t } = useT();
  const { pref, setPref } = useTheme();
  const coarse = useCoarsePointer();
  // A menu item is a finger's size on a coarse pointer (mob3): 44 px tall, its padding taken out of the height.
  const touchItem = coarse ? { height: 44, paddingBlock: 0, lineHeight: "44px" } : undefined;
  const Current = GLYPH[pref];
  const items = CHOICES.map((c) => {
    const Glyph = GLYPH[c];
    return { key: c, style: touchItem, label: <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}><Glyph size={16} aria-hidden="true" focusable="false" />{t(`theme.${c}`)}</span> };
  });
  return (
    <Dropdown
      trigger={["click"]}
      placement="bottomRight"
      menu={{ items, selectedKeys: [pref], onClick: ({ key }) => setPref(key as Pref) }}
    >
      <IconButton data-testid="theme" label={t("theme.current", { choice: t(`theme.${pref}`) })} icon={<Current size={18} strokeWidth={1.75} aria-hidden="true" focusable="false" />} />
    </Dropdown>
  );
}
