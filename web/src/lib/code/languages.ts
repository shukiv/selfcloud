// SPDX-License-Identifier: AGPL-3.0-or-later

// The language map of the code viewer (decision 115): one dynamic import per pack, so a pack downloads the first time a file
// of its kind is shown, and never with the page. A read-only viewer needs the parser only, so each pack gives its Language
// (javascriptLanguage and the rest), not its full support: the completions, snippets and lint that the support functions add
// are never imported, and the bundler leaves them out. The legacy modes (Ruby, shell, TOML, properties, Dockerfile) are stream
// parsers that the core's StreamLanguage runs. Markdown's pack is @codemirror/lang-markdown (decision 124); plain text has none.
// A switch, not an object keyed by pack: the source check refuses the text "javascript" followed by a colon anywhere.
import type { Extension } from "@codemirror/state";
import { StreamLanguage } from "@codemirror/language";
import { extensionOf, type Pack } from "./names";

// loadLanguage loads a pack's parser for a file of this name, or gives null for a pack with none (plain text).
export function loadLanguage(pack: Pack, name: string): Promise<Extension | null> {
  switch (pack) {
    case "javascript":
      return import("@codemirror/lang-javascript").then((m) => (extensionOf(name) === "jsx" ? m.jsxLanguage : m.javascriptLanguage));
    case "typescript":
      return import("@codemirror/lang-javascript").then((m) => (extensionOf(name) === "tsx" ? m.tsxLanguage : m.typescriptLanguage));
    case "json":
      return import("@codemirror/lang-json").then((m) => m.jsonLanguage);
    case "html":
      return import("@codemirror/lang-html").then((m) => m.htmlLanguage);
    case "xml":
      return import("@codemirror/lang-xml").then((m) => m.xmlLanguage);
    case "css":
      return import("@codemirror/lang-css").then((m) => m.cssLanguage);
    case "python":
      return import("@codemirror/lang-python").then((m) => m.pythonLanguage);
    case "go":
      return import("@codemirror/lang-go").then((m) => m.goLanguage);
    case "java":
      return import("@codemirror/lang-java").then((m) => m.javaLanguage);
    case "cpp":
      return import("@codemirror/lang-cpp").then((m) => m.cppLanguage);
    case "php":
      return import("@codemirror/lang-php").then((m) => m.phpLanguage);
    case "rust":
      return import("@codemirror/lang-rust").then((m) => m.rustLanguage);
    case "sql":
      return import("@codemirror/lang-sql").then((m) => m.StandardSQL.language);
    case "yaml":
      return import("@codemirror/lang-yaml").then((m) => m.yamlLanguage);
    case "ruby":
      return import("@codemirror/legacy-modes/mode/ruby").then((m) => StreamLanguage.define(m.ruby));
    case "shell":
      return import("@codemirror/legacy-modes/mode/shell").then((m) => StreamLanguage.define(m.shell));
    case "toml":
      return import("@codemirror/legacy-modes/mode/toml").then((m) => StreamLanguage.define(m.toml));
    case "properties":
      return import("@codemirror/legacy-modes/mode/properties").then((m) => StreamLanguage.define(m.properties));
    case "dockerfile":
      return import("@codemirror/legacy-modes/mode/dockerfile").then((m) => StreamLanguage.define(m.dockerFile));
    case "markdown":
      return import("@codemirror/lang-markdown").then((m) => m.markdownLanguage);
    case "text":
      return Promise.resolve(null);
  }
}
