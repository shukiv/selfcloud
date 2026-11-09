// SPDX-License-Identifier: AGPL-3.0-or-later

// Search: the words and the filters, in the address; the results below. A query is sent when the address changes, and only
// the answer to the latest address is shown. A failed request keeps the list it had, with its error above it; a failed first
// answer shows the error alone, never the "nothing matches" text. There is no count: a page says only whether more may follow.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, Button, Grid, Input, Skeleton, theme as antdTheme } from "antd";
import type { InputRef } from "antd";
import { Search } from "lucide-react";
import { useLocation, useSearchParams } from "react-router-dom";
import { ApiError } from "../lib/api";
import { fileErrorText } from "../lib/files/fileErrors";
import { listLabelFields, type LabelField } from "../lib/labels/api";
import { translate, useLang, useT, type Lang } from "../lib/i18n";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";
import { session, sessionKey } from "../lib/session";
import { useCoarsePointer } from "../lib/pointer";
import { searchNodes, type SearchHit } from "../lib/search/api";
import {
  hasFilter,
  paramsFromState,
  resolveLabels,
  queryProblem,
  searchParamsFor,
  stateFromParams,
  type Problem,
  type SearchState,
} from "../lib/search/query";
import { loadSearchText } from "../lib/search/text";
import HitRow from "../components/search/HitRow";
import SearchFilters from "../components/search/SearchFilters";
import { useDraft } from "../components/search/useDraft";
import EmptyState from "../components/EmptyState";

// searchErrorText is the text of a failed search, by its code. The server's own text is never shown.
export function searchErrorText(err: unknown, lang: Lang): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "rate_limited":
        return translate(lang, "search.err.rateLimited");
      case "invalid_request":
        return translate(lang, "search.err.invalid");
      case "unavailable":
        return translate(lang, "search.err.slow");
      case "not_found":
        return translate(lang, "search.err.off");
    }
  }
  return fileErrorText(err, lang);
}

// mergeHits appends a page to the results shown, leaving out a node that is already there.
export function mergeHits(shown: SearchHit[], page: SearchHit[]): SearchHit[] {
  const seen = new Set(shown.map((h) => h.id));
  return [...shown, ...page.filter((h) => !seen.has(h.id))];
}

const PROBLEM_KEY: Record<Problem, string> = {
  tooLong: "search.err.tooLong",
  terms: "search.err.terms",
  words: "search.err.words",
  needWords: "search.needWords",
  labelValue: "search.err.labelValue",
};

export default function SearchPage() {
  const { t } = useT();
  const lang = useLang();
  const { token } = antdTheme.useToken();
  const coarse = useCoarsePointer();
  const screens = Grid.useBreakpoint();
  const wide = !!screens.md;
  const brandName = useStore(brand).name;
  const me = useStore(session);
  const sk = sessionKey(me);
  const [params, setParams] = useSearchParams();
  const key = params.toString();
  const state = useMemo(() => stateFromParams(new URLSearchParams(key)), [key]);

  // The page's own strings load before the first render of the page, in the current language.
  const [textLang, setTextLang] = useState<Lang | null>(null);
  const [textFailed, setTextFailed] = useState(false);
  // textTry counts the tries: Try again after a failed load starts the load again.
  const [textTry, setTextTry] = useState(0);
  useEffect(() => {
    let live = true;
    setTextFailed(false);
    loadSearchText(lang).then(
      () => live && setTextLang(lang),
      () => live && setTextFailed(true),
    );
    return () => {
      live = false;
    };
  }, [lang, textTry]);
  const ready = textLang === lang;

  // Opened from the phone's button, the page takes the focus for its own box, once its strings are there.
  const location = useLocation();
  const focusOnOpen = useRef((location.state as { focusSearch?: boolean } | null)?.focusSearch === true);
  const boxRef = useRef<InputRef>(null);
  useEffect(() => {
    if (ready && focusOnOpen.current) {
      focusOnOpen.current = false;
      boxRef.current?.focus();
    }
  }, [ready]);

  useEffect(() => {
    if (ready) document.title = `${t("search.title")} · ${brandName}`;
  }, [ready, t, brandName]);

  // The write of the address: a change of the words or of a filter keeps the others, and replaces the entry (Back returns to
  // the page the search was opened from).
  const write = useCallback(
    (f: (s: SearchState) => SearchState) => {
      setParams((prev) => paramsFromState(f(stateFromParams(prev))), { replace: true });
    },
    [setParams],
  );
  const draft = useDraft(state.q, (v) => write((s) => ({ ...s, q: v })));

  // The label fields, read once for the person; without them the label filters are not offered.
  const [fields, setFields] = useState<LabelField[] | null>(null);
  const [fieldsOff, setFieldsOff] = useState(false);
  useEffect(() => {
    let live = true;
    setFields(null);
    setFieldsOff(false);
    listLabelFields().then(
      (r) => live && setFields(r.items),
      () => live && setFieldsOff(true),
    );
    return () => {
      live = false;
    };
  }, [sk]);

  // The label filters the account can search. While the fields are being read, a label filter waits: the words are not
  // sent until it has been checked. A filter that is not kept is taken out of the address too, so the page shows what it
  // searches, and reqKey is the request's own query.
  const resolved = useMemo<SearchState>(() => ({ ...state, labels: resolveLabels(state.labels, fields, fieldsOff) }), [state, fields, fieldsOff]);
  const waitingLabels = fields === null && !fieldsOff && state.labels.length > 0;
  const reqKey = paramsFromState(resolved).toString();
  useEffect(() => {
    if (fields === null && !fieldsOff) return;
    if (resolved.labels.length === state.labels.length) return;
    setParams(
      (prev) => {
        const cur = stateFromParams(prev);
        return paramsFromState({ ...cur, labels: resolveLabels(cur.labels, fields, fieldsOff) });
      },
      { replace: true },
    );
  }, [fields, fieldsOff, resolved.labels.length, state.labels.length, setParams]);

  // The results. seq numbers each request; an answer or a failure that is not the latest one is dropped.
  const [items, setItems] = useState<SearchHit[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [failure, setFailure] = useState<unknown>(null);
  const [failedMore, setFailedMore] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [attempt, setAttempt] = useState(0);
  const seq = useRef(0);
  // The clock of a query is fixed when it starts, so every page of it asks for the same window (the cursor depends on it).
  const anchor = useRef(0);

  useEffect(() => {
    if (!ready || waitingLabels) return;
    const s = stateFromParams(new URLSearchParams(reqKey));
    const p = queryProblem(s);
    const mine = ++seq.current;
    anchor.current = Date.now();
    setItems([]);
    setCursor(null);
    setFailure(null);
    setFailedMore(false);
    setProblem(p);
    if (p !== null || (s.q.trim() === "" && !hasFilter(s))) {
      setLoading(false);
      return;
    }
    setLoading(true);
    searchNodes(searchParamsFor(s, anchor.current)).then(
      (page) => {
        if (mine !== seq.current) return;
        setItems(page.items);
        setCursor(page.next_cursor ?? null);
        setLoading(false);
      },
      (err: unknown) => {
        if (mine !== seq.current) return;
        setFailure(err);
        setLoading(false);
      },
    );
    // The session is the trigger too: a new person reads again. attempt repeats the same query after a failure.
  }, [reqKey, ready, waitingLabels, sk, attempt]);

  const loadMore = () => {
    if (cursor === null || loading) return;
    const p = searchParamsFor(stateFromParams(new URLSearchParams(reqKey)), anchor.current);
    p.set("cursor", cursor);
    const mine = ++seq.current;
    setLoading(true);
    setFailure(null);
    setFailedMore(false);
    searchNodes(p).then(
      (page) => {
        if (mine !== seq.current) return;
        setItems((prev) => mergeHits(prev, page.items));
        setCursor(page.next_cursor ?? null);
        setLoading(false);
      },
      (err: unknown) => {
        if (mine !== seq.current) return;
        setFailure(err);
        setFailedMore(true);
        setLoading(false);
      },
    );
  };

  const retry = () => {
    if (failedMore) loadMore();
    else setAttempt((n) => n + 1);
  };

  const failureMessage = failure === null ? null : searchErrorText(failure, lang);
  // While a label filter waits to be checked, the page is neither asking nor answering: no empty state is shown for it.
  const asked = !waitingLabels && problem === null && (resolved.q.trim() !== "" || hasFilter(resolved));
  const empty = !loading && failure === null && problem === null && asked && items.length === 0 && cursor === null;
  const pageEmpty = !loading && failure === null && problem === null && asked && items.length === 0 && cursor !== null;
  const hasSnippet = items.some((h) => h.snippet !== undefined);

  const setInText = (inText: boolean) => write((s) => ({ ...s, inText, kind: inText ? "" : s.kind }));

  return (
    <div>
      {/* The heading uses the main catalogue's word for search: it is shown before the page's own strings have loaded. */}
      <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>{t("shell.search.open")}</h1>

      {!wide && (
        <Input
          ref={boxRef}
          type="search"
          enterKeyHint="search"
          aria-label={t("shell.search.placeholder")}
          placeholder={t("shell.search.placeholder")}
          prefix={<Search size={16} strokeWidth={1.75} aria-hidden="true" />}
          value={draft.draft}
          onChange={(e) => draft.change(e.target.value)}
          onPressEnter={() => draft.submit()}
          style={{ marginBlockEnd: 12, ...(coarse ? { minHeight: 44 } : {}) }}
        />
      )}

      {/* A failed load shows the main catalogue's words: the page's own strings are not there. */}
      {textFailed && (
        <Alert
          type="error"
          showIcon
          role="alert"
          message={t("error.generic")}
          action={
            <Button size="small" onClick={() => setTextTry((n) => n + 1)}>
              {t("files.retry")}
            </Button>
          }
          style={{ marginBlockEnd: 12 }}
        />
      )}

      {ready && (
        <>
          <div role="group" aria-label={t("search.results")} style={{ display: "flex", flexWrap: "wrap", gap: 8, marginBlockEnd: 10 }}>
            {[
              { value: false, label: t("search.names") },
              { value: true, label: t("search.text") },
            ].map((o) => (
              <Button
                key={String(o.value)}
                aria-pressed={state.inText === o.value}
                type={state.inText === o.value ? "primary" : "default"}
                onClick={() => setInText(o.value)}
                style={coarse ? { minHeight: 44 } : undefined}
              >
                {o.label}
              </Button>
            ))}
          </div>
          {state.inText && (
            <p style={{ margin: "0 0 10px", fontSize: token.fontSizeSM, color: token.colorTextSecondary }}>{t("search.textNote")}</p>
          )}

          <SearchFilters state={state} change={(next) => write(() => next)} fields={fields} fieldsOff={fieldsOff} coarse={coarse} />

          <div style={{ marginBlock: 16 }}>
            {problem !== null && (
              <p role="status" style={{ margin: 0, color: token.colorTextSecondary }}>
                {t(PROBLEM_KEY[problem])}
              </p>
            )}
            {problem === null && !asked && <p role="status" style={{ margin: 0, color: token.colorTextSecondary }}>{t("search.prompt")}</p>}

            {failureMessage !== null && (
              <Alert
                type="error"
                showIcon
                role="alert"
                message={failureMessage}
                action={
                  <Button size="small" onClick={retry} disabled={loading}>
                    {t("search.err.retry")}
                  </Button>
                }
                style={{ marginBlockEnd: 12 }}
              />
            )}

            {loading && items.length === 0 && (
              <>
                <p role="status" style={{ margin: "0 0 8px", color: token.colorTextSecondary }}>
                  {t("search.loading")}
                </p>
                <Skeleton active />
              </>
            )}

            {items.length > 0 && (
              <ul
                aria-label={t("search.results")}
                data-testid="search-results"
                style={{ display: "grid", gap: 8, margin: 0, padding: 0, listStyle: "none" }}
              >
                {items.map((h) => (
                  <HitRow key={h.id} hit={h} />
                ))}
              </ul>
            )}

            {hasSnippet && items.length > 0 && (
              <p style={{ margin: "8px 0 0", fontSize: token.fontSizeSM, color: token.colorTextSecondary }}>{t("search.snippetNote")}</p>
            )}

            {empty && <EmptyState role="status" text={t("search.empty")} />}
            {pageEmpty && <EmptyState role="status" text={t("search.pageEmpty")} />}

            {cursor !== null && (
              <div style={{ marginBlockStart: 12, display: "flex", flexWrap: "wrap", gap: 8, alignItems: "center" }}>
                <Button onClick={loadMore} disabled={loading} style={coarse ? { minHeight: 44 } : undefined}>
                  {t("files.loadMore")}
                </Button>
                <span style={{ color: token.colorTextSecondary }}>{t("search.moreMayFollow")}</span>
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}
