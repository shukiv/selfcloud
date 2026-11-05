// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState } from "react";
import { Button } from "antd";
import { confirmEmailChange } from "../lib/auth";
import { isLinkFailure } from "../lib/errors";
import { go } from "../lib/router";
import { useT } from "../lib/i18n";
import AuthLayout from "../components/AuthLayout";
import Alert from "../components/Alert";
import { readFragmentToken } from "./linkToken";

// The state of a confirmation: it is being made, it is done, the link no longer works, or it failed for another reason.
type Outcome = "working" | "done" | "unusable" | "failed";

// EmailChangePage confirms a new address. The token is in the address's fragment: it is read once, and removed from the
// address bar, before the confirmation is sent. The page signs no one in; the person signs in with the new address.
export default function EmailChangePage() {
  const { t } = useT();
  const [outcome, setOutcome] = useState<Outcome>("working");
  const read = useRef(false);

  useEffect(() => {
    if (read.current) return;
    read.current = true;
    const token = readFragmentToken();
    if (token === "") {
      setOutcome("unusable");
      return;
    }
    confirmEmailChange(token)
      .then(() => setOutcome("done"))
      .catch((err: unknown) => setOutcome(isLinkFailure(err) ? "unusable" : "failed"));
  }, []);

  return (
    <AuthLayout>
      <h1 style={{ marginTop: 0, fontSize: 24 }}>{t("emailChange.title")}</h1>
      {outcome === "working" && <p>{t("emailChange.working")}</p>}
      {outcome === "done" && <p>{t("emailChange.done")}</p>}
      {outcome === "unusable" && <Alert message={{ key: "emailChange.linkUsed" }} />}
      {outcome === "failed" && <Alert message={{ key: "emailChange.failed" }} />}
      {outcome !== "working" && (
        <Button type="primary" block onClick={() => go("/login", { replace: true })}>
          {t("emailChange.signIn")}
        </Button>
      )}
    </AuthLayout>
  );
}
