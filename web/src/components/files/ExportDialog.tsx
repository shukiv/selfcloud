// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState } from "react";
import { Alert, Button, Modal, Space, Spin, theme as antdTheme } from "antd";
import { failureKey, startExport, type Failure } from "../../lib/files/convert";
import { downloadExport, liveExportDeps } from "../../lib/files/convertLive";
import { useT } from "../../lib/i18n";

// ExportDialog makes an export of a document in one format, and starts its download when it is ready. It shows the work
// as text and a spinner, not as a percentage: the server reports no progress. Closing the dialog stops the watch only: the
// server finishes the export, and no download starts after that.
//
// Three ends are shown, and none of them is a success by default. A download that cannot start is an error with a try again.
// An outage the server names is an error with a try again, which asks the server again. A failed conversion has no retry: the
// server keeps the refusal for a day.
type Phase =
  | { kind: "making" }
  | { kind: "ready" }
  | { kind: "downloadFailed" }
  | { kind: "failed"; why: Failure }
  | { kind: "slow" };

export default function ExportDialog({ nod, format, onClose }: { nod: string; format: string; onClose: () => void }) {
  const { t } = useT();
  const { token } = antdTheme.useToken();
  const [phase, setPhase] = useState<Phase>({ kind: "making" });
  // attempt changes when the person asks for the export again, which starts the watch over.
  const [attempt, setAttempt] = useState(0);
  // href is the export's address once it is ready. It is kept for the download again button and never shown.
  const href = useRef<string | null>(null);

  // startDownload starts the browser's download of the ready file. A download that does not start is shown as one.
  const startDownload = (address: string) => {
    setPhase({ kind: "ready" });
    downloadExport(address).then(
      (ok) => {
        if (!ok) setPhase({ kind: "downloadFailed" });
      },
      () => setPhase({ kind: "downloadFailed" }),
    );
  };

  useEffect(() => {
    const stop = startExport(
      nod,
      format,
      {
        making: () => setPhase({ kind: "making" }),
        ready: (h) => {
          href.current = h;
          startDownload(h);
        },
        failed: (why) => setPhase({ kind: "failed", why }),
        slow: () => setPhase({ kind: "slow" }),
      },
      liveExportDeps(),
    );
    return stop;
    // startDownload is defined per render but only uses setPhase, which is stable.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nod, format, attempt]);

  const canAgain = (phase.kind === "ready" || phase.kind === "downloadFailed") && href.current !== null;

  return (
    <Modal
      open
      title={t("files.convert.exportTitle", { format: format.toUpperCase() })}
      onCancel={onClose}
      maskClosable={false}
      destroyOnHidden
      footer={
        <Space>
          {canAgain && <Button onClick={() => href.current && startDownload(href.current)}>{t("files.convert.again")}</Button>}
          <Button type="primary" onClick={onClose}>
            {t("common.close")}
          </Button>
        </Space>
      }
    >
      <div role="status" aria-live="polite">
        {phase.kind === "making" && (
          <Space align="start" size="middle">
            <span role="img" aria-label={t("files.convert.making")}>
              <Spin />
            </span>
            <p style={{ margin: 0 }}>
              <span>{t("files.convert.making")}</span>
              <br />
              <span style={{ color: token.colorTextSecondary }}>{t("files.convert.exportNote")}</span>
            </p>
          </Space>
        )}
        {phase.kind === "ready" && <Alert type="success" showIcon role="presentation" message={t("files.convert.ready")} />}
      </div>
      {phase.kind === "downloadFailed" && <Alert type="error" showIcon role="alert" message={t("error.generic")} />}
      {phase.kind === "failed" && (
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          <Alert type="error" showIcon role="alert" message={t(failureKey(phase.why))} />
          {phase.why === "unavailable" && <Button onClick={() => setAttempt((n) => n + 1)}>{t("files.convert.retry")}</Button>}
        </Space>
      )}
      {phase.kind === "slow" && (
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          <Alert type="warning" showIcon role="alert" message={t("files.convert.slow")} />
          <Button onClick={() => setAttempt((n) => n + 1)}>{t("files.convert.retry")}</Button>
        </Space>
      )}
    </Modal>
  );
}
