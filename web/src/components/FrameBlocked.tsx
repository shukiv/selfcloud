// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef } from "react";
import { Alert, Button, Spin } from "antd";
import AuthLayout from "./AuthLayout";
import { useLang, useT } from "../lib/i18n";
import { hostLabel, postToHost } from "../lib/frame";
import { loadEmbed, useET } from "../lib/embed/i18n";

// FrameBlocked is the frame's refused-cookie view (decision 214): a frame sign-in link whose cookie the browser refuses
// draws it. The session read after the redeem answered 401, so the frame cannot keep a session; the host application is
// told once, from the origins the redeem named, and the person is asked to open the drive in a new tab. No password form
// is drawn: a password cannot help a cookie the browser refuses. The words are the embed catalogue's; while it is on its
// way a Spin shows, and a failed load says so in the main catalogue with a Retry that loads it again.
export default function FrameBlocked() {
  const { t, ready, failed } = useET();
  const { t: mt } = useT();
  const lang = useLang();
  // StrictMode runs the effect twice on the same instance; the host is told once.
  const told = useRef(false);
  useEffect(() => {
    if (told.current) return;
    told.current = true;
    postToHost("selfcloud.frame-blocked");
  }, []);
  const host = hostLabel();
  return (
    <AuthLayout>
      {failed ? (
        <Alert
          type="error"
          showIcon
          role="alert"
          message={mt("app.crash.title")}
          action={
            <Button size="small" onClick={() => void loadEmbed(lang)}>
              {mt("upload.retry")}
            </Button>
          }
        />
      ) : !ready ? (
        <div role="status" style={{ display: "grid", placeItems: "center", minHeight: 120 }}>
          <Spin />
        </div>
      ) : (
        <>
          <h1 style={{ marginTop: 0, fontSize: 24 }}>{t("embed.blocked.title")}</h1>
          <p>{host !== null ? t("embed.blocked.text", { host }) : t("embed.blocked.textApp")}</p>
        </>
      )}
    </AuthLayout>
  );
}
