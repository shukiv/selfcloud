// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { LogOut } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Spin } from "antd";
import Alert from "./Alert";
import Confirm from "./Confirm";
import Listing, { type ListColumn } from "./files/Listing";
import { cliDevices, cliRevokeDevice, type CliDevice } from "../lib/cli";
import { useCliText } from "../lib/cli/cliText";
import { errorMessage, type Message } from "../lib/errors";
import { useLang, useT } from "../lib/i18n";
import { formatDate } from "../lib/files/format";
import { queryKey } from "../lib/query";
import { session } from "../lib/session";
import { useStore } from "../lib/store";

// CliDevices is the one list of the signed-in person's terminal sign-ins (decision 125), drawn with the unified preset list
// (Listing, decision 114). A row is a device that still works: its name, the address the terminal started from (a secondary
// column, on a phone the name's second line), when it was signed in and when it ends, and a revoke icon that asks before it
// ends the device. A device is added from the terminal, so the empty state offers no action. The server returns only the
// person's own devices.
export default function CliDevices() {
  const cliReady = useCliText();
  const { t } = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const signedIn = useStore(session) !== null;
  const [revoking, setRevoking] = useState<CliDevice | null>(null);
  const [notice, setNotice] = useState<Message | null>(null);
  const [error, setError] = useState<Message | null>(null);

  const q = useQuery({
    queryKey: queryKey("cli-devices"),
    queryFn: async () => (await cliDevices()).devices,
    enabled: signedIn,
    refetchOnMount: "always",
  });

  const revoke = useMutation({
    mutationFn: (id: string) => cliRevokeDevice(id),
    onSuccess: async () => {
      setRevoking(null);
      setError(null);
      setNotice({ key: "devices.revoked" });
      await qc.invalidateQueries({ queryKey: queryKey("cli-devices") });
    },
    onError: (err) => {
      setError(errorMessage(err, "generic"));
    },
  });

  const rows = q.data ?? [];

  const columns: ListColumn<CliDevice>[] = [
    { key: "name", title: t("devices.name"), render: (d) => <bdi>{d.client_name}</bdi> },
    {
      key: "address",
      title: t("devices.address"),
      secondary: true,
      render: (d) => <bdi dir="ltr">{d.client_ip}</bdi>,
    },
    { key: "signedIn", title: t("devices.signedIn"), render: (d) => formatDate(d.created_at, lang) },
    { key: "expires", title: t("devices.expires"), render: (d) => formatDate(d.expires_at, lang) },
  ];

  // Its words are in their own catalogue, which loads with the page: until it is there, a spinner stands in for the list.
  if (!cliReady) {
    return (
      <div style={{ textAlign: "center", padding: 24 }}>
        <Spin />
      </div>
    );
  }

  return (
    <div>
      <Alert message={notice} tone="success" />
      <Alert message={error} />
      <Listing<CliDevice>
        label={t("devices.title")}
        items={rows}
        rowLabel={(d) => d.client_name}
        columns={columns}
        actions={(d) => [
          {
            key: "revoke",
            label: t("devices.revokeLabel", { name: d.client_name }),
            icon: <LogOut size={18} aria-hidden="true" focusable="false" />,
            danger: true,
            run: () => {
              setNotice(null);
              setError(null);
              setRevoking(d);
            },
          },
        ]}
        empty={{ title: t("devices.empty"), description: t("devices.emptyText") }}
      />
      {revoking !== null && (
        <Confirm
          id="devices-revoke"
          title={t("devices.revokeTitle", { name: revoking.client_name })}
          message={t("devices.revokeBody")}
          action={t("devices.revokeOk")}
          danger
          busy={revoke.isPending}
          error={error === null ? null : t(error.key)}
          onconfirm={() => revoke.mutate(revoking.id)}
          onclose={() => setRevoking(null)}
        />
      )}
    </div>
  );
}
