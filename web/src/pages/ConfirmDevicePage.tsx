// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState } from "react";
import { Button, Typography } from "antd";
import { homeFor } from "../lib/routes";
import { confirmDevice, establish } from "../lib/auth";
import { ApiError } from "../lib/api";
import { errorMessage, isLinkFailure, type Message } from "../lib/errors";
import { go } from "../lib/router";
import { useT } from "../lib/i18n";
import AuthLayout from "../components/AuthLayout";
import Alert from "../components/Alert";
import { readFragmentToken } from "./linkToken";

// ConfirmDevicePage confirms the browser that asked for a sign-in the server held (sec2, decision 86). The token is read
// from the fragment, as on the reset page. The person presses the button: a link opened by something other than the
// person (a mail scanner, say) does not use the link up.
export default function ConfirmDevicePage() {
  const { t } = useT();
  // token is null until the fragment has been read. The button waits for it, so a press before the first effect cannot
  // send an empty token.
  const [token, setToken] = useState<string | null>(null);
  const [linkUsable, setLinkUsable] = useState(true);
  const [error, setError] = useState<Message | null>(null);
  const [busy, setBusy] = useState(false);
  const read = useRef(false);

  useEffect(() => {
    if (read.current) return;
    read.current = true;
    const value = readFragmentToken();
    setToken(value);
    if (value === "") {
      setLinkUsable(false);
      setError({ key: "confirmDevice.linkInvalid" });
    }
  }, []);

  // confirm redeems the link. The browser is signed in and remembered, and the page goes on to the place it would go to.
  async function confirm() {
    if (token === null || token === "") return;
    setError(null);
    setBusy(true);
    try {
      const signed = await confirmDevice(token);
      await establish(signed);
      go(homeFor(signed.user), { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.details.reason === "other_browser") {
        // Another browser signs nothing in, and the link is still usable here.
        setError({ key: "confirmDevice.otherBrowser" });
      } else if (isLinkFailure(err)) {
        setLinkUsable(false);
        setError({ key: "confirmDevice.linkInvalid" });
      } else {
        setError(errorMessage(err, "login"));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthLayout>
      <h1 style={{ marginTop: 0, fontSize: 24 }}>{t("confirmDevice.title")}</h1>
      {linkUsable && <p>{t("confirmDevice.body")}</p>}
      <Alert message={error} />
      {linkUsable && token === null ? null : linkUsable ? (
        <Button type="primary" block loading={busy} disabled={busy} onClick={() => void confirm()}>
          {t("confirmDevice.submit")}
        </Button>
      ) : (
        <p>
          <Typography.Link href="/login">{t("login.title")}</Typography.Link>
        </p>
      )}
    </AuthLayout>
  );
}
