// SPDX-License-Identifier: AGPL-3.0-or-later

// The search entry of the top bar. It is the only part of search the shell loads: the search page itself is a lazy route.
// On a wide window it is the box; typing opens the search page with the words in its address, after a short pause. On a
// phone it is one button that opens the search page, which has its own box there.
import { Button, theme as antdTheme } from "antd";
import { Search } from "lucide-react";
import { useEffect, useState } from "react";
import { useLocation, useNavigate, useNavigationType, useSearchParams } from "react-router-dom";
import { useT } from "../../lib/i18n";
import { useCoarsePointer } from "../../lib/pointer";
import { RADIUS } from "../../theme/tokens";
import { useDraft } from "./useDraft";

export const SEARCH_PATH = "/search";

export default function ShellSearch({ wide }: { wide: boolean }) {
  const { t } = useT();
  const { token } = antdTheme.useToken();
  const coarse = useCoarsePointer();
  const navigate = useNavigate();
  const location = useLocation();
  const [params, setParams] = useSearchParams();
  const [focused, setFocused] = useState(false);
  const onSearch = location.pathname === SEARCH_PATH;
  // On the search page the box shows the address's words; elsewhere it starts empty, and a word sends the person there.
  const urlWords = onSearch ? (params.get("q") ?? "") : "";

  const send = (value: string) => {
    const q = value.trim();
    if (onSearch) {
      setParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          if (q === "") next.delete("q");
          else next.set("q", value);
          return next;
        },
        { replace: true },
      );
      return;
    }
    if (q === "") return;
    navigate(`${SEARCH_PATH}?${new URLSearchParams({ q: value }).toString()}`);
  };
  // A move the person makes (a link, back or forward) drops a pause in progress. A page's own replace, such as /files
  // moving on to the first folder once the folders are read, is not the person's move, so it keeps what they typed.
  const navType = useNavigationType();
  const [scope, setScope] = useState(location.pathname);
  useEffect(() => {
    if (navType !== "REPLACE") setScope(location.pathname);
  }, [location.pathname, navType]);
  const { draft, change, submit } = useDraft(urlWords, send, 300, scope);

  if (!wide) {
    // On the search page the phone has the page's own box, so the button is not needed there.
    if (onSearch) return null;
    return (
      <Button
        type="text"
        aria-label={t("shell.search.open")}
        title={t("shell.search.open")}
        onClick={() => navigate(SEARCH_PATH, { state: { focusSearch: true } })}
        icon={<Search size={18} strokeWidth={1.75} aria-hidden="true" />}
        style={{ flex: "none", ...(coarse ? { minWidth: 44, minHeight: 44 } : {}) }}
      />
    );
  }

  return (
    <label
      style={{
        flex: "1 1 200px",
        minWidth: 0,
        maxWidth: 520,
        display: "flex",
        alignItems: "center",
        gap: 8,
        height: coarse ? 44 : 36,
        padding: "0 11px",
        boxSizing: "border-box",
        border: `1px solid ${focused ? token.colorPrimary : token.colorBorder}`,
        borderRadius: RADIUS,
        background: token.colorBgContainer,
        color: token.colorTextTertiary,
      }}
    >
      <Search size={16} strokeWidth={1.75} aria-hidden="true" style={{ flex: "none" }} />
      <input
        type="search"
        enterKeyHint="search"
        aria-label={t("shell.search.placeholder")}
        placeholder={t("shell.search.placeholder")}
        value={draft}
        onChange={(e) => change(e.target.value)}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
        onKeyDown={(e) => {
          if (e.key === "Enter") submit();
        }}
        style={{
          flex: 1,
          minWidth: 0,
          border: 0,
          background: "transparent",
          color: token.colorText,
          font: "inherit",
          outline: "none",
        }}
      />
    </label>
  );
}
