// SPDX-License-Identifier: AGPL-3.0-or-later

import { Component, type CSSProperties, type ReactNode } from "react";
import { Alert, Button } from "antd";
import { useT } from "../lib/i18n";

// SectionBoundary keeps a failed part of a page to its own place. A lazy chunk that does not load, or a render error in
// it, shows a message and a reload button where the part was, and the rest of the page stays usable. The page's own
// ErrorBoundary would replace the whole page. The reload is the retry: a browser keeps a module that failed to load,
// so a second import in the same page fails the same way.
// failedStyle places the message when the part floats over the page instead of having a place in it.
export default class SectionBoundary extends Component<{ children: ReactNode; failedStyle?: CSSProperties }, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError(): { failed: boolean } {
    return { failed: true };
  }

  render(): ReactNode {
    if (!this.state.failed) return this.props.children;
    return this.props.failedStyle ? (
      <div style={this.props.failedStyle}>
        <SectionFailed />
      </div>
    ) : (
      <SectionFailed />
    );
  }
}

export function SectionFailed() {
  const { t } = useT();
  return (
    <Alert
      type="error"
      showIcon
      role="alert"
      message={t("error.generic")}
      action={
        <Button size="small" onClick={() => window.location.reload()}>
          {t("app.crash.reload")}
        </Button>
      }
    />
  );
}
