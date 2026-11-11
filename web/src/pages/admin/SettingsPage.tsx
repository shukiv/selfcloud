// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Modal, Tabs } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type ReactNode } from "react";
import { useBlocker } from "react-router-dom";
import { useLang, useT } from "../../lib/i18n";
import { brand } from "../../lib/brand";
import { useStore } from "../../lib/store";
import { isAdmin, session } from "../../lib/session";
import { queryKey } from "../../lib/query";
import { getInfo } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import { useAdminText } from "../../lib/manage/adminText";
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

// useWide says whether the window is 768 px or more: the section list sits beside the cards there, and the sections are tabs
// below it.
function useWide(): boolean {
  const query = "(min-width: 768px)";
  const read = () => (typeof window.matchMedia === "function" ? window.matchMedia(query).matches : true);
  const [wide, setWide] = useState(read);
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const m = window.matchMedia(query);
    const on = () => setWide(m.matches);
    on();
    m.addEventListener?.("change", on);
    return () => m.removeEventListener?.("change", on);
  }, []);
  return wide;
}

function SettingsView() {
  const { t } = useT();
  const lang = useLang();
  const brandName = useStore(brand).name;
  const qc = useQueryClient();
  const guard = useGuard();
  const ready = useAdminText();
  const wide = useWide();
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
    document.title = `${t("settings.title")} · ${brandName}`;
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
    if (!wide || answer === undefined || typeof IntersectionObserver === "undefined") return;
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
  }, [wide, answer]);

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
      busy={busy === s.id}
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
  if (!ready || (answer === undefined && !q.isError)) {
    body = <p role="status">{t("app.loading")}</p>;
  } else if (answer !== undefined) {
    body = wide ? (
      <div style={{ display: "grid", gridTemplateColumns: "200px minmax(0, 1fr)", gap: 24, alignItems: "start" }}>
        <nav aria-label={t("settings.sections")} style={{ position: "sticky", top: 0, display: "flex", flexDirection: "column", gap: 2, borderInlineStart: "2px solid var(--ant-color-split)" }}>
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
        <div style={{ display: "flex", flexDirection: "column", gap: 16, minWidth: 0 }}>{cards.map((c) => c.node)}</div>
      </div>
    ) : (
      <Tabs
        activeKey={tab}
        onChange={(k) => setTab(k as Tab)}
        items={cards.map((c) => ({ key: c.id, label: t(`settings.section.${c.id}`), children: c.node }))}
        style={{ minWidth: 0 }}
      />
    );
  }

  return (
    <div style={{ minWidth: 0 }}>
      <h1 style={pageTitle}>{t("settings.title")}</h1>
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
