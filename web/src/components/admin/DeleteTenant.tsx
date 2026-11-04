// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, App, Button, Input, Modal } from "antd";
import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useLang, useT } from "../../lib/i18n";
import { formatBytes } from "../../lib/files/format";
import { useFlight } from "../../lib/flight";
import { deleteAccount, getJob, usage } from "../../lib/manage/api";
import type { Job } from "../../lib/manage/types";
import { failureText } from "../../lib/manage/failure";
import { poll } from "../../lib/jobs/backoff";
import { typedNameMatches } from "../../lib/manage/confirm";
import { queryKey } from "../../lib/query";
import { sessionChanged } from "../../lib/session";
import { field } from "./adminStyles";
import { useGuard } from "./hooks";

type Stage = "confirm" | "deleting" | "background" | "failed";

// DeleteTenant deletes a tenant for good. The dialog lists what is lost, asks for the name typed, and then polls the
// deletion job until it ends. A poll that gives up says the deletion goes on in the background: it is not shown as failed.
// The dialog cannot be closed while the deletion runs, since it cannot be undone once started.
export default function DeleteTenant({
  id,
  name,
  onClose,
  onDeleted,
}: {
  id: string;
  name: string;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const { t, tp } = useT();
  const lang = useLang();
  const flight = useGuard();
  const [stage, setStage] = useState<Stage>("confirm");
  const [typed, setTyped] = useState("");
  const [error, setError] = useState<string | null>(null);
  // started is true from the first send until the deletion fails: a second click while it is out sends nothing.
  const started = useRef(false);
  const stopPolling = useRef<(() => void) | null>(null);
  const request = useFlight();
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [sending, setSending] = useState(false);
  const figuresQ = useQuery({ queryKey: queryKey("usage", id), queryFn: () => usage(id), retry: false });
  const figures = figuresQ.data ?? null;
  const ok = typedNameMatches(typed, name);

  useEffect(() => () => stopPolling.current?.(), []);

  const fail = (message: string) => {
    started.current = false;
    setStage("failed");
    setError(message);
  };

  const start = () => {
    if (!ok || started.current) return;
    started.current = true;
    void flight(async () => {
      const changed = sessionChanged();
      setStage("deleting");
      setError(null);
      // The request that asks for the deletion can be cancelled while it is out: the answer may or may not have come (fu4).
      const signal = request.start();
      setSending(true);
      let job: Job;
      try {
        job = await deleteAccount(id, signal);
      } catch (err) {
        request.end();
        setSending(false);
        if (changed() || signal.aborted) return;
        fail(failureText(err, lang));
        return;
      }
      request.end();
      setSending(false);
      try {
        if (changed()) return;
        stopPolling.current = poll(
          () => getJob(job.id),
          (j) => j.status === "succeeded" || j.status === "failed",
          (j) => {
            stopPolling.current = null;
            if (changed()) return;
            if (j.status === "succeeded") {
              started.current = false;
              onDeleted();
            } else {
              fail(t("tenant.delete.jobFailed"));
            }
          },
          (err) => {
            stopPolling.current = null;
            if (changed()) return;
            fail(failureText(err, lang));
          },
          () => {
            stopPolling.current = null;
            if (changed()) return;
            setStage("background");
          },
        );
      } catch (err) {
        if (changed()) return;
        fail(failureText(err, lang));
      }
    });
  };

  const close = () => {
    if (stage === "deleting") return;
    onClose();
  };

  // cancelSend closes the dialog while the request that asks for the deletion is out: the request is aborted, and the
  // person is told it may or may not have been started. Once the job exists, the dialog stays until it ends.
  const cancelSend = () => {
    if (!request.abort()) return;
    void message.warning(t("common.cancelledMaybe"));
    // The deletion may have started: the tenant list and this tenant are read again, as after a success (fu4 review 3).
    void qc.invalidateQueries({ queryKey: queryKey("admin-tenants") });
    void qc.invalidateQueries({ queryKey: queryKey("admin-tenant", id) });
    onClose();
  };

  const running = stage === "deleting";
  const footer =
    stage === "background"
      ? [
          <Button key="close" onClick={close}>
            {t("common.close")}
          </Button>,
        ]
      : running
        ? sending
          ? [
              <Button key="cancel" onClick={cancelSend}>
                {t("common.cancel")}
              </Button>,
            ]
          : null
        : [
            <Button key="cancel" onClick={close}>
              {t("common.cancel")}
            </Button>,
            <Button key="go" danger type="primary" disabled={!ok} onClick={start}>
              {t("tenant.delete.action", { name })}
            </Button>,
          ];

  return (
    <Modal
      open
      title={t("tenant.delete.title", { name })}
      closable={!running || sending}
      keyboard={!running || sending}
      maskClosable={false}
      onCancel={sending ? cancelSend : close}
      footer={footer}
    >
      {stage === "deleting" && <p role="status">{t("tenant.delete.running")}</p>}
      {stage === "background" && <p role="status">{t("tenant.delete.background")}</p>}
      {stage !== "deleting" && stage !== "background" && (
        <div style={{ display: "grid", gap: 12 }}>
          <p>{t("tenant.delete.lost")}</p>
          {figures !== null && (
            <ul style={{ margin: 0, paddingInlineStart: 16 }}>
              <li>{tp("tenant.delete.people", figures.users_active + figures.users_pending)}</li>
              <li>{tp("tenant.delete.files", figures.files)}</li>
              <li>{t("tenant.delete.size", { size: formatBytes(figures.used_bytes, lang) })}</li>
            </ul>
          )}
          <div style={field}>
            <label htmlFor="delete-tenant-name">{t("confirm.typeName", { name })}</label>
            <Input id="delete-tenant-name" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} autoFocus />
          </div>
        </div>
      )}
      {error !== null && <Alert type="error" showIcon role="alert" message={error} style={{ marginBlockStart: 12 }} />}
    </Modal>
  );
}
