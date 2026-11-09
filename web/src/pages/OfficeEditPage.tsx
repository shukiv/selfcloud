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
import { childrenPage, statNode } from "../lib/files/api";
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
import {
  SAVE_AS_WINDOW_MS,
  announceNodeChanged,
  copyCandidates,
  decodeEditorName,
  displayName,
  isSameName,
  listDirectory,
} from "../lib/files/officeSync";
import type { Node } from "../lib/files/types";
import { sessionChanged } from "../lib/session";
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
  | { status: "ready"; origin: string; folder: string | null; parent: string | null; name: string }
  | { status: "failed"; why: Failure };

// SaveAsPending is a Save As this page started: the name it asked for, and when it was asked (decision 107).
// before is the set of the folder's file ids when the Save As was asked (empty until then); at is when the request went to the
// editor (0 until then). answered says the editor's answer naming the file has come; dead says the window has ended.
interface SaveAsPending {
  filename: string;
  at: number;
  before: ReadonlySet<string>;
  answered: boolean;
  dead: boolean;
  timer: ReturnType<typeof setTimeout> | null;
}

// Notice is a note the bar shows about the session or a copy (ef1). Its words are the catalogue key after files.editor.
type Notice = "sessionEnded" | "tokenRefreshFailed" | "copyNotShown" | "copyNotStarted";

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
  const [notice, setNotice] = useState<Notice | null>(null);
  const frameRef = useRef<HTMLIFrameElement | null>(null);
  const holderRef = useRef<HTMLDivElement | null>(null);
  // pending is the form built for the editor, until it is posted. It is cleared after the post.
  const pendingRef = useRef<HTMLFormElement | null>(null);
  const valid = NOD.test(nod);
  // ef1: the node the editor has now (the copy, after a Save As), the file's name as the server last gave it, the state of
  // the editor's save (read by the message handler), the latest state for the handler, and the session watch of this editor.
  const nodRef = useRef(nod);
  const authNameRef = useRef("");
  const saveRef = useRef<SaveState>(null);
  const stateRef = useRef(state);
  const seqRef = useRef(0);
  const renewingRef = useRef(false);
  const watchRef = useRef<(() => boolean) | null>(null);
  const originRef = useRef<string | null>(null);
  const modeRef = useRef(mode);
  modeRef.current = mode;
  // saveAsRef is the Save As this page started and is waiting for its copy (decision 107), with its timer on the window.
  const saveAsRef = useRef<SaveAsPending | null>(null);
  // copyBusy is true while a Save As is pending: the Save As dialog cannot start another one (decision 107).
  const [copyBusy, setCopyBusy] = useState(false);

  // copyLostRef is true once a Save As's copy could not be bound (copyNotShown): the editor may then show the copy, so the page
  // mints no token for the original (renew). It is cleared when the page opens a file, or binds a copy.
  const copyLostRef = useRef(false);
  // titleRef runs one title check at a time: a title that comes while one is in flight waits, and the latest waiting name runs
  // once after it, if it differs from the name in flight.
  const titleRef = useRef<{ busy: boolean; queued: string | null }>({ busy: false, queued: null });

  // finishSaveAs ends a window of a Save As: the copy is bound, not found, superseded, or timed out. A window ends once, and only
  // the window that is still the pending one clears the busy flag, so an old window never touches a newer one.
  const finishSaveAs = (win: SaveAsPending) => {
    if (win.dead) return;
    win.dead = true;
    if (win.timer !== null) {
      clearTimeout(win.timer);
      win.timer = null;
    }
    if (saveAsRef.current === win) {
      saveAsRef.current = null;
      setCopyBusy(false);
    }
  };
  // copyLost ends a window whose copy the page could not show, and stops renewals for the original.
  const copyLost = (win: SaveAsPending) => {
    finishSaveAs(win);
    copyLostRef.current = true;
    setNotice("copyNotShown");
  };
  // supersede ends the pending window, if any, because the page moves on to another file (a read, a new node).
  const supersede = () => {
    if (saveAsRef.current !== null) finishSaveAs(saveAsRef.current);
  };

  useEffect(() => () => supersede(), []);

  useEffect(() => {
    stateRef.current = state;
  }, [state]);

  useEffect(() => {
    if (!on || !valid) return;
    let cancelled = false;
    // A new node ends the Save As window and any bind in flight: neither may move the page to the old folder (decision 107).
    supersede();
    seqRef.current++;
    copyLostRef.current = false;
    nodRef.current = nod;
    watchRef.current = sessionChanged();
    setState({ status: "opening" });
    setLoaded(false);
    setNotice(null);
    saveRef.current = null;
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
        authNameRef.current = node.name;
        originRef.current = origin;
        pendingRef.current = buildEditorForm(document, answer.url, answer.token, Date.parse(answer.expires_at), EDITOR_FRAME);
        setState({ status: "ready", origin, folder: node.folder_id, parent: node.parent_id, name: node.name });
      } catch (err) {
        if (!cancelled) setState({ status: "failed", why: failureOf(err) });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [nod, mode, on, valid]);

  // applyName shows the name a file has when the server says so, and tells the other tabs when it changed.
  const applyName = (n: Node) => {
    const changed = n.name !== authNameRef.current;
    authNameRef.current = n.name;
    setState((s) => (s.status === "ready" ? { ...s, name: n.name, folder: n.folder_id, parent: n.parent_id } : s));
    if (changed) announceNodeChanged(n.id, n.folder_id, n.parent_id);
  };

  // bindCopy moves the page to the copy that a Save As this page started has made (decision 107). It runs only while that
  // Save As's window is open (win), and only for a candidate of it (copyCandidates): a file that was not in the folder when the
  // request was asked, named exactly as the request or its free form, made at or after the request. signal is the name the
  // editor gave (the title's or the answer's); it picks among the candidates and never makes one. last says the answer has
  // come, so the window ends with this check whatever it finds.
  const bindCopy = async (win: SaveAsPending, signal: string, last: boolean) => {
    if (win.at === 0) return; // the request has not gone to the editor yet: nothing is a candidate
    const seq = ++seqRef.current;
    let cur: Node;
    try {
      cur = await statNode(nodRef.current);
    } catch {
      if (win.dead || seq !== seqRef.current) return;
      if (last) copyLost(win);
      return;
    }
    if (win.dead || seq !== seqRef.current) return;
    let items: Node[] = [];
    if (cur.parent_id !== null) {
      try {
        items = (await childrenPage(cur.folder_id, cur.parent_id, undefined, { key: "modified", dir: "desc" })).items;
      } catch {
        items = [];
      }
    }
    if (win.dead || seq !== seqRef.current) return;
    const want = signal.normalize("NFC");
    const hit = copyCandidates(items, win.filename, win.at, win.before).find((n) => n.name.normalize("NFC") === want);
    if (hit === undefined) {
      if (last) copyLost(win);
      return;
    }
    finishSaveAs(win);
    copyLostRef.current = false;
    nodRef.current = hit.id;
    window.history.replaceState(window.history.state, "", `/edit/${hit.id}${window.location.search}`);
    authNameRef.current = hit.name;
    saveRef.current = null;
    setSaveState(null);
    setNotice(null);
    setState((s) => (s.status === "ready" ? { ...s, name: hit.name, folder: hit.folder_id, parent: hit.parent_id } : s));
    announceNodeChanged(hit.id, hit.folder_id, hit.parent_id);
  };

  // bindTitle runs bindCopy for a title. One check is in flight at a time: a title that comes meanwhile waits, and the latest
  // waiting name runs once after it, only if it differs from the name in flight (decision 107).
  const bindTitle = (win: SaveAsPending, name: string) => {
    const r = titleRef.current;
    if (r.busy) {
      r.queued = name;
      return;
    }
    r.busy = true;
    void bindCopy(win, name, false).finally(() => {
      r.busy = false;
      const next = r.queued;
      r.queued = null;
      if (next !== null && next !== name && saveAsRef.current === win && !win.answered) bindTitle(win, next);
    });
  };

  // readName re-reads the file after a rename the editor reported. A rename never makes a copy, so a name the page cannot read
  // is not a Save As: the file's own read is shown, and nothing else is looked for.
  const readName = async () => {
    const seq = ++seqRef.current;
    supersede();
    try {
      const cur = await statNode(nodRef.current);
      if (seq === seqRef.current) applyName(cur);
    } catch {
      // The name stays as it was; the next read or message shows it again.
    }
  };

  // renew answers the editor's request for a fresh token (App_TokenExpiring, App_TokenExpired; decision 107). The token is for
  // the mode the page opened with, and for the node the editor has now, and it goes to the editor's own origin only. A page
  // whose browser session has changed mints nothing: the session-ended note says so.
  const renew = async () => {
    if (renewingRef.current) return;
    if (copyLostRef.current) {
      setNotice("tokenRefreshFailed");
      return;
    }
    if (watchRef.current?.() ?? false) {
      setNotice("sessionEnded");
      return;
    }
    renewingRef.current = true;
    try {
      const answer = await api<OfficeAnswer>("POST", `/api/v1/nodes/${nodRef.current}/office`, { body: { mode: modeRef.current } });
      if (watchRef.current?.() ?? false) {
        setNotice("sessionEnded");
        return;
      }
      const target = originRef.current;
      if (target === null || editorOrigin(answer.url) !== target) {
        setNotice("tokenRefreshFailed");
        return;
      }
      frameRef.current?.contentWindow?.postMessage(
        editorMessage("Reset_Access_Token", { token: answer.token, ttl: Date.parse(answer.expires_at) }),
        target,
      );
      setNotice(null);
    } catch {
      setNotice(watchRef.current?.() ?? false ? "sessionEnded" : "tokenRefreshFailed");
    } finally {
      renewingRef.current = false;
    }
  };

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
      const prevSave = saveRef.current;
      const nextSave = saveStateAfter(prevSave, msg);
      saveRef.current = nextSave;
      setSaveState(nextSave);
      // A save that made the file newer (unsaved, then saved) changes its size and time: the other tabs read it again.
      if (prevSave === "unsaved" && nextSave === "saved") {
        const cur = stateRef.current;
        if (cur.status === "ready" && cur.folder !== null) announceNodeChanged(nodRef.current, cur.folder, cur.parent);
      }
      if (msg.id === "File_Rename") {
        const sent = decodeEditorName(msg.values?.NewName);
        if (sent !== null) {
          const shown = displayName(sent, authNameRef.current);
          setState((s) => (s.status === "ready" ? { ...s, name: shown } : s));
        }
        void readName();
      }
      // A title is a name the editor shows. While a Save As of this page is pending it may name the copy; otherwise it only
      // tells the page to read its own file again, which never moves the page (decision 107).
      if (msg.id === "Doc_Title" && typeof msg.values?.DocName === "string" && msg.values.DocName !== "") {
        const pending = saveAsRef.current;
        if (pending !== null) {
          if (!pending.answered) bindTitle(pending, msg.values.DocName);
        } else if (!isSameName(authNameRef.current, msg.values.DocName)) void readName();
      }
      // The answer to a Save As this page started names the copy (the request asked for it with Notify). Only an answer that
      // names a file is that answer: a plain save's answer has no name, and neither has a failed Save As (Socket.ts), so
      // those leave the window open, and its timer ends it. Without a pending Save As, the answer is ignored.
      if (msg.id === "Action_Save_Resp") {
        const pending = saveAsRef.current;
        const named = typeof msg.values?.fileName === "string" && msg.values.fileName !== "" ? msg.values.fileName : null;
        // Before the request has gone to the editor (at 0), an answer cannot be the copy's: it is not consumed.
        if (pending !== null && !pending.answered && pending.at !== 0 && named !== null) {
          pending.answered = true;
          void bindCopy(pending, named, true);
        }
      }
      if (msg.id === "App_TokenExpiring" || msg.id === "App_TokenExpired") void renew();
      if (msg.id === "UI_Close") close();
      // Collabora's postMessage channel opens when the host answers its Initialized status with Host_PostmessageReady.
      // The reply goes to the editor's own origin only, never to "*".
      if (msg.id === "App_LoadingStatus" && msg.values?.Status === "Initialized") {
        frameRef.current?.contentWindow?.postMessage(JSON.stringify({ MessageId: "Host_PostmessageReady" }), origin);
      }
      if (msg.id === "App_LoadingStatus" && msg.values?.Status === "Document_Loaded") setLoaded(true);
      // Save As: the editor asks for the copy's name, the person types it, and confirmSaveAs sends it back (Action_SaveAs).
      if (msg.id === "UI_SaveAs" && stateRef.current.status === "ready") {
        const ask = saveAsSuggestion(stateRef.current.name);
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
    if (saveAs === null || origin === null || saveAsRef.current !== null || !saveAsNameOk(saveAsName)) return;
    const filename = saveAsFilename(saveAsName, saveAs.ext).normalize("NFC");
    // The window opens with the request and ends at the answer, or after SAVE_AS_WINDOW_MS. Before the request goes to the
    // editor, the folder's files are read in full: the copy must be one that was not there (copyCandidates). A folder that
    // cannot be read in full is no basis, so the Save As is not asked and the page says so.
    const win: SaveAsPending = { filename, at: 0, before: new Set(), answered: false, dead: false, timer: null };
    saveAsRef.current = win;
    win.timer = setTimeout(() => finishSaveAs(win), SAVE_AS_WINDOW_MS);
    setCopyBusy(true);
    setSaveAs(null);
    void (async () => {
      let items: Node[] | null = null;
      try {
        const node = await statNode(nodRef.current);
        if (node.parent_id !== null) {
          const dir = node.parent_id;
          items = await listDirectory((cursor) => childrenPage(node.folder_id, dir, cursor, { key: "modified", dir: "desc" }));
        }
      } catch {
        items = null;
      }
      if (win.dead) return;
      if (items === null) {
        finishSaveAs(win);
        setNotice("copyNotStarted");
        return;
      }
      win.before = new Set(items.map((n) => n.id));
      win.at = Date.now();
      frameRef.current?.contentWindow?.postMessage(editorMessage("Action_SaveAs", { Filename: filename, Notify: true }), origin);
    })();
  };

  if (!on || !valid) return <NotFoundPage />;

  const name = state.status === "ready" ? state.name : "";
  // The bar's save state: "View only" for a viewer, otherwise the state the editor's messages gave (none until one is known).
  const stateText =
    mode === "view"
      ? t("files.editor.viewOnly")
      : saveState === "saved"
        ? t("files.editor.saved")
        : saveState === "unsaved"
          ? t("files.editor.unsaved")
          : saveState === "failed"
            ? t("files.editor.saveFailed")
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
      {notice !== null && (
        <div style={{ padding: "8px 12px" }}>
          <Alert type="warning" showIcon message={t(`files.editor.${notice}`)} />
        </div>
      )}
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
          okButtonProps={{
            disabled: copyBusy || !saveAsNameOk(saveAsName),
          }}
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
          {copyBusy && <p role="status">{t("files.editor.saveAsBusy")}</p>}
        </Modal>
      )}
    </div>
  );
}
