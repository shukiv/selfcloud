// SPDX-License-Identifier: AGPL-3.0-or-later

// CodeEditor is the code area of the editor page (decision 115, 3.4, decision 124): CodeMirror with the viewer's look, its
// language pack and its find panel, and the editing keys: undo and redo, and Ctrl or Cmd+S, which asks the page to save and
// takes the key from the browser (the browser's save dialog does not open). Tab leaves the area: no indent key is bound, so a
// keyboard user is not held in it (Ctrl or Cmd+] indents). The page owns the text's saved state; this only reports changes.
//
// It is loaded with the editor page, never with the first load. The viewer (CodeView) shares its look and packs.
import { useEffect, useRef } from "react";
import { Compartment, EditorState, Prec, type Extension } from "@codemirror/state";
import { EditorView, keymap, lineNumbers } from "@codemirror/view";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { syntaxHighlighting } from "@codemirror/language";
import { openSearchPanel, search, searchKeymap } from "@codemirror/search";
import { classHighlighter } from "@lezer/highlight";
import { readNonce } from "../../csp/nonce";
import { useLang } from "../../lib/i18n";
import { loadLanguage } from "../../lib/code/languages";
import type { Pack } from "../../lib/code/names";
import { codeText, phrasesOf } from "../../lib/code/text";
import { translateLint } from "../../lib/code/lint";
import { codeTheme, modeTheme } from "../../lib/code/theme";

// EditorHandle is what the page reads from the editor: its whole text, with LF line breaks, and the find panel.
export interface EditorHandle {
  text: () => string;
  find: () => void;
}

export interface CodeEditorProps {
  // initial is the text the editor opens with, with LF line breaks. The page remounts the editor (a new key) to show other text.
  initial: string;
  pack: Pack;
  // name is the file's name: the code area's accessible name, and what the language pack reads (jsx, tsx).
  name: string;
  dark: boolean;
  // readOnly is set for a viewer, a file that is not UTF-8, and a file the page can no longer save.
  readOnly: boolean;
  wrap: boolean;
  // onChange is told each change to the text; onSave is told Ctrl or Cmd+S.
  onChange: () => void;
  onSave: () => void;
  // lint turns on the JSON lint for a file that is JSON by its name (not jsonc, which allows comments). The lint package loads only
  // then (design 7.2). onProblems is told the count of problems each time the lint runs, 0 when there are none (decision 130).
  lint: boolean;
  onProblems: (n: number) => void;
  // handle receives the editor's handle while it is shown, and null when it goes.
  handle: { current: EditorHandle | null };
}

// readOnlyFacets is the read-only state of an editor: nothing is typed, and the caret still moves.
function readOnlyFacets(readOnly: boolean): Extension {
  return [EditorState.readOnly.of(readOnly), EditorView.editable.of(!readOnly)];
}

export default function CodeEditor({ initial, pack, name, dark, readOnly, wrap, onChange, onSave, lint, onProblems, handle }: CodeEditorProps) {
  const lang = useLang();
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const parts = useRef({
    mode: new Compartment(),
    phrases: new Compartment(),
    language: new Compartment(),
    readOnly: new Compartment(),
    wrap: new Compartment(),
    lint: new Compartment(),
  });
  // The callbacks and the look are read through refs: the editor is made once for its text, and it calls the latest ones.
  const now = useRef({ dark, lang, onChange, onSave, readOnly, wrap, onProblems });
  now.current = { dark, lang, onChange, onSave, readOnly, wrap, onProblems };

  useEffect(() => {
    const parent = host.current;
    if (parent === null) return;
    const c = parts.current;
    const made = new EditorView({
      parent,
      state: EditorState.create({
        doc: initial,
        extensions: [
          EditorView.cspNonce.of(readNonce()),
          EditorView.contentAttributes.of({ "aria-label": name, autocapitalize: "off", autocorrect: "off", spellcheck: "false" }),
          c.readOnly.of(readOnlyFacets(now.current.readOnly)),
          lineNumbers(),
          history(),
          c.wrap.of(now.current.wrap ? EditorView.lineWrapping : []),
          search({ top: true }),
          // Ctrl or Cmd+S saves, whatever the focus inside the editor (the find panel too). The key is taken from the browser.
          Prec.highest(
            keymap.of([
              {
                key: "Mod-s",
                // The find panel's own keys run under its scope, so Ctrl+S typed there saves too.
                scope: "editor search-panel",
                run: () => {
                  now.current.onSave();
                  return true;
                },
                preventDefault: true,
              },
            ]),
          ),
          keymap.of([...searchKeymap, ...historyKeymap, ...defaultKeymap]),
          syntaxHighlighting(classHighlighter),
          codeTheme,
          c.mode.of(modeTheme(now.current.dark)),
          c.phrases.of(EditorState.phrases.of(phrasesOf(codeText(now.current.lang)))),
          c.language.of([]),
          c.lint.of([]),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) now.current.onChange();
          }),
        ],
      }),
    });
    view.current = made;
    handle.current = {
      text: () => made.state.doc.toString(),
      find: () => openSearchPanel(made),
    };
    made.focus();
    let live = true;
    loadLanguage(pack, name)
      .then((ext) => {
        if (live && ext !== null) made.dispatch({ effects: c.language.reconfigure(ext) });
      })
      // A pack that cannot load (the connection dropped) leaves the text without colours.
      .catch(() => undefined);
    return () => {
      live = false;
      handle.current = null;
      view.current = null;
      made.destroy();
    };
  }, [initial, pack, name]);

  // The JSON lint: its package and the JSON parser load only for a JSON file, once the editor exists (design 7.2). The count is
  // reported each time the lint runs. A failed load leaves the text without markers, and never blocks a save.
  useEffect(() => {
    const made = view.current;
    if (!lint || made === null) return;
    let live = true;
    Promise.all([import("@codemirror/lint"), import("@codemirror/lang-json")])
      .then(([l, j]) => {
        if (!live || view.current !== made) return;
        const parse = j.jsonParseLinter();
        made.dispatch({
          effects: parts.current.lint.reconfigure([
            l.lintGutter(),
            l.linter(
              (v) => {
                const found = translateLint(parse(v), codeText(now.current.lang).lintInvalid);
                now.current.onProblems(found.length);
                return found;
              },
              { delay: 300 },
            ),
          ]),
        });
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [lint, initial, pack, name]);

  useEffect(() => {
    view.current?.dispatch({ effects: parts.current.mode.reconfigure(modeTheme(dark)) });
  }, [dark]);

  useEffect(() => {
    view.current?.dispatch({ effects: parts.current.readOnly.reconfigure(readOnlyFacets(readOnly)) });
  }, [readOnly]);

  useEffect(() => {
    view.current?.dispatch({ effects: parts.current.wrap.reconfigure(wrap ? EditorView.lineWrapping : []) });
  }, [wrap]);

  useEffect(() => {
    view.current?.dispatch({ effects: parts.current.phrases.reconfigure(EditorState.phrases.of(phrasesOf(codeText(lang)))) });
  }, [lang]);

  return <div ref={host} dir="ltr" className="code-editor" data-testid="code-editor" style={{ flex: "1 1 auto", minHeight: 0, minWidth: 0, width: "100%" }} />;
}
