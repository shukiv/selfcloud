// SPDX-License-Identifier: AGPL-3.0-or-later

import { memo, type ReactNode } from "react";
import { theme as antdTheme } from "antd";
import { useT } from "../../lib/i18n";
import type { ListSelection } from "../../lib/lists";
import CheckBadge from "./CheckBadge";

// PresetMark is a preset row's selection mark on a mouse's screen: the row's icon (where the page gives one) wrapped in a button.
// A click on it toggles the row, and the badge shows on the icon when the row is selected. It is not a tab stop (the row takes
// focus). It is memoised on its own state, so a row is drawn again only when its own selection changes (decision 128); the icon is
// drawn through renderIcon inside that memo, so it is part of the same pass.
const PresetMark = memo(function PresetMark({ id, checked, name, renderIcon, pick }: { id: string; checked: boolean; name: string; renderIcon?: () => ReactNode; pick: ListSelection["pick"] }) {
  const { t } = useT();
  const { token } = antdTheme.useToken();
  return (
    <button
      type="button"
      className="row-mark"
      tabIndex={-1}
      aria-pressed={checked}
      aria-label={t("files.select.item", { name })}
      style={{ minWidth: 22, height: 22 }}
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
      {renderIcon && <span className="row-icon">{renderIcon()}</span>}
      {checked && <CheckBadge size={17} ring={token.colorPrimaryBg} pos={{ insetBlockEnd: -3, insetInlineEnd: -3 }} />}
    </button>
  );
}, (a, b) => a.id === b.id && a.checked === b.checked && a.name === b.name && a.pick === b.pick);

export default PresetMark;
