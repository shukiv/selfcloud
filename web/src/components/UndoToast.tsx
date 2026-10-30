// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, type CSSProperties } from "react";
import { Button } from "antd";
import { X } from "lucide-react";
import { useT } from "../lib/i18n";

// Logical properties only: the toast sits at the start edge in either direction.
const TOAST: CSSProperties = {
  position: "fixed",
  insetBlockEnd: 16,
  insetInlineStart: 16,
  zIndex: 40,
  display: "flex",
  flexWrap: "wrap",
  alignItems: "center",
  gap: 12,
  maxWidth: "min(560px, calc(100vw - 32px))",
  padding: "12px 16px",
  borderRadius: 4,
};
const TEXT: CSSProperties = { margin: 0, flex: "1 1 200px" };

// UndoToast offers to undo the last action for a few seconds. Its text is announced through a live region.
// The window closes at expiresAt; the timer runs against the time it was given.
export default function UndoToast({
  label,
  expiresAt,
  status = "",
  onUndo,
  onExpire,
}: {
  label: string;
  expiresAt: number;
  status?: string;
  onUndo: () => void;
  onExpire: () => void;
}) {
  const { t } = useT();
  useEffect(() => {
    const timer = setInterval(() => {
      if (Date.now() >= expiresAt) onExpire();
    }, 250);
    return () => clearInterval(timer);
  }, [expiresAt, onExpire]);
  return (
    <div role="group" aria-label={t("files.undo.group")} data-testid="undo-toast" style={TOAST}>
      <p style={TEXT} aria-live="polite">
        {status || label}
      </p>
      {!status && (
        <Button onClick={onUndo}>{t("files.undo.action")}</Button>
      )}
      <Button onClick={onExpire} aria-label={t("common.dismiss")} icon={<X size={16} strokeWidth={1.75} aria-hidden="true" />} />
    </div>
  );
}
