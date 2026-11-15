// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button } from "antd";
import { useLang, useT } from "../../lib/i18n";
import { loadAudit } from "../../lib/audit/i18n";

// AuditTextFailure says the activity log's sentences did not load, and offers Retry, which loads them once more. The
// message is the main catalogue's, which is always there, as the media page's failed catalogue is.
export default function AuditTextFailure() {
  const { t } = useT();
  const lang = useLang();
  return (
    <Alert
      type="error"
      showIcon
      role="alert"
      message={t("app.crash.title")}
      action={
        <Button size="small" onClick={() => void loadAudit(lang)}>
          {t("upload.retry")}
        </Button>
      }
    />
  );
}
