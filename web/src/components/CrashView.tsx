// SPDX-License-Identifier: AGPL-3.0-or-later

import { Button, Result } from "antd";
import { useT } from "../lib/i18n";

// CrashView is the page a render error leaves: a message and a reload, and nothing of the failed tree. It is a lazy
// chunk (decision 185): antd's Result and its pictures are not on the first load's path. The entry warms it once the
// first screen is drawn, so a crash finds it already there.
export default function CrashView() {
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
