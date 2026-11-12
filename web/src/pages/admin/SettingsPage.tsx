// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button, Modal, Tabs } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { useBlocker } from "react-router-dom";
import { translate, useLang, useT } from "../../lib/i18n";
import { brand } from "../../lib/brand";
import { useStore } from "../../lib/store";
import { isAdmin, session } from "../../lib/session";
import { queryKey } from "../../lib/query";
import { getInfo } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import { useAdminTextState } from "../../lib/manage/adminText";
import { changeSettings, getSettings, type SettingsChange } from "../../lib/settings/api";
import { fieldKeys, SECTIONS, type SectionId, type SectionSpec } from "../../lib/settings/fields";
import { draftOf, isChanged, refusal, sectionChange, type Draft, type Edits } from "../../lib/settings/form";
import NotFoundPage from "../NotFoundPage";
import { pageTitle } from "../../components/admin/adminStyles";
import { useGuard } from "../../components/admin/hooks";
import About from "../../components/admin/settings/About";
import SectionCard, { type Notice } from "../../components/admin/settings/SectionCard";

// SettingsPage is the installation admin's Settings (decision 143). Each section of settings has its own Save, which sends that
// section's changed keys only. A setting the config file writes is read-only here. The Branding section sits before the first
// card when it lands (the list of sections and the tabs are built from the one list below).
export default function SettingsPage() {
  const user = useStore(session)?.user;
  return isAdmin(user) ? <SettingsView /> : <NotFoundPage />;
}

type Tab = SectionId;
const TABS: Tab[] = [...SECTIONS.map((s) => s.id), "about"];

// The layout follows the width of the page's own content, not the window's: the docked sidebar takes 264 px of a window from 992 px.
// Below TABS_BELOW the sections are tabs; from LIST_FROM the section list sits beside the cards; in between the cards are stacked.
// A row puts its input beside its text when the card is at least ROW_FROM wide.
export const TABS_BELOW = 600;
export const LIST_FROM = 900;
const ROW_FROM = 600;
const LIST_WIDTH = 224;
// The section list sticks below the 56 px top bar, with a gap.
const SECTION_LIST_TOP = 72;
const CARD_PADDING = 48;

export interface Layout {
  tabs: boolean;
  list: boolean;
  rowWide: boolean;
}

export function layoutFor(width: number): Layout {
  const list = width >= LIST_FROM;
  return { tabs: width < TABS_BELOW, list, rowWide: width - (list ? LIST_WIDTH : 0) - CARD_PADDING >= ROW_FROM };
}

// SCROLLBAR is the widest classic scrollbar: the drop that stacks the cards can bring one that takes this much, which must not
// push the content back under the tabs boundary and flip it again. Each boundary is crossed this much further to change course.
const SCROLLBAR = 24;

// settle keeps the layout it has until the width crosses a boundary by more than SCROLLBAR: from tabs to stacked at TABS_BELOW
// plus it, and back only under TABS_BELOW; the section list appears at LIST_FROM plus it and goes only under LIST_FROM.
export function settle(prev: Layout, width: number): Layout {
  const tabs = prev.tabs ? width < TABS_BELOW + SCROLLBAR : width < TABS_BELOW;
  const list = prev.list ? width >= LIST_FROM : width >= LIST_FROM + SCROLLBAR;
  return { tabs, list, rowWide: width - (list ? LIST_WIDTH : 0) - CARD_PADDING >= ROW_FROM };
}

// useLayout measures the page's content, settling across each boundary so a scrollbar that appears when the cards stack cannot
// flip it back. The first width is read before the first paint. Until it is measured (a window with no layout, as in a test)
// the window's own width stands in: 768 px and up is a roomy content, below it a phone's.
function useLayout(ref: RefObject<HTMLElement | null>): Layout {
  const query = "(min-width: 768px)";
  const viewport = () => (typeof window.matchMedia === "function" ? window.matchMedia(query).matches : true);
  const [wide, setWide] = useState(viewport);
  const [layout, setLayout] = useState<Layout>(() => layoutFor(wide ? 1200 : 375));
  const read = (w: number) => setLayout((prev) => settle(prev, w));
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const m = window.matchMedia(query);
    const on = () => setWide(m.matches);
    on();
    m.addEventListener?.("change", on);
    return () => m.removeEventListener?.("change", on);
  }, []);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const w = el.getBoundingClientRect().width;
    if (w > 0) read(w);
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver((entries) => {
      const width = entries[0]?.contentRect.width;
      if (typeof width === "number" && width > 0) read(width);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [ref]);
  return layout;
}

function SettingsView() {
  const { t } = useT();
  const lang = useLang();
  const brandName = useStore(brand).name;
  const qc = useQueryClient();
  const guard = useGuard();
  const { ready, failed: textFailed, retry: retryText } = useAdminTextState();
  const root = useRef<HTMLDivElement>(null);
  const layout = useLayout(root);
  const wide = layout.rowWide;
  const settingsKey = queryKey("settings");
  const infoKey = queryKey("installation");
  const q = useQuery({ queryKey: settingsKey, queryFn: getSettings, refetchOnMount: "always", retry: false });
  const infoQ = useQuery({ queryKey: infoKey, queryFn: getInfo, refetchOnMount: "always", retry: false });
  const [edits, setEdits] = useState<Edits>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [notices, setNotices] = useState<Partial<Record<SectionId, Notice | null>>>({});
  const [busy, setBusy] = useState<SectionId | null>(null);
  const [tab, setTab] = useState<Tab>("uploads");
  const answer = q.data;
  const settings = answer?.settings ?? [];

  useEffect(() => {
    document.title = `${t("nav.settings")} · ${brandName}`;
  }, [t, brandName, ready]);

  const sectionDirty = (s: SectionSpec) =>
    fieldKeys(s).some((k) => {
      const x = settings.find((v) => v.key === k);
      return x !== undefined && isChanged(x, edits[k]);
    });
  const dirty = SECTIONS.some(sectionDirty);

  // The unsaved guard: a link, Back or a route change that would leave the page with unsaved changes asks first, and a reload or a
  // close asks through the browser. A move to the same address is not a leave.
  const blocker = useBlocker(({ currentLocation, nextLocation }) => dirty && (currentLocation.pathname !== nextLocation.pathname || currentLocation.search !== nextLocation.search));
  const blocked = blocker.state === "blocked" ? blocker : null;
  useEffect(() => {
    if (!dirty) return;
    const guardUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", guardUnload);
    return () => window.removeEventListener("beforeunload", guardUnload);
  }, [dirty]);

  // The section list marks the section in view.
  const [inView, setInView] = useState<Tab>("uploads");
  useEffect(() => {
    if (!layout.list || answer === undefined || typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver(
      (entries) => {
        const top = entries.filter((e) => e.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)[0];
        if (top) setInView(top.target.getAttribute("data-tab") as Tab);
      },
      { rootMargin: "0px 0px -70% 0px" },
    );
    for (const id of TABS) {
      const el = document.getElementById(`settings-${id}`);
      if (el) {
        el.setAttribute("data-tab", id);
        io.observe(el);
      }
    }
    return () => io.disconnect();
  }, [layout.list, layout.tabs, answer]);

  const dropEdits = (keys: string[]) =>
    setEdits((cur) => {
      const next = { ...cur };
      for (const k of keys) delete next[k];
      return next;
    });
  const dropErrors = (keys: string[]) =>
    setErrors((cur) => {
      const next = { ...cur };
      for (const k of keys) delete next[k];
      return next;
    });
  const setNotice = (id: SectionId, n: Notice | null) => setNotices((cur) => ({ ...cur, [id]: n }));

  const change = (section: SectionSpec, key: string, value: Draft) => {
    setEdits((cur) => ({ ...cur, [key]: { value, reset: false } }));
    dropErrors([key]);
    setNotice(section.id, null);
  };
  const reset = (section: SectionSpec, key: string) => {
    const s = settings.find((x) => x.key === key);
    if (s === undefined) return;
    setEdits((cur) => ({ ...cur, [key]: { value: draftOf({ type: s.type, value: s.default }), reset: true } }));
    dropErrors([key]);
    setNotice(section.id, null);
  };
  const discard = (section: SectionSpec) => {
    dropEdits(fieldKeys(section));
    dropErrors(fieldKeys(section));
    setNotice(section.id, null);
  };

  const save = (section: SectionSpec) =>
    guard(async () => {
      const keys = fieldKeys(section);
      const got = sectionChange(settings, keys, edits);
      dropErrors(keys);
      if (got.incomplete.length > 0) {
        setErrors((cur) => ({ ...cur, ...Object.fromEntries(got.incomplete.map((k) => [k, t("settings.needNumber")])) }));
        return;
      }
      const body: SettingsChange = {};
      if (Object.keys(got.set).length > 0) body.set = got.set;
      if (got.reset.length > 0) body.reset = got.reset;
      if (body.set === undefined && body.reset === undefined) return;
      setBusy(section.id);
      setNotice(section.id, null);
      try {
        const next = await changeSettings(body);
        qc.setQueryData(settingsKey, next);
        dropEdits(keys);
        setNotice(section.id, { kind: "saved" });
        // What the rest of the page reads from the server follows the change: the answer is re-read, and so is About's.
        void qc.invalidateQueries({ queryKey: settingsKey });
        void qc.invalidateQueries({ queryKey: infoKey });
      } catch (e) {
        const r = refusal(e);
        if (r.kind === "invalid") {
          setErrors((cur) => ({ ...cur, ...Object.fromEntries(Object.entries(r.fields).map(([k, m]) => [k, m ?? t("settings.refused")])) }));
          if (r.unmatched.length > 0 || Object.keys(r.fields).length === 0) {
            setNotice(section.id, { kind: "error", text: r.unmatched.join(" ") || t("settings.refused") });
          }
        } else if (r.kind === "locked") {
          // The keys are locked now: their edits go, the rest stay, and the answer is read again.
          dropEdits(r.keys);
          setNotice(section.id, { kind: "conflict", keys: r.keys });
          void qc.invalidateQueries({ queryKey: settingsKey });
        } else {
          setNotice(section.id, { kind: "error", text: failureText(r.error, lang) });
        }
      } finally {
        setBusy(null);
      }
    });

  const card = (s: SectionSpec): ReactNode => (
    <SectionCard
      key={s.id}
      section={s}
      settings={settings}
      edits={edits}
      errors={errors}
      notice={notices[s.id] ?? null}
      dirty={sectionDirty(s)}
      busy={busy !== null}
      wide={wide}
      onChange={(k, v) => change(s, k, v)}
      onReset={(k) => reset(s, k)}
      onSave={() => void save(s)}
      onDiscard={() => discard(s)}
    />
  );
  const about = <About key="about" info={infoQ.data} facts={answer?.facts ?? []} failure={infoQ.isError ? failureText(infoQ.error, lang) : null} />;
  const cards: { id: Tab; node: ReactNode }[] = [...SECTIONS.map((s) => ({ id: s.id as Tab, node: card(s) })), { id: "about", node: about }];

  const go = (id: Tab) => {
    setInView(id);
    const el = document.getElementById(`settings-${id}`);
    if (el && typeof el.scrollIntoView === "function") el.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  let body: ReactNode = null;
  if (textFailed) {
    body = (
      <Alert
        type="error"
        showIcon
        role="alert"
        message={translate(lang, "error.network")}
        action={<Button size="small" onClick={retryText}>{translate(lang, "files.retry")}</Button>}
      />
    );
  } else if (!ready || (answer === undefined && !q.isError)) {
    body = <p role="status">{t("app.loading")}</p>;
  } else if (answer !== undefined && !layout.tabs) {
    const stacked = <div style={{ display: "flex", flexDirection: "column", gap: 16, minWidth: 0 }}>{cards.map((c) => c.node)}</div>;
    body = !layout.list ? (
      stacked
    ) : (
      <div style={{ display: "grid", gridTemplateColumns: "200px minmax(0, 1fr)", gap: 24, alignItems: "start" }}>
        <nav aria-label={t("settings.sections")} style={{ position: "sticky", top: SECTION_LIST_TOP, display: "flex", flexDirection: "column", gap: 2, borderInlineStart: "2px solid var(--ant-color-split)" }}>
          {TABS.map((id) => (
            <button
              key={id}
              type="button"
              onClick={() => go(id)}
              aria-current={inView === id ? "true" : undefined}
              style={{
                all: "unset",
                cursor: "pointer",
                padding: "6px 12px",
                marginInlineStart: -2,
                borderInlineStart: `2px solid ${inView === id ? "var(--ant-color-primary)" : "transparent"}`,
                fontSize: 14,
                fontWeight: inView === id ? 600 : 400,
                color: inView === id ? "var(--ant-color-text)" : "var(--ant-color-text-secondary)",
              }}
            >
              {t(`settings.section.${id}`)}
            </button>
          ))}
        </nav>
        {stacked}
      </div>
    );
  } else if (answer !== undefined) {
    body = (
      <Tabs
        activeKey={tab}
        onChange={(k) => setTab(k as Tab)}
        items={cards.map((c) => ({ key: c.id, label: t(`settings.section.${c.id}`), children: c.node }))}
        style={{ minWidth: 0 }}
      />
    );
  }

  return (
    <div ref={root} style={{ minWidth: 0 }}>
      <h1 style={pageTitle}>{t("nav.settings")}</h1>
      {ready && <p style={{ margin: "-8px 0 16px", color: "var(--ant-color-text-secondary)", maxWidth: "70ch" }}>{t("settings.intro")}</p>}
      {q.isError && <Alert type="error" showIcon role="alert" message={failureText(q.error, lang)} style={{ marginBlockEnd: 16 }} />}
      {body}
      <Modal
        open={blocked !== null}
        maskClosable={false}
        title={t("settings.leave.title")}
        okText={t("settings.leave.ok")}
        cancelText={t("settings.leave.stay")}
        okButtonProps={{ danger: true }}
        onOk={() => blocked?.proceed()}
        onCancel={() => blocked?.reset()}
      >
        {t("settings.leave.body")}
      </Modal>
    </div>
  );
}
