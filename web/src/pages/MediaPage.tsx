// SPDX-License-Identifier: AGPL-3.0-or-later

import { lazy, Suspense, useEffect, useLayoutEffect, useRef, useSyncExternalStore } from "react";
import { Alert, Button, Skeleton, Tabs } from "antd";
import { useNavigate, useSearchParams } from "react-router-dom";
import EmptyState from "../components/EmptyState";
import { brand } from "../lib/brand";
import { attachPage, getPlayback, subscribePlayback } from "../lib/files/playback";
import { kindOf } from "../lib/files/kind";
import { usePhoneWidth } from "../lib/files/phone";
import { useT } from "../lib/i18n";
import { useMT } from "../lib/media/i18n";
import { useStore } from "../lib/store";

// Media is the page of the running session and the library of the person's media (decisions 186, 206): the Now playing
// tab keeps the session's player on its stage and the queue below it, and the library tabs list the videos, the music
// and the recently played files. The session's player box moves into the stage while the page is open
// (playback.attachPage), so the element that plays is never made again for the move. The hosts sit in the Now playing
// pane, which is force-rendered and never destroyed: a library tab hides the pane, it does not unmount the hosts, so a
// session keeps playing and its box keeps its identity across tab changes. With no session the pane says so and offers
// the way back to My files.

// The library is a separate chunk: its shared list is not part of the page's first load.
const MediaLibrary = lazy(() => import("../components/media/MediaLibrary"));
// The Playlists tab is its own chunk too: the saved playlists and Continue watching load only when the tab opens.
const MediaPlaylistsTab = lazy(() => import("../components/media/MediaPlaylistsTab"));

type TabKey = "now-playing" | "playlists" | "videos" | "music" | "recent";

// tabOf reads the tab from the address: an absent or unknown value is Now playing.
function tabOf(params: URLSearchParams): TabKey {
  const v = params.get("tab");
  return v === "playlists" || v === "videos" || v === "music" || v === "recent" ? v : "now-playing";
}

export default function MediaPage() {
  const { t, ready, failed } = useMT();
  const { t: mt } = useT();
  const navigate = useNavigate();
  const brandName = useStore(brand).name;
  const snap = useSyncExternalStore(subscribePlayback, getPlayback, getPlayback);
  const stageRef = useRef<HTMLDivElement>(null);
  const queueRef = useRef<HTMLDivElement>(null);
  const [params, setParams] = useSearchParams();
  const tab = tabOf(params);
  // The album-page queue host (mp7) takes the divider and the full-width list only for an audio file at phone width; a video
  // keeps the host as it was. The host itself is never re-created for the width change or the file switch, so the queue keeps
  // its place and the player keeps playing.
  const phone = usePhoneWidth();
  const albumPhone = phone && snap.current !== null && kindOf(snap.current.node) === "audio";

  // choose writes the tab in the address with replace (the back button leaves the page, it does not walk the tabs).
  const choose = (key: string) => {
    setParams(key === "now-playing" ? {} : { tab: key }, { replace: true });
  };

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

  const library = (key: "videos" | "music" | "recent", label: string) => ({
    key,
    label,
    children: (
      <Suspense fallback={<Skeleton active />}>
        <MediaLibrary tab={key} onPlay={() => choose("now-playing")} />
      </Suspense>
    ),
  });

  return (
    <div>
      <h1 style={{ fontSize: 20, fontWeight: 600, margin: "0 0 16px" }}>{ready ? t("media.title") : mt("nav.media")}</h1>
      <Tabs
        activeKey={tab}
        onChange={choose}
        items={[
          {
            key: "now-playing",
            label: ready ? t("media.tab.nowPlaying") : mt("nav.media"),
            // The pane is drawn even while another tab is active, and is never destroyed: the hosts stay mounted.
            forceRender: true,
            children: (
              <>
                {snap.seed === null && ready && (
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
                )}
                {/* The session's places: the player box moves into the stage, the queue panel into the host below it.
                    Empty hosts take no room while no session runs. The stage's test id is the page's own; the player
                    inside keeps media-stage. */}
                <div ref={stageRef} className="media-page-stage" data-testid="media-page-stage" />
                <div ref={queueRef} className={`media-page-queue${albumPhone ? " media-page-queue--phone" : ""}`} data-testid="media-page-queue" />
              </>
            ),
          },
          ...(ready
            ? [
                {
                  key: "playlists",
                  label: t("media.tab.playlists"),
                  children: (
                    <Suspense fallback={<Skeleton active />}>
                      <MediaPlaylistsTab onPlay={() => choose("now-playing")} />
                    </Suspense>
                  ),
                },
                library("videos", t("media.tab.videos")),
                library("music", t("media.tab.music")),
                library("recent", t("media.tab.recent")),
              ]
            : []),
        ]}
      />
    </div>
  );
}
