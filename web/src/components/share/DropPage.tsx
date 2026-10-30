// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, type DragEvent } from "react";
import { Alert, Button, Input } from "antd";
import { useT } from "../../lib/i18n";
import { ApiError } from "../../lib/apiError";
import { visitorInfo } from "../../lib/sharing/api";
import { renewDelay } from "../../lib/sharing/anyone";
import {
  dropMetadata,
  dropTitle,
  uploaderFields,
  uploaderProblem,
  type DropInfo,
  type Uploader,
  type UploaderProblem,
} from "../../lib/sharing/drop";
import { UploadManager, type Transport, type UploadItem, type UploadStatus } from "../../lib/files/uploads";

// DropPage is the page a visitor of a drop link sees: its title and message, the name and email when the link asks
// for them, and a place to send files. It never lists the folder. It lists only the files this page sent in this
// visit, from its own upload manager, which keeps none of them in the browser.
//
// The credential is the page's, passed in as getters: bearer answers the token of the moment, and expiresAt the time
// it ends. onrenew renews the credential (the link page does it, and answers whether one is held now); onlost says
// the link is gone or the credential cannot be renewed.
//
// The manager is made when the page mounts and halted when it leaves. A remount (StrictMode runs the effects twice)
// makes a new one, so a halted manager is never reused.
export interface DropPageProps {
  bearer: () => string | null;
  expiresAt: () => string;
  onrenew: () => Promise<boolean>;
  onlost: (why: "expired" | "gone") => void;
}

export default function DropPage(props: DropPageProps) {
  const { t, tp } = useT();
  const [info, setInfo] = useState<DropInfo | null>(null);
  const [uploader, setUploader] = useState<Uploader>({ name: "", email: "" });
  const [problem, setProblem] = useState<UploaderProblem | "too_large" | null>(null);
  const [failed, setFailed] = useState(false);
  const [items, setItems] = useState<UploadItem[]>([]);
  const [dragging, setDragging] = useState(false);
  const fileInput = useRef<HTMLInputElement | null>(null);
  const managerRef = useRef<UploadManager | null>(null);
  // The props are read through a ref, so the manager and the requests always use the page's current credential.
  const propsRef = useRef(props);
  propsRef.current = props;
  const infoRef = useRef<DropInfo | null>(null);
  infoRef.current = info;

  useEffect(() => {
    let live = true;

    // The transport gives the manager the credential of each request, read when the request is made, and the drop
    // metadata. fresh renews the credential before a piece when it is near its end, or when forced after a refusal.
    const transport: Transport = {
      bearer: () => propsRef.current.bearer(),
      metadata: (it) => dropMetadata(it.name, it.target.uploader ?? { name: "", email: "" }),
      async fresh(force: boolean): Promise<void> {
        const p = propsRef.current;
        if (!force && renewDelay(p.expiresAt(), Date.now()) > 0) return;
        if (!(await p.onrenew())) throw new ApiError(401, "unauthenticated", {}, 0);
      },
      sink(list: UploadItem[]): void {
        if (!live) return;
        setItems(list);
        // A file the server cannot find any more means the link is gone: the page shows the one dead-link message.
        if (list.some((it) => it.error?.code === "not_found")) propsRef.current.onlost("gone");
      },
    };
    const manager = new UploadManager(() => Date.now(), transport);
    managerRef.current = manager;

    // loadInfo reads what the link says. A refused credential is renewed once and asked again; a dead link is lost.
    const loadInfo = async (again: boolean): Promise<void> => {
      const token = propsRef.current.bearer();
      if (token === null) return;
      try {
        const got = await visitorInfo(token);
        if (live) setInfo(got);
      } catch (err) {
        if (!live) return;
        if (err instanceof ApiError && err.status === 401 && again) {
          if (await propsRef.current.onrenew()) return loadInfo(false);
          return;
        }
        if (err instanceof ApiError && err.status === 404) propsRef.current.onlost("gone");
        else if (err instanceof ApiError && err.status === 401) propsRef.current.onlost("expired");
        else setFailed(true);
      }
    };
    void loadInfo(true);

    return () => {
      live = false;
      manager.halt();
      if (managerRef.current === manager) managerRef.current = null;
    };
  }, []);

  // chooseFiles takes the files a person picked or dropped. The uploader's fields must be right first, and each file
  // is checked against the largest file the link takes.
  const chooseFiles = (list: FileList | File[] | null): void => {
    const current = infoRef.current;
    const manager = managerRef.current;
    if (list === null || current === null || manager === null) return;
    const files = Array.from(list);
    if (files.length === 0) return;
    const p = uploaderProblem(current.ask_name, uploader);
    if (p !== null) {
      setProblem(p);
      return;
    }
    if (files.some((f) => f.size > current.max_file_size)) {
      setProblem("too_large");
      return;
    }
    setProblem(null);
    const snapshot = uploaderFields(uploader);
    manager.add(files.map((file) => ({ file, target: { folder: "", uploader: snapshot } })));
    if (fileInput.current) fileInput.current.value = "";
  };

  const onDrop = (e: DragEvent<HTMLDivElement>): void => {
    e.preventDefault();
    setDragging(false);
    chooseFiles(e.dataTransfer?.files ?? null);
  };

  const onDragOver = (e: DragEvent<HTMLDivElement>): void => {
    e.preventDefault();
    setDragging(true);
  };

  const statusText = (s: UploadStatus): string => {
    switch (s) {
      case "uploading":
        return t("drop.status.uploading");
      case "waiting":
        return t("drop.status.waiting");
      case "done":
        return t("drop.status.done");
      case "failed":
      case "cancelled":
        return t("drop.status.failed");
      default:
        return t("drop.status.queued");
    }
  };

  const problemText = (p: UploaderProblem | "too_large"): string => {
    switch (p) {
      case "name_required":
        return t("drop.err.nameRequired");
      case "email_required":
        return t("drop.err.emailRequired");
      case "name_long":
        return t("drop.err.nameLong");
      case "email_long":
        return t("drop.err.emailLong");
      case "email_invalid":
        return t("drop.err.emailInvalid");
      case "too_large":
        return t("drop.err.tooLarge");
    }
  };

  // failureText says what a failed file means, by its code. The server's text is never shown.
  const failureText = (err: ApiError | null): string => {
    if (err === null) return t("error.generic");
    if (err.code === "too_many_uploads") return t("drop.err.tooMany");
    if (err.code === "rate_limited") return tp("error.rateLimited", 1);
    if (err.code === "too_large") return t("drop.err.tooLarge");
    if (err.status === 0) return t("error.network");
    return t("error.generic");
  };

  const percent = (it: UploadItem): number => (it.size === 0 ? 100 : Math.min(100, Math.round((it.offset / it.size) * 100)));

  if (failed) return <Alert type="error" showIcon role="alert" message={t("error.generic")} />;
  if (info === null) {
    return (
      <p role="status" style={{ color: "var(--ant-color-text-secondary)" }}>
        {t("openlink.working")}
      </p>
    );
  }

  const title = dropTitle(info, t("drop.defaultTitle"));
  return (
    <div>
      <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>{title}</h1>
      {info.message.trim() !== "" && <p style={{ whiteSpace: "pre-line" }}>{info.message}</p>}

      {problem !== null && <Alert type="error" showIcon role="alert" message={problemText(problem)} style={{ marginBottom: 12 }} />}

      <form onSubmit={(e) => e.preventDefault()} style={{ display: "grid", gap: 12 }}>
        <div style={{ display: "grid", gap: 12, gridTemplateColumns: "repeat(auto-fit, minmax(12rem, 1fr))" }}>
          <div style={{ display: "grid", gap: 6 }}>
            <label htmlFor="drop-name">{t(info.ask_name ? "drop.name" : "drop.nameOptional")}</label>
            <Input
              id="drop-name"
              autoComplete="name"
              required={info.ask_name}
              value={uploader.name}
              onChange={(e) => setUploader((u) => ({ ...u, name: e.target.value }))}
            />
          </div>
          <div style={{ display: "grid", gap: 6 }}>
            <label htmlFor="drop-email">{t(info.ask_name ? "drop.email" : "drop.emailOptional")}</label>
            <Input
              id="drop-email"
              type="email"
              autoComplete="email"
              required={info.ask_name}
              value={uploader.email}
              onChange={(e) => setUploader((u) => ({ ...u, email: e.target.value }))}
            />
            <p style={{ margin: 0, fontSize: 14, color: "var(--ant-color-text-secondary)" }}>{t("drop.emailHint")}</p>
          </div>
        </div>
      </form>

      <div
        className="dropzone"
        role="region"
        aria-label={t("drop.zone")}
        onDragOver={onDragOver}
        onDragLeave={() => setDragging(false)}
        onDrop={onDrop}
        style={{
          display: "grid",
          gap: 8,
          justifyItems: "center",
          padding: 16,
          textAlign: "center",
          border: `1px ${dragging ? "solid" : "dashed"} var(--ant-color-text-secondary)`,
          borderRadius: 4,
        }}
      >
        <p style={{ margin: 0 }}>{t("drop.zoneHint")}</p>
        <Button type="primary" onClick={() => fileInput.current?.click()}>
          {t("drop.choose")}
        </Button>
        <input
          ref={fileInput}
          type="file"
          multiple
          tabIndex={-1}
          aria-hidden="true"
          style={{ display: "none" }}
          onChange={(e) => chooseFiles(e.currentTarget.files)}
        />
      </div>

      {items.length > 0 && (
        <ul aria-label={t("drop.sent")} className="rows" style={{ display: "grid", gap: 8, margin: 0, padding: 0, listStyle: "none" }}>
          {items.map((it) => (
            <li key={it.id} className="row" style={{ display: "flex", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
              <span className="name" style={{ overflowWrap: "anywhere", minInlineSize: 0 }}>
                {it.name}
              </span>
              <span className="muted" style={{ color: "var(--ant-color-text-secondary)" }}>
                {statusText(it.status)}
              </span>
              {it.status === "failed" && <span style={{ color: "var(--ant-color-error)" }}>{failureText(it.error)}</span>}
              {(it.status === "uploading" || it.status === "queued" || it.status === "waiting") && (
                <>
                  <progress max={100} value={percent(it)} style={{ width: 120 }}>
                    {percent(it)}%
                  </progress>
                  <Button onClick={() => void managerRef.current?.cancel(it.id)}>{t("drop.cancel")}</Button>
                </>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
