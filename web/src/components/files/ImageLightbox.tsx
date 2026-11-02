// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type RefObject, type TouchEvent as ReactTouchEvent } from "react";
import { Alert, Modal } from "antd";
import IconButton from "../IconButton";
import { ChevronLeft, ChevronRight, Download, X } from "lucide-react";
import { kindOf } from "../../lib/files/kind";
import type { Node } from "../../lib/files/types";
import { useT } from "../../lib/i18n";
import type { Link } from "./MediaPlayer";

// The swipe that counts as a step, in pixels. A shorter move is a tap or a shaky finger.
const SWIPE = 40;

export type ImageView =
  | { status: "loading" }
  | { status: "ready"; link?: Link }
  | { status: "restricted" }
  | { status: "unsupported" }
  | { status: "error" };

// useKeysAfterStep keeps the viewer's keys alive. A step to the last file turns its own button off, and the focus falls off
// the button: the keys then reach neither the arrows nor Escape. The focus goes to the viewer instead.
export function useKeysAfterStep(stage: RefObject<HTMLElement | null>, id: string | undefined) {
  useEffect(() => {
    const active = document.activeElement;
    if (active === null || active === document.body || (active instanceof HTMLButtonElement && active.disabled)) {
      stage.current?.focus({ preventScroll: true });
    }
  }, [id]);
}

// ImageLightbox shows a picture over the dimmed page: centred, rounded, with nothing around it but the way out, the file's
// name and the steps to the other pictures (decision 60). The steps count pictures only: a text file between two pictures is
// not a step here. A click outside the picture does nothing, as with every dialog; the X and Escape close it.
export default function ImageLightbox({
  items,
  index,
  view,
  canDownload,
  onClose,
  onIndex,
  onDownload,
}: {
  items: Node[];
  index: number;
  view: ImageView;
  canDownload: boolean;
  onClose: () => void;
  onIndex: (i: number) => void;
  onDownload: () => void;
}) {
  const { t } = useT();
  const node = items[index];
  const images = items.flatMap((n, i) => (kindOf(n) === "image" ? [i] : []));
  const position = images.indexOf(index);
  const previous = position > 0 ? images[position - 1] : undefined;
  const next = position >= 0 && position < images.length - 1 ? images[position + 1] : undefined;

  // A picture that would not load is recorded with its file, so the next file is tried afresh.
  const [failed, setFailed] = useState<string | null>(null);
  useEffect(() => setFailed(null), [node.id]);

  const stageRef = useRef<HTMLDivElement>(null);
  useKeysAfterStep(stageRef, node.id);

  const rtl = () => document.documentElement.dir === "rtl";

  function step(target: number | undefined) {
    if (target !== undefined) onIndex(target);
  }

  function keydown(e: ReactKeyboardEvent) {
    const forward = rtl() ? "ArrowLeft" : "ArrowRight";
    const back = rtl() ? "ArrowRight" : "ArrowLeft";
    if (e.key === forward) {
      e.preventDefault();
      step(next);
    } else if (e.key === back) {
      e.preventDefault();
      step(previous);
    }
  }

  // A swipe towards the start of the reading direction brings the next picture, as a page turns.
  const touchStart = useRef<{ x: number; y: number } | null>(null);
  function touchstart(e: ReactTouchEvent) {
    const p = e.touches[0];
    touchStart.current = p ? { x: p.clientX, y: p.clientY } : null;
  }
  function touchend(e: ReactTouchEvent) {
    const from = touchStart.current;
    const p = e.changedTouches[0];
    touchStart.current = null;
    if (!from || !p) return;
    const dx = p.clientX - from.x;
    if (Math.abs(dx) < SWIPE || Math.abs(dx) < Math.abs(p.clientY - from.y)) return;
    step(dx < 0 !== rtl() ? next : previous);
  }

  return (
    <Modal
      open
      footer={null}
      closable={false}
      maskClosable={false}
      onCancel={onClose}
      // Antd puts the focus on its own frame as the dialog opens, outside the stage that reads the keys.
      afterOpenChange={(open) => {
        if (open) stageRef.current?.focus({ preventScroll: true });
      }}
      width="100%"
      rootClassName="lightbox"
      style={{ top: 0, margin: 0, maxWidth: "100%", paddingBlockEnd: 0 }}
      styles={{
        wrapper: { overflow: "hidden" },
        mask: { backgroundColor: "rgba(0, 0, 0, 0.9)" },
        content: { height: "100dvh", padding: 0, overflow: "hidden", background: "transparent", boxShadow: "none" },
        body: { padding: 0, height: "100%" },
        header: { display: "none" },
      }}
      title={node.name}
      destroyOnHidden
      // The page puts the focus back on the row that opened the viewer (FilesPage's onClose).
      focusTriggerAfterClose={false}
      data-testid="preview"
    >
      <div ref={stageRef} tabIndex={-1} className="lightbox-stage" onKeyDown={keydown} onTouchStart={touchstart} onTouchEnd={touchend}>
        <header>
          <div className="lightbox-actions">
            <IconButton className="lightbox-btn" label={t("files.preview.close")} icon={<X size={20} aria-hidden="true" focusable="false" />} onClick={onClose} />
            {canDownload && (
              <IconButton className="lightbox-btn" label={t("files.act.download")} icon={<Download size={20} aria-hidden="true" focusable="false" />} onClick={onDownload} />
            )}
          </div>
          <p className="lightbox-name" title={node.name}>
            <bdi>{node.name}</bdi>
          </p>
        </header>
        <div className="lightbox-picture">
          {view.status === "loading" ? (
            <p role="status">{t("files.preview.loading")}</p>
          ) : view.status === "restricted" ? (
            <Alert type="info" role="status" message={t("files.err.downloadRestricted")} />
          ) : view.status === "error" || view.status === "unsupported" || failed === node.id ? (
            <Alert type="error" role="alert" message={t("error.generic")} />
          ) : view.link ? (
            <img src={view.link.url} alt={node.name} onError={() => setFailed(node.id)} />
          ) : null}
        </div>
        {images.length > 1 && (
          <nav className="lightbox-steps" aria-label={t("files.preview.steps")}>
            <IconButton
              className="lightbox-btn"
              label={t("files.preview.prev")}
              icon={<ChevronLeft className="icon-dir" size={20} aria-hidden="true" focusable="false" />}
              disabled={previous === undefined}
              onClick={() => step(previous)}
            />
            <span className="lightbox-count" aria-live="polite">
              {t("files.preview.count", { n: position + 1, total: images.length })}
            </span>
            <IconButton
              className="lightbox-btn"
              label={t("files.preview.next")}
              icon={<ChevronRight className="icon-dir" size={20} aria-hidden="true" focusable="false" />}
              disabled={next === undefined}
              onClick={() => step(next)}
            />
          </nav>
        )}
      </div>
    </Modal>
  );
}
