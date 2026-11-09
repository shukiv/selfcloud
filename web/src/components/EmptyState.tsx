// SPDX-License-Identifier: AGPL-3.0-or-later

// EmptyState is the one empty state of the web interface (decision 111): antd's Empty, with the words the page had before.
// "page" is a page's main list and shows antd's default picture; "compact" is a panel, a drawer, a dialog, a card or a table
// body, and shows the simple picture. antd's Empty takes no role, class or test id, so the wrapper carries what the old
// element had, and the action (if the page offers one next to the empty text) goes under the text.
import { Empty } from "antd";
import type { ReactNode } from "react";

export default function EmptyState({
  text,
  size = "page",
  role,
  className,
  testId,
  children,
}: {
  text: ReactNode;
  size?: "page" | "compact";
  role?: "status";
  className?: string;
  testId?: string;
  children?: ReactNode;
}) {
  return (
    <div role={role} className={className} data-testid={testId}>
      <Empty image={size === "compact" ? Empty.PRESENTED_IMAGE_SIMPLE : Empty.PRESENTED_IMAGE_DEFAULT} description={text}>
        {children}
      </Empty>
    </div>
  );
}
