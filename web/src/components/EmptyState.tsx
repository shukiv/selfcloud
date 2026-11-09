// SPDX-License-Identifier: AGPL-3.0-or-later

// EmptyState is the one empty state of the web interface (decision 111): antd's Empty, with the words the page had before.
// "page" is a page's main list and shows antd's default picture; "compact" is a panel, a drawer, a dialog, a card or a table
// body, and shows the simple picture. antd's Empty takes no role, class or test id, so the wrapper carries what the old
// element had, and the action (if the page offers one next to the empty text) goes under the text.
// "table" is an empty list inside its table, under the column headers (the owner's rule of 2026-11-09, decision 114): the
// simple picture, the text as a bold title, the description as one muted line, and the action under them.
import { Empty, theme as antdTheme } from "antd";
import type { ReactNode } from "react";

export default function EmptyState({
  text,
  description,
  size = "page",
  role,
  className,
  testId,
  children,
}: {
  text: ReactNode;
  // description is the table's muted line under its title; the other sizes have the text alone.
  description?: ReactNode;
  size?: "page" | "compact" | "table";
  role?: "status";
  className?: string;
  testId?: string;
  children?: ReactNode;
}) {
  const { token } = antdTheme.useToken();
  if (size === "table") {
    // The approved mockup's empty table: 48 and 56 pixels above and below, 6 between the lines, the title at weight 600.
    return (
      <div role={role} className={["empty-table", className ?? ""].filter(Boolean).join(" ")} data-testid={testId} style={{ padding: "48px 16px 56px" }}>
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          style={{ margin: 0 }}
          description={
            <span style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 6 }}>
              <span className="empty-title" style={{ fontWeight: 600, color: token.colorText, marginTop: 8 }}>
                {text}
              </span>
              {description !== undefined && description !== null && (
                <span className="empty-desc" style={{ maxWidth: 420, color: token.colorTextSecondary }}>
                  {description}
                </span>
              )}
            </span>
          }
        >
          {children}
        </Empty>
      </div>
    );
  }
  return (
    <div role={role} className={className} data-testid={testId}>
      <Empty image={size === "compact" ? Empty.PRESENTED_IMAGE_SIMPLE : Empty.PRESENTED_IMAGE_DEFAULT} description={text}>
        {children}
      </Empty>
    </div>
  );
}
