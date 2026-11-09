// SPDX-License-Identifier: AGPL-3.0-or-later

import { lazy, Suspense, useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { Alert, Button, Dropdown, Modal } from "antd";
import { BookOpen, ChevronLeft, ChevronRight, FileOutput, Pencil, Search, X, Download } from "lucide-react";
import IconButton from "../IconButton";
import { ApiError } from "../../lib/apiError";
import { coarsePointer, editorEntry, officeEdit, openEditorTab } from "../../lib/files/editor";
import { go, location as routerLocation } from "../../lib/router";
import { signedLink } from "../../lib/files/api";
import {
  exportFormatsFor,
  failureKey,
  officeConvert,
  watchPreview,
  type Failure,
} from "../../lib/files/convert";
import { livePreviewDeps } from "../../lib/files/convertLive";
import { kindOf, previewable, TEXT_PREVIEW_BYTES, type Kind } from "../../lib/files/kind";
import { startDownload } from "../../lib/files/ops";
import { startPlayback, type PlaybackSeed } from "../../lib/files/playback";
import { downloadAllowed } from "../../lib/files/roles";
import type { Node } from "../../lib/files/types";
import { needsSniff } from "../../lib/code/kind";
import { codeNamed } from "../../lib/code/names";
import { readCode, sniffs, type Code } from "../../lib/code/read";
import { codeText } from "../../lib/code/text";
import { useLang, useT } from "../../lib/i18n";
import { get, useStore } from "../../lib/store";
import { useModeOrLight } from "../../theme/theme";
import type { FindAction } from "./CodeView";
import ImageLightbox, { type ImageView, useKeysAfterStep } from "./ImageLightbox";
import type { Link } from "./MediaPlayer";

// The export dialog is loaded on first use, as the zip dialog is.
const ExportDialog = lazy(() => import("./ExportDialog"));
// The code viewer and CodeMirror load with the first text file shown, never with the page (decision 115).
const CodeView = lazy(() => import("./CodeView"));

// OfficeState is what a document's preview shows: nothing asked yet, the conversion being made, the PDF's address, a failure
// by its name, or a conversion that has taken too long to wait for.
type OfficeState =
  | { status: "idle" }
  | { status: "making" }
  | { status: "ready"; src: string }
  | { status: "failed"; why: Failure }
  | { status: "slow" };

// Preview shows one file full screen. Images and PDFs load through a short signed link on the content origin. Text is
// read on this origin and shown read-only in the code viewer (CodeView, decision 115), never as HTML. Which files open there is
// the rule order of lib/code/kind.ts: a file the rules may take is sniffed first (its first 8 KiB), and only text is read for
// showing, the first 1 MiB. A file the sniff finds binary keeps the viewer it had (a PDF's frame), or shows its card. There is
// no editing here. A video or audio file opens the playlist instead: the session (lib/files/playback.ts) is started
// here, and MediaHost shows it in the shell, as the viewer and then as the mini player for audio. This component draws
// nothing for a media file, except a media file whose name is in the code list (a .ts file stored as video/mp2t): it is
// sniffed first, and shown as code when it is text. A picture is shown over the dimmed page (ImageLightbox). PDFs and text keep
// the steps described here. Escape closes (antd's Modal calls onCancel); the arrows step through the items, mirrored in
// right-to-left. Keys pressed in the code viewer (its caret, its find field) stay there. Tab stays inside the dialog: antd's
// Modal traps focus, and the dialog takes focus when it opens.

type LoadState =
  | { status: "loading" }
  | { status: "ready"; link?: Link; code?: Code; truncated?: boolean }
  // none: the file has no inline view (the sniff found no text, and nothing else shows it), so its card is shown.
  | { status: "none" }
  | { status: "restricted" }
  | { status: "unsupported" }
  | { status: "error" };

// Loaded is an answer and the file it was given for. An answer for another file is never shown: the link in it is that
// file's, and playing it would play the wrong file.
interface Loaded {
  id: string | null;
  state: LoadState;
}

// shownState is the state of the file on screen: the answer for it, or a loading card while its answer is pending.
export function shownState(loaded: Loaded, id: string | undefined): LoadState {
  return id !== undefined && loaded.id === id ? loaded.state : { status: "loading" };
}

// inlineLink asks for a signed link that shows the file inline, or gives null when the address is not one the page may
// load (safeURL). The server's end time is not read: a player decides from what it sees, not from a clock. The media
// session asks for its links through it too (MediaDock).
export async function inlineLink(id: string): Promise<Link | null> {
  const got = await signedLink(id, "inline", 300);
  const url = safeURL(got.url);
  if (url === null) return null;
  return { url };
}

// safeURL returns the address when the page may load it: http, https, or the same origin. Any other scheme, such as
// a script scheme or a data scheme, gives null, and the preview shows its error instead of an element with that source.
export function safeURL(raw: string): string | null {
  try {
    const u = new URL(raw, window.location.href);
    return u.protocol === "https:" || u.protocol === "http:" ? u.href : null;
  } catch {
    return null;
  }
}

const FULL: React.CSSProperties = { display: "flex", flexDirection: "column", height: "calc(100dvh - 24px)", gap: 12 };

// MediaSeed is the playlist a video or audio file opens: the file it starts with, the folder's files read a page at a time,
// or the selection the person chose to play.
export type MediaSeed = PlaybackSeed;

export default function Preview({
  items,
  index,
  onClose,
  onIndex,
  media,
}: {
  items: Node[];
  index: number;
  // onClose is told the file on screen when the viewer closes (the playlist's current file in media mode), so the page can
  // put the focus back on its row.
  onClose: (current: Node | null) => void;
  onIndex: (i: number) => void;
  media?: MediaSeed;
}) {
  const { t } = useT();
  const lang = useLang();
  const dark = useModeOrLight() === "dark";
  // asText is the media file the sniff found to be text (a .ts file stored as video/mp2t): it shows here as code, not in the player.
  const [asText, setAsText] = useState<string | null>(null);
  const mediaMode = media !== undefined && asText !== media.start.id;
  const node = items[index] as Node | undefined;
  const [loaded, setLoaded] = useState<Loaded>({ id: null, state: { status: "loading" } });
  // find is the code viewer's Find action while it shows the file, for the header's Find button.
  const [find, setFind] = useState<FindAction | null>(null);
  // A newer request makes an older answer stale.
  const token = useRef(0);
  // The document's preview is asked for by the person, and watched until it is ready or has failed. A watch belongs to one file:
  // it stops when the viewer shows another one, or closes.
  const officeOn = useStore(officeConvert);
  const editorOn = useStore(officeEdit);
  const [office, setOffice] = useState<{ id: string; state: OfficeState }>({ id: "", state: { status: "idle" } });
  const stopOffice = useRef<(() => void) | null>(null);
  const [exporting, setExporting] = useState<string | null>(null);

  // A media file starts its session once, when it is opened. The viewer is MediaHost's, not this component's. A media file
  // whose name is in the code list is sniffed first (rule 3 of lib/code/kind.ts): text shows here as code, anything else plays.
  useEffect(() => {
    if (!media) return;
    const start = media.start;
    if (!codeNamed(start.name)) {
      startPlayback(media, onClose);
      return;
    }
    let live = true;
    sniffs(start).then(
      (viewer) => {
        if (!live) return;
        if (viewer !== null && viewer !== "restricted") setAsText(start.id);
        else startPlayback(media, onClose);
      },
      () => {
        if (live) startPlayback(media, onClose);
      },
    );
    return () => {
      live = false;
    };
    // The session starts for each seed. onClose is read as it was when the viewer opened.
  }, [media]);

  // Leaving a file stops its watch and forgets its preview: coming back to it offers the Preview button again, and asks
  // nothing until the person does (a wait with no watch behind it would never end).
  useEffect(() => {
    stopOffice.current?.();
    stopOffice.current = null;
    setExporting(null);
    setOffice({ id: "", state: { status: "idle" } });
  }, [node?.id]);

  useEffect(() => () => stopOffice.current?.(), []);

  // startOffice asks for the preview of a document and watches it. The answer is recorded with the file it is for.
  function startOffice(n: Node) {
    stopOffice.current?.();
    const id = n.id;
    setOffice({ id, state: { status: "making" } });
    stopOffice.current = watchPreview(
      id,
      {
        making: () => undefined,
        ready: (src) => setOffice({ id, state: { status: "ready", src } }),
        failed: (why) => setOffice({ id, state: { status: "failed", why } }),
        slow: () => setOffice({ id, state: { status: "slow" } }),
      },
      livePreviewDeps(),
    );
  }

  useEffect(() => {
    if (!node || mediaMode) return;
    const nodeID = node.id;
    // Each answer is recorded with the file it is for.
    const put = (state: LoadState) => setLoaded({ id: nodeID, state });
    // A file the viewer cannot show gets its card at once; no link or read is made for it. The Svelte page asks
    // for a link here too, and the answer is never shown.
    const sniffing = needsSniff(node);
    if (!previewable(node) && !sniffing) {
      token.current++;
      put({ status: "ready" });
      return;
    }
    const my = ++token.current;
    put({ status: "loading" });
    const stale = () => my !== token.current;
    void (async () => {
      const kind: Kind = kindOf(node);
      try {
        if (sniffing) {
          // The rule order of lib/code/kind.ts: text opens in the code viewer, the first 1 MiB of it.
          const code = await readCode(node);
          if (stale()) return;
          if (code === "restricted") {
            put({ status: "restricted" });
            return;
          }
          if (code !== null) {
            put({ status: "ready", code, truncated: node.size > TEXT_PREVIEW_BYTES });
            return;
          }
          // Rule 6: not text. A file with a viewer of its own keeps it (a PDF); text that is binary, and the rest, show the card.
          if (kind === "text" || !previewable(node)) {
            put({ status: "none" });
            return;
          }
        }
        const link = await inlineLink(node.id);
        if (stale()) return;
        if (link === null) {
          put({ status: "error" });
          return;
        }
        put({ status: "ready", link });
      } catch (err) {
        if (stale()) return;
        put(err instanceof ApiError && err.code === "download_restricted" ? { status: "restricted" } : { status: "error" });
      }
    })();
    // A media file found to be text (asText) is read once it leaves media mode.
  }, [node?.id, mediaMode]);

  const stageRef = useRef<HTMLDivElement>(null);
  useKeysAfterStep(stageRef, node?.id);

  const rtl = () => document.documentElement.dir === "rtl";

  function step(delta: number) {
    const next = index + delta;
    if (next >= 0 && next < items.length) onIndex(next);
  }

  function keydown(e: ReactKeyboardEvent) {
    // The code viewer's own keys stay in it: its caret moves with the arrows, and so does its find field's.
    if (e.target instanceof Element && e.target.closest(".cm-editor")) return;
    const forward = rtl() ? "ArrowLeft" : "ArrowRight";
    const back = rtl() ? "ArrowRight" : "ArrowLeft";
    if (e.key === forward) {
      e.preventDefault();
      step(1);
    } else if (e.key === back) {
      e.preventDefault();
      step(-1);
    }
  }

  // download saves the file on screen. A refused download (the server's no-download rule, which the page cannot see when it
  // is inherited) shows the card's notice.
  async function download() {
    if (!node) return;
    const ok = await startDownload(node.id);
    if (ok) return;
    setLoaded({ id: node.id, state: { status: "restricted" } });
  }

  // A media file is shown by MediaHost, not here.
  if (mediaMode) return null;

  const canDownload = node ? downloadAllowed(node) : false;
  const kindNow: Kind = node ? kindOf(node) : "file";
  const view = shownState(loaded, node?.id);
  // An office document has a preview and exports only while the server offers them (officeOn). Without the feature it is the
  // card it always was: no button, and nothing asked of the server.
  const officeFile = node !== undefined && officeOn && kindNow === "office";
  const hasPreview = node ? previewable(node) || officeFile || needsSniff(node) : false;
  const shownOffice: OfficeState = node && office.id === node.id ? office.state : { status: "idle" };
  // The editor's entry: Open in editor for a file the caller may edit, Open in viewer otherwise, and none while the feature is off.
  const editorMode = node ? editorEntry(node, editorOn) : null;
  const exportFormats = node && officeFile ? exportFormatsFor(node.name) : [];
  // The header's Find button shows while the code viewer shows a file with text in it.
  const codeShown = view.status === "ready" && view.code !== undefined && view.code.text !== "";

  if (node && kindNow === "image" && hasPreview) {
    // A picture is never sniffed, so its state is never "none"; the type cannot see that.
    const imageView: ImageView = view.status === "none" ? { status: "unsupported" } : view;
    return (
      <ImageLightbox
        items={items}
        index={index}
        view={imageView}
        canDownload={canDownload}
        onClose={() => onClose(node)}
        onIndex={onIndex}
        onDownload={() => void download()}
      />
    );
  }

  return (
    <Modal
      open
      footer={null}
      closable={false}
      maskClosable={false}
      onCancel={() => onClose(node ?? null)}
      // Antd puts the focus on its own frame as the dialog opens, outside the stage that reads the keys.
      afterOpenChange={(open) => {
        if (open) stageRef.current?.focus({ preventScroll: true });
      }}
      width="100%"
      style={{ top: 0, margin: 0, maxWidth: "100%", paddingBlockEnd: 0 }}
      // The preview fills the window: no scroll bars of its own, so the page does not show native ones.
      styles={{ wrapper: { overflow: "hidden" }, content: { height: "100dvh", padding: 0, overflow: "hidden" }, body: { padding: 12 }, header: { display: "none" } }}
      title={node?.name ?? ""}
      destroyOnHidden
      // The page puts the focus back on the row that opened the viewer (FilesPage's onClose). Antd would restore its own
      // trigger after the close animation, which is the body when a row was pointed at rather than focused.
      focusTriggerAfterClose={false}
      data-testid="preview"
    >
      <div ref={stageRef} tabIndex={-1} data-preview-stage style={FULL} onKeyDown={keydown}>
        <header style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 12 }}>
          {/* No autoFocus: the player's Space must reach the player when the viewer opens, not press Close. */}
          <IconButton label={t("files.preview.close")} onClick={() => onClose(node ?? null)} icon={<X size={18} aria-hidden="true" focusable="false" />} />
          <p style={{ flex: "1 1 160px", margin: 0, overflowWrap: "anywhere", fontWeight: 600 }}>
            <bdi>{node?.name ?? ""}</bdi>
          </p>
          {/* A count reads left to right in every language: "2 / 5" keeps its order in Hebrew. */}
          <span aria-live="polite" dir="ltr" style={{ fontVariantNumeric: "tabular-nums" }}>
            {index + 1} / {items.length}
          </span>
          {node && editorMode !== null && (
            <IconButton
              className="editor-btn"
              label={editorMode === "view" ? t("files.act.openViewer") : t("files.act.openEditor")}
              onClick={() => openEditorTab(node.id, editorMode, get(routerLocation).path + get(routerLocation).search, coarsePointer(), go)}
              icon={editorMode === "view" ? <BookOpen size={18} aria-hidden="true" focusable="false" /> : <Pencil size={18} aria-hidden="true" focusable="false" />}
            />
          )}
          {officeFile && canDownload && exportFormats.length > 0 && (
            <Dropdown
              trigger={["click"]}
              menu={{ items: exportFormats.map((f) => ({ key: f, label: f.toUpperCase() })), onClick: ({ key }) => setExporting(key) }}
            >
              <IconButton className="convert-btn" label={t("files.convert.export")} icon={<FileOutput size={18} aria-hidden="true" focusable="false" />} />
            </Dropdown>
          )}
          {codeShown && find && <IconButton className="find-btn" label={find.label} onClick={find.run} icon={<Search size={18} aria-hidden="true" focusable="false" />} />}
          {canDownload && <IconButton label={t("files.act.download")} onClick={() => void download()} icon={<Download size={18} aria-hidden="true" focusable="false" />} />}
        </header>
        <div style={{ flex: "1 1 auto", minHeight: 0, minWidth: 0, overflow: "auto", display: "flex", alignItems: "center", justifyContent: "center" }}>
          {(node && !hasPreview) || view.status === "none" ? (
            <div>
              <p>{t("files.preview.none")}</p>
              {canDownload && (
                <Button type="primary" onClick={() => void download()}>
                  {t("files.act.download")}
                </Button>
              )}
            </div>
          ) : officeFile && node ? (
            <OfficeBody
              node={node}
              state={shownOffice}
              canDownload={canDownload}
              onAsk={() => startOffice(node)}
            />
          ) : view.status === "loading" ? (
            <p role="status">{t("files.preview.loading")}</p>
          ) : view.status === "restricted" ? (
            <Alert type="info" role="status" message={t("files.err.downloadRestricted")} />
          ) : view.status === "error" ? (
            <Alert type="error" role="alert" message={t("error.generic")} />
          ) : view.status === "unsupported" ? (
            <Alert type="warning" role="alert" message={t("files.preview.unsupported")} />
          ) : view.status === "ready" && node ? (
            kindNow === "pdf" && view.link ? (
              <iframe title={node.name} src={view.link.url} style={{ width: "100%", height: "100%", border: 0, background: "#fff" }} />
            ) : view.code ? (
              <div style={{ alignSelf: "stretch", width: "100%", minWidth: 0, display: "flex", flexDirection: "column", gap: 8 }}>
                {/* A file that is not UTF-8 still shows, read-only, with a notice and its download (design-ce 2.3). */}
                {!view.code.utf8 && (
                  <Alert
                    type="warning"
                    role="status"
                    message={codeText(lang).notUtf8}
                    action={
                      canDownload ? (
                        <Button size="small" onClick={() => void download()}>
                          {t("files.act.download")}
                        </Button>
                      ) : undefined
                    }
                  />
                )}
                {view.truncated && <p style={{ margin: 0 }}>{t("files.preview.truncated")}</p>}
                <Suspense fallback={<p role="status">{t("files.preview.loading")}</p>}>
                  <CodeView text={view.code.text} pack={view.code.viewer.pack} name={node.name} dark={dark} onFind={setFind} />
                </Suspense>
              </div>
            ) : null
          ) : null}
        </div>

        <nav aria-label={t("files.preview.steps")} style={{ display: "flex", justifyContent: "space-between", gap: 8 }}>
          <IconButton label={t("files.preview.prev")} onClick={() => step(-1)} disabled={index <= 0} icon={<ChevronLeft className="icon-dir" size={20} aria-hidden="true" focusable="false" />} />
          <IconButton label={t("files.preview.next")} onClick={() => step(1)} disabled={index >= items.length - 1} icon={<ChevronRight className="icon-dir" size={20} aria-hidden="true" focusable="false" />} />
        </nav>
        {exporting && node && (
          <Suspense fallback={null}>
            <ExportDialog nod={node.id} format={exporting} onClose={() => setExporting(null)} />
          </Suspense>
        )}
      </div>
    </Modal>
  );
}

// OfficeBody shows an office document: the button that asks for its preview, the state of that preview, and the PDF in a frame
// once the server has it. The frame's address is the preview's own: a redirect to the content origin is followed by the frame.
function OfficeBody({
  node,
  state,
  canDownload,
  onAsk,
}: {
  node: Node;
  state: OfficeState;
  canDownload: boolean;
  onAsk: () => void;
}) {
  const { t } = useT();
  if (!canDownload) return <Alert type="info" role="status" message={t("files.err.downloadRestricted")} />;
  switch (state.status) {
    case "idle":
      return (
        <div style={{ textAlign: "center" }}>
          <p>{t("files.convert.previewHint")}</p>
          <Button type="primary" className="convert-btn" onClick={onAsk}>
            {t("files.convert.preview")}
          </Button>
        </div>
      );
    case "making":
      return <p role="status">{t("files.convert.making")}</p>;
    case "ready":
      return <iframe title={node.name} src={state.src} style={{ width: "100%", height: "100%", border: 0, background: "#fff" }} />;
    case "slow":
      return <Alert type="warning" role="alert" message={t("files.convert.slow")} />;
    case "failed":
      return (
        <div style={{ display: "grid", gap: 12, justifyItems: "center" }}>
          <Alert type="error" role="alert" message={t(failureKey(state.why))} />
          {state.why === "unavailable" && (
            <Button className="convert-btn" onClick={onAsk}>
              {t("files.convert.retry")}
            </Button>
          )}
        </div>
      );
  }
}
