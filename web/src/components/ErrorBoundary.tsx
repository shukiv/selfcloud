// SPDX-License-Identifier: AGPL-3.0-or-later

import { Component, type ReactNode } from "react";
import { Button, Result } from "antd";
import { useT } from "../lib/i18n";

// ErrorBoundary is the last line of the page: a render error shows a message and a reload button, and nothing
// else of the failed tree. On the share-link address the secret is in the fragment, so the fragment is stripped
// first (the page reads the secret once, before any render that could fail, so nothing is lost by it). The error
// itself is not shown, and this code keeps and sends nothing of it. React 18 still reports a caught render error
// through console.error (in production too), so the console copy is React's, not ours.
export default class ErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError(): { failed: boolean } {
    return { failed: true };
  }

  componentDidCatch(): void {
    if (window.location.pathname === "/s" && window.location.hash !== "") {
      window.history.replaceState(null, "", window.location.pathname + window.location.search);
    }
  }

  render(): ReactNode {
    return this.state.failed ? <CrashView /> : this.props.children;
  }
}

function CrashView() {
  const { t } = useT();
  return (
    <Result
      status="error"
      title={t("app.crash.title")}
      subTitle={t("app.crash.body")}
      extra={
        <Button type="primary" onClick={() => window.location.reload()}>
          {t("app.crash.reload")}
        </Button>
      }
    />
  );
}
