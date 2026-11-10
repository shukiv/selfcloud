// SPDX-License-Identifier: AGPL-3.0-or-later

// The code editor, full page, in its own tab (design-ce 3.2, 3.4, 4.2, 4.3, decision 124). The address is /edit/<node>, the
// one the office editor uses; App sends a code file here and an office file to OfficeEditPage. The page reads the file (a GET
// of the whole content, which keeps its ETag), shows it in CodeMirror, and saves on an explicit save only: a button, or Ctrl or
// Cmd+S. A save is a PUT with If-Match set to the ETag read, and the stored type sent unchanged. A viewer, or an editor who
// opened it by its address with no right to save, reads the text with "View only" and no Save.
//
// The page is drawn outside the app shell, with a slim bar: the app's mark (to the folder), the file's name, the save state,
// then icon buttons with tooltips (Save, Find, Wrap lines, Close). Close ends the tab a script opened, and otherwise goes back.
// A save that did not happen says why, with the draft kept on the page; a conflict offers a reload (the full conflict dialog and
// keep-as-a-copy are the next slice).
import { useEffect, useRef, useState } from "react";
import { useLocation } from "react-router-dom";
import { Alert, Button, Popconfirm, Spin } from "antd";
import { Save, Search, WrapText, X } from "lucide-react";
import CodeEditor, { type EditorHandle } from "../components/files/CodeEditor";
import IconButton from "../components/IconButton";
import { ApiError } from "../lib/apiError";
import { brand } from "../lib/brand";
import { decodeText, sniffWhole } from "../lib/code/encoding";
import { encodeDraft, EDIT_LIMIT, lineBreakOf, normalizeText } from "../lib/code/edit";
import { packOf, type Pack } from "../lib/code/names";
import { codeText } from "../lib/code/text";
import { readWhole, saveText, type SaveFailure } from "../lib/code/save";
import { wraps } from "../lib/code/lines";
import { statNode } from "../lib/files/api";
import { readEditorPath } from "../lib/files/editor";
import { startDownload } from "../lib/files/ops";
import { can, downloadAllowed } from "../lib/files/roles";
import type { Node } from "../lib/files/types";
import { useLang, useT } from "../lib/i18n";
import { go } from "../lib/router";
import { useStore } from "../lib/store";
import { useModeOrLight } from "../theme/theme";

// The failure the page shows when the file cannot be opened, chosen by the status and never by the server's text.
type OpenFailure = "notFound" | "forbidden" | "failed";

// SaveState is the bar's save state. null is nothing to say yet.
type SaveState = "saved" | "unsaved" | "saving" | "failed" | null;

// Notice is a note in the page's notice area: a save that did not happen (its SaveFailure), a file that is not UTF-8, one too
// large to edit, or one that is not text at all (binary: a NUL byte anywhere in the file, MED-2).
type Notice = SaveFailure | "notUtf8" | "tooLargeToEdit" | "binary" | null;

// Ready is an open file: its text as the editor starts with it, and what the save needs to write it back (the BOM and the line
// break the file had). initial changes with each reload, and key remounts the editor with it.
interface Ready {
  name: string;
  folder: string | null;
  canEdit: boolean;
  utf8: boolean;
  bom: boolean;
  sep: "\n" | "\r\n" | "\r";
  initial: string;
  pack: Pack;
  wrap: boolean;
  key: number;
  node: Node;
}

type Page =
  | { s: "opening" }
  | { s: "failed"; why: OpenFailure }
  | { s: "tooLarge"; name: string; node: Node }
  | { s: "binary"; name: string; node: Node }
  | { s: "ready"; ready: Ready };

// failureOf reads a refusal of the open by its status.
function failureOf(err: unknown): OpenFailure {
  if (!(err instanceof ApiError)) return "failed";
  if (err.status === 404) return "notFound";
  if (err.status === 403) return "forbidden";
  return "failed";
}

// NOD is the node in the address, as the API gives it.
const NOD = /^nod_[A-Za-z0-9_-]+$/;

export default function CodeEditPage() {
  const { t } = useT();
  const lang = useLang();
  const ct = codeText(lang);
  const dark = useModeOrLight() === "dark";
  const location = useLocation();
  const nod = /^\/edit\/([^/]+)$/.exec(location.pathname)?.[1] ?? "";
  const valid = NOD.test(nod);
  const { mode, from, tab } = readEditorPath(location.search);
  const b = useStore(brand);

  const [page, setPage] = useState<Page>({ s: "opening" });
  const [saveState, setSaveState] = useState<SaveState>(null);
  const [notice, setNotice] = useState<Notice>(null);
  // closed is set when a save finds the file gone or the person no longer allowed to write it: the text stays, read-only.
  const [closed, setClosed] = useState(false);
  const [wrap, setWrap] = useState(true);
  const handle = useRef<EditorHandle | null>(null);
  // The file's ETag and stored type, as the last read or save gave them; the text it was last saved as (LF line breaks).
  const file = useRef({ etag: "", mime: "application/octet-stream" });
  const saved = useRef("");
  const saving = useRef(false);
  // openGen counts the opens. A save answers for the open it was sent in: an answer for a file the page has since left is not
  // shown, and does not touch the new file's state (LOW-2).
  const openGen = useRef(0);
  const keyRef = useRef(0);
  // The latest page and save, read by the editor's keys, which are bound once for the editor's text.
  const readyRef = useRef<Ready | null>(null);

  // The page opens the file each time the address changes: a read with no Range, so the whole file, and its ETag kept.
  useEffect(() => {
    if (!valid) return;
    let cancelled = false;
    openGen.current++;
    readyRef.current = null;
    handle.current = null;
    // The new file starts with no save in flight and no ETag, saved text or type of the last file (LOW-2).
    saving.current = false;
    saved.current = "";
    file.current = { etag: "", mime: "application/octet-stream" };
    setPage({ s: "opening" });
    setSaveState(null);
    setNotice(null);
    setClosed(false);
    void (async () => {
      try {
        const n = await statNode(nod);
        if (cancelled) return;
        if (n.size > EDIT_LIMIT) {
          setPage({ s: "tooLarge", name: n.name, node: n });
          setNotice("tooLargeToEdit");
          return;
        }
        const got = await readWhole(nod);
        if (cancelled) return;
        // The editor has the whole file, so the sniff reads all of it: a NUL byte anywhere means it is not text (MED-2).
        if (sniffWhole(got.bytes) === "binary") {
          setPage({ s: "binary", name: n.name, node: n });
          setNotice("binary");
          return;
        }
        const d = decodeText(got.bytes, false);
        file.current = { etag: got.etag, mime: n.mime_type ?? "application/octet-stream" };
        const initial = normalizeText(d.text);
        saved.current = initial;
        const canEdit = mode === "edit" && can.edit(n.role);
        const ready: Ready = {
          name: n.name,
          folder: n.folder_id,
          canEdit,
          utf8: d.utf8,
          bom: d.bom,
          sep: lineBreakOf(d.text),
          initial,
          pack: packOf(n.name) ?? "text",
          wrap: wraps(initial),
          key: ++keyRef.current,
          node: n,
        };
        readyRef.current = ready;
        setPage({ s: "ready", ready });
        if (!d.utf8) setNotice("notUtf8");
      } catch (err) {
        if (!cancelled) setPage({ s: "failed", why: failureOf(err) });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [nod, mode, valid]);

  const ready = page.s === "ready" ? page.ready : null;
  // The editor is read-only for a viewer, for a file that is not UTF-8, and once a save found the file gone or forbidden.
  const readOnly = ready === null || !ready.canEdit || !ready.utf8 || closed;

  // onChange sets the save state from the text: unsaved while it differs from the saved text.
  const onChange = () => {
    const cur = handle.current?.text();
    if (cur === undefined || saving.current) return;
    setSaveState(cur === saved.current ? "saved" : "unsaved");
  };

  // save writes the draft with If-Match and the stored type. It sends nothing when the text is the saved text, or while a save is
  // in flight, or when the file is not the person's to write. A draft the server refuses stays on the page.
  const save = async () => {
    const r = readyRef.current;
    if (r === null || !r.canEdit || !r.utf8 || closed || saving.current) return;
    const text = handle.current?.text();
    if (text === undefined || text === saved.current) return;
    const bytes = encodeDraft(text, r.bom, r.sep);
    const gen = openGen.current;
    saving.current = true;
    setSaveState("saving");
    setNotice(null);
    const got = await saveText(nod, file.current.etag, file.current.mime, bytes);
    if (gen !== openGen.current) return;
    saving.current = false;
    if (got.ok) {
      file.current = { etag: got.node.etag ?? "", mime: got.node.mime_type ?? file.current.mime };
      saved.current = text;
      const now = handle.current?.text();
      setSaveState(now === text ? "saved" : "unsaved");
      return;
    }
    setSaveState("failed");
    setNotice(got.why);
    if (got.why === "forbidden" || got.why === "gone") setClosed(true);
  };
  // The editor's Ctrl or Cmd+S runs the latest save.
  const saveRef = useRef(save);
  saveRef.current = save;

  // reload throws the draft away and shows the file as it is on the server now (the conflict's reload choice, asked first).
  const reload = async () => {
    const r = readyRef.current;
    if (r === null) return;
    try {
      const got = await readWhole(nod);
      const d = decodeText(got.bytes, false);
      file.current = { ...file.current, etag: got.etag };
      const initial = normalizeText(d.text);
      saved.current = initial;
      const next: Ready = { ...r, initial, utf8: d.utf8, bom: d.bom, sep: lineBreakOf(d.text), wrap: wraps(initial), key: ++keyRef.current };
      readyRef.current = next;
      setPage({ s: "ready", ready: next });
      setSaveState(null);
      setNotice(d.utf8 ? null : "notUtf8");
    } catch (err) {
      const why = failureOf(err);
      setNotice(why === "forbidden" ? "forbidden" : why === "notFound" ? "gone" : "failed");
      if (why === "forbidden" || why === "notFound") setClosed(true);
    }
  };

  const folderHref = ready?.folder ? `/files/${ready.folder}` : "/files";
  const back = () => go(from ?? folderHref);
  // close ends the tab when a script opened it (tab=1). The browser refuses a close it did not allow, and then the page is still
  // here after a moment, so it goes back instead; a page opened directly goes back at once.
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

  if (!valid) return null;

  const canWrite = ready !== null && ready.canEdit && ready.utf8 && !closed;
  // The bar's save state: "View only" for a file the person may only read, and otherwise the state of the save.
  const stateText =
    ready !== null && !ready.canEdit
      ? t("files.editor.viewOnly")
      : saveState === "saving"
        ? ct.saving
        : saveState === "saved"
          ? t("files.editor.saved")
          : saveState === "unsaved"
            ? t("files.editor.unsaved")
            : saveState === "failed"
              ? t("files.editor.saveFailed")
              : null;
  const name = page.s === "ready" ? page.ready.name : page.s === "tooLarge" || page.s === "binary" ? page.name : "";
  // The download rule is the preview's (lib/files/roles.ts): a no-download file may still be downloaded by a contributor and above.
  const canDownload =
    (page.s === "ready" && downloadAllowed(page.ready.node)) ||
    ((page.s === "tooLarge" || page.s === "binary") && downloadAllowed(page.node));
  const nodeId = page.s === "ready" ? page.ready.node.id : page.s === "tooLarge" || page.s === "binary" ? page.node.id : "";

  return (
    <div
      data-testid="code-editor-page"
      style={{ position: "fixed", inset: 0, zIndex: 1000, display: "flex", flexDirection: "column", background: "var(--ant-color-bg-container, #fff)" }}
    >
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
        {canWrite && (
          <IconButton label={ct.save} onClick={() => void saveRef.current()} icon={<Save size={18} aria-hidden="true" focusable="false" />} />
        )}
        {ready !== null && (
          <IconButton label={ct.find} onClick={() => handle.current?.find()} className="find-btn" icon={<Search size={18} aria-hidden="true" focusable="false" />} />
        )}
        {ready !== null && (
          <IconButton
            label={ct.wrap}
            pressed={wrap}
            aria-pressed={wrap}
            onClick={() => setWrap((w) => !w)}
            icon={<WrapText size={18} aria-hidden="true" focusable="false" />}
          />
        )}
        <IconButton label={t("files.editor.close")} onClick={close} icon={<X size={18} aria-hidden="true" focusable="false" />} />
      </header>
      {notice !== null && (
        <div style={{ padding: "8px 12px" }}>
          <Alert
            type={notice === "notUtf8" ? "warning" : "error"}
            showIcon
            role={notice === "notUtf8" ? "status" : "alert"}
            message={noticeText(notice, ct)}
            action={noticeAction(notice)}
          />
        </div>
      )}
      {notice === null && page.s === "opening" && (
        <div role="status" style={{ display: "grid", placeItems: "center", padding: 24 }}>
          <Spin tip={t("files.editor.opening")} />
        </div>
      )}
      {page.s === "failed" && (
        <div style={{ padding: 16 }}>
          <Alert type="error" showIcon role="alert" message={t(`files.editor.${page.why === "failed" ? "failed" : page.why}`)} />
        </div>
      )}
      {page.s === "ready" && (
        <CodeEditor
          initial={page.ready.initial}
          key={page.ready.key}
          pack={page.ready.pack}
          name={page.ready.name}
          dark={dark}
          readOnly={readOnly}
          wrap={wrap}
          onChange={onChange}
          onSave={() => void saveRef.current()}
          handle={handle}
        />
      )}
    </div>
  );

  // noticeText is the words of a notice: the page's own, by the failure's name.
  function noticeText(n: Exclude<Notice, null>, c: ReturnType<typeof codeText>): string {
    switch (n) {
      case "notUtf8":
        return c.notUtf8;
      case "tooLargeToEdit":
        return c.tooLargeToEdit;
      case "binary":
        return c.binary;
      case "conflict":
        return c.conflict;
      case "locked":
        return c.locked;
      case "forbidden":
        return c.forbidden;
      case "gone":
        return c.gone;
      case "notText":
        return c.notText;
      case "tooLarge":
        return c.tooLarge;
      case "failed":
        return c.failed;
    }
  }

  // noticeAction is the notice's one choice: a reload for a conflict, a retry for a save that may work again, a download for
  // a file that is not UTF-8 or too large to edit.
  function noticeAction(n: Exclude<Notice, null>) {
    if (n === "conflict") {
      return (
        <Popconfirm title={ct.reloadConfirm} okText={ct.reload} cancelText={t("common.cancel")} onConfirm={() => void reload()}>
          <Button size="small">{ct.reload}</Button>
        </Popconfirm>
      );
    }
    if (n === "locked" || n === "failed") {
      return (
        <Button size="small" onClick={() => void saveRef.current()}>
          {ct.retry}
        </Button>
      );
    }
    if ((n === "notUtf8" || n === "tooLargeToEdit" || n === "binary") && canDownload) {
      return (
        <Button size="small" onClick={() => void startDownload(nodeId)}>
          {t("files.act.download")}
        </Button>
      );
    }
    return undefined;
  }
}
