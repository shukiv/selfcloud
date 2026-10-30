// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { Alert, Button, Modal } from "antd";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { ApiError } from "../../lib/apiError";
import { contentPath, signedLink } from "../../lib/files/api";
import { kindOf, previewable, TEXT_PREVIEW_BYTES, type Kind } from "../../lib/files/kind";
import { startDownload } from "../../lib/files/ops";
import { downloadAllowed } from "../../lib/files/roles";
import type { Node } from "../../lib/files/types";
import { useT } from "../../lib/i18n";

// Preview shows one file full screen. Images, video and audio, and PDFs load through a short signed link on the
// content origin. Text is read on this origin, the first 1 MiB only, and shown as text, never as HTML. The Svelte
// page has no text editing, and this one has none either.
// Escape closes (antd's Modal calls onCancel); the arrows step through the items, mirrored in right-to-left.
// Tab stays inside the dialog: antd's Modal traps focus, and the dialog takes focus when it opens.

type LoadState =
  | { status: "loading" }
  | { status: "ready"; url?: string; text?: string; truncated?: boolean }
  | { status: "restricted" }
  | { status: "error" };

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

export default function Preview({
  items,
  index,
  onClose,
  onIndex,
}: {
  items: Node[];
  index: number;
  onClose: () => void;
  onIndex: (i: number) => void;
}) {
  const { t } = useT();
  const node = items[index] as Node | undefined;
  const [loaded, setLoaded] = useState<LoadState>({ status: "loading" });
  // A newer request makes an older answer stale.
  const token = useRef(0);

  useEffect(() => {
    if (!node) return;
    // A file the viewer cannot show gets its card at once; no link or read is made for it. The Svelte page asks
    // for a link here too, and the answer is never shown.
    if (!previewable(node)) {
      token.current++;
      setLoaded({ status: "ready" });
      return;
    }
    const my = ++token.current;
    setLoaded({ status: "loading" });
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
            setLoaded({ status: "restricted" });
            return;
          }
          if (!res.ok && res.status !== 416) throw new ApiError(res.status, "internal", {}, 0);
          const text = res.status === 416 ? "" : await res.text();
          if (stale()) return;
          setLoaded({ status: "ready", text, truncated: node.size > TEXT_PREVIEW_BYTES });
          return;
        }
        const link = await signedLink(node.id, "inline", 300);
        if (stale()) return;
        const url = safeURL(link.url);
        if (url === null) {
          setLoaded({ status: "error" });
          return;
        }
        setLoaded({ status: "ready", url });
      } catch (err) {
        if (stale()) return;
        setLoaded(err instanceof ApiError && err.code === "download_restricted" ? { status: "restricted" } : { status: "error" });
      }
    })();
  }, [node?.id]);

  const rtl = () => document.documentElement.dir === "rtl";

  function step(delta: number) {
    const next = index + delta;
    if (next >= 0 && next < items.length) onIndex(next);
  }

  function keydown(e: ReactKeyboardEvent) {
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

  async function download() {
    if (!node) return;
    const ok = await startDownload(node.id);
    if (!ok) setLoaded({ status: "restricted" });
  }

  const canDownload = node ? downloadAllowed(node) : false;
  const kindNow: Kind = node ? kindOf(node) : "file";
  const hasPreview = node ? previewable(node) : false;

  return (
    <Modal
      open
      footer={null}
      closable={false}
      maskClosable={false}
      onCancel={onClose}
      width="100%"
      style={{ top: 0, margin: 0, maxWidth: "100%", paddingBlockEnd: 0 }}
      // The preview fills the window: no scroll bars of its own, so the page does not show native ones.
      styles={{ wrapper: { overflow: "hidden" }, content: { height: "100dvh", padding: 0, overflow: "hidden" }, body: { padding: 12 }, header: { display: "none" } }}
      title={node?.name ?? ""}
      destroyOnHidden
      data-testid="preview"
    >
      <div style={FULL} onKeyDown={keydown}>
        <header style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 12 }}>
          <Button onClick={onClose} autoFocus>
            {t("files.preview.close")}
          </Button>
          <p style={{ flex: "1 1 160px", margin: 0, overflowWrap: "anywhere", fontWeight: 600 }}>
            <bdi>{node?.name ?? ""}</bdi>
          </p>
          <span aria-live="polite" style={{ fontVariantNumeric: "tabular-nums" }}>
            {index + 1} / {items.length}
          </span>
          {canDownload && <Button onClick={() => void download()}>{t("files.act.download")}</Button>}
        </header>

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
          ) : loaded.status === "loading" ? (
            <p role="status">{t("files.preview.loading")}</p>
          ) : loaded.status === "restricted" ? (
            <Alert type="info" role="status" message={t("files.err.downloadRestricted")} />
          ) : loaded.status === "error" ? (
            <Alert type="error" role="alert" message={t("error.generic")} />
          ) : loaded.status === "ready" && node ? (
            kindNow === "image" && loaded.url ? (
              <img src={loaded.url} alt={node.name} style={{ maxWidth: "100%", maxHeight: "100%", objectFit: "contain" }} />
            ) : kindNow === "pdf" && loaded.url ? (
              <iframe title={node.name} src={loaded.url} style={{ width: "100%", height: "100%", border: 0, background: "#fff" }} />
            ) : kindNow === "video" && loaded.url ? (
              <video controls src={loaded.url} aria-label={node.name} style={{ maxWidth: "100%", maxHeight: "100%" }} />
            ) : kindNow === "audio" && loaded.url ? (
              <audio controls src={loaded.url} aria-label={node.name} />
            ) : kindNow === "text" ? (
              <div style={{ alignSelf: "stretch", width: "100%" }}>
                {loaded.truncated && <p>{t("files.preview.truncated")}</p>}
                <pre style={{ margin: 0, padding: 16, overflow: "auto", whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{loaded.text}</pre>
              </div>
            ) : null
          ) : null}
        </div>

        <nav aria-label={t("files.preview.steps")} style={{ display: "flex", justifyContent: "space-between", gap: 8 }}>
          <Button onClick={() => step(-1)} disabled={index <= 0}>
            <ChevronLeft className="icon-dir" size={14} aria-hidden="true" /> {t("files.preview.prev")}
          </Button>
          <Button onClick={() => step(1)} disabled={index >= items.length - 1}>
            {t("files.preview.next")} <ChevronRight className="icon-dir" size={14} aria-hidden="true" />
          </Button>
        </nav>
      </div>
    </Modal>
  );
}
