// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState } from "react";
import { Alert, Button, Modal, Space, Spin, theme as antdTheme } from "antd";
import { downloadZip, type ZipPhase } from "../../lib/files/zips";
import { translatePlural, useLang, useT } from "../../lib/i18n";

// ZipDialog shows a zip being made, the items it left out, and a refusal in words. The page starts the zip and owns its
// phase. Closing the dialog stops the progress view only: the server finishes the zip, and no download starts.
//
// The status region is in the dialog from its first render, and its text is written after the first render. A live
// region that is already present when its text changes is announced; text that is present when the region appears
// often is not.
export default function ZipDialog({ phase, onClose }: { phase: ZipPhase; onClose: () => void }) {
  const { t } = useT();
  const lang = useLang();
  const { token } = antdTheme.useToken();
  const [live, setLive] = useState(false);
  useEffect(() => setLive(true), []);
  return (
    <Modal
      open
      title={t("files.zip.title")}
      onCancel={onClose}
      maskClosable={false}
      destroyOnHidden
      footer={
        <Space>
          {phase.kind === "ready" && <Button onClick={() => downloadZip(phase.zip)}>{t("files.zip.again")}</Button>}
          <Button type="primary" onClick={onClose}>
            {t("common.close")}
          </Button>
        </Space>
      }
    >
      <div role="status" aria-live="polite">
        {live && phase.kind === "making" && (
          <Space align="start" size="middle">
            <span role="img" aria-label={t("files.zip.making")}>
              <Spin />
            </span>
            <p style={{ margin: 0 }}>
              <span>{t("files.zip.making")}</span>
              <br />
              <span style={{ color: token.colorTextSecondary }}>{t("files.zip.closeNote")}</span>
            </p>
          </Space>
        )}
        {live && phase.kind === "ready" && (
          <Space direction="vertical" size="middle" style={{ width: "100%" }}>
            <Alert type="success" showIcon role="presentation" message={t("files.zip.ready")} />
            {phase.skipped > 0 && (
              <Alert type="warning" showIcon role="presentation" message={translatePlural(lang, "files.zip.skipped", phase.skipped)} />
            )}
          </Space>
        )}
      </div>
      {phase.kind === "failed" && <Alert type="error" showIcon role="alert" message={phase.text} />}
      {phase.kind === "slow" && <Alert type="warning" showIcon role="alert" message={t("files.zip.slow")} />}
    </Modal>
  );
}
