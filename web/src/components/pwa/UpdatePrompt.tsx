// SPDX-License-Identifier: AGPL-3.0-or-later

// The offer of a new version of the app (decision 65). It is shown when a new worker is waiting, and only while the page is
// idle: Reload waits for a playing file or an upload to end (lib/pwa/worker.ts, busy). It sits at the end edge of the page's
// bottom, which is the content side in both directions, so it never covers the sidebar.

import { useEffect, useState } from "react";
import { Button, theme as antdTheme } from "antd";
import { X } from "lucide-react";
import { useT } from "../../lib/i18n";
import { useStore } from "../../lib/store";
import { applyUpdate, busy, waiting } from "../../lib/pwa/worker";
import { useCoarsePointer } from "../../lib/pointer";
import { RADIUS } from "../../theme/tokens";

// POLL_MS is how often the offer checks whether the page is still busy, so it appears when the last file or upload ends.
const POLL_MS = 1000;

export default function UpdatePrompt() {
  const { t } = useT();
  const { token } = antdTheme.useToken();
  const coarse = useCoarsePointer();
  const next = useStore(waiting);
  const [idle, setIdle] = useState(() => !busy());
  const [closed, setClosed] = useState(false);
  // note says why Reload did not apply the update, when it did not: another tab is busy, or the worker could not take over.
  const [note, setNote] = useState<string | null>(null);

  useEffect(() => {
    if (!next) return;
    // A new waiting worker is a new offer: a closed one is not kept closed for it.
    setClosed(false);
    setNote(null);
    const sync = () => setIdle(!busy());
    sync();
    const timer = window.setInterval(sync, POLL_MS);
    return () => window.clearInterval(timer);
  }, [next]);

  if (!next || !idle || closed) return null;
  const target = coarse ? { minHeight: 44, minWidth: 44 } : undefined;
  return (
    <div
      role="status"
      data-testid="update-prompt"
      style={{
        position: "fixed",
        insetBlockEnd: 16,
        insetInlineEnd: 16,
        zIndex: 900,
        maxWidth: "min(360px, calc(100vw - 32px))",
        boxSizing: "border-box",
        display: "flex",
        alignItems: "center",
        gap: 8,
        paddingBlock: 10,
        paddingInlineStart: 16,
        paddingInlineEnd: 8,
        borderRadius: RADIUS * 2,
        background: token.colorBgElevated,
        boxShadow: token.boxShadowSecondary,
        color: token.colorText,
      }}
    >
      <span style={{ flex: 1, minWidth: 0 }}>
        {t("pwa.updateReady")}
        {note && (
          <span role="alert" style={{ display: "block", color: token.colorTextSecondary, fontSize: 13 }}>
            {note}
          </span>
        )}
      </span>
      <Button
        type="primary"
        style={target}
        onClick={() => {
          void applyUpdate().then((result) => {
            if (result === "otherTabs") setNote(t("pwa.waitingTabs"));
            else if (result === "failed") setNote(t("pwa.updateFailed"));
          });
        }}
      >
        {t("pwa.reload")}
      </Button>
      <Button type="text" aria-label={t("pwa.close")} style={target} icon={<X size={16} aria-hidden />} onClick={() => setClosed(true)} />
    </div>
  );
}
