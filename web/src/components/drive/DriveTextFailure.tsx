// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Button } from "antd";
import { useT } from "../../lib/i18n";

// DriveTextFailure says a drive list's own strings did not load, and offers Try again, which loads them once more (tl4 r1,
// finding 10). The list draws no table until they are there.
export default function DriveTextFailure({ retry }: { retry: () => void }) {
  const { t } = useT();
  return (
    <Alert
      type="error"
      showIcon
      role="alert"
      message={t("error.generic")}
      action={
        <Button size="small" onClick={retry}>
          {t("files.retry")}
        </Button>
      }
      style={{ marginBottom: 12 }}
    />
  );
}
