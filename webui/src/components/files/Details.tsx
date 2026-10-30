// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type CSSProperties } from "react";
import { Button, Drawer } from "antd";
import { useT, useLang } from "../../lib/i18n";
import { session } from "../../lib/session";
import { useStore } from "../../lib/store";
import { formatBytes, formatDate } from "../../lib/files/format";
import { personLabel } from "../../lib/files/people";
import { kindOf } from "../../lib/files/kind";
import type { Node } from "../../lib/files/types";
import Icon from "./Icon";

// Details is the side drawer for one node. The sharing part of the Svelte details view is left out
// until the sharing slice: no share action and no access list are shown here.
export default function Details({
  node,
  canVersions,
  onClose,
  onVersions,
}: {
  node: Node;
  canVersions: boolean;
  onClose: () => void;
  onVersions: () => void;
}) {
  const { t } = useT();
  const lang = useLang();
  const me = useStore(session);
  const [copied, setCopied] = useState<"idle" | "done" | "failed">("idle");

  const who = (id: string | undefined): string | null => personLabel(id, me, t);

  async function copyHash() {
    try {
      await navigator.clipboard.writeText(node.sha256 ?? "");
      setCopied("done");
    } catch {
      setCopied("failed");
    }
  }

  return (
    <Drawer
      open
      maskClosable={false}
      width="min(420px, 100vw)"
      title={
        <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
          <Icon kind={kindOf(node)} size={24} />
          <bdi>{node.name}</bdi>
        </span>
      }
      onClose={onClose}
      data-testid="details"
    >
      <dl style={{ display: "grid", gridTemplateColumns: "max-content minmax(0, 1fr)", gap: "8px 16px", margin: 0 }}>
        {node.kind === "file" && (
          <>
            <dt>{t("files.details.size")}</dt>
            <dd style={DD}>{formatBytes(node.size, lang)}</dd>
            <dt>{t("files.details.type")}</dt>
            <dd style={DD}>
              <bdi>{node.mime_type ?? ""}</bdi>
            </dd>
          </>
        )}
        <dt>{t("files.details.modified")}</dt>
        <dd style={DD}>{formatDate(node.modified_at, lang)}</dd>
        <dt>{t("files.details.created")}</dt>
        <dd style={DD}>{formatDate(node.created_at, lang)}</dd>
        {who(node.created_by) && (
          <>
            <dt>{t("files.details.createdBy")}</dt>
            <dd style={DD}>
              <bdi>{who(node.created_by)}</bdi>
            </dd>
          </>
        )}
        {who(node.owner_id) && (
          <>
            <dt>{t("files.details.owner")}</dt>
            <dd style={DD}>
              <bdi>{who(node.owner_id)}</bdi>
            </dd>
          </>
        )}
        <dt>{t("files.details.role")}</dt>
        <dd style={DD}>{node.role ? t(`role.${node.role}`) : ""}</dd>
        {node.sha256 && (
          <>
            <dt>{t("files.details.sha256")}</dt>
            <dd style={DD}>
              <code style={{ display: "block", overflowWrap: "anywhere", marginBlockEnd: 8 }}>{node.sha256}</code>
              <Button onClick={copyHash}>{t("files.details.copy")}</Button>{" "}
              <span role="status">
                {copied === "done" ? t("files.details.copied") : copied === "failed" ? t("files.details.copyFailed") : ""}
              </span>
            </dd>
          </>
        )}
      </dl>
      {node.kind === "file" && canVersions && (
        <Button block style={{ marginBlockStart: 16 }} onClick={onVersions}>
          {t("files.details.versions")}
        </Button>
      )}
    </Drawer>
  );
}

const DD: CSSProperties = { margin: 0, minWidth: 0, overflowWrap: "anywhere" };
