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
//
// Decision 96: focusing the field, or clicking it, while it holds fewer than two characters opens the account's list
// (the answer to an empty q, asked once per session). From two characters the search runs as before.
export interface PeopleComboboxProps {
  id: string;
  label: string;
  hint: string;
  onpick: (entry: DirectoryEntry | null) => void;
  invalid?: boolean;
  errorId?: string;
  kind?: "all" | "user" | "group";
}

const isShort = (q: string) => q.trim().length < MIN_QUERY;

export default function PeopleCombobox({ id, label, hint, onpick, invalid = false, errorId, kind = "all" }: PeopleComboboxProps) {
  const { t, tp } = useT();
  const [state, dispatch] = useReducer(comboReducer, COMBO_START);
  const stateRef = useRef(state);
  stateRef.current = state;
  const listId = `${id}-list`;
  const moreId = `${id}-more`;
  const optionId = (i: number) => `${id}-opt-${i}`;
  // A search answer that arrives after a sign-out is dropped: it belongs to the session that asked.
  const sess = useStore(session);
  // gen counts the sessions. An answer from an earlier one is dropped, the focus list included.
  const gen = useRef(0);
  // listAsked is true while the account's list is on its way, so a second focus does not ask again.
  const listAsked = useRef(false);

  const search = useMemo(
    () =>
      debounce((q: string) => {
        void searchDirectory(q.trim(), kind)
          .then((res) => dispatch({ type: "results", query: q, items: res.items, more: res.more === true }))
          .catch(() => dispatch({ type: "fail", query: q }));
      }, SEARCH_DELAY),
    [kind],
  );

  useEffect(() => () => search.cancel(), [search]);
  // Results from a session that has ended are not shown, and the cached list is dropped with them.
  useEffect(() => {
    search.cancel();
    gen.current += 1;
    listAsked.current = false;
    dispatch({ type: "clear" });
  }, [sess, search]);

  // loadList asks for the account's list once per session. It is not asked again while the answer is on its way or
  // after it arrived; a failed ask may be tried again. The ref is set at once, so a focus and a click in one gesture
  // ask once.
  const loadList = () => {
    if (listAsked.current) return;
    listAsked.current = true;
    const mine = gen.current;
    void searchDirectory("", kind)
      .then((res) => {
        if (mine !== gen.current) return;
        dispatch({ type: "listed", items: res.items, more: res.more === true });
      })
      .catch(() => {
        if (mine !== gen.current) return;
        listAsked.current = false;
        dispatch({ type: "listfail" });
      });
  };

  // browse runs when the field is focused or clicked: under two characters it opens the account's list.
  const browse = () => {
    dispatch({ type: "focus" });
    if (isShort(stateRef.current.query)) loadList();
  };

  const onInput = (value: string) => {
    const hadChoice = stateRef.current.chosen !== null;
    dispatch({ type: "type", query: value });
    if (hadChoice) onpick(null);
    if (!isShort(value)) {
      search.call(value);
    } else {
      search.cancel();
      loadList();
    }
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
      case "ArrowUp": {
        e.preventDefault();
        // On a closed field under two characters the arrow opens the list and leaves the active option where it is.
        if (isShort(state.query) && !state.open) {
          browse();
          break;
        }
        dispatch({ type: "open" });
        dispatch({ type: "move", delta: e.key === "ArrowDown" ? 1 : -1 });
        break;
      }
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
          : showList
            ? tp("share.combo.count", state.items.length)
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
        aria-describedby={[`${id}-hint`, errorId, showList && state.more ? moreId : undefined].filter(Boolean).join(" ")}
        aria-activedescendant={showList && state.active >= 0 ? optionId(state.active) : undefined}
        aria-invalid={invalid ? true : undefined}
        status={invalid ? "error" : undefined}
        value={state.query}
        onChange={(e) => onInput(e.target.value)}
        onKeyDown={onKey}
        onFocus={browse}
        onClick={browse}
        onBlur={() => dispatch({ type: "close" })}
      />
      <p className="hint" id={`${id}-hint`} style={{ margin: 0, fontSize: 13, color: "var(--ant-color-text-secondary)" }}>
        {hint}
      </p>
      <p role="status" aria-live="polite" style={{ margin: 0, fontSize: 13 }}>
        {statusText}
      </p>
      {showList && (
        // The popup holds the listbox and, under it, a line of text that is not an option, so the arrow keys skip it.
        <div
          style={{
            position: "absolute",
            insetInline: 0,
            insetBlockStart: "100%",
            zIndex: 5,
            padding: "4px 0",
            background: "var(--ant-color-bg-container)",
            border: "1px solid var(--ant-color-border)",
            borderRadius: 4,
          }}
        >
          <ul
            id={listId}
            role="listbox"
            aria-label={label}
            style={{ margin: 0, padding: 0, maxHeight: 240, overflow: "auto", listStyle: "none" }}
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
          {state.more && (
            <p id={moreId} style={{ margin: 0, padding: "8px 12px", fontSize: 13, color: "var(--ant-color-text-secondary)" }}>
              {t("share.combo.more")}
            </p>
          )}
        </div>
      )}
    </div>
  );
}
