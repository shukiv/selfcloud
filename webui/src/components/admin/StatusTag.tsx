// SPDX-License-Identifier: AGPL-3.0-or-later

import { Tag } from "antd";
import type { ReactNode } from "react";

// StatusTag names a state in words; the colour says it again, it never says it alone.
export default function StatusTag({ tone, children }: { tone: "ok" | "warn" | "danger" | "neutral"; children: ReactNode }) {
  const color = tone === "ok" ? "success" : tone === "warn" ? "warning" : tone === "danger" ? "error" : "default";
  return <Tag color={color}>{children}</Tag>;
}
