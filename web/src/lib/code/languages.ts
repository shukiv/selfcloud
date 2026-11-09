// SPDX-License-Identifier: AGPL-3.0-or-later

// The language map of the code viewer (decision 115): one dynamic import per pack, so a pack downloads the first time a file
// of its kind is shown, and never with the page. A read-only viewer needs the parser only, so each pack gives its Language
// (javascriptLanguage and the rest), not its full support: the completions, snippets and lint that the support functions add
// are never imported, and the bundler leaves them out. The legacy modes (Ruby, shell, TOML, properties, Dockerfile) are stream
// parsers that the core's StreamLanguage runs. Plain text and Markdown have no pack here: Markdown's comes with the editor.
import type { Extension } from "@codemirror/state";
import { StreamLanguage } from "@codemirror/language";
import { extensionOf, type Pack } from "./names";

type Loader = (name: string) => Promise<Extension>;

const LOADERS: Readonly<Partial<Record<Pack, Loader>>> = {
  javascript: (name) =>
    import("@codemirror/lang-javascript").then((m) => (extensionOf(name) === "jsx" ? m.jsxLanguage : m.javascriptLanguage)),
  typescript: (name) =>
    import("@codemirror/lang-javascript").then((m) => (extensionOf(name) === "tsx" ? m.tsxLanguage : m.typescriptLanguage)),
  json: () => import("@codemirror/lang-json").then((m) => m.jsonLanguage),
  html: () => import("@codemirror/lang-html").then((m) => m.htmlLanguage),
  xml: () => import("@codemirror/lang-xml").then((m) => m.xmlLanguage),
  css: () => import("@codemirror/lang-css").then((m) => m.cssLanguage),
  python: () => import("@codemirror/lang-python").then((m) => m.pythonLanguage),
  go: () => import("@codemirror/lang-go").then((m) => m.goLanguage),
  java: () => import("@codemirror/lang-java").then((m) => m.javaLanguage),
  cpp: () => import("@codemirror/lang-cpp").then((m) => m.cppLanguage),
  php: () => import("@codemirror/lang-php").then((m) => m.phpLanguage),
  rust: () => import("@codemirror/lang-rust").then((m) => m.rustLanguage),
  sql: () => import("@codemirror/lang-sql").then((m) => m.StandardSQL.language),
  yaml: () => import("@codemirror/lang-yaml").then((m) => m.yamlLanguage),
  ruby: () => import("@codemirror/legacy-modes/mode/ruby").then((m) => StreamLanguage.define(m.ruby)),
  shell: () => import("@codemirror/legacy-modes/mode/shell").then((m) => StreamLanguage.define(m.shell)),
  toml: () => import("@codemirror/legacy-modes/mode/toml").then((m) => StreamLanguage.define(m.toml)),
  properties: () => import("@codemirror/legacy-modes/mode/properties").then((m) => StreamLanguage.define(m.properties)),
  dockerfile: () => import("@codemirror/legacy-modes/mode/dockerfile").then((m) => StreamLanguage.define(m.dockerFile)),
};

// loadLanguage loads a pack's parser for a file of this name, or gives null for a pack with none (plain text, Markdown).
export function loadLanguage(pack: Pack, name: string): Promise<Extension | null> {
  const load = LOADERS[pack];
  return load ? load(name) : Promise.resolve(null);
}
