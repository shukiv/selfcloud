// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert } from "antd";
import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";
import { useLang, useT } from "../../lib/i18n";
import { brand } from "../../lib/brand";
import { formatBytes } from "../../lib/files/format";
import { useStore } from "../../lib/store";
import { isAdmin, session } from "../../lib/session";
import { queryKey } from "../../lib/query";
import { getInfo } from "../../lib/manage/api";
import { failureText } from "../../lib/manage/failure";
import NotFoundPage from "../NotFoundPage";
import { muted, pageTitle } from "../../components/admin/adminStyles";

// InstallationPage shows the facts of this installation, read-only. The links open the API's description and the
// readiness check.
export default function InstallationPage() {
  const user = useStore(session)?.user;
  return isAdmin(user) ? <InstallationView /> : <NotFoundPage />;
}

function InstallationView() {
  const { t } = useT();
  const lang = useLang();
  const brandName = useStore(brand).name;
  const q = useQuery({ queryKey: queryKey("installation"), queryFn: getInfo, refetchOnMount: "always", retry: false });
  const info = q.data;

  useEffect(() => {
    document.title = `${t("admin.installation.title")} · ${brandName}`;
  }, [t, brandName]);

  return (
    <div>
      <h1 style={pageTitle}>{t("admin.installation.title")}</h1>
      {q.isError && <Alert type="error" showIcon role="alert" message={failureText(q.error, lang)} />}
      {info === undefined && !q.isError && <p role="status">{t("app.loading")}</p>}
      {info !== undefined && (
        <>
          <dl style={{ display: "grid", gap: 12, margin: 0 }}>
            <div>
              <dt>{t("installation.version")}</dt>
              <dd style={{ margin: 0, fontFamily: "ui-monospace, monospace" }}>{info.version}</dd>
            </div>
            <div>
              <dt>{t("installation.publicUrl")}</dt>
              <dd style={{ margin: 0 }}>
                <bdi>{info.public_url}</bdi>
              </dd>
            </div>
            <div>
              <dt>{t("installation.webInterface")}</dt>
              <dd style={{ margin: 0 }}>{t(info.web_interface ? "installation.on" : "installation.off")}</dd>
            </div>
            <div>
              <dt>{t("installation.pieceSize")}</dt>
              <dd style={{ margin: 0 }}>{formatBytes(info.limits.max_chunk_size, lang)}</dd>
            </div>
            <div>
              <dt>{t("installation.largestFile")}</dt>
              <dd style={{ margin: 0 }}>{formatBytes(info.limits.max_file_size, lang)}</dd>
            </div>
          </dl>
          <p style={{ ...muted, display: "flex", flexWrap: "wrap", gap: 16, marginBlockStart: 16 }}>
            <a href="/api/v1/openapi.yaml">{t("installation.spec")}</a>
            <a href="/readyz">{t("installation.readiness")}</a>
          </p>
        </>
      )}
    </div>
  );
}
