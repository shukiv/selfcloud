// SPDX-License-Identifier: AGPL-3.0-or-later

import CliDevices from "../components/CliDevices";
import { useT } from "../lib/i18n";

// DevicesPage is Signed-in devices (decision 125): the terminals signed in as the person who opens it, for every
// signed-in person except an installation admin, who holds no device. It is an account-menu page, not a Manage page.
export default function DevicesPage() {
  const { t } = useT();
  return (
    <div>
      <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 4px" }}>{t("devices.title")}</h1>
      <p style={{ margin: "0 0 16px" }}>{t("devices.intro")}</p>
      <CliDevices />
    </div>
  );
}
