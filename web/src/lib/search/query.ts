// SPDX-License-Identifier: AGPL-3.0-or-later

// The search page's state as plain functions. The address holds the whole state (q, in, and each filter), so a search can
// be reloaded and walked with Back and Forward. The server checks every rule (internal/api/search.go); the checks here only
// stop a request the server would refuse, so a person reads a sentence instead of a bad-request error.

export type Kind = "" | "file" | "dir" | "shortcut";
export type Modified = "" | "7" | "30" | "365";
export type Size = "" | "small" | "medium" | "large";

export interface LabelFilter {
  field: string;
  value: string;
}

export interface SearchState {
  q: string;
  // inText is the content search (in=content): the words are looked for in the text of files.
  inText: boolean;
  kind: Kind;
  exts: string[];
  modified: Modified;
  size: Size;
  labels: LabelFilter[];
}

// The limits the server sets by default (search.max_query_bytes, search.max_terms, decision 40). A term is at most 64 bytes.
export const MAX_QUERY_BYTES = 200;
export const MAX_TERMS = 8;
export const MAX_TERM_BYTES = 64;
export const MAX_EXTS = 8;
export const MAX_LABELS = 4;
export const PAGE_LIMIT = 25;
export const MAX_TEXT_VALUE_BYTES = 500;

const KINDS: readonly string[] = ["file", "dir", "shortcut"];
const MODIFIED: readonly string[] = ["7", "30", "365"];
const SIZES: readonly string[] = ["small", "medium", "large"];
const EXT = /^[a-z0-9]{1,16}$/;
const FIELD = /^[a-z]{2,6}_[a-z0-9]{1,64}$/;
const DAY_MS = 86_400_000;
const MIB = 1_048_576;

export const EXT_CHOICES = ["pdf", "docx", "xlsx", "pptx", "odt", "txt", "md", "csv", "jpg", "png", "mp4", "mp3"] as const;

export function emptyState(): SearchState {
  return { q: "", inText: false, kind: "", exts: [], modified: "", size: "", labels: [] };
}

// stateFromParams reads the address. Anything it does not recognise is dropped, so a hand-typed address never breaks the page.
export function stateFromParams(p: URLSearchParams): SearchState {
  const kind = p.get("kind") ?? "";
  const exts = (p.get("ext") ?? "")
    .split(",")
    .map((e) => e.trim().toLowerCase().replace(/^\./, ""))
    .filter((e) => EXT.test(e))
    .slice(0, MAX_EXTS);
  const labels: LabelFilter[] = [];
  for (const raw of p.getAll("label")) {
    const at = raw.indexOf(":");
    if (at <= 0) continue;
    const field = raw.slice(0, at);
    const value = raw.slice(at + 1);
    if (FIELD.test(field) && value !== "" && labels.length < MAX_LABELS) labels.push({ field, value });
  }
  const inText = p.get("in") === "content";
  const modified = p.get("modified") ?? "";
  const size = p.get("size") ?? "";
  return {
    q: p.get("q") ?? "",
    inText,
    // A text search finds files only, so a kind in the address is not applied (and no chip says it is).
    kind: !inText && KINDS.includes(kind) ? (kind as Kind) : "",
    exts,
    modified: MODIFIED.includes(modified) ? (modified as Modified) : "",
    size: SIZES.includes(size) ? (size as Size) : "",
    labels,
  };
}

// paramsFromState writes the address for a state: only what is set is written.
export function paramsFromState(s: SearchState): URLSearchParams {
  const p = new URLSearchParams();
  if (s.q !== "") p.set("q", s.q);
  if (s.inText) p.set("in", "content");
  if (s.kind !== "") p.set("kind", s.kind);
  if (s.exts.length > 0) p.set("ext", s.exts.join(","));
  if (s.modified !== "") p.set("modified", s.modified);
  if (s.size !== "") p.set("size", s.size);
  for (const l of s.labels) p.append("label", `${l.field}:${l.value}`);
  return p;
}

// hasFilter says whether any filter is set, so that a search may run without words.
export function hasFilter(s: SearchState): boolean {
  return s.kind !== "" || s.exts.length > 0 || s.modified !== "" || s.size !== "" || s.labels.length > 0;
}

export const byteLength = (s: string): number => new TextEncoder().encode(s).length;

// FieldShape is the part of a label field the filters need: its ID, its type and its choices.
export interface FieldShape {
  id: string;
  type: string;
  choices: { id: string; archived: boolean }[];
}

// resolveLabels keeps the label filters the account can search. A filter of a field the account does not have, of a user
// field (not offered in v1), or of a choice that is not a live choice of its field is dropped, so no raw ID reaches the
// request's chips or the server's refusal. While the fields are being read they are kept, to be checked; when they could not
// be read, none can be checked, so none is kept.
export function resolveLabels(labels: LabelFilter[], fields: readonly FieldShape[] | null, fieldsOff: boolean): LabelFilter[] {
  if (fields === null) return fieldsOff ? [] : labels;
  return labels.filter((l) => {
    const f = fields.find((x) => x.id === l.field);
    if (!f || f.type === "user") return false;
    if (f.type === "choice") return f.choices.some((c) => c.id === l.value && !c.archived);
    return true;
  });
}

// Problem names what is wrong with a query, as a key of the search catalogue. null means the query may be sent.
export type Problem = "tooLong" | "terms" | "words" | "needWords" | "labelValue";

// queryProblem checks the words of a state at the boundary: the byte limits, the term count, and the two-character minimum
// (the server refuses a one-letter term, since as a prefix it matches nearly every name). A text search needs words.
export function queryProblem(s: SearchState): Problem | null {
  const q = s.q.trim();
  if (q === "") return s.inText ? "needWords" : null;
  if (byteLength(q) > MAX_QUERY_BYTES) return "tooLong";
  const terms = q.split(/\s+/);
  if (terms.length > MAX_TERMS) return "terms";
  for (const t of terms) {
    if (byteLength(t) > MAX_TERM_BYTES) return "tooLong";
    if ([...t].length < 2) return "words";
  }
  return null;
}

// labelValueProblem checks one label filter's value by its field's type, as the server parses it (internal/api/search.go,
// labelTest). A choice is an ID the picker supplies, so it is only checked for being present.
export function labelValueProblem(type: string, value: string): Problem | null {
  if (type === "text") return value === "" || byteLength(value) > MAX_TEXT_VALUE_BYTES ? "labelValue" : null;
  if (type === "number") return /^-?\d+(\.\d+)?(\.\.-?\d+(\.\d+)?)?$/.test(value) ? null : "labelValue";
  if (type === "date") return /^\d{4}-\d{2}-\d{2}(\.\.\d{4}-\d{2}-\d{2})?$/.test(value) ? null : "labelValue";
  return value === "" ? "labelValue" : null;
}

// searchParamsFor builds the query of one request. A text search finds files only, so the kind is left out. The clock is
// passed in, so a test can fix the "last 7 days" edge.
export function searchParamsFor(s: SearchState, now: number): URLSearchParams {
  const p = new URLSearchParams();
  const q = s.q.trim();
  if (q !== "") p.set("q", q);
  if (s.inText) p.set("in", "content");
  else if (s.kind !== "") p.set("kind", s.kind);
  if (s.exts.length > 0) p.set("ext", s.exts.join(","));
  if (s.modified !== "") p.set("modified_after", String(now - Number(s.modified) * DAY_MS));
  if (s.size === "small") p.set("size_max", String(MIB - 1));
  if (s.size === "medium") {
    p.set("size_min", String(MIB));
    p.set("size_max", String(100 * MIB - 1));
  }
  if (s.size === "large") p.set("size_min", String(100 * MIB));
  for (const l of s.labels) p.append("label", `${l.field}:${l.value}`);
  p.set("limit", String(PAGE_LIMIT));
  return p;
}
