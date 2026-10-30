// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState, type FormEvent } from "react";
import { Alert, Button, Checkbox, Drawer, Input, List, Space, Typography } from "antd";
import { ApiError } from "../../lib/apiError";
import { contentPath, deleteVersion, listVersions, restoreVersion, statNode, updateVersion } from "../../lib/files/api";
import { fileErrorText } from "../../lib/files/fileErrors";
import { formatBytes, formatDate } from "../../lib/files/format";
import { startDownloadURL } from "../../lib/files/ops";
import { can, downloadAllowed } from "../../lib/files/roles";
import type { Node, Version } from "../../lib/files/types";
import { useLang, useT } from "../../lib/i18n";

// Versions lists a file's earlier contents, newest first, and lets the person name, keep, restore and
// delete them, as their role allows. Errors are shown by code through fileErrorText, never as server text.
export default function Versions({
  node,
  onClose,
  onChanged,
}: {
  node: Node;
  onClose: () => void;
  onChanged: () => void;
}) {
  const { t } = useT();
  const lang = useLang();
  const [items, setItems] = useState<Version[]>([]);
  const [loading, setLoading] = useState(true);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [labels, setLabels] = useState<Record<string, string>>({});

  async function load() {
    setLoading(true);
    try {
      const page = await listVersions(node.id);
      setItems(page.items);
      setLabels(Object.fromEntries(page.items.map((v) => [v.id, v.label ?? ""])));
      setError(null);
    } catch (err) {
      setError(fileErrorText(err, lang));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
  }, [node.id]);

  // The newest version ID must come from the server; only the current content has the ID "current".
  async function restore(v: Version) {
    setMessage(null);
    setError(null);
    try {
      const fresh = await statNode(node.id);
      await restoreVersion(node.id, v.id, fresh.etag ?? "");
      setMessage(t("files.versions.restored"));
      onChanged();
      await load();
    } catch (err) {
      setError(fileErrorText(err, lang));
    }
  }

  async function keep(v: Version, on: boolean) {
    setError(null);
    try {
      await updateVersion(node.id, v.id, { keep_forever: on });
      await load();
    } catch (err) {
      if (err instanceof ApiError && err.code === "conflict" && typeof err.details.max_kept === "number") {
        setError(t("files.versions.cap", { max: err.details.max_kept }));
      } else {
        setError(fileErrorText(err, lang));
      }
    }
  }

  async function saveLabel(v: Version) {
    setError(null);
    try {
      const value = (labels[v.id] ?? "").trim();
      await updateVersion(node.id, v.id, { label: value === "" ? null : value });
      setMessage(t("files.versions.labelSaved"));
      await load();
    } catch (err) {
      setError(fileErrorText(err, lang));
    }
  }

  async function remove(v: Version) {
    setError(null);
    try {
      await deleteVersion(node.id, v.id);
      setMessage(t("files.versions.deleted"));
      await load();
    } catch (err) {
      setError(fileErrorText(err, lang));
    }
  }

  async function download(v: Version) {
    const url = v.current ? contentPath(node.id) : `/api/v1/nodes/${node.id}/versions/${v.id}/content?disposition=attachment`;
    const ok = await startDownloadURL(url);
    if (!ok) setError(t("files.err.downloadRestricted"));
  }

  function submitLabel(e: FormEvent, v: Version) {
    e.preventDefault();
    void saveLabel(v);
  }

  return (
    <Drawer open maskClosable={false} width="min(560px, 100vw)" title={t("files.versions.title")} onClose={onClose} data-testid="versions">
      <Typography.Paragraph type="secondary">
        <bdi>{node.name}</bdi>
      </Typography.Paragraph>
      {error && <Alert type="error" showIcon role="alert" message={error} style={{ marginBlockEnd: 12 }} />}
      {message && <Alert type="success" showIcon role="status" message={message} style={{ marginBlockEnd: 12 }} />}
      {loading ? (
        <p role="status">{t("app.loading")}</p>
      ) : items.length <= 1 ? (
        <p>{t("files.versions.none")}</p>
      ) : null}
      <List
        dataSource={items}
        rowKey="id"
        renderItem={(v) => (
          <List.Item style={{ display: "block" }} data-testid="version">
            <Space wrap>
              <strong>{v.current ? t("files.versions.current") : formatDate(v.created_at, lang)}</strong>
              <span>{formatBytes(v.size, lang)}</span>
              {!v.current && v.replaced_at && <span>{t("files.versions.replaced", { date: formatDate(v.replaced_at, lang) })}</span>}
            </Space>
            <Space wrap style={{ marginBlock: 8 }}>
              {downloadAllowed(node) && <Button onClick={() => void download(v)}>{t("files.versions.download")}</Button>}
              {!v.current && can.restoreVersion(node.role) && (
                <Button onClick={() => void restore(v)}>{t("files.versions.restore")}</Button>
              )}
              {can.editVersion(node.role) && !v.current && (
                <>
                  <Checkbox checked={v.keep_forever} onChange={(e) => void keep(v, e.target.checked)}>
                    {t("files.versions.keep")}
                  </Checkbox>
                  <Button danger onClick={() => void remove(v)}>
                    {t("files.versions.delete")}
                  </Button>
                </>
              )}
            </Space>
            {can.editVersion(node.role) ? (
              <form onSubmit={(e) => submitLabel(e, v)} style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
                <label htmlFor={`label-${v.id}`} style={{ position: "absolute", width: 1, height: 1, overflow: "hidden" }}>
                  {t("files.versions.label")}
                </label>
                <Input id={`label-${v.id}`} maxLength={100} style={{ flex: "1 1 200px" }} value={labels[v.id] ?? ""} onChange={(e) => setLabels({ ...labels, [v.id]: e.target.value })} />
                <Button htmlType="submit">{t("files.versions.labelSave")}</Button>
              </form>
            ) : (
              v.label && <Typography.Text type="secondary">{v.label}</Typography.Text>
            )}
          </List.Item>
        )}
      />
    </Drawer>
  );
}
