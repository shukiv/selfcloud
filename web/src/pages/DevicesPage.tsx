// SPDX-License-Identifier: AGPL-3.0-or-later

import { Spin } from "antd";
import CliDevices from "../components/CliDevices";
import { useCliText } from "../lib/cli/cliText";
import { useT } from "../lib/i18n";

// DevicesPage is Signed-in devices (decision 125): the terminals signed in as the person who opens it, for every
// signed-in person except an installation admin, who holds no device. It is an account-menu page, not a Manage page.
// Its words are in their own catalogue (lib/cli/cliText.ts), which loads with the page: a spinner stands in until it is there.
export default function DevicesPage() {
  const cliReady = useCliText();
  const { t } = useT();
  if (!cliReady) {
    return (
      <div style={{ textAlign: "center", padding: 24 }}>
        <Spin />
      </div>
    );
  }
  return (
    <div>
      <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 4px" }}>{t("devices.title")}</h1>
      <p style={{ margin: "0 0 16px" }}>{t("devices.intro")}</p>
      <CliDevices />
    </div>
  );
}
