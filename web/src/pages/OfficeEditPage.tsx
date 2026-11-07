// SPDX-License-Identifier: AGPL-3.0-or-later

// The office editor, full page, in its own tab (decision 72, decision 102). The page asks the API for the editor's address and
// a token, then posts the token into a hidden form whose target is the editor's frame. The token is in the form's body and
// nowhere else: not in the address, not in a log, not in the page's text. The editor's messages are read only from its
// origin and its own frame. The page is drawn outside the app shell, with a slim bar: the app's mark (to the folder), the
// file's name, the save state and a close button. Close ends the tab when a script opened it, and otherwise goes back.

import { X } from "lucide-react";
import { Alert, Button, Input, Modal, Spin } from "antd";
import { useEffect, useRef, useState } from "react";
import { useLocation } from "react-router-dom";
import IconButton from "../components/IconButton";
import Sentence from "../components/Sentence";
import { api } from "../lib/api";
import { ApiError } from "../lib/apiError";

import { brand } from "../lib/brand";
import { endImpersonation } from "../lib/impersonation";
import { session } from "../lib/session";
import { useT } from "../lib/i18n";
import { go } from "../lib/router";
import { useStore } from "../lib/store";
import { statNode } from "../lib/files/api";
import {
  EDITOR_FRAME,
  type SaveAsAsk,
  type SaveState,
  buildEditorForm,
  editorMessage,
  editorOrigin,
  isTrustedEditorMessage,
  officeEdit,
  parseEditorMessage,
  readEditorPath,
  saveStateAfter,
  saveAsFilename,
  saveAsNameOk,
  saveAsSuggestion,
} from "../lib/files/editor";
import NotFoundPage from "./NotFoundPage";

// The editor's answer to POST /api/v1/nodes/{nod}/office. The token is held only until the form has carried it.
interface OfficeAnswer {
  url: string;
  token: string;
  expires_at: string;
}

// The failure the page shows is chosen by the status and never by the server's text.
type Failure = "notFound" | "forbidden" | "notSupported" | "unavailable" | "failed";

type State =
  | { status: "opening" }
  | { status: "ready"; origin: string; folder: string | null; name: string }
  | { status: "failed"; why: Failure };

const NOD = /^nod_[A-Za-z0-9_-]+$/;

// failureOf reads a refusal by its status. The editor is not offered for a folder, a file over the size limit, or a type the
// editor does not open; those are 400 and 413 and read as "not supported" and "failed".
function failureOf(err: unknown): Failure {
  if (!(err instanceof ApiError)) return "failed";
  switch (err.status) {
    case 404:
      return "notFound";
    case 403:
      return "forbidden";
    case 400:
    case 413:
    case 415:
      return "notSupported";
    case 429:
    case 503:
      return "unavailable";
    default:
      return "failed";
  }
}

export default function OfficeEditPage() {
  const { t } = useT();
  // The app's router is one catch-all route (lib/router.ts), so the node is read from the address, not from route params.
  const location = useLocation();
  const nod = /^\/edit\/([^/]+)$/.exec(location.pathname)?.[1] ?? "";
  const on = useStore(officeEdit);
  const { mode, from, tab } = readEditorPath(location.search);
  const [state, setState] = useState<State>({ status: "opening" });
  const [loaded, setLoaded] = useState(false);
  // saveState is the save state the editor's messages give; null until one of them is known (decision 102).
  const [saveState, setSaveState] = useState<SaveState>(null);
  const me = useStore(session);
  const b = useStore(brand);
  // saveAs is the Save As dialog while it is open: the file's name split for it, and the name the person has typed.
  const [saveAs, setSaveAs] = useState<SaveAsAsk | null>(null);
  const [saveAsName, setSaveAsName] = useState("");
  const frameRef = useRef<HTMLIFrameElement | null>(null);
  const holderRef = useRef<HTMLDivElement | null>(null);
  // pending is the form built for the editor, until it is posted. It is cleared after the post.
  const pendingRef = useRef<HTMLFormElement | null>(null);
  const valid = NOD.test(nod);

  useEffect(() => {
    if (!on || !valid) return;
    let cancelled = false;
    setState({ status: "opening" });
    setLoaded(false);
    void (async () => {
      try {
        const node = await statNode(nod);
        const answer = await api<OfficeAnswer>("POST", `/api/v1/nodes/${nod}/office`, { body: { mode } });
        if (cancelled) return;
        const origin = editorOrigin(answer.url);
        if (origin === null) {
          setState({ status: "failed", why: "unavailable" });
          return;
        }
        pendingRef.current = buildEditorForm(document, answer.url, answer.token, Date.parse(answer.expires_at), EDITOR_FRAME);
        setState({ status: "ready", origin, folder: node.folder_id, name: node.name });
      } catch (err) {
        if (!cancelled) setState({ status: "failed", why: failureOf(err) });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [nod, mode, on, valid]);

  // Once the frame is on the page, the form is posted into it, and the form is taken off the pending slot. The post reads
  // the form when it is submitted, so the form then leaves the page, and no script of the page can read the token from it.
  useEffect(() => {
    if (state.status !== "ready" || pendingRef.current === null || holderRef.current === null) return;
    const form = pendingRef.current;
    pendingRef.current = null;
    holderRef.current.appendChild(form);
    form.submit();
    form.remove();
  }, [state]);

  const origin = state.status === "ready" ? state.origin : null;
  // folderHref is the folder the file is in, or the listing when the file is not known yet.
  const folderHref = state.status === "ready" && state.folder ? `/files/${state.folder}` : "/files";
  const back = () => go(from ?? folderHref);
  // close ends the tab when a script opened it (tab=1). The browser refuses a close it did not allow, and then the page is
  // still here after a moment, so it goes back instead; a page opened directly goes back at once.
  const close = () => {
    if (!tab) {
      back();
      return;
    }
    window.close();
    window.setTimeout(() => {
      if (!window.closed) back();
    }, 150);
  };

  // The editor's messages: its close button goes back, and its loading status clears the wait. Any other origin or window
  // is ignored.
  useEffect(() => {
    if (origin === null) return;
    const onMessage = (ev: MessageEvent) => {
      if (!isTrustedEditorMessage({ origin: ev.origin, source: ev.source }, origin, frameRef.current?.contentWindow ?? null)) {
        return;
      }
      const msg = parseEditorMessage(ev.data);
      if (msg === null) return;
      setSaveState((prev) => saveStateAfter(prev, msg));
      if (msg.id === "UI_Close") close();
      // Collabora's postMessage channel opens when the host answers its Initialized status with Host_PostmessageReady.
      // The reply goes to the editor's own origin only, never to "*".
      if (msg.id === "App_LoadingStatus" && msg.values?.Status === "Initialized") {
        frameRef.current?.contentWindow?.postMessage(JSON.stringify({ MessageId: "Host_PostmessageReady" }), origin);
      }
      if (msg.id === "App_LoadingStatus" && msg.values?.Status === "Document_Loaded") setLoaded(true);
      // Save As: the editor asks for the copy's name, the person types it, and confirmSaveAs sends it back (Action_SaveAs).
      if (msg.id === "UI_SaveAs" && state.status === "ready") {
        const ask = saveAsSuggestion(state.name);
        setSaveAsName(ask.base);
        setSaveAs(ask);
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [origin, from, state, tab, folderHref]);

  // confirmSaveAs sends the copy's name to the editor, which makes the copy with PutRelativeFile. The dialog closes; the
  // editor keeps the file it has open.
  const confirmSaveAs = () => {
    if (saveAs === null || origin === null || !saveAsNameOk(saveAsName)) return;
    frameRef.current?.contentWindow?.postMessage(
      editorMessage("Action_SaveAs", { Filename: saveAsFilename(saveAsName, saveAs.ext) }),
      origin,
    );
    setSaveAs(null);
  };

  if (!on || !valid) return <NotFoundPage />;

  const name = state.status === "ready" ? state.name : "";
  // The bar's save state: "View only" for a viewer, otherwise the state the editor's messages gave (none until one is known).
  const stateText =
    mode === "view"
      ? t("files.editor.viewOnly")
      : saveState === "saving"
        ? t("files.editor.saving")
        : saveState === "saved"
          ? t("files.editor.saved")
          : saveState === "unsaved"
            ? t("files.editor.unsaved")
            : null;

  return (
    <div
      data-testid="office-editor"
      style={{ position: "fixed", inset: 0, zIndex: 1000, display: "flex", flexDirection: "column", background: "var(--ant-color-bg-container, #fff)" }}
    >
      {me?.impersonation && (
        // The shell's banner is not drawn on this page, so the banner's words and its way back are drawn here (decision 102).
        <div role="status" style={{ display: "flex", alignItems: "center", gap: 12, padding: "8px 12px", fontWeight: 600 }}>
          <span style={{ flex: "1 1 auto", minWidth: 0, overflowWrap: "anywhere" }}>
            <Sentence template={t("impersonation.banner")} names={{ name: me.user.display_name || me.user.email || "" }} />
          </span>
          <Button size="small" onClick={() => void endImpersonation()} style={{ flex: "none" }}>
            {t("impersonation.back")}
          </Button>
        </div>
      )}
      <header style={{ display: "flex", alignItems: "center", gap: 12, minHeight: 48, padding: "0 12px", borderBottom: "1px solid rgba(0,0,0,0.12)" }}>
        <a
          href={folderHref}
          aria-label={t("files.editor.toFolder")}
          title={t("files.editor.toFolder")}
          style={{ display: "inline-flex", alignItems: "center", gap: 6, flex: "none", color: "inherit", textDecoration: "none", fontWeight: 700 }}
        >
          {b.logo_url && <img src={b.logo_url} alt="" width={24} height={24} />}
          <bdi>{b.name}</bdi>
        </a>
        <p style={{ flex: "1 1 auto", minWidth: 0, margin: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontWeight: 600 }}>
          <bdi>{name}</bdi>
        </p>
        {stateText !== null && (
          <span role="status" style={{ flex: "none", fontSize: 13, color: "var(--ant-color-text-secondary, #5a6b85)" }}>
            {stateText}
          </span>
        )}
        <IconButton label={t("files.editor.close")} onClick={close} icon={<X size={18} aria-hidden="true" focusable="false" />} />
      </header>
      {state.status === "failed" && (
        <div style={{ padding: 16 }}>
          <Alert type="error" showIcon message={t(`files.editor.${state.why}`)} />
        </div>
      )}
      {state.status !== "failed" && !loaded && (
        <div role="status" style={{ display: "grid", placeItems: "center", padding: 24 }}>
          <Spin tip={t("files.editor.opening")} />
        </div>
      )}
      {/* The form is put here to be posted, then removed. It is hidden, and it holds the token only as a form input. */}
      <div ref={holderRef} hidden />
      {state.status === "ready" && (
        <iframe
          ref={frameRef}
          name={EDITOR_FRAME}
          title={t("files.editor.frame")}
          style={{ flex: "1 1 auto", width: "100%", minHeight: 0, border: 0 }}
        />
      )}
      {saveAs !== null && (
        <Modal
          open
          destroyOnClose
          maskClosable={false}
          title={t("files.editor.saveAsTitle")}
          okText={t("files.editor.saveAsOk")}
          cancelText={t("common.cancel")}
          okButtonProps={{ disabled: !saveAsNameOk(saveAsName) }}
          onOk={confirmSaveAs}
          onCancel={() => setSaveAs(null)}
        >
          <Input
            autoFocus
            aria-label={t("files.editor.saveAsName")}
            value={saveAsName}
            addonAfter={saveAs.ext !== "" ? saveAs.ext : undefined}
            onChange={(e) => setSaveAsName(e.target.value)}
            onPressEnter={confirmSaveAs}
          />
          {saveAsName !== "" && !saveAsNameOk(saveAsName) && <p role="alert">{t("files.editor.saveAsInvalid")}</p>}
        </Modal>
      )}
    </div>
  );
}
