// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useId, useMemo, useReducer, useRef, type KeyboardEvent } from "react";
import { Input } from "antd";
import { listPeople, type Person } from "../../lib/comments/api";
import { useCT } from "../../lib/comments/i18n";
import { commentFault, uniquePeople } from "../../lib/comments/logic";
import { useT } from "../../lib/i18n";
import { COMBO_START, MIN_QUERY, SEARCH_DELAY, comboReducer, debounce } from "../../lib/sharing/combobox";

// MentionPicker finds a person who reaches the item, by name, and gives the one chosen. It is the people combobox
// of the sharing dialog with the rules of a comment: the answer shows names only (never an e-mail address or an ID),
// and the choice is the reference the server gave. A search that the server limits says so in words.
// The state is the shared combobox reducer, so an answer to an old query is dropped.
export default function MentionPicker({ nodeID, onPick }: { nodeID: string; onPick: (p: Person) => void }) {
  const { t: ct } = useCT();
  const { t: common } = useT();
  const id = useId();
  const [state, dispatch] = useReducer(comboReducer, COMBO_START);
  const stateRef = useRef(state);
  stateRef.current = state;
  const note = useRef<string | null>(null);
  // latest is the query the person has typed last. An answer or a refusal for any other query is an old one.
  const latest = useRef("");
  const [, redraw] = useReducer((n: number) => n + 1, 0);
  const listId = `${id}-list`;
  const optionId = (i: number) => `${id}-opt-${i}`;

  const search = useMemo(
    () =>
      debounce((q: string) => {
        void listPeople(nodeID, q.trim())
          .then((res) => {
            if (q === latest.current) note.current = null;
            dispatch({
              type: "results",
              query: q,
              items: uniquePeople(res.items).map((p) => ({ id: p.ref, kind: "user" as const, name: p.name })),
            });
          })
          .catch((err: unknown) => {
            // A refusal for an old query must not put its note on the newer one.
            if (q === latest.current) note.current = commentFault(err) === "rate" ? ct("comments.peopleRateLimited") : null;
            dispatch({ type: "fail", query: q });
            redraw();
          });
      }, SEARCH_DELAY),
    [nodeID, ct],
  );

  // A new item means a new people list: an answer for the old one is not shown, and the pending search is dropped.
  useEffect(() => {
    search.cancel();
    dispatch({ type: "clear" });
    return () => search.cancel();
  }, [nodeID, search]);

  const onInput = (value: string) => {
    latest.current = value;
    dispatch({ type: "type", query: value });
    note.current = null;
    if (value.trim().length >= MIN_QUERY) search.call(value);
    else search.cancel();
  };

  const choose = (index: number) => {
    const entry = stateRef.current.items[index];
    if (!entry) return;
    onPick({ ref: entry.id, name: entry.name });
    // The box is cleared for the next name; the text of the comment keeps the mention.
    latest.current = "";
    search.cancel();
    dispatch({ type: "clear" });
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
        // Escape closes the list, and stops there while the box holds a name: the drawer must not close and lose the draft.
        if (state.open || state.query !== "") {
          e.preventDefault();
          e.stopPropagation();
          dispatch({ type: "close" });
        }
        break;
    }
  };

  const showList = state.open && state.items.length > 0;
  const status =
    note.current ??
    (state.status === "loading"
      ? common("share.combo.searching")
      : state.status === "error"
        ? common("share.combo.error")
        : state.status === "ready" && state.items.length === 0 && state.query.trim().length >= MIN_QUERY
          ? common("share.combo.none")
          : "");

  return (
    <div style={{ position: "relative", display: "grid", gap: 4 }}>
      <label htmlFor={id} style={{ fontSize: 13 }}>
        {ct("comments.mentionLabel")}
      </label>
      <Input
        id={id}
        role="combobox"
        autoComplete="off"
        aria-autocomplete="list"
        aria-expanded={showList}
        aria-controls={showList ? listId : undefined}
        aria-describedby={`${id}-hint`}
        aria-activedescendant={showList && state.active >= 0 ? optionId(state.active) : undefined}
        value={state.query}
        onChange={(e) => onInput(e.target.value)}
        onKeyDown={onKey}
        onBlur={() => dispatch({ type: "close" })}
      />
      <p className="hint" id={`${id}-hint`} style={{ margin: 0, fontSize: 13 }}>
        {ct("comments.mentionHint")}
      </p>
      <p role="status" aria-live="polite" style={{ margin: 0, fontSize: 13 }}>
        {status}
      </p>
      {showList && (
        <ul
          id={listId}
          role="listbox"
          aria-label={ct("comments.mentionLabel")}
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
              key={item.id}
              id={optionId(i)}
              role="option"
              aria-selected={i === state.active}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => choose(i)}
              style={{
                minHeight: 44,
                display: "flex",
                alignItems: "center",
                padding: "8px 12px",
                cursor: "pointer",
                background: i === state.active ? "var(--ant-control-item-bg-hover)" : undefined,
              }}
            >
              <bdi>{item.name}</bdi>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
