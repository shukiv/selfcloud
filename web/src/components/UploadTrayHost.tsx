// SPDX-License-Identifier: AGPL-3.0-or-later

import { lazy, Suspense, type CSSProperties } from "react";
import { conflictAsk, uploads } from "../lib/files/uploads";
import { useStore } from "../lib/store";
import SectionBoundary from "./SectionBoundary";

// The shell's place for the upload tray. The tray's code (a card and its tabs from antd) loads when the first upload
// exists, so it stays out of the first load. The tray itself draws nothing while there is no upload, so this host shows
// it exactly when it did before. A tray that cannot load says so where it would have been, with a reload, and the rest of
// the page stays: without the tray nobody could follow the upload or answer its conflict question.
const UploadTray = lazy(() => import("./UploadTray"));

// The tray's own corner of the window (UploadTray).
const FAILED: CSSProperties = { position: "fixed", insetBlockEnd: 16, insetInlineEnd: 16, zIndex: 30, width: "min(420px, calc(100vw - 32px))" };

export default function UploadTrayHost() {
  const list = useStore(uploads);
  const ask = useStore(conflictAsk);
  if (list.length === 0 && ask === null) return null;
  return (
    <SectionBoundary failedStyle={FAILED}>
      <Suspense fallback={null}>
        <UploadTray />
      </Suspense>
    </SectionBoundary>
  );
}
