// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef } from "react";
import { Alert, Button, Spin } from "antd";
import AuthLayout from "./AuthLayout";
import { useLang, useT } from "../lib/i18n";
import { hostLabel, postToHost } from "../lib/frame";
import { loadEmbed, useET } from "../lib/embed/i18n";

// FrameEnded is the frame's session-ended view (decision 214): a frame session whose session ended draws it at every
// address but the link page. It tells the host application once, from the origins the session named, and offers no way
// to sign in: the host opens the drive again with a new link. The words are the embed catalogue's; while it is on its
// way a Spin shows, and a failed load says so in the main catalogue with a Retry that loads it again.
export default function FrameEnded() {
  const { t, ready, failed } = useET();
  const { t: mt } = useT();
  const lang = useLang();
  // StrictMode runs the effect twice on the same instance; the host is told once.
  const told = useRef(false);
  useEffect(() => {
    if (told.current) return;
    told.current = true;
    postToHost("selfcloud.session-ended");
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
          <h1 style={{ marginTop: 0, fontSize: 24 }}>{t("embed.ended.title")}</h1>
          <p>{host !== null ? t("embed.ended.from", { host }) : t("embed.ended.fromApp")}</p>
        </>
      )}
    </AuthLayout>
  );
}
