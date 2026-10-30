// SPDX-License-Identifier: AGPL-3.0-or-later

import { useT } from "../lib/i18n";

// NotFoundPage is the view for an address with no page, or one the person may not open.
export default function NotFoundPage() {
  const { t } = useT();
  return (
    <div>
      <h1 style={{ fontSize: 24 }}>{t("app.notFound.title")}</h1>
      <p>{t("app.notFound.body")}</p>
    </div>
  );
}
