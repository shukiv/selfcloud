// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { Table, Typography } from "antd";
import { LogOut } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import IconButton from "./IconButton";
import Alert from "./Alert";
import Confirm from "./Confirm";
import EmptyState from "./EmptyState";
import { cliDevices, cliRevokeDevice, type CliDevice } from "../lib/cli";
import { errorMessage, type Message } from "../lib/errors";
import { useLang, useT } from "../lib/i18n";
import { formatDate } from "../lib/files/format";
import { queryKey } from "../lib/query";
import { session } from "../lib/session";
import { useStore } from "../lib/store";

// CliDevices is the one list of the signed-in person's terminal sign-ins (decision 125): one row a device that still works.
// Its name, when it was signed in and when it ends, and a revoke icon that asks before it ends the device. A device is
// added from the terminal, so the empty state offers no action. The list is the person's own: the server returns no other.
export default function CliDevices() {
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

  return (
    <div>
      <Alert message={notice} tone="success" />
      <Alert message={error} />
      <Table<CliDevice>
        rowKey="id"
        dataSource={rows}
        loading={q.isPending && signedIn}
        pagination={false}
        locale={{
          emptyText: (
            <EmptyState text={t("devices.empty")} size="compact" testId="devices-empty">
              <Typography.Text type="secondary">{t("devices.emptyText")}</Typography.Text>            </EmptyState>
          ),
        }}
        columns={[
          { title: t("devices.name"), dataIndex: "client_name", key: "name" },
          {
            title: t("devices.address"),
            dataIndex: "client_ip",
            key: "address",
            render: (v: string) => <Typography.Text type="secondary">{v}</Typography.Text>,
          },
          {
            title: t("devices.signedIn"),
            dataIndex: "created_at",
            key: "signedIn",
            render: (v: string) => formatDate(v, lang),
          },
          {
            title: t("devices.expires"),
            dataIndex: "expires_at",
            key: "expires",
            render: (v: string) => formatDate(v, lang),
          },
          {
            title: "",
            key: "actions",
            width: 56,
            render: (_: unknown, d: CliDevice) => (
              <IconButton
                label={t("devices.revokeLabel", { name: d.client_name })}
                icon={<LogOut size={18} aria-hidden="true" focusable="false" />}
                onClick={() => {
                  setNotice(null);
                  setError(null);
                  setRevoking(d);
                }}
              />
            ),
          },
        ]}
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
