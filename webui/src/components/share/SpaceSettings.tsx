// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState } from "react";
import { Alert, Modal, Switch } from "antd";
import { useT } from "../../lib/i18n";
import { ApiError } from "../../lib/apiError";
import type { FolderAccess } from "../../lib/files/types";
import { updateSpaceSettings, type SpaceSettings as Flags } from "../../lib/sharing/api";
import { sessionChanged } from "../../lib/session";

// SpaceSettings holds a team space's restrictions. Only a manager of the space's root changes them, and the server
// checks each change. Each switch saves when it changes; a refused change puts the switch back and says why by code.
//
// Props (used by Shell.tsx in v3 and by the Spaces page in v4):
//   space    the FolderAccess of the team space (its four flags are read from it)
//   onclose  the close button, Escape, or the dialog's own close
//   onsaved  called with the space and its new flags after each change that the server accepted
export interface SpaceSettingsProps {
  space: FolderAccess;
  onclose: () => void;
  onsaved: (f: FolderAccess) => void;
}

const FLAGS = ["members_only", "sharing_managers_only", "no_download_for_viewers", "allow_links"] as const;
type Flag = (typeof FLAGS)[number];

function flagsOf(space: FolderAccess): Required<Flags> {
  return {
    members_only: space.members_only,
    sharing_managers_only: space.sharing_managers_only,
    no_download_for_viewers: space.no_download_for_viewers,
    allow_links: space.allow_links,
  };
}

export default function SpaceSettings({ space, onclose, onsaved }: SpaceSettingsProps) {
  const { t } = useT();
  const [values, setValues] = useState<Required<Flags>>(() => flagsOf(space));
  const [status, setStatus] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setValues(flagsOf(space));
  }, [space]);

  const save = async (key: Flag, on: boolean) => {
    const before = values[key];
    const stale = sessionChanged();
    setValues((v) => ({ ...v, [key]: on }));
    setBusy(true);
    setError(null);
    setStatus(null);
    try {
      await updateSpaceSettings(space.id, { [key]: on });
      if (stale()) return;
      setStatus(t("space.saved"));
      onsaved({ ...space, ...values, [key]: on });
    } catch (err) {
      if (stale()) return;
      setValues((v) => ({ ...v, [key]: before }));
      setError(err instanceof ApiError && err.code === "forbidden" ? t("space.err.forbidden") : t("error.generic"));
    } finally {
      setBusy(false);
    }
  };

  const rows: Array<{ key: Flag; label: string; hint: string }> = [
    { key: "members_only", label: t("space.membersOnly"), hint: t("space.membersOnlyHint") },
    { key: "sharing_managers_only", label: t("space.managersOnly"), hint: t("space.managersOnlyHint") },
    { key: "no_download_for_viewers", label: t("space.noDownload"), hint: t("space.noDownloadHint") },
    { key: "allow_links", label: t("space.links"), hint: t("space.linksHint") },
  ];

  return (
    <Modal
      open
      title={t("space.title", { name: space.name })}
      onCancel={onclose}
      maskClosable={false}
      footer={null}
      width={720}
      destroyOnClose
    >
      <div style={{ display: "grid", gap: 16 }}>
        <p className="live" role="status" aria-live="polite" style={{ margin: 0 }}>
          {status ?? ""}
        </p>
        {error && <Alert type="error" showIcon role="alert" message={error} />}
        {rows.map((r) => (
          <label key={r.key} className="switch" style={{ display: "flex", alignItems: "flex-start", gap: 12, minHeight: 44 }}>
            <Switch
              id={`space-${r.key}`}
              checked={values[r.key]}
              disabled={busy}
              onChange={(on) => void save(r.key, on)}
            />
            <span style={{ display: "grid", gap: 4 }}>
              <span style={{ fontWeight: 600 }}>{r.label}</span>
              <span style={{ fontSize: 13, color: "var(--ant-color-text-secondary)" }}>{r.hint}</span>
            </span>
          </label>
        ))}
      </div>
    </Modal>
  );
}
