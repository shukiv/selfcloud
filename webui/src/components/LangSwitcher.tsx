// SPDX-License-Identifier: AGPL-3.0-or-later

import { Button, Dropdown } from "antd";
import type { MenuProps } from "antd";
import { Languages } from "lucide-react";
import { LANGUAGES, NATIVE_NAMES, setLang, useLang, useT, type Lang } from "../lib/i18n";
import { PREF_LANG, writePref } from "../lib/prefs";
import { get } from "../lib/store";
import { session } from "../lib/session";
import { setLocale } from "../lib/auth";

// change applies the language at once and saves it: to the account when signed in, so every device
// gets it; otherwise to this browser.
export async function change(next: Lang) {
  await setLang(next);
  writePref(PREF_LANG, next);
  if (get(session)) {
    try {
      await setLocale(next);
    } catch {
      // Kept for this page; the account keeps its previous language.
    }
  }
}

export default function LangSwitcher() {
  const { t } = useT();
  const current = useLang();
  const items: MenuProps["items"] = LANGUAGES.map((code) => ({
    key: code,
    label: <span lang={code}>{NATIVE_NAMES[code]}</span>,
  }));
  return (
    <Dropdown
      menu={{ items, selectedKeys: [current], onClick: ({ key }) => void change(key as Lang) }}
      placement="bottomRight"
      trigger={["click"]}
    >
      <Button type="text" data-testid="language" icon={<Languages size={18} strokeWidth={1.75} />} aria-label={t("lang.label")}>
        {NATIVE_NAMES[current]}
      </Button>
    </Dropdown>
  );
}
