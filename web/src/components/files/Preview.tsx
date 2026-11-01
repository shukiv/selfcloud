// SPDX-License-Identifier: AGPL-3.0-or-later

import { lazy, Suspense, useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { Alert, Button, Modal } from "antd";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { ApiError } from "../../lib/apiError";
import { contentPath, signedLink } from "../../lib/files/api";
import { kindOf, previewable, TEXT_PREVIEW_BYTES, type Kind } from "../../lib/files/kind";
import { startDownload } from "../../lib/files/ops";
import type { Pager } from "../../lib/files/pager";
import { downloadAllowed } from "../../lib/files/roles";
import type { Node } from "../../lib/files/types";
import { useT } from "../../lib/i18n";
import type { Link } from "./MediaPlayer";

// The player and its playlist load when a video or an audio file opens, not with the browser.
const MediaPlaylist = lazy(() => import("./MediaPlaylist"));

// Preview shows one file full screen. Images and PDFs load through a short signed link on the content origin. Text is
// read on this origin, the first 1 MiB only, and shown as text, never as HTML. The Svelte page has no text editing, and
// this one has none either. A video or audio file opens the playlist instead (MediaPlaylist): its Previous and Next, and
// the N and P keys, step through the playlist, while the arrows belong to the player for seeking. Images, PDFs and text
// keep the steps described here.
// Escape closes (antd's Modal calls onCancel); the arrows step through the items, mirrored in right-to-left.
// Tab stays inside the dialog: antd's Modal traps focus, and the dialog takes focus when it opens.

type LoadState =
  | { status: "loading" }
  | { status: "ready"; link?: Link; text?: string; truncated?: boolean }
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
// load (safeURL). The server's end time is not read: a player decides from what it sees, not from a clock.
async function inlineLink(id: string): Promise<Link | null> {
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

// Media is the playlist a video or audio file opens: the file it starts with, the folder's files read a page at a time,
// or the selection the person chose to play.
export interface MediaSeed {
  start: Node;
  folder: Pager;
  selection?: Node[];
}

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
  const mediaMode = media !== undefined;
  // In media mode the file on screen is the one the playlist reports.
  const [mediaNode, setMediaNode] = useState<Node | null>(null);
  // mediaPos is the playlist's place for the file on screen ("2 / 3"), shown in the header.
  const [mediaPos, setMediaPos] = useState<{ position: number; total: string } | null>(null);
  // mediaRefused is the file whose download the server refused, so the notice is shown for that file only.
  const [mediaRefused, setMediaRefused] = useState<string | null>(null);
  const node = mediaMode ? (mediaNode ?? undefined) : (items[index] as Node | undefined);
  const [loaded, setLoaded] = useState<Loaded>({ id: null, state: { status: "loading" } });
  // A newer request makes an older answer stale.
  const token = useRef(0);

  useEffect(() => {
    if (!node || mediaMode) return;
    const nodeID = node.id;
    // Each answer is recorded with the file it is for.
    const put = (state: LoadState) => setLoaded({ id: nodeID, state });
    // A file the viewer cannot show gets its card at once; no link or read is made for it. The Svelte page asks
    // for a link here too, and the answer is never shown.
    if (!previewable(node)) {
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
        if (kind === "text") {
          const res = await fetch(contentPath(node.id, "inline"), {
            credentials: "same-origin",
            cache: "no-store",
            headers: { Range: `bytes=0-${TEXT_PREVIEW_BYTES - 1}` },
          });
          if (stale()) return;
          if (res.status === 403) {
            put({ status: "restricted" });
            return;
          }
          if (!res.ok && res.status !== 416) throw new ApiError(res.status, "internal", {}, 0);
          const text = res.status === 416 ? "" : await res.text();
          if (stale()) return;
          put({ status: "ready", text, truncated: node.size > TEXT_PREVIEW_BYTES });
          return;
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
  }, [node?.id]);

  const rtl = () => document.documentElement.dir === "rtl";

  function step(delta: number) {
    const next = index + delta;
    if (next >= 0 && next < items.length) onIndex(next);
  }

  function keydown(e: ReactKeyboardEvent) {
    // In media mode the arrows belong to the player (seek); the playlist's own Previous and Next step the files.
    if (mediaMode) return;
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
  // is inherited) shows the same notice in both modes: the viewer's card, or the notice under the header in media mode.
  async function download() {
    if (!node) return;
    const ok = await startDownload(node.id);
    if (ok) return;
    if (mediaMode) setMediaRefused(node.id);
    else setLoaded({ id: node.id, state: { status: "restricted" } });
  }

  // fetchMediaLink asks for a link to a file of the playlist, and for a new one when a link has ended. A refused link (no
  // download allowed) reaches the playlist as the error it is.
  const fetchMediaLink = async (n: Node): Promise<Link> => {
    const link = await inlineLink(n.id);
    if (link === null) throw new Error("unsafe link");
    return link;
  };

  const canDownload = node ? downloadAllowed(node) : false;
  const kindNow: Kind = node ? kindOf(node) : "file";
  const hasPreview = node ? previewable(node) : false;
  const view = shownState(loaded, node?.id);

  return (
    <Modal
      open
      footer={null}
      closable={false}
      maskClosable={false}
      onCancel={() => onClose(node ?? null)}
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
      <div style={FULL} onKeyDown={keydown}>
        <header style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 12 }}>
          {/* No autoFocus: the player's Space must reach the player when the viewer opens, not press Close. */}
          <Button onClick={() => onClose(node ?? null)}>{t("files.preview.close")}</Button>
          <p style={{ flex: "1 1 160px", margin: 0, overflowWrap: "anywhere", fontWeight: 600 }}>
            <bdi>{node?.name ?? ""}</bdi>
          </p>
          {/* A count reads left to right in every language: "2 / 5" keeps its order in Hebrew. A playlist shows its own. */}
          {!mediaMode && (
            <span aria-live="polite" dir="ltr" style={{ fontVariantNumeric: "tabular-nums" }}>
              {index + 1} / {items.length}
            </span>
          )}
          {mediaMode && mediaPos && (
            <span aria-live="polite" dir="ltr" data-testid="media-count" style={{ fontVariantNumeric: "tabular-nums" }}>
              {mediaPos.position} / {mediaPos.total}
            </span>
          )}
          {canDownload && <Button onClick={() => void download()}>{t("files.act.download")}</Button>}
        </header>
        {mediaMode && node && mediaRefused === node.id && (
          <Alert type="info" role="status" showIcon message={t("files.err.downloadRestricted")} />
        )}

        {mediaMode && media ? (
          // The viewer does not scroll: the playlist's own panel does, and the player stays on top.
          <div style={{ flex: "1 1 auto", minHeight: 0, display: "flex", flexDirection: "column", overflow: "hidden" }}>
            <Suspense fallback={<p role="status">{t("files.preview.loading")}</p>}>
              <MediaPlaylist
                start={media.start}
                folder={media.folder}
                selection={media.selection}
                fetchLink={fetchMediaLink}
                onCurrent={setMediaNode}
                onPosition={setMediaPos}
              />
            </Suspense>
          </div>
        ) : (
        <div style={{ flex: "1 1 auto", minHeight: 0, overflow: "auto", display: "flex", alignItems: "center", justifyContent: "center" }}>
          {node && !hasPreview ? (
            <div>
              <p>{t("files.preview.none")}</p>
              {canDownload && (
                <Button type="primary" onClick={() => void download()}>
                  {t("files.act.download")}
                </Button>
              )}
            </div>
          ) : view.status === "loading" ? (
            <p role="status">{t("files.preview.loading")}</p>
          ) : view.status === "restricted" ? (
            <Alert type="info" role="status" message={t("files.err.downloadRestricted")} />
          ) : view.status === "error" ? (
            <Alert type="error" role="alert" message={t("error.generic")} />
          ) : view.status === "unsupported" ? (
            <Alert type="warning" role="alert" message={t("files.preview.unsupported")} />
          ) : view.status === "ready" && node ? (
            kindNow === "image" && view.link ? (
              <img src={view.link.url} alt={node.name} style={{ maxWidth: "100%", maxHeight: "100%", objectFit: "contain" }} />
            ) : kindNow === "pdf" && view.link ? (
              <iframe title={node.name} src={view.link.url} style={{ width: "100%", height: "100%", border: 0, background: "#fff" }} />
            ) : kindNow === "text" ? (
              <div style={{ alignSelf: "stretch", width: "100%" }}>
                {view.truncated && <p>{t("files.preview.truncated")}</p>}
                <pre style={{ margin: 0, padding: 16, overflow: "auto", whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{view.text}</pre>
              </div>
            ) : null
          ) : null}
        </div>
        )}

        {!mediaMode && (
          <nav aria-label={t("files.preview.steps")} style={{ display: "flex", justifyContent: "space-between", gap: 8 }}>
            <Button onClick={() => step(-1)} disabled={index <= 0}>
              <ChevronLeft className="icon-dir" size={14} aria-hidden="true" /> {t("files.preview.prev")}
            </Button>
            <Button onClick={() => step(1)} disabled={index >= items.length - 1}>
              {t("files.preview.next")} <ChevronRight className="icon-dir" size={14} aria-hidden="true" />
            </Button>
          </nav>
        )}
      </div>
    </Modal>
  );
}
