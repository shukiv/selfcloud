// SPDX-License-Identifier: AGPL-3.0-or-later

// The code viewer's look (decision 115). The editor's ground, text, gutter and find panel take the app's antd colours, as
// CSS variables (theme/antdVars.ts), so they follow the theme by themselves. The token colours have one set for light and one
// for dark, chosen by CodeMirror's dark flag: modeTheme(dark) is held in a compartment and swapped when the theme changes,
// with no reload. Every token colour keeps 4.5:1 on its card (theme.test.ts). Only logical properties are used: the code area
// is left to right in every language, and check-source refuses a physical left or right.
import { EditorView } from "@codemirror/view";
import type { Extension } from "@codemirror/state";
import { RADIUS } from "../../theme/tokens";

// Role is a colour role of the highlighter's classes (classHighlighter's tok-* names).
export type Role =
  | "keyword"
  | "constant"
  | "number"
  | "string"
  | "string2"
  | "comment"
  | "meta"
  | "type"
  | "property"
  | "special"
  | "definition"
  | "invalid"
  | "link"
  | "heading"
  | "inserted"
  | "deleted";

// The colours of each role, on the light card (#FFFFFF) and the dark card (#0D2142).
export const CODE_COLOURS: Readonly<Record<"light" | "dark", Readonly<Record<Role, string>>>> = {
  light: {
    keyword: "#CF222E",
    constant: "#0550AE",
    number: "#0550AE",
    string: "#0A3069",
    string2: "#116329",
    comment: "#57606A",
    meta: "#6639BA",
    type: "#953800",
    property: "#0550AE",
    special: "#8250DF",
    definition: "#8250DF",
    invalid: "#82071E",
    link: "#0550AE",
    heading: "#0550AE",
    inserted: "#116329",
    deleted: "#82071E",
  },
  dark: {
    keyword: "#FF7B72",
    constant: "#79C0FF",
    number: "#79C0FF",
    string: "#A5D6FF",
    string2: "#7EE787",
    comment: "#8B949E",
    meta: "#D2A8FF",
    type: "#FFA657",
    property: "#79C0FF",
    special: "#D2A8FF",
    definition: "#D2A8FF",
    invalid: "#FFA198",
    link: "#79C0FF",
    heading: "#79C0FF",
    inserted: "#7EE787",
    deleted: "#FFA198",
  },
};

// The highlighter's classes and the role each takes. A later class wins over an earlier one on the same span (a definition
// over its variable name).
const CLASS_ROLE: ReadonlyArray<[string, Role]> = [
  ["tok-keyword", "keyword"],
  ["tok-atom", "constant"],
  ["tok-bool", "constant"],
  ["tok-literal", "constant"],
  ["tok-number", "number"],
  ["tok-string", "string"],
  ["tok-string2", "string2"],
  ["tok-comment", "comment"],
  ["tok-meta", "meta"],
  ["tok-typeName", "type"],
  ["tok-className", "type"],
  ["tok-namespace", "type"],
  ["tok-propertyName", "property"],
  ["tok-variableName2", "special"],
  ["tok-macroName", "special"],
  ["tok-labelName", "special"],
  ["tok-definition", "definition"],
  ["tok-invalid", "invalid"],
  ["tok-link", "link"],
  ["tok-url", "link"],
  ["tok-heading", "heading"],
  ["tok-inserted", "inserted"],
  ["tok-deleted", "deleted"],
];

const MONO = 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace';

function tokenRules(): Record<string, Record<string, string>> {
  const out: Record<string, Record<string, string>> = {};
  for (const mode of ["light", "dark"] as const) {
    for (const [cls, role] of CLASS_ROLE) out[`&${mode} .${cls}`] = { color: CODE_COLOURS[mode][role] };
  }
  return out;
}

// codeTheme is the viewer's look in both modes.
export const codeTheme: Extension = [
  EditorView.baseTheme({
    ...tokenRules(),
    ".tok-heading, .tok-strong": { fontWeight: "bold" },
    ".tok-emphasis": { fontStyle: "italic" },
    ".tok-link": { textDecoration: "underline" },
    ".tok-strikethrough": { textDecoration: "line-through" },
  }),
  EditorView.theme({
    "&": {
      height: "100%",
      fontSize: "14px",
      color: "var(--ant-color-text)",
      backgroundColor: "var(--ant-color-bg-container)",
      border: "1px solid var(--ant-color-border-secondary)",
      borderRadius: `${RADIUS}px`,
    },
    "&.cm-focused": { outline: "2px solid var(--ant-color-primary)", outlineOffset: "-1px" },
    ".cm-scroller": { fontFamily: MONO, lineHeight: "1.5" },
    ".cm-gutters": {
      backgroundColor: "var(--ant-color-bg-container)",
      color: "var(--ant-color-text-secondary)",
      borderInlineEnd: "1px solid var(--ant-color-border-secondary)",
    },
    ".cm-panels": { backgroundColor: "var(--ant-color-bg-container)", color: "var(--ant-color-text)" },
    ".cm-panels.cm-panels-top": { borderBlockEnd: "1px solid var(--ant-color-border-secondary)" },
    ".cm-textfield": {
      fontSize: "14px",
      color: "var(--ant-color-text)",
      backgroundColor: "var(--ant-color-bg-container)",
      border: "1px solid var(--ant-color-border)",
      borderRadius: `${RADIUS}px`,
    },
    ".cm-button, .cm-button:active": {
      fontSize: "14px",
      color: "var(--ant-color-text)",
      backgroundImage: "none",
      backgroundColor: "var(--ant-color-bg-container)",
      border: "1px solid var(--ant-color-border)",
      borderRadius: `${RADIUS}px`,
    },
    ".cm-search label": { fontSize: "14px" },
    // A finger: 16 px text, so a phone does not zoom when the find field takes the focus (design-ce 3.2). Inside an @media
    // block style-mod scopes plain selectors only, not "&".
    "@media (pointer: coarse)": {
      ".cm-scroller, .cm-textfield, .cm-button, .cm-search label": { fontSize: "16px" },
    },
  }),
];

// modeTheme is the part of the look that follows the theme: CodeMirror's dark flag (its own base colours and the token
// colours above) and a class that names the mode.
export function modeTheme(dark: boolean): Extension {
  return [EditorView.darkTheme.of(dark), EditorView.editorAttributes.of({ class: dark ? "sc-code-dark" : "sc-code-light" })];
}
