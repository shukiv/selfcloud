// SPDX-License-Identifier: AGPL-3.0-or-later

import type { ReactNode } from "react";
import { theme as antdTheme } from "antd";

// Field is a labelled control: the label above it, a hint, the control, and the error below it. The
// control names the hint and the error with aria-describedby, so a screen reader reads them.
export default function Field({
  id,
  label,
  error,
  hint,
  children,
}: {
  id: string;
  label: string;
  error?: string | null;
  hint?: string | null;
  children: ReactNode;
}) {
  const { token } = antdTheme.useToken();
  return (
    <div style={{ marginBottom: 16 }}>
      <label htmlFor={id} style={{ display: "block", marginBottom: 6, fontWeight: 600 }}>
        {label}
      </label>
      {hint && (
        <p id={`${id}-hint`} style={{ margin: "0 0 6px", color: token.colorTextSecondary }}>
          {hint}
        </p>
      )}
      {children}
      {error && (
        <p id={`${id}-error`} style={{ margin: "6px 0 0", color: token.colorError }}>
          {error}
        </p>
      )}
    </div>
  );
}

// describedBy names the hint and the error of a field, for its control.
export function describedBy(id: string, hint?: string | null, error?: string | null): string | undefined {
  const ids = [hint ? `${id}-hint` : null, error ? `${id}-error` : null].filter(Boolean);
  return ids.length ? ids.join(" ") : undefined;
}
