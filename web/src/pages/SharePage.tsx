// SPDX-License-Identifier: AGPL-3.0-or-later

// The share page (decision 82): where the system share sheet sends the files a person shared with Selfcloud. The files are
// taken from the service worker once; they are listed, saved to a place the person picks (My files at first), and queued into
// the upload manager, which does the rest as it does for a file picked on the files page. Nothing uploads before Upload.

import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { Alert, Button, List, Space, Spin } from "antd";
import { Folder, FolderOpen } from "lucide-react";
import IconButton from "../components/IconButton";
import PickDestination, { type Destination } from "../components/files/PickDestination";
import { getMe, myFolders } from "../lib/files/api";
import { formatBytes } from "../lib/files/format";
import { fileErrorText } from "../lib/files/fileErrors";
import { uploadTargetWhere } from "../lib/files/ops";
import { filesURL } from "../lib/files/route";
import type { FolderAccess } from "../lib/files/types";
import { manager } from "../lib/files/uploads";
import { useLang, useT } from "../lib/i18n";
import { dropShare, takeShare } from "../lib/pwa/share";
import { session } from "../lib/session";
import { useStore } from "../lib/store";

type Phase = "loading" | "none" | "refused" | "ready";

export default function SharePage() {
  const { t } = useT();
  const lang = useLang();
  const navigate = useNavigate();
  const { search } = useLocation();
  const me = useStore(session);
  const id = new URLSearchParams(search).get("id") ?? "";
  const viewingAs = me?.impersonation != null;

  const [phase, setPhase] = useState<Phase>("loading");
  const [files, setFiles] = useState<File[]>([]);
  const [spaces, setSpaces] = useState<FolderAccess[]>([]);
  const [personalID, setPersonalID] = useState<string | null>(null);
  // dest is where the files go; its name is set once the person has picked a place, and My files is the default.
  const [dest, setDest] = useState<Destination | null>(null);
  const [picking, setPicking] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    // A person who views as another person may not save here: the files are refused and the worker drops them.
    if (viewingAs) {
      dropShare(id);
      setPhase("refused");
      return () => {
        live = false;
      };
    }
    if (id === "") {
      setPhase("none");
      return () => {
        live = false;
      };
    }
    void (async () => {
      try {
        const [got, mine, who] = await Promise.all([takeShare(id), myFolders(), getMe()]);
        if (!live) return;
        setSpaces(mine);
        setPersonalID(who.personal_folder_id);
        if (got.length === 0) {
          setPhase("none");
          return;
        }
        setFiles(got);
        setDest(who.personal_folder_id ? { folder: who.personal_folder_id, dir: null } : null);
        setPhase("ready");
      } catch (err) {
        if (!live) return;
        setError(fileErrorText(err, lang));
        setPhase("none");
      }
    })();
    return () => {
      live = false;
    };
    // The page reads the share once for its address; lang only words an error.
  }, [id, viewingAs]);

  // upload queues each file into the chosen place, as a file picked on the files page is queued, and opens that place.
  function upload() {
    if (!dest) return;
    const target = uploadTargetWhere({ folder: dest.folder, dirID: dest.dir, path: null }, "");
    if (!target || files.length === 0) return;
    manager.add(files.map((file) => ({ file, target })));
    setFiles([]);
    navigate(filesURL(dest.folder, dest.dir));
  }

  // cancel forgets the files and goes to My files; the worker is told to drop any it still holds.
  function cancel() {
    dropShare(id);
    setFiles([]);
    navigate("/files");
  }

  if (phase === "loading") {
    return (
      <div role="status" style={{ display: "grid", placeItems: "center", minHeight: 240 }}>
        <Spin tip={t("app.loading")} />
      </div>
    );
  }

  return (
    <section style={{ maxWidth: 560, margin: "0 auto", padding: 16 }} aria-labelledby="share-title">
      <h1 id="share-title" style={{ fontSize: 20, margin: "0 0 16px" }}>
        {t("shareTarget.title")}
      </h1>
      {error && <Alert type="error" showIcon message={error} style={{ marginBlockEnd: 12 }} />}

      {phase === "refused" && <Alert type="warning" showIcon message={t("shareTarget.viewingAs")} />}

      {phase === "none" && (
        <Space direction="vertical" size={12}>
          <Alert type="info" showIcon message={t("shareTarget.none")} />
          <IconButton label={t("nav.files")} icon={<Folder size={18} aria-hidden="true" focusable="false" />} onClick={() => navigate("/files")} />
        </Space>
      )}

      {phase === "ready" && (
        <>
          <List
            size="small"
            dataSource={files}
            renderItem={(f, i) => (
              <List.Item key={`${i}-${f.name}`} style={{ gap: 12 }}>
                <span dir="auto" style={{ flex: 1, minWidth: 0, overflowWrap: "anywhere" }}>
                  {f.name}
                </span>
                <span style={{ whiteSpace: "nowrap", opacity: 0.75 }}>{formatBytes(f.size, lang)}</span>
              </List.Item>
            )}
          />
          <p style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap", margin: "16px 0" }}>
            <span>{t("shareTarget.saveTo")}:</span>
            <bdi>{dest?.name ?? t("nav.files")}</bdi>
            <IconButton
              label={t("shareTarget.change")}
              icon={<FolderOpen size={18} aria-hidden="true" focusable="false" />}
              onClick={() => setPicking(true)}
            />
          </p>
          <Space wrap>
            <Button onClick={cancel}>{t("shareTarget.cancel")}</Button>
            <Button type="primary" disabled={dest === null || files.length === 0} onClick={upload}>
              {t("shareTarget.upload")}
            </Button>
          </Space>
        </>
      )}

      {picking && (
        <PickDestination
          mode="save"
          spaces={spaces}
          startAt={personalID ?? undefined}
          onPick={(d) => {
            setDest(d);
            setPicking(false);
          }}
          onClose={() => setPicking(false)}
        />
      )}
    </section>
  );
}
