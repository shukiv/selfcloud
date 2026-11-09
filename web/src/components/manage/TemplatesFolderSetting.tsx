// SPDX-License-Identifier: AGPL-3.0-or-later

// The account's templates folder (nt2, decision 105): the directory whose files the New menu's gallery offers as the
// account's own templates. Only the account's owner sees it; the choice is made with the folder picker and saved with PUT.
// null clears it. An error keeps the old value on the page.
import { useState } from "react";
import { Alert, Button, Space } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useLang, useT } from "../../lib/i18n";
import { useStore } from "../../lib/store";
import { session } from "../../lib/session";
import { mayOpen } from "../../lib/routes";
import { myFolders, statOrNull } from "../../lib/files/api";
import { getTemplatesFolder, setTemplatesFolder } from "../../lib/files/templates";
import { fileErrorText } from "../../lib/files/fileErrors";
import PickDestination, { type Destination } from "../files/PickDestination";

export default function TemplatesFolderSetting({ account }: { account: string }) {
  const { t } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const user = useStore(session)?.user;
  const owner = mayOpen({ area: "manage", section: "overview" }, user);
  const key = ["templatesFolder", account];
  const [picking, setPicking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const q = useQuery({
    queryKey: key,
    enabled: owner && account !== "",
    queryFn: async () => {
      const nod = await getTemplatesFolder(account);
      if (!nod) return { nod: null as string | null, name: null as string | null };
      const n = await statOrNull(nod);
      return { nod, name: n?.name ?? null };
    },
  });
  const spaces = useQuery({ queryKey: ["templatesFolder", "spaces"], enabled: picking, queryFn: () => myFolders() });

  if (!owner) return null;

  async function save(nod: string | null) {
    setBusy(true);
    setError(null);
    try {
      await setTemplatesFolder(account, nod);
      setPicking(false);
      await qc.invalidateQueries({ queryKey: key });
    } catch (err) {
      setError(fileErrorText(err, lang));
    } finally {
      setBusy(false);
    }
  }

  const current = q.data;
  return (
    <section aria-labelledby="templates-setting" style={{ marginBottom: 24, maxWidth: 640 }}>
      <h2 id="templates-setting" style={{ fontSize: 15, fontWeight: 600, margin: "0 0 4px" }}>{t("settings.templates.title")}</h2>
      <p style={{ margin: "0 0 12px" }}>{t("settings.templates.help")}</p>
      {q.isError && <Alert type="error" showIcon role="alert" message={fileErrorText(q.error, lang)} style={{ marginBottom: 12 }} />}
      {error !== null && <Alert type="error" showIcon role="alert" message={error} style={{ marginBottom: 12 }} />}
      <p style={{ margin: "0 0 12px" }} data-testid="templates-current">
        {current?.nod ? (current.name ?? t("settings.templates.unreachable")) : q.isSuccess ? t("settings.templates.none") : ""}
      </p>
      <Space>
        <Button onClick={() => setPicking(true)} disabled={busy}>{t("settings.templates.change")}</Button>
        {current?.nod && (
          <Button danger onClick={() => void save(null)} loading={busy}>{t("settings.templates.clear")}</Button>
        )}
      </Space>
      {picking && (
        <PickDestination
          mode="shortcut"
          spaces={spaces.data ?? []}
          heading={t("settings.templates.pick")}
          action={t("settings.templates.save")}
          onPick={(d: Destination) => {
            if (d.dir) void save(d.dir);
          }}
          onClose={() => setPicking(false)}
        />
      )}
    </section>
  );
}
