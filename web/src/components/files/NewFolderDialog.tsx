// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState } from "react";
import { App, Button, Form, Input, Modal, type InputRef } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { useT, useLang } from "../../lib/i18n";
import { mkdir, mkdirIn } from "../../lib/files/api";
import { fileErrorText } from "../../lib/files/fileErrors";
import { joinPath } from "../../lib/files/location";
import { nameMessageKey, nameProblem, type UploadWhere } from "../../lib/files/ops";
import { filesURL } from "../../lib/files/route";
import { refreshTree } from "../../lib/query";
import { sessionChanged } from "../../lib/session";

// NewFolderDialog makes a folder in a place the sidebar's New acts on (decision 66). It is a lazy chunk, mounted only while it is
// open. A folder made off the files pages gets a message with a way to open it; on a files page the listing shows it, and the
// message says it was made. The dialog never closes on a click outside (maskClosable is off).
export default function NewFolderDialog({ where, openable, onClose }: { where: UploadWhere; openable: boolean; onClose: () => void }) {
  const { t } = useT();
  const lang = useLang();
  const { message } = App.useApp();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [name, setName] = useState("");
  // antd moves the focus into the dialog as it opens, after the field has mounted (as InviteDialog notes). The name field takes the
  // focus once that has happened, so a person who types at once (Alt+C, then F) keeps the first letters in the field.
  const nameRef = useRef<InputRef>(null);
  useEffect(() => {
    const timer = window.setTimeout(() => nameRef.current?.focus(), 0);
    return () => window.clearTimeout(timer);
  }, []);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function create() {
    if (busy) return;
    const clean = name.trim();
    const problem = nameProblem(clean);
    if (problem) {
      setError(t(nameMessageKey(problem)));
      return;
    }
    const path = joinPath(where.path, clean);
    if (!path) return;
    const changed = sessionChanged();
    setBusy(true);
    try {
      // A folder in a directory names the directory by its ID; the root keeps the path form (decision 74).
      const node = where.dirID ? await mkdirIn(where.folder, where.dirID, clean) : await mkdir(where.folder, path);
      if (changed()) return;
      onClose();
      // The branch the folder is in is read again, and the listing with it, so the new folder shows where it belongs.
      void refreshTree(qc, [{ folder: where.folder, dir: where.dirID }]);
      if (openable) {
        const target = filesURL(where.folder, node.id);
        void message.success({
          content: (
            <>
              {t("files.newFolder.created", { name: clean })}{" "}
              <Button type="link" size="small" style={{ padding: 0 }} onClick={() => navigate(target)}>
                {t("files.newFolder.open")}
              </Button>
            </>
          ),
          duration: 6,
        });
      } else {
        void message.success(t("files.newFolder.created", { name: clean }));
      }
    } catch (err) {
      if (!changed()) setError(fileErrorText(err, lang));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      title={t("files.newFolder.title")}
      okText={t("files.newFolder.submit")}
      cancelText={t("common.cancel")}
      onCancel={onClose}
      onOk={() => void create()}
      confirmLoading={busy}
      maskClosable={false}
      destroyOnClose
    >
      <Form layout="vertical" onFinish={() => void create()}>
        <Form.Item htmlFor="newfolder-name" label={t("files.newFolder.label")} validateStatus={error ? "error" : undefined} help={error ?? undefined}>
          <Input
            ref={nameRef}
            id="newfolder-name"
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              setError(null);
            }}
            aria-invalid={error ? true : undefined}
          />
        </Form.Item>
      </Form>
    </Modal>
  );
}
