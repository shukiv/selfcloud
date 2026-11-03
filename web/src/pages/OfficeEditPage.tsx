// SPDX-License-Identifier: AGPL-3.0-or-later

// The office editor, full window, inside the app (decision 72). The page asks the API for the editor's address and a
// token, then posts the token into a hidden form whose target is the editor's frame. The token is in the form's body and
// nowhere else: not in the address, not in a log, not in the page's text. The editor's messages are read only from its
// origin and its own frame; its close button returns to the place the file was opened from.

import { ArrowLeft, Pencil, BookOpen } from "lucide-react";
import { Alert, Spin } from "antd";
import { useEffect, useRef, useState } from "react";
import { useLocation } from "react-router-dom";
import IconButton from "../components/IconButton";
import { api } from "../lib/api";
import { ApiError } from "../lib/apiError";

import { useT } from "../lib/i18n";
import { go } from "../lib/router";
import { useStore } from "../lib/store";
import { statNode } from "../lib/files/api";
import {
  EDITOR_FRAME,
  type EditorMode,
  buildEditorForm,
  editorOrigin,
  isTrustedEditorMessage,
  officeEdit,
  parseEditorMessage,
  readEditorPath,
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
  const { mode, from } = readEditorPath(location.search);
  const [state, setState] = useState<State>({ status: "opening" });
  const [loaded, setLoaded] = useState(false);
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
  const back = () => go(from ?? (state.status === "ready" && state.folder ? `/files/${state.folder}` : "/files"));

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
      if (msg.id === "UI_Close") back();
      if (msg.id === "App_LoadingStatus" && msg.values?.Status === "Document_Loaded") setLoaded(true);
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [origin, from, state]);

  if (!on || !valid) return <NotFoundPage />;

  const modeLabel: EditorMode = mode;
  const Mode = modeLabel === "view" ? BookOpen : Pencil;
  const name = state.status === "ready" ? state.name : "";

  return (
    <div
      data-testid="office-editor"
      style={{ position: "fixed", inset: 0, zIndex: 1000, display: "flex", flexDirection: "column", background: "var(--ant-color-bg-container, #fff)" }}
    >
      <header style={{ display: "flex", alignItems: "center", gap: 8, minHeight: 56, padding: "0 12px", borderBottom: "1px solid rgba(0,0,0,0.12)" }}>
        <IconButton label={t("files.editor.back")} onClick={back} icon={<ArrowLeft className="icon-dir" size={18} aria-hidden="true" focusable="false" />} />
        <p style={{ flex: "1 1 auto", minWidth: 0, margin: 0, overflowWrap: "anywhere", fontWeight: 600 }}>
          <bdi>{name}</bdi>
        </p>
        <span style={{ display: "inline-flex", alignItems: "center", gap: 4 }}>
          <Mode size={16} aria-hidden="true" focusable="false" />
          {modeLabel === "view" ? t("files.editor.viewing") : t("files.editor.editing")}
        </span>
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
    </div>
  );
}
