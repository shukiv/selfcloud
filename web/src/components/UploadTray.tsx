// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useMemo, useRef, useState, type ChangeEvent, type FormEvent } from "react";
import { Button, Card } from "antd";
import { fileErrorText } from "../lib/files/fileErrors";
import { formatBytes, formatRate } from "../lib/files/format";
import { conflictAsk, manager, uploads, type UploadItem } from "../lib/files/uploads";
import { useLang, useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import Conflict from "./files/Conflict";

// UploadTray lists the uploads: their progress, their state, and the controls each state allows. It is
// collapsible to a header that shows the overall progress. It holds no upload state of its own: the manager
// is a module singleton, and it is never started from an effect (StrictMode runs effects twice).
const ACTIVE = ["queued", "uploading", "waiting", "conflict", "awaiting_name"];
const WAITING = ["uploading", "queued", "waiting"];
const CANCELLABLE = ["uploading", "queued", "waiting", "paused", "interrupted", "awaiting_name"];
const FINISHED = ["done", "skipped", "cancelled", "failed"];

export default function UploadTray() {
  const { t, tp } = useT();
  const lang = useLang();
  const list = useStore(uploads);
  const ask = useStore(conflictAsk);
  const [open, setOpen] = useState(true);
  const [nameDraft, setNameDraft] = useState<Record<number, string>>({});
  const [announce, setAnnounce] = useState("");
  const [resumeFor, setResumeFor] = useState<number | null>(null);
  const [mismatch, setMismatch] = useState<number | null>(null);
  const resumeInput = useRef<HTMLInputElement>(null);
  // Screen readers hear a finish or a failure once, not every piece.
  const seen = useRef(new Map<number, string>());

  const active = useMemo(() => list.filter((u) => ACTIVE.includes(u.status)), [list]);
  const total = active.reduce((n, u) => n + u.size, 0);
  const sent = active.reduce((n, u) => n + u.offset, 0);
  const percent = total > 0 ? Math.floor((sent / total) * 100) : 0;

  // The announcement follows the list; the seen map keeps a finish from being announced twice.
  useEffect(() => {
    for (const u of list) {
      const before = seen.current.get(u.id);
      if (before !== u.status) {
        seen.current.set(u.id, u.status);
        if (u.status === "done") setAnnounce(t("upload.announce.done", { name: u.name }));
        if (u.status === "failed") setAnnounce(t("upload.announce.failed", { name: u.name }));
      }
    }
  }, [list]);

  function commit(u: UploadItem) {
    const name = (nameDraft[u.id] ?? u.suggested ?? "").trim();
    if (name) void manager.commitName(u.id, name);
  }

  function pickResume(u: UploadItem) {
    setResumeFor(u.id);
    resumeInput.current?.click();
  }

  async function onResumePicked(e: ChangeEvent<HTMLInputElement>) {
    const input = e.currentTarget;
    const file = input.files?.[0];
    input.value = "";
    const id = resumeFor;
    setResumeFor(null);
    if (!file || id === null) return;
    const ok = await manager.attach(id, file);
    setMismatch(ok ? null : id);
  }

  function errorText(u: UploadItem): string {
    return u.error ? fileErrorText(u.error, lang) : "";
  }

  // Nothing to show: no upload in the list and no conflict waiting for an answer, so no empty box.
  if (list.length === 0 && ask === null) return null;

  return (
    <>
      <Card
        size="small"
        role="region"
        aria-labelledby="upload-tray-title"
        title={<h2 id="upload-tray-title" style={{ fontSize: "inherit", margin: 0 }}>{t("upload.title")}</h2>}
        data-testid="upload-tray"
        style={{
          position: "fixed",
          insetBlockEnd: 16,
          insetInlineEnd: 16,
          zIndex: 30,
          width: "min(420px, calc(100vw - 32px))",
          maxHeight: "60dvh",
          display: "flex",
          flexDirection: "column",
          overflow: "hidden",
        }}
        styles={{ body: { overflow: "auto", padding: open ? 12 : 0 } }}
        extra={
          <span style={{ display: "inline-flex", gap: 8, alignItems: "center" }}>
            <Button aria-expanded={open} aria-controls="tray-list" onClick={() => setOpen(!open)}>
              {open ? t("upload.collapse") : t("upload.expand")}
            </Button>
            <Button onClick={() => manager.clearFinished()}>{t("upload.clear")}</Button>
          </span>
        }
      >
        <p role="status">{active.length > 0 ? tp("upload.summary", active.length, { percent: String(percent) }) : ""}</p>
        <p aria-live="polite" style={SR_ONLY}>
          {announce}
        </p>
        {open && (
          <ul id="tray-list" style={{ listStyle: "none", margin: 0, padding: 0, display: "grid", gap: 12 }}>
            {list.map((u) => (
              <li key={u.id} data-status={u.status} style={{ display: "grid", gap: 6 }}>
                <div style={{ display: "flex", justifyContent: "space-between", gap: 8 }}>
                  <bdi style={{ overflowWrap: "anywhere" }}>{u.name}</bdi>
                  <span>{t(`upload.status.${u.status}`)}</span>
                </div>
                {u.status !== "done" && u.status !== "skipped" && u.status !== "cancelled" && (
                  <>
                    <progress max={Math.max(1, u.size)} value={u.offset} style={{ width: "100%" }} />
                    <p style={{ margin: 0, fontVariantNumeric: "tabular-nums" }}>
                      {formatBytes(u.offset, lang)} / {formatBytes(u.size, lang)}
                      {u.status === "uploading" && u.speed > 0 && ` · ${t("upload.speed", { rate: formatRate(u.speed, lang) })}`}
                    </p>
                  </>
                )}
                {(u.status === "failed" || u.status === "waiting") && (
                  <p role={u.status === "failed" ? "alert" : "status"} style={{ margin: 0 }}>
                    {errorText(u)}
                  </p>
                )}
                {u.status === "awaiting_name" && (
                  <>
                    <form
                      onSubmit={(e: FormEvent) => {
                        e.preventDefault();
                        commit(u);
                      }}
                      style={{ display: "flex", flexWrap: "wrap", gap: 8 }}
                    >
                      <label htmlFor={`name-${u.id}`}>{t("upload.name.label", { name: u.name })}</label>
                      <input
                        id={`name-${u.id}`}
                        type="text"
                        value={nameDraft[u.id] ?? u.suggested}
                        onChange={(e) => setNameDraft({ ...nameDraft, [u.id]: e.target.value })}
                      />
                      <Button type="primary" htmlType="submit">
                        {t("upload.name.submit")}
                      </Button>
                    </form>
                    {u.error && (
                      <p role="alert" style={{ margin: 0 }}>
                        {errorText(u)}
                      </p>
                    )}
                  </>
                )}
                {mismatch === u.id && (
                  <p role="alert" style={{ margin: 0 }}>
                    {t("upload.resume.mismatch")}
                  </p>
                )}
                <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
                  {WAITING.includes(u.status) && (
                    <Button onClick={() => manager.pause(u.id)}>{t("upload.pause")}</Button>
                  )}
                  {u.status === "paused" && <Button onClick={() => manager.resume(u.id)}>{t("upload.resume")}</Button>}
                  {u.status === "failed" && <Button onClick={() => manager.retry(u.id)}>{t("upload.retry")}</Button>}
                  {u.status === "interrupted" && <Button onClick={() => pickResume(u)}>{t("upload.resume.pick")}</Button>}
                  {CANCELLABLE.includes(u.status) && <Button onClick={() => void manager.cancel(u.id)}>{t("upload.cancel")}</Button>}
                  {FINISHED.includes(u.status) && <Button onClick={() => manager.dismiss(u.id)}>{t("upload.dismiss")}</Button>}
                </div>
              </li>
            ))}
          </ul>
        )}
        <input
          ref={resumeInput}
          type="file"
          tabIndex={-1}
          aria-hidden="true"
          onChange={(e) => void onResumePicked(e)}
          style={SR_ONLY}
        />
      </Card>
      {ask !== null && (
        <Conflict
          name={ask}
          batch={active.length > 1}
          onAnswer={(choice, remember) => manager.answerConflict(choice, remember)}
          onCancel={() => manager.answerConflict("skip", false)}
        />
      )}
    </>
  );
}

// SR_ONLY hides an element visually and keeps it for assistive technology. Logical properties only.
const SR_ONLY: React.CSSProperties = {
  position: "absolute",
  width: 1,
  height: 1,
  margin: 0,
  padding: 0,
  overflow: "hidden",
  clip: "rect(0 0 0 0)",
  whiteSpace: "nowrap",
  border: 0,
  opacity: 0,
};
