// SPDX-License-Identifier: AGPL-3.0-or-later

// CodeView shows a text file read-only in CodeMirror (decision 115): colours from the file's language pack, line numbers and
// the find panel. It is loaded on the first text file a viewer shows, never with the page (Preview loads it lazily), and the
// language pack loads after it, so the text shows at once and the colours follow.
//
// - CSP: CodeMirror writes its style sheets with the page's nonce (EditorView.cspNonce). It sets an element's style through
//   the style object, never as an attribute, which the policy allows.
// - The code area is left to right in every language, so in Hebrew and Arabic the gutter stays on the left.
// - Lines wrap, unless one is longer than LONG_LINE characters: then the code area scrolls sideways, not the page.
// - Dark mode: modeTheme sits in a compartment and is swapped when the theme changes, with no reload of the text.
// - Keys: Escape closes the find panel first and goes no further; with no panel open it reaches the viewer, which closes.
//   Tab leaves the code area (no indent key is bound). A phone shows no keyboard for it (inputmode none).
// - An empty file shows antd's Empty, not an empty editor.
import { useEffect, useRef } from "react";
import { Empty } from "antd";
import { Compartment, EditorState, Prec } from "@codemirror/state";
import { EditorView, keymap, lineNumbers } from "@codemirror/view";
import { defaultKeymap } from "@codemirror/commands";
import { syntaxHighlighting } from "@codemirror/language";
import { closeSearchPanel, openSearchPanel, search, searchKeymap } from "@codemirror/search";
import { classHighlighter } from "@lezer/highlight";
import { readNonce } from "../../csp/nonce";
import { useLang } from "../../lib/i18n";
import { loadLanguage } from "../../lib/code/languages";
import { wraps } from "../../lib/code/lines";
import type { Pack } from "../../lib/code/names";
import { codeText, phrasesOf } from "../../lib/code/text";
import { codeTheme, modeTheme } from "../../lib/code/theme";

// FindAction is what the viewer's header needs for its Find button: the action and its label, in the page's language.
export interface FindAction {
  run: () => void;
  label: string;
}

export interface CodeViewProps {
  text: string;
  pack: Pack;
  // name is the file's name: the code area's accessible name, and what the language pack reads (jsx, tsx).
  name: string;
  dark: boolean;
  // onFind is told the find action once the text is shown, and null when it goes.
  onFind?: (find: FindAction | null) => void;
}

export default function CodeView({ text, pack, name, dark, onFind }: CodeViewProps) {
  const lang = useLang();
  const t = codeText(lang);
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const parts = useRef({ mode: new Compartment(), phrases: new Compartment(), language: new Compartment() });
  // The values the editor is made with; later changes are sent to it through the compartments below.
  const now = useRef({ dark, lang, onFind });
  now.current = { dark, lang, onFind };

  useEffect(() => {
    const parent = host.current;
    if (parent === null || text === "") return;
    const c = parts.current;
    const made = new EditorView({
      parent,
      state: EditorState.create({
        doc: text,
        extensions: [
          EditorView.cspNonce.of(readNonce()),
          EditorState.readOnly.of(true),
          EditorView.contentAttributes.of({ "aria-label": name, inputmode: "none" }),
          lineNumbers(),
          wraps(text) ? EditorView.lineWrapping : [],
          search({ top: true }),
          Prec.highest(keymap.of([{ key: "Escape", run: closeSearchPanel, scope: "editor search-panel", stopPropagation: true }])),
          keymap.of([...searchKeymap, ...defaultKeymap]),
          syntaxHighlighting(classHighlighter),
          codeTheme,
          c.mode.of(modeTheme(now.current.dark)),
          c.phrases.of(EditorState.phrases.of(phrasesOf(codeText(now.current.lang)))),
          c.language.of([]),
        ],
      }),
    });
    view.current = made;
    now.current.onFind?.({ run: () => openSearchPanel(made), label: codeText(now.current.lang).find });
    let live = true;
    loadLanguage(pack, name)
      .then((ext) => {
        if (live && ext !== null) made.dispatch({ effects: c.language.reconfigure(ext) });
      })
      // A pack that cannot load (the connection dropped) leaves the text without colours.
      .catch(() => undefined);
    return () => {
      live = false;
      now.current.onFind?.(null);
      made.destroy();
      view.current = null;
    };
  }, [text, pack, name]);

  useEffect(() => {
    view.current?.dispatch({ effects: parts.current.mode.reconfigure(modeTheme(dark)) });
  }, [dark]);

  useEffect(() => {
    const v = view.current;
    if (v === null) return;
    v.dispatch({ effects: parts.current.phrases.reconfigure(EditorState.phrases.of(phrasesOf(codeText(lang)))) });
    now.current.onFind?.({ run: () => openSearchPanel(v), label: codeText(lang).find });
  }, [lang]);

  if (text === "") return <Empty description={t.empty} />;
  return <div ref={host} dir="ltr" className="code-view" data-testid="code-view" style={{ flex: "1 1 auto", minHeight: 0, minWidth: 0, width: "100%" }} />;
}
