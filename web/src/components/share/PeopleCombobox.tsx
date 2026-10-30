// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useMemo, useReducer, useRef, type KeyboardEvent } from "react";
import { Input } from "antd";
import { useT } from "../../lib/i18n";
import { searchDirectory } from "../../lib/sharing/api";
import { COMBO_START, MIN_QUERY, SEARCH_DELAY, comboReducer, debounce } from "../../lib/sharing/combobox";
import type { DirectoryEntry } from "../../lib/files/types";
import { useStore } from "../../lib/store";
import { session } from "../../lib/session";

// PeopleCombobox finds a person or a group by name or email. It follows the ARIA combobox pattern: the input is the
// combobox, the list is its popup, and aria-activedescendant follows the arrow keys. onpick gets the entry when one is
// chosen, and null when the person types again after a choice. Escape closes the list only; it never reaches the
// dialog around it. The search is kept as a plain combobox, not an antd Select, because the keyboard rules and the
// id of the input are the ones the interface has always had (see docs/design.md, section 10).
export interface PeopleComboboxProps {
  id: string;
  label: string;
  hint: string;
  onpick: (entry: DirectoryEntry | null) => void;
  invalid?: boolean;
  errorId?: string;
  kind?: "all" | "user" | "group";
}

export default function PeopleCombobox({ id, label, hint, onpick, invalid = false, errorId, kind = "all" }: PeopleComboboxProps) {
  const { t } = useT();
  const [state, dispatch] = useReducer(comboReducer, COMBO_START);
  const stateRef = useRef(state);
  stateRef.current = state;
  const listId = `${id}-list`;
  const optionId = (i: number) => `${id}-opt-${i}`;
  // A search answer that arrives after a sign-out is dropped: it belongs to the session that asked.
  const sess = useStore(session);

  const search = useMemo(
    () =>
      debounce((q: string) => {
        void searchDirectory(q.trim(), kind)
          .then((res) => dispatch({ type: "results", query: q, items: res.items }))
          .catch(() => dispatch({ type: "fail", query: q }));
      }, SEARCH_DELAY),
    [kind],
  );

  useEffect(() => () => search.cancel(), [search]);
  // Results from a session that has ended are not shown.
  useEffect(() => {
    search.cancel();
    dispatch({ type: "clear" });
  }, [sess, search]);

  const onInput = (value: string) => {
    const hadChoice = stateRef.current.chosen !== null;
    dispatch({ type: "type", query: value });
    if (hadChoice) onpick(null);
    if (value.trim().length >= MIN_QUERY) search.call(value);
    else search.cancel();
  };

  const choose = (index: number) => {
    const entry = stateRef.current.items[index];
    if (!entry) return;
    dispatch({ type: "pick", entry });
    onpick(entry);
  };

  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        dispatch({ type: "open" });
        dispatch({ type: "move", delta: 1 });
        break;
      case "ArrowUp":
        e.preventDefault();
        dispatch({ type: "open" });
        dispatch({ type: "move", delta: -1 });
        break;
      case "Enter":
        if (state.open && state.active >= 0) {
          e.preventDefault();
          choose(state.active);
        }
        break;
      case "Escape":
        if (state.open) {
          e.preventDefault();
          e.stopPropagation();
          dispatch({ type: "close" });
        }
        break;
    }
  };

  const showList = state.open && state.items.length > 0;
  const statusText =
    state.status === "loading"
      ? t("share.combo.searching")
      : state.status === "error"
        ? t("share.combo.error")
        : state.status === "ready" && state.items.length === 0 && state.query.trim().length >= MIN_QUERY
          ? t("share.combo.none")
          : "";

  return (
    <div className="combo" style={{ position: "relative", display: "grid", gap: 8 }}>
      <label htmlFor={id} style={{ fontWeight: 600 }}>
        {label}
      </label>
      <Input
        id={id}
        role="combobox"
        autoComplete="off"
        aria-autocomplete="list"
        aria-expanded={showList}
        aria-controls={listId}
        aria-activedescendant={showList && state.active >= 0 ? optionId(state.active) : undefined}
        aria-describedby={errorId ? `${id}-hint ${errorId}` : `${id}-hint`}
        aria-invalid={invalid ? true : undefined}
        status={invalid ? "error" : undefined}
        value={state.query}
        onChange={(e) => onInput(e.target.value)}
        onKeyDown={onKey}
        onBlur={() => dispatch({ type: "close" })}
      />
      <p className="hint" id={`${id}-hint`} style={{ margin: 0, fontSize: 13, color: "var(--ant-color-text-secondary)" }}>
        {hint}
      </p>
      <p role="status" aria-live="polite" style={{ margin: 0, fontSize: 13 }}>
        {statusText}
      </p>
      {showList && (
        <ul
          id={listId}
          role="listbox"
          aria-label={label}
          style={{
            position: "absolute",
            insetInline: 0,
            insetBlockStart: "100%",
            zIndex: 5,
            margin: 0,
            padding: "4px 0",
            maxHeight: 240,
            overflow: "auto",
            listStyle: "none",
            background: "var(--ant-color-bg-container)",
            border: "1px solid var(--ant-color-border)",
            borderRadius: 4,
          }}
        >
          {state.items.map((item, i) => (
            <li
              key={item.kind + item.id}
              id={optionId(i)}
              role="option"
              aria-selected={i === state.active}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => choose(i)}
              style={{
                display: "grid",
                gap: 2,
                minHeight: 44,
                padding: "8px 12px",
                cursor: "pointer",
                background: i === state.active ? "var(--ant-control-item-bg-hover)" : undefined,
              }}
            >
              <span style={{ fontWeight: 600 }}>
                <bdi>{item.name}</bdi>
              </span>
              <span style={{ fontSize: 13, color: "var(--ant-color-text-secondary)" }}>
                {item.kind === "group" ? t("share.kind.group") : <bdi>{item.email ?? ""}</bdi>}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
