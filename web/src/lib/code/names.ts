// SPDX-License-Identifier: AGPL-3.0-or-later

// The code list (decision 115): the file names the code viewer takes by name, whatever their stored type says, once the
// sniff finds text. The server keeps its own copy in internal/api/textnames.go (decision 113), and names.test.ts fails when
// the two differ. Change both in the same commit.

// Pack names the language support a name gets. "text" is plain text: the viewer, with no colours.
export type Pack =
  | "javascript"
  | "typescript"
  | "json"
  | "markdown"
  | "html"
  | "xml"
  | "css"
  | "python"
  | "go"
  | "ruby"
  | "php"
  | "java"
  | "cpp"
  | "rust"
  | "shell"
  | "sql"
  | "yaml"
  | "toml"
  | "properties"
  | "dockerfile"
  | "text";

// CODE_EXTENSIONS maps each listed extension (lower case, no dot) to its pack. Markdown is shown as source, with colours from
// its pack (design-ce 2.2, decision 124).
export const CODE_EXTENSIONS: Readonly<Record<string, Pack>> = {
  js: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  jsx: "javascript",
  ts: "typescript",
  tsx: "typescript",
  json: "json",
  jsonc: "json",
  md: "markdown",
  markdown: "markdown",
  html: "html",
  htm: "html",
  xml: "xml",
  svg: "xml",
  css: "css",
  py: "python",
  go: "go",
  rb: "ruby",
  php: "php",
  java: "java",
  c: "cpp",
  h: "cpp",
  cc: "cpp",
  cpp: "cpp",
  cxx: "cpp",
  hpp: "cpp",
  rs: "rust",
  sh: "shell",
  bash: "shell",
  sql: "sql",
  yml: "yaml",
  yaml: "yaml",
  toml: "toml",
  ini: "properties",
  conf: "properties",
  cfg: "properties",
  env: "properties",
  properties: "properties",
  txt: "text",
  csv: "text",
  tsv: "text",
  log: "text",
};

// CODE_NAMES maps the listed whole names (lower case), which have no extension, to their pack.
export const CODE_NAMES: Readonly<Record<string, Pack>> = {
  dockerfile: "dockerfile",
  makefile: "text",
};

// extensionOf is a name's extension: the text after its last dot, in lower case, or "" when there is none. A name that
// starts with its only dot (".env") has that text as its extension, as the server's path.Ext reads it.
export function extensionOf(name: string): string {
  const dot = name.lastIndexOf(".");
  return dot < 0 ? "" : name.slice(dot + 1).toLowerCase();
}

// packOf is the pack a name gets from the list, by its whole name first and then by its extension; null when the name is
// not in the list. The name is never read as a path, and only the lists' own keys count (a file named "constructor" is not in
// the list).
export function packOf(name: string): Pack | null {
  const lower = name.toLowerCase();
  if (Object.hasOwn(CODE_NAMES, lower)) return CODE_NAMES[lower];
  const ext = extensionOf(name);
  return ext !== "" && Object.hasOwn(CODE_EXTENSIONS, ext) ? CODE_EXTENSIONS[ext] : null;
}

// codeNamed reports whether a name is in the code list.
export function codeNamed(name: string): boolean {
  return packOf(name) !== null;
}
