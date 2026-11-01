// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect } from "react";
import { theme } from "antd";
import { Link } from "react-router-dom";
import { useT } from "../../lib/i18n";
import { useStore } from "../../lib/store";
import { session, sessionKey } from "../../lib/session";
import { myStorage, refreshMyStorage } from "../../lib/storage/api";
import { heldBytes, storageNoticePercent } from "../../lib/storage/logic";
import UsageMeter from "../UsageMeter";

// StorageNotice says how full the person's own storage is, from 80% of its limit, with a link to Storage and the meter.
// It is shown on My files only, in the person's own folder (decision 53). The figures are read each time it is shown.
export default function StorageNotice() {
  const { t } = useT();
  const { token } = theme.useToken();
  const my = useStore(myStorage);
  const sk = sessionKey(useStore(session));

  useEffect(() => {
    void refreshMyStorage().catch(() => undefined);
  }, [sk]);

  const percent = storageNoticePercent(my);
  if (percent === null || my === null) return null;
  return (
    <section
      aria-label={t("nav.storage")}
      style={{ display: "grid", gap: 8, padding: 12, fontSize: 14, background: token.colorWarningBg, borderRadius: token.borderRadius }}
    >
      <p style={{ margin: 0 }}>
        {t("home.storageNotice", { percent: String(percent) })} <Link to="/storage">{t("home.storageLink")}</Link>
      </p>
      <UsageMeter used={heldBytes(my)} quota={my.quota_bytes} />
    </section>
  );
}
