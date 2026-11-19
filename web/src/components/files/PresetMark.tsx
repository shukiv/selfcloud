// SPDX-License-Identifier: AGPL-3.0-or-later

import { memo } from "react";
import { theme as antdTheme } from "antd";
import { useT } from "../../lib/i18n";
import type { ListSelection } from "../../lib/lists";
import CheckBadge from "./CheckBadge";

export type PresetMarkProps = { id: string; checked: boolean; name: string; pick: ListSelection["pick"] };

// presetMarkEqual is the mark's memo contract (decision 128): the mark is drawn again only when its own row's selection changes.
// The row's icon is drawn outside the mark, so it always follows its item; it is not part of this comparison.
export const presetMarkEqual = (a: PresetMarkProps, b: PresetMarkProps) => a.id === b.id && a.checked === b.checked && a.name === b.name && a.pick === b.pick;

// PresetMark is a preset row's selection mark on a mouse's screen: a transparent button that covers the icon's box (the icon is
// drawn beside it, in the name cell) and holds the badge when the row is selected. A click on it toggles the row, and it opens
// nothing. It is not a tab stop (the row takes focus). It is memoised on its own state, so a row is drawn again only when its own
// selection changes (decision 128).
const PresetMark = memo(function PresetMark({ id, checked, name, pick }: PresetMarkProps) {
  const { t } = useT();
  const { token } = antdTheme.useToken();
  return (
    <button
      type="button"
      className="row-mark"
      tabIndex={-1}
      aria-pressed={checked}
      aria-label={t("files.select.item", { name })}
      style={{ position: "absolute", insetBlock: 0, insetInline: 0 }}
      onClick={(e) => {
        e.stopPropagation();
        pick(id, { toggle: true });
      }}
      onKeyDown={(e) => {
        // Enter and Space are the mark's own activation; the arrow keys reach the row, so they move on from the mark.
        if (e.key === "Enter" || e.key === " ") e.stopPropagation();
      }}
      onDoubleClick={(e) => e.stopPropagation()}
    >
      {checked && <CheckBadge size={17} ring={token.colorPrimaryBg} pos={{ insetBlockEnd: -3, insetInlineEnd: -3 }} />}
    </button>
  );
}, presetMarkEqual);

export default PresetMark;
