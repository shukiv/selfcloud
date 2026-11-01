// SPDX-License-Identifier: AGPL-3.0-or-later

import { theme as antdTheme } from "antd";
import { Link } from "react-router-dom";
import { useLang, useT } from "../../lib/i18n";
import { formatBytes, formatDate } from "../../lib/files/format";
import { kindOf } from "../../lib/files/kind";
import { openShortcut } from "../../lib/files/shortcut";
import type { SearchHit } from "../../lib/search/api";
import { snippetParts } from "../../lib/search/snippet";
import Icon from "../files/Icon";

// HitRow is one result: its kind, its name (a link to the item, or to the target of a shortcut the caller reaches), when it
// changed and how big it is, and for a text match the snippet with its matches marked. The snippet is text: each piece is a
// text node, and the marks are elements the page makes, so no markup from the server is ever parsed.
export default function HitRow({ hit }: { hit: SearchHit }) {
  const { token } = antdTheme.useToken();
  const { t } = useT();
  const lang = useLang();
  const opened = hit.kind === "shortcut" ? openShortcut(hit) : null;
  const name = <bdi>{hit.name}</bdi>;
  const meta = [t("search.modifiedOn", { date: formatDate(hit.modified_at, lang) }), hit.kind === "dir" ? "" : formatBytes(hit.size, lang)]
    .filter((x) => x !== "")
    .join(" · ");
  return (
    <li
      className="item"
      style={{
        display: "flex",
        flexWrap: "wrap",
        alignItems: "flex-start",
        gap: "8px 12px",
        minBlockSize: 44,
        padding: "10px 12px",
        background: token.colorBgContainer,
        border: `1px solid ${token.colorBorderSecondary}`,
        borderRadius: token.borderRadius,
      }}
    >
      <span style={{ paddingBlockStart: 2 }}>
        <Icon kind={kindOf(hit)} size={24} />
      </span>
      <div style={{ flex: "1 1 200px", minWidth: 0 }}>
        {/* An unavailable shortcut links to itself, as the listing offers its access request from the shortcut: the target's
            ID is never shown, and the shortcut's own folder is one the caller reaches, since the search found it there. */}
        <Link
          to={opened?.kind === "go" ? opened.to : `/n/${hit.id}`}
          style={{ fontWeight: 600, color: token.colorLink, overflowWrap: "anywhere" }}
        >
          {name}
        </Link>
        <div style={{ fontSize: token.fontSizeSM, color: token.colorTextSecondary }}>{meta}</div>
        {hit.snippet && (
          <p style={{ margin: "6px 0 0", fontSize: token.fontSize, color: token.colorText, overflowWrap: "anywhere" }}>
            <bdi>
              {snippetParts(hit.snippet).map((part, i) =>
                part.hit ? (
                  <mark key={i} style={{ background: token.colorWarningBg, color: token.colorText, borderRadius: 2, fontWeight: 600 }}>
                    {part.text}
                  </mark>
                ) : (
                  <span key={i}>{part.text}</span>
                ),
              )}
            </bdi>
          </p>
        )}
      </div>
    </li>
  );
}
