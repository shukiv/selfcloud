// SPDX-License-Identifier: AGPL-3.0-or-later

// The code editor, full page, in its own tab (decisions 115, 124 and 130). The address is /edit/<node>, the
// one the office editor uses; App sends a code file here and an office file to OfficeEditPage. The page reads the file's node (its
// etag is the file's version, decision 135), then the whole content with If-Match set to that etag, shows it in CodeMirror, and
// saves on an explicit save only: a button, or Ctrl or Cmd+S. A save is a PUT with If-Match set to the etag of the last answer, and
// the stored type sent unchanged. No response header is read for a version. A viewer, or an editor who
// opened it by its address with no right to save, reads the text with "View only" and no Save.
//
// The page is drawn outside the app shell, with a slim bar: the app's mark (to the folder), the file's name, the save state,
// then icon buttons with tooltips (Save, Find, Wrap lines, Close). Close ends the tab a script opened, and otherwise goes back.
// A save refused with 412 opens the conflict dialog (4.4): reload theirs, keep as a copy, overwrite theirs, or keep my draft. Unsaved
// text asks before the page is left by a link, Close or Back, and before a reload or close of the tab (4.6).
import { useEffect, useRef, useState } from "react";
import { useBlocker, useLocation } from "react-router-dom";
import { Alert, Button, Modal, Popconfirm, Spin } from "antd";
import { Copy, Pencil, RotateCcw, Save, Search, WrapText, X } from "lucide-react";
import CodeEditor, { type EditorHandle } from "../components/files/CodeEditor";
import IconButton from "../components/IconButton";
import { ApiError } from "../lib/apiError";
import { brand } from "../lib/brand";
import { decodeText, sniffWhole } from "../lib/code/encoding";
import { encodeDraft, EDIT_LIMIT, lineBreakOf, mixedBreaks, normalizeText } from "../lib/code/edit";
import { currentEtag, keepCopy } from "../lib/code/conflict";
import { dropUnsaved, keepUnsaved, readUnsaved } from "../lib/code/kept";
import { extensionOf, packOf, type Pack } from "../lib/code/names";
import { codeText } from "../lib/code/text";
import { readWhole, saveText, type SaveFailure } from "../lib/code/save";
import { wraps } from "../lib/code/lines";
import { statNode } from "../lib/files/api";
import { readEditorPath } from "../lib/files/editor";
import { startDownload } from "../lib/files/ops";
import { can, downloadAllowed } from "../lib/files/roles";
import type { Node } from "../lib/files/types";
import { useLang, useT } from "../lib/i18n";
import { go, signInURL } from "../lib/router";
import { clearSession, session, signingOut, type Session } from "../lib/session";
import { get, useStore } from "../lib/store";
import { useModeOrLight } from "../theme/theme";

// The failure the page shows when the file cannot be opened, chosen by the status and never by the server's text.
type OpenFailure = "notFound" | "forbidden" | "signedOut" | "failed";

// SaveState is the bar's save state. null is nothing to say yet.
type SaveState = "saved" | "unsaved" | "saving" | "failed" | null;

// Notice is a note in the page's notice area: a save that did not happen (its SaveFailure), a file that is not UTF-8, one too
// large to edit, or one that is not text at all (binary: a NUL byte anywhere in the file). signedOutKept is a sign-in that
// ended while this tab kept the unsaved text (decision 133); restored is the kept text coming back after a sign-in.
type Notice = SaveFailure | "notUtf8" | "tooLargeToEdit" | "binary" | "signedOutKept" | "restored" | null;

// Ready is an open file: its text as the editor starts with it, and what the save needs to write it back (the BOM and the line
// break the file had). initial changes with each reload, and key remounts the editor with it. mixed says the file uses more than
// one line-break style (4.8).
interface Ready {
  name: string;
  folder: string | null;
  canEdit: boolean;
  utf8: boolean;
  bom: boolean;
  mixed: boolean;
  sep: "\n" | "\r\n" | "\r";
  initial: string;
  pack: Pack;
  // lint is the JSON lint for a file that is JSON by its name (jsonc allows comments, so it is not linted).
  lint: boolean;
  wrap: boolean;
  key: number;
  node: Node;
}

// Conflict is the conflict dialog's state (4.4): the draft the refused save sent (its text, and its bytes as the save encoded them),
// the file's ETag as the check read it, and whether a copy found no free name. null when the dialog is closed.
interface Conflict {
  text: string;
  bytes: Uint8Array;
  etag: string;
  noName: boolean;
  // maybe says a save before this one got no answer: the server may have stored the draft already (decision 124).
  maybe: boolean;
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
  // A sign-in that has ended (401) is not a failure to retry: the page offers to sign in again.
  if (err.status === 401) return "signedOut";
  return "failed";
}

// readVersion reads a file's bytes as the version its node gave (decision 135): If-Match is the node's etag, so the bytes are that
// version. A 412 means the file changed after the node was read: the node is read again, and the bytes once more. A second 412 is
// thrown, and the caller shows its failure. The node is returned with the bytes, so the page uses the version it read.
async function readVersion(nod: string, first: Node): Promise<{ node: Node; bytes: Uint8Array }> {
  let node = first;
  for (let attempt = 1; ; attempt++) {
    try {
      return { node, bytes: await readWhole(nod, node.etag ?? "") };
    } catch (err) {
      if (!(err instanceof ApiError) || err.status !== 412 || attempt >= 2) throw err;
      node = await statNode(nod);
    }
  }
}

// NOD is the node in the address, as the API gives it.
const NOD = /^nod_[A-Za-z0-9_-]+$/;

// fitsDraft says a text is kept across a sign-in only up to the editor's own gate, 2 MiB after encoding (decision 133).
function fitsDraft(text: string, bom: boolean, sep: string): boolean {
  return encodeDraft(text, bom, sep).length <= EDIT_LIMIT;
}

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
  // conflict is the dialog of a save refused with 412 (4.4). copied is the copy the last "Keep as a copy" made: its name and id, for
  // the page's notice and its link. problems is the JSON lint's count, shown in the bar when it is not 0.
  const [conflict, setConflict] = useState<Conflict | null>(null);
  const [copied, setCopied] = useState<{ id: string; name: string } | null>(null);
  const [problems, setProblems] = useState(0);
  const handle = useRef<EditorHandle | null>(null);
  // The file's ETag and stored type, as the last read or save gave them; the text it was last saved as (LF line breaks).
  const file = useRef({ etag: "", mime: "application/octet-stream" });
  const saved = useRef("");
  const saving = useRef(false);
  // openGen counts the opens. A save answers for the open it was sent in: an answer for a file the page has since left is not
  // shown, and does not touch the new file's state.
  const openGen = useRef(0);
  // lostSave is set when a save got no answer, and cleared by a save that is answered: a later conflict then says the draft may already
  // be on the server (decision 124).
  const lostSave = useRef(false);
  const keyRef = useRef(0);
  // The latest page and save, read by the editor's keys, which are bound once for the editor's text.
  const readyRef = useRef<Ready | null>(null);
  // openUser is the person the open file was opened by: text kept for the file is theirs, whoever signs in later (decision 133).
  const openUser = useRef<string | null>(null);

  // The page opens the file each time the address changes: its node, then a read with no Range, so the whole file, with If-Match set to the node's etag.
  useEffect(() => {
    if (!valid) return;
    let cancelled = false;
    openGen.current++;
    readyRef.current = null;
    handle.current = null;
    openUser.current = get(session)?.user.id ?? null;
    // The new file starts with no save in flight and no ETag, saved text or type of the last file.
    saving.current = false;
    saved.current = "";
    // A new file is guarded again: a leave chosen for the last one does not carry over.
    leaving.current = false;
    lostSave.current = false;
    file.current = { etag: "", mime: "application/octet-stream" };
    setPage({ s: "opening" });
    setSaveState(null);
    setNotice(null);
    setClosed(false);
    setConflict(null);
    setCopied(null);
    setProblems(0);
    void (async () => {
      try {
        const first = await statNode(nod);
        if (cancelled) return;
        if (first.size > EDIT_LIMIT) {
          setPage({ s: "tooLarge", name: first.name, node: first });
          setNotice("tooLargeToEdit");
          return;
        }
        // The read is of the version the node gave; a 412 reads the node again (readVersion), and the page uses that node.
        const got = await readVersion(nod, first);
        if (cancelled) return;
        const n = got.node;
        // The editor has the whole file, so the sniff reads all of it: a NUL byte anywhere means it is not text.
        if (sniffWhole(got.bytes) === "binary") {
          setPage({ s: "binary", name: n.name, node: n });
          setNotice("binary");
          return;
        }
        const d = decodeText(got.bytes, false);
        const serverText = normalizeText(d.text);
        const canEdit = mode === "edit" && can.edit(n.role);
        // A file opened to edit comes back with the text this tab kept for this person (decision 133). It is unsaved text: the server's
        // text is the saved text, and the draft's ETag is the one a save is checked against, so a change since then meets the conflict dialog.
        const back = canEdit && d.utf8 && openUser.current !== null ? readUnsaved(openUser.current, nod) : null;
        const kept = back !== null && fitsDraft(back.text, back.bom, back.sep) ? back : null;
        file.current = { etag: kept ? kept.etag : (n.etag ?? ""), mime: n.mime_type ?? "application/octet-stream" };
        saved.current = serverText;
        const initial = kept ? kept.text : serverText;
        const ready: Ready = {
          name: n.name,
          folder: n.folder_id,
          canEdit,
          utf8: d.utf8,
          bom: kept ? kept.bom : d.bom,
          mixed: mixedBreaks(d.text),
          sep: kept ? kept.sep : lineBreakOf(d.text),
          initial,
          pack: packOf(n.name) ?? "text",
          lint: packOf(n.name) === "json" && extensionOf(n.name) === "json",
          wrap: wraps(initial),
          key: ++keyRef.current,
          node: n,
        };
        readyRef.current = ready;
        // The file's own rule sets the wrap: off for a long line (design 3.2), and the wrap button then turns it on.
        setWrap(ready.wrap);
        setPage({ s: "ready", ready });
        if (!d.utf8) setNotice("notUtf8");
        else if (kept) {
          setNotice("restored");
          setSaveState("unsaved");
        }
      } catch (err) {
        if (!cancelled) {
          const why = failureOf(err);
          // A file that now answers not found, or forbidden, drops the text kept for it (decision 133). Opening to read does not.
          if (mode === "edit" && openUser.current !== null && (why === "notFound" || why === "forbidden")) dropUnsaved(openUser.current, nod);
          setPage({ s: "failed", why });
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [nod, mode, valid]);

  const ready = page.s === "ready" ? page.ready : null;
  // The editor is read-only for a viewer, for a file that is not UTF-8, and once a save found the file gone or forbidden.
  const readOnly = ready === null || !ready.canEdit || !ready.utf8 || closed;
  // dirty is unsaved text: the draft differs from the saved text, or a save of it has not been answered (4.6).
  const dirty = saveState === "unsaved" || saveState === "saving" || saveState === "failed";

  // onChange sets the save state from the text: unsaved while it differs from the saved text.
  const onChange = () => {
    const cur = handle.current?.text();
    if (cur === undefined || saving.current) return;
    setSaveState(cur === saved.current ? "saved" : "unsaved");
  };

  // applySaved takes an answer of 200 for the text that was sent: the node's ETag is the next If-Match, and the text is the saved text.
  const applySaved = (node: Node, text: string) => {
    file.current = { etag: node.etag ?? "", mime: node.mime_type ?? file.current.mime };
    saved.current = text;
    // A save that took the text clears the text kept for the file (decision 133).
    drop();
    const now = handle.current?.text();
    setSaveState(now === text ? "saved" : "unsaved");
  };

  // drop clears the text kept for the open file, when a save took it, a discard was chosen, or the file is gone or forbidden.
  const drop = () => {
    if (openUser.current !== null) dropUnsaved(openUser.current, nod);
  };

  // keep writes the text into this tab's store for the person who opened the file, with the ETag a save of it is checked against
  // (decision 133). It says whether the store kept it: a text over the editor's gate, or one the store refuses, is not kept. A text that
  // is not kept also drops the older draft for the file, so a stale text never comes back.
  const keep = (text: string): boolean => {
    const r = readyRef.current;
    const u = openUser.current;
    if (r === null || u === null || !r.utf8 || !fitsDraft(text, r.bom, r.sep)) {
      if (u !== null) dropUnsaved(u, nod);
      return false;
    }
    const ok = keepUnsaved(u, nod, { text, etag: file.current.etag, bom: r.bom, sep: r.sep });
    if (!ok) dropUnsaved(u, nod);
    return ok;
  };

  // signedOutNotice is the notice for a sign-in that ended while the text was unsaved: kept, when this tab kept the text, and the
  // copy-first words otherwise (decision 133). It keeps the editor's text as it is now, not the text a save sent: the
  // person may have typed while the save ran, and that text is the one on the page. The text it was sent with is only the
  // fallback for a page with no editor.
  const signedOutNotice = (sent: string): "signedOutKept" | "signedOut" => {
    const cur = handle.current?.text() ?? sent;
    if (cur === saved.current) {
      drop();
      return "signedOut";
    }
    return keep(cur) ? "signedOutKept" : "signedOut";
  };

  // openConflict opens the dialog for a save refused with 412 (4.4): the check reads the file's current etag from its node's stat
  // (decision 135), and the dialog's Overwrite saves over it. A check that fails shows its notice instead of the dialog.
  const openConflict = async (text: string, bytes: Uint8Array, gen: number) => {
    setSaveState("unsaved");
    const h = await currentEtag(nod);
    if (gen !== openGen.current) return;
    if (!h.ok) {
      setNotice(h.why === "signedOut" ? signedOutNotice(text) : h.why);
      setSaveState("failed");
      if (h.why === "forbidden" || h.why === "gone") {
        drop();
        setClosed(true);
      }
      return;
    }
    setConflict({ text, bytes, etag: h.etag, noName: false, maybe: lostSave.current });
  };

  // save writes the draft with If-Match and the stored type. It sends nothing when the text is the saved text, or while a save is
  // in flight, or when the file is not the person's to write. A draft the server refuses stays on the page. It answers whether the
  // draft is saved (true when there was nothing to save), so a guard can leave once it is.
  const save = async (): Promise<boolean> => {
    const r = readyRef.current;
    if (r === null || !r.canEdit || !r.utf8 || closed || saving.current) return false;
    const text = handle.current?.text();
    if (text === undefined) return false;
    if (text === saved.current) return true;
    const bytes = encodeDraft(text, r.bom, r.sep);
    const gen = openGen.current;
    saving.current = true;
    setSaveState("saving");
    setNotice(null);
    setCopied(null);
    const got = await saveText(nod, file.current.etag, file.current.mime, bytes);
    if (gen !== openGen.current) return false;
    saving.current = false;
    if (got.ok) {
      lostSave.current = false;
      applySaved(got.node, text);
      return true;
    }
    if (got.why === "conflict") {
      await openConflict(text, bytes, gen);
      return false;
    }
    if (got.why === "failed") lostSave.current = true;
    setSaveState("failed");
    setNotice(got.why === "signedOut" ? signedOutNotice(text) : got.why);
    if (got.why === "forbidden" || got.why === "gone") {
      drop();
      setClosed(true);
    }
    return false;
  };
  // The editor's Ctrl or Cmd+S runs the latest save.
  const saveRef = useRef(save);
  saveRef.current = save;

  // answerOnce runs one of the dialog's two saves (overwrite, keep as a copy). A second click while one is in flight sends nothing, and
  // the buttons show as busy until it is answered.
  const answering = useRef(false);
  const [busy, setBusy] = useState(false);
  const answerOnce = async (run: () => Promise<void>) => {
    if (answering.current) return;
    answering.current = true;
    setBusy(true);
    try {
      await run();
    } finally {
      answering.current = false;
      setBusy(false);
    }
  };

  // overwrite saves the draft over the changed file, with the ETag the check read (the dialog's third choice). A second change after the
  // check opens the dialog again, with a fresh check.
  const overwrite = async () => {
    const c = conflict;
    if (c === null) return;
    const gen = openGen.current;
    saving.current = true;
    setSaveState("saving");
    const got = await saveText(nod, c.etag, file.current.mime, c.bytes);
    if (gen !== openGen.current) return;
    saving.current = false;
    setConflict(null);
    if (got.ok) {
      lostSave.current = false;
      applySaved(got.node, c.text);
      return;
    }
    if (got.why === "conflict") {
      await openConflict(c.text, c.bytes, gen);
      return;
    }
    // A save that got no answer may still have reached the server, so the next conflict says so.
    if (got.why === "failed") lostSave.current = true;
    setSaveState("failed");
    setNotice(got.why === "signedOut" ? signedOutNotice(c.text) : got.why);
    if (got.why === "forbidden" || got.why === "gone") {
      drop();
      setClosed(true);
    }
  };

  // keepAsCopy saves the draft as a new file beside the original (the dialog's second choice). The original is not changed, and the
  // draft stays on the page, so it is still unsaved here.
  const keepAsCopy = async () => {
    const c = conflict;
    const r = readyRef.current;
    if (c === null || r === null) return;
    const gen = openGen.current;
    const got = await keepCopy(r.node, file.current.mime, c.bytes);
    if (gen !== openGen.current) return;
    if (got.ok) {
      lostSave.current = false;
      // The copy took the text, so the text kept for the file is cleared (decision 133).
      drop();
      setConflict(null);
      setSaveState("unsaved");
      setCopied({ id: got.node.id, name: got.node.name });
      return;
    }
    if (got.why === "noName") {
      setConflict({ ...c, noName: true });
      return;
    }
    // Only a copy that was sent can have reached the server, so only it is a lost reply.
    if (got.why === "failed" && got.sent) lostSave.current = true;
    setConflict(null);
    setSaveState("failed");
    setNotice(got.why === "signedOut" ? signedOutNotice(c.text) : got.why);
    if (got.why === "forbidden" || got.why === "gone") {
      drop();
      setClosed(true);
    }
  };

  // keepDraft closes the dialog and keeps the draft on the page: the next save is checked again (the dialog's fourth choice).
  const keepDraft = () => {
    setConflict(null);
    setSaveState("unsaved");
  };

  // reload throws the draft away and shows the file as it is on the server now (the conflict's reload choice, asked first).
  const reload = async () => {
    const r = readyRef.current;
    if (r === null) return;
    // An answer for a file the page has since left is not shown, as save's is.
    const gen = openGen.current;
    setConflict(null);
    try {
      const got = await readVersion(nod, await statNode(nod));
      if (gen !== openGen.current) return;
      const d = decodeText(got.bytes, false);
      // The saved version replaces the draft, so the text kept for the file goes too: Reload and Discard are that choice (decision 133).
      drop();
      file.current = { ...file.current, etag: got.node.etag ?? "" };
      const initial = normalizeText(d.text);
      saved.current = initial;
      const next: Ready = {
        ...r,
        initial,
        utf8: d.utf8,
        bom: d.bom,
        mixed: mixedBreaks(d.text),
        sep: lineBreakOf(d.text),
        wrap: wraps(initial),
        key: ++keyRef.current,
      };
      readyRef.current = next;
      setWrap(next.wrap);
      setPage({ s: "ready", ready: next });
      setSaveState(null);
      setCopied(null);
      setNotice(d.utf8 ? null : "notUtf8");
    } catch (err) {
      if (gen !== openGen.current) return;
      const why = failureOf(err);
      setNotice(why === "forbidden" ? "forbidden" : why === "notFound" ? "gone" : why === "signedOut" ? "signedOut" : "failed");
      if (why === "forbidden" || why === "notFound") {
        drop();
        setClosed(true);
      }
    }
  };

  // The unsaved guard (4.6): a link, Back or Close that would leave the page with unsaved text asks first. A move to the same address
  // is not a leave.
  // leaving is set once the person has chosen to leave, so the page's own way out (a tab close, then back) is not asked again.
  const leaving = useRef(false);
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      !leaving.current &&
      dirty &&
      (currentLocation.pathname !== nextLocation.pathname || currentLocation.search !== nextLocation.search),
  );
  const blocked = blocker.state === "blocked" ? blocker : null;
  // askClose is the guard for a Close that would end a script's tab with unsaved text: the same modal as a link.
  const [askClose, setAskClose] = useState(false);
  // askSignIn is the guard for Sign in with unsaved text: the same modal, and Leave there ends the sign-in.
  const [askSignIn, setAskSignIn] = useState(false);

  // A reload or a close of the tab with unsaved text asks first, through the browser's own prompt (4.6).
  useEffect(() => {
    if (!dirty) return;
    const guard = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", guard);
    return () => window.removeEventListener("beforeunload", guard);
  }, [dirty]);

  const folderHref = ready?.folder ? `/files/${ready.folder}` : "/files";
  const back = () => go(from ?? folderHref);
  // close ends the tab when a script opened it (tab=1). The browser refuses a close it did not allow, and then the page is still
  // here after a moment, so it goes back instead; a page opened directly goes back at once.
  const leaveTab = () => {
    window.close();
    window.setTimeout(() => {
      if (!window.closed) back();
    }, 150);
  };
  const close = () => {
    if (!tab) {
      back();
      return;
    }
    if (dirty) {
      setAskClose(true);
      return;
    }
    leaveTab();
  };
  // The guard's three answers, for a blocked link and for a Close that asked. Leave sets leaving first, so the close and the
  // back that follows it are not asked again.
  // signInNow ends the sign-in and opens the sign-in page, as the app does on a 401 (lib/api.ts): the session is cleared, then the
  // page is opened with replace. It runs only after the person chooses to leave, so the guard does not ask again.
  const signInNow = () => {
    leaving.current = true;
    setAskSignIn(false);
    clearSession();
    go(signInURL(), { replace: true });
  };
  // askSignInFirst is the page's Sign in choice. Without unsaved text the sign-in goes at once. With unsaved text, this tab keeps the text
  // first (decision 133), and the sign-in goes at once when it does, since nothing is lost, text typed after the session ended included.
  // Only a text this tab cannot keep (over the gate, or refused by the store) asks first, in the leave modal (decision 130).
  const askSignInFirst = () => {
    const cur = handle.current?.text();
    if (cur === undefined) {
      signInNow();
      return;
    }
    if (cur === saved.current) {
      drop();
      signInNow();
      return;
    }
    if (keep(cur)) signInNow();
    else setAskSignIn(true);
  };

  // sessionEnded runs when the session ends from the person who has this file open, other than by a sign-out (decision 133). The
  // unsaved text is kept first. When it is kept, the guard does not hold the app's redirect to the sign-in page, as nothing is lost;
  // when it is not, the guard asks as before.
  const sessionEnded = () => {
    const cur = handle.current?.text();
    if (readyRef.current === null || closed || cur === undefined) return;
    // A text back at the saved text has nothing unsaved to keep, so the draft kept before for the file goes too.
    if (cur === saved.current) {
      drop();
      return;
    }
    if (keep(cur)) leaving.current = true;
  };
  const sessionEndedRef = useRef(sessionEnded);
  sessionEndedRef.current = sessionEnded;

  // The page watches the session. A watcher is told the current session at once, and then each change, so only a change from this
  // person to none is an end. A sign-out's own clear is not one: the sign-out clears the kept text itself (auth.ts).
  useEffect(() => {
    let was: Session | null = get(session);
    return session.subscribe((now) => {
      const before = was;
      was = now;
      if (before !== null && now === null && before.user.id === openUser.current && !signingOut()) sessionEndedRef.current();
    });
  }, []);

  // discardOnLeave: a Leave with the person still signed in discards the text kept for the file (decision 133, rule 5). After the
  // session has ended, the guard is the app's own redirect, not the person's choice, so nothing is discarded then.
  const discardOnLeave = () => {
    if (openUser.current !== null && get(session)?.user.id === openUser.current) drop();
  };
  const stayHere = () => {
    if (blocked !== null) blocked.reset();
    setAskClose(false);
    setAskSignIn(false);
  };
  const leaveNow = () => {
    if (blocked !== null) {
      discardOnLeave();
      blocked.proceed();
      return;
    }
    if (askSignIn) {
      signInNow();
      return;
    }
    discardOnLeave();
    leaving.current = true;
    setAskClose(false);
    leaveTab();
  };
  const saveAndLeaveNow = () => {
    const gate = blocked;
    const signingIn = askSignIn;
    void save().then((ok) => {
      if (!ok) {
        gate?.reset();
        setAskClose(false);
        setAskSignIn(false);
        return;
      }
      if (gate !== null) gate.proceed();
      else if (signingIn) signInNow();
      else leaveNow();
    });
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
  const problemsText = problems === 1 ? ct.problemOne : ct.problemMany.replace("{n}", String(problems));

  return (
    <div
      data-testid="code-editor-page"
      style={{ position: "fixed", inset: 0, zIndex: 1000, display: "flex", flexDirection: "column", background: "var(--ant-color-bg-container, #fff)" }}
    >
      <header style={{ display: "flex", alignItems: "center", gap: 12, minHeight: 48, padding: "0 12px", borderBottom: "1px solid rgba(0,0,0,0.12)" }}>
        {/* The router leaves the page, so the unsaved guard asks first, as it does for a link in the page. */}
        <a
          href={folderHref}
          onClick={(e) => {
            // Only a plain left click leaves the page here. A modifier (a new tab, a new window) or another button goes to the browser.
            if (e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
            e.preventDefault();
            go(folderHref);
          }}
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
        {problems > 0 && (
          <span data-testid="lint-count" style={{ flex: "none", fontSize: 13, color: "var(--ant-color-text-secondary, #5a6b85)" }}>
            {problemsText}
          </span>
        )}
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
            type={notice === "notUtf8" ? "warning" : notice === "restored" ? "info" : "error"}
            showIcon
            role={notice === "notUtf8" || notice === "restored" ? "status" : "alert"}
            message={noticeText(notice, ct)}
            description={notice === "signedOut" ? ct.signedOutCopy : notice === "signedOutKept" ? ct.signedOutKept : undefined}
            action={noticeAction(notice)}
          />
        </div>
      )}
      {copied !== null && (
        <div style={{ padding: "8px 12px" }}>
          <Alert
            type="success"
            showIcon
            role="status"
            message={ct.savedAs.replace("{name}", copied.name)}
            action={
              <Button size="small" onClick={() => go(`/edit/${copied.id}?mode=edit`)}>
                {ct.open}
              </Button>
            }
          />
        </div>
      )}
      {ready?.mixed && (
        <div style={{ padding: "8px 12px" }}>
          <Alert type="warning" showIcon role="status" message={ct.mixedBreaks} />
        </div>
      )}
      {notice === null && page.s === "opening" && (
        <div role="status" style={{ display: "grid", placeItems: "center", padding: 24 }}>
          <Spin tip={t("files.editor.opening")} />
        </div>
      )}
      {page.s === "failed" && (
        <div style={{ padding: 16 }}>
          {page.why === "signedOut" ? (
            <Alert
              type="error"
              showIcon
              role="alert"
              message={ct.signedOut}
              description={ct.signedOutCopy}
              action={
                <Button size="small" onClick={askSignInFirst}>
                  {ct.signIn}
                </Button>
              }
            />
          ) : (
            <Alert type="error" showIcon role="alert" message={t(`files.editor.${page.why === "failed" ? "failed" : page.why}`)} />
          )}
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
          lint={page.ready.lint}
          onProblems={setProblems}
          handle={handle}
        />
      )}
      <Modal
        open={conflict !== null}
        title={ct.conflictTitle}
        onCancel={keepDraft}
        maskClosable={false}
        destroyOnHidden
        footer={
          <div style={{ display: "flex", flexWrap: "wrap", justifyContent: "flex-end", gap: 8 }}>
            <Popconfirm title={ct.reloadConfirm} okText={ct.reload} cancelText={t("common.cancel")} onConfirm={() => void reload()}>
              <Button icon={<RotateCcw size={16} aria-hidden="true" focusable="false" />}>{ct.reloadTheirs}</Button>
            </Popconfirm>
            <Button icon={<Copy size={16} aria-hidden="true" focusable="false" />} loading={busy} disabled={busy} onClick={() => void answerOnce(keepAsCopy)}>
              {ct.keepCopy}
            </Button>
            <Button type="primary" icon={<Save size={16} aria-hidden="true" focusable="false" />} loading={busy} disabled={busy} onClick={() => void answerOnce(overwrite)}>
              {ct.overwrite}
            </Button>
            <Button icon={<Pencil size={16} aria-hidden="true" focusable="false" />} onClick={keepDraft}>
              {ct.keepDraft}
            </Button>
          </div>
        }
      >
        <p>{ct.conflict}</p>
        {conflict?.maybe && <Alert type="warning" showIcon role="status" message={ct.maybeSaved} />}
        {conflict?.noName && <Alert type="error" showIcon role="alert" message={ct.noFreeName} />}
      </Modal>
      <Modal
        open={blocked !== null || askClose || askSignIn}
        title={ct.leaveTitle}
        onCancel={stayHere}
        maskClosable={false}
        destroyOnHidden
        footer={
          <div style={{ display: "flex", flexWrap: "wrap", justifyContent: "flex-end", gap: 8 }}>
            <Button onClick={stayHere}>{ct.stay}</Button>
            <Button onClick={leaveNow}>{ct.leave}</Button>
            <Button type="primary" onClick={saveAndLeaveNow}>
              {ct.saveAndLeave}
            </Button>
          </div>
        }
      >
        <p>{ct.leaveBody}</p>
      </Modal>
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
      case "signedOut":
      case "signedOutKept":
        return c.signedOut;
      case "restored":
        return c.restored;
      case "storageFull":
        return c.storageFull;
      case "badName":
        return c.badName;
      case "folderGone":
        return c.folderGone;
    }
  }

  // noticeAction is the notice's one choice: a retry for a save that may work again, a download for a file that is not UTF-8 or too
  // large to edit. A conflict has its own dialog.
  function noticeAction(n: Exclude<Notice, null>) {
    if (n === "locked" || n === "failed") {
      return (
        <Button size="small" onClick={() => void saveRef.current()}>
          {ct.retry}
        </Button>
      );
    }
    // The sign-in ended: a retry would fail the same way, so the choice is to sign in again. The draft is kept, and the
    // unsaved guard asks before the sign-in page is opened.
    if (n === "signedOut" || n === "signedOutKept") {
      return (
        <Button size="small" onClick={askSignInFirst}>
          {ct.signIn}
        </Button>
      );
    }
    // The kept text comes back as the file's unsaved text. Discard is the saved version, as Reload is (decision 133).
    if (n === "restored") {
      return (
        <Button size="small" onClick={() => void reload()}>
          {ct.discard}
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
