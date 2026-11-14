// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useLayoutEffect, useRef, useSyncExternalStore } from "react";
import { Alert, Button, Tabs } from "antd";
import { useNavigate } from "react-router-dom";
import EmptyState from "../components/EmptyState";
import { brand } from "../lib/brand";
import { attachPage, getPlayback, subscribePlayback } from "../lib/files/playback";
import { useT } from "../lib/i18n";
import { useMT } from "../lib/media/i18n";
import { useStore } from "../lib/store";

// Media is the page of the running session (decision 172): one tab for now, the player on its stage and the queue below.
// The session's player box moves here while the page is open (playback.attachPage), so the element that plays is never
// made again for the move. With no session the tab says so and offers the way back to My files. The hosts stay mounted
// either way, so a session that starts or ends while the page is open draws into the same places.

export default function MediaPage() {
  const { t, ready, failed } = useMT();
  const { t: mt } = useT();
  const navigate = useNavigate();
  const brandName = useStore(brand).name;
  const snap = useSyncExternalStore(subscribePlayback, getPlayback, getPlayback);
  const stageRef = useRef<HTMLDivElement>(null);
  const queueRef = useRef<HTMLDivElement>(null);

  // The hosts are given in a layout effect, so the detach runs before they leave the page: the box is parked while the
  // session still holds it, and the viewer closes one microtask later (a remount in the same tick keeps the session).
  useLayoutEffect(() => {
    const stage = stageRef.current;
    const queue = queueRef.current;
    if (stage === null || queue === null) return;
    return attachPage({ stage, queue });
  }, []);

  useEffect(() => {
    document.title = `${ready ? t("media.title") : mt("nav.media")} · ${brandName}`;
  }, [ready, t, mt, brandName]);

  // The catalogue did not load: the main catalogue says so, and a reload asks again (as the notifications page does).
  if (failed) {
    return (
      <Alert
        type="error"
        showIcon
        role="alert"
        message={mt("app.crash.title")}
        action={<Button onClick={() => window.location.reload()}>{mt("app.crash.reload")}</Button>}
      />
    );
  }

  return (
    <div>
      <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>{ready ? t("media.title") : mt("nav.media")}</h1>
      <Tabs
        items={[
          {
            key: "now-playing",
            label: ready ? t("media.tab.nowPlaying") : mt("nav.media"),
            children:
              snap.seed === null && ready ? (
                <EmptyState
                  role="status"
                  text={
                    <span style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 4 }}>
                      <span style={{ fontWeight: 600 }}>{t("media.empty.title")}</span>
                      <span>{t("media.empty.body")}</span>
                    </span>
                  }
                >
                  <Button type="primary" onClick={() => navigate("/files")}>
                    {t("media.empty.action")}
                  </Button>
                </EmptyState>
              ) : null,
          },
        ]}
      />
      {/* The session's places: the player box moves into the stage, the queue panel into the host below it. Empty hosts
          take no room while no session runs. The stage's test id is the page's own; the player inside keeps media-stage. */}
      <div ref={stageRef} className="media-page-stage" data-testid="media-page-stage" />
      <div ref={queueRef} className="media-page-queue" data-testid="media-page-queue" />
    </div>
  );
}
