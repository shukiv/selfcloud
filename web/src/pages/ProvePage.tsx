// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState } from "react";
import { Button, Spin } from "antd";
import { confirmEmailProof } from "../lib/sharing/api";
import { errorMessage, type Message } from "../lib/errors";
import { go } from "../lib/router";
import { useT } from "../lib/i18n";
import AuthLayout from "../components/AuthLayout";
import Alert from "../components/Alert";
import DriveTextFailure from "../components/drive/DriveTextFailure";
import { useExternalText } from "../lib/share/externalText";
import { readFragmentToken } from "./linkToken";

// ProvePage redeems the token a proof mail carries, so the signed-in person's address is proved and the items people in
// other accounts shared with it appear. The token is in the address's fragment: it is read once, and removed from the
// address bar, before anything else happens, and travels to the server in the body only. A bad or absent token shows the
// same bad-link message the verify page shows. The page needs no session of its own to read: the proof is asked of the
// address the mail was sent to.
export default function ProvePage() {
  const { t } = useT();
  const [done, setDone] = useState(false);
  const [error, setError] = useState<Message | null>(null);
  const read = useRef(false);

  useEffect(() => {
    if (read.current) return;
    read.current = true;
    const token = readFragmentToken();
    if (token === "") {
      setError({ key: "error.linkInvalid" });
      return;
    }
    void confirmEmailProof(token).then(
      () => setDone(true),
      (err: unknown) => setError(errorMessage(err, "link")),
    );
  }, []);

  // The page's own words load with it (decision 187); until they are there it draws the spinner, never a raw key.
  const ext = useExternalText();
  if (!ext.ready) {
    return <AuthLayout>{ext.failed ? <DriveTextFailure retry={ext.retry} /> : <Spin />}</AuthLayout>;
  }

  return (
    <AuthLayout>
      <h1 style={{ marginTop: 0, fontSize: 24 }}>{t("prove.title")}</h1>
      <Alert message={error} />
      {done && (
        <>
          <Alert message={{ key: "prove.done" }} tone="success" />
          <Button type="primary" onClick={() => go("/shared")} style={{ marginTop: 16 }}>
            {t("nav.shared")}
          </Button>
        </>
      )}
    </AuthLayout>
  );
}