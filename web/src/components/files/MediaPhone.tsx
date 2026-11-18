// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState } from "react";
import { Button, Dropdown, type MenuProps } from "antd";
import type { MenuItemType } from "antd/es/menu/interface";
import { Download, Ellipsis, ListMusic, Pause, Play, SkipBack, SkipForward } from "lucide-react";
import { useNavigate } from "react-router-dom";
import IconButton from "../IconButton";
import { useT } from "../../lib/i18n";
import { useMT } from "../../lib/media/i18n";
import { useStore, useStoreSelect } from "../../lib/store";
import { session } from "../../lib/session";
import { folders, hiddenFolders } from "../../lib/files/folders";
import { filesURL } from "../../lib/files/route";
import { kindOf } from "../../lib/files/kind";
import { SPEEDS, refusedPlay } from "../../lib/files/mediaKeys";
import type { Repeat } from "../../lib/files/queue";
import type { Marks } from "../../lib/files/bookmarks";
import type { Node } from "../../lib/files/types";
import { displayArtist, displayTitle } from "../../lib/files/tags";
import MediaEqualizer, { useEqOnPlay } from "./MediaEqualizer";
import { useTagState } from "./TagCover";

// MediaPhone is the album-page player's own parts at phone width (mp7): the one centred line "{n}. {artist} - {title}", the
// large transport that drives the same element and the same navigation as the bar, and the action row (Playlist, Download
// when the person may download, and More with Speed, Shuffle, Repeat, Equalizer, Details and Open folder). The owner draws these only
// at phone width and only for the signed-in session; a share page and every wider window keep the bar as it was. Nothing
// here makes or moves the element that plays: the transport calls the same play/pause and Previous/Next the bar uses.

// SubRadioItem is a menu item of the radio kind (a speed): it carries its role and its checked state, which the menu's own
// types do not name.
type SubRadioItem = MenuItemType & { role: "menuitemradio"; "aria-checked": boolean };

export interface MediaPhoneProps {
  node: Node;
  // position is the file's place in the queue, 1-based, as the line shows it.
  position: number;
  media: HTMLMediaElement | null;
  playing: boolean;
  nav: { next: () => void; previous: () => void };
  onPress: (v: boolean) => void;
  onPlaylist: () => void;
  // onDownload is given only when the person may download the file; without it the Download action is not drawn.
  onDownload?: () => void;
  onDetails: () => void;
  shuffle: boolean;
  repeat: Repeat;
  onShuffle: () => void;
  onRepeat: () => void;
  // marks, when the person may keep bookmarks (decision 218), adds Add a bookmark and Bookmarks (n) to the More menu.
  marks?: Marks;
}

export default function MediaPhone({ node, position, media, playing, nav, onPress, onPlaylist, onDownload, onDetails, shuffle, repeat, onShuffle, onRepeat, marks }: MediaPhoneProps) {
  const { t } = useT();
  const mt = useMT();
  const navigate = useNavigate();
  const spaces = [...useStore(folders), ...useStore(hiddenFolders)];
  const tags = useTagState(node.id);
  const audio = kindOf(node) === "audio";
  const title = displayTitle(node.name, tags);
  const artist = audio ? displayArtist(tags) : undefined;
  const line = `${position}. ${artist !== undefined ? `${artist} - ` : ""}${title}`;

  // rate is the element's playback rate, kept in step with it so the Speed menu shows the chosen one. A new file takes the
  // rate the person last chose (the bar sets it), and the ratechange event reports it here.
  const [rate, setRate] = useState(() => media?.playbackRate ?? 1);
  useEffect(() => {
    if (!media) return;
    const on = () => setRate(media.playbackRate);
    on();
    media.addEventListener("ratechange", on);
    return () => media.removeEventListener("ratechange", on);
  }, [media]);

  const userId = useStoreSelect(session, (s) => s?.user.id ?? null);
  const [eqOpen, setEqOpen] = useState(false);

  // The equalizer panel belongs to the element on screen: a new element closes it.
  useEffect(() => {
    setEqOpen(false);
  }, [media]);

  // A kept On equalizer choice applies to a new element at its first play, through attach alone.
  useEqOnPlay(media, userId);

  const toggle = () => {
    if (!media) return;
    if (playing) media.pause();
    else void media.play().catch((err: unknown) => refusedPlay(err) && onPress(true));
  };

  // openFolder goes to the folder the file sits in, as the library's Open folder does: the space's own address when the
  // file is at its top, else the folder's address within the space.
  const openFolder = () => {
    const root = spaces.find((f) => f.id === node.folder_id)?.root_node_id;
    const dir = node.parent_id !== null && node.parent_id !== root ? node.parent_id : null;
    navigate(filesURL(node.folder_id, dir));
  };

  const repeatLabel = repeat === "all" ? t("files.media.repeatAll") : repeat === "one" ? t("files.media.repeatOne") : t("files.media.repeatOff");
  const speedRadio = (v: number): SubRadioItem => ({ key: `speed-${v}`, label: `${v}×`, role: "menuitemradio", "aria-checked": rate === v });
  const shuffleItem: MenuItemType & { role: "menuitemcheckbox"; "aria-checked": boolean } = { key: "shuffle", label: t("files.media.shuffle"), role: "menuitemcheckbox", "aria-checked": shuffle };
  const items: MenuProps["items"] = [
    ...(marks
      ? [
          { key: "bookmark-add", label: mt.t("media.bookmarks.add"), disabled: !marks.ready || marks.full || !media },
          { key: "bookmarks", label: mt.t("media.bookmarks.count", { count: marks.items.length }) },
          { type: "divider" as const },
        ]
      : []),
    { key: "speed", label: t("files.media.speed"), children: SPEEDS.map(speedRadio) },
    shuffleItem,
    { key: "repeat", label: repeatLabel },
    { key: "equalizer", label: t("files.media.equalizer") },
    { type: "divider" },
    { key: "details", label: t("files.act.details") },
    { key: "open-folder", label: mt.t("media.lib.openFolder") },
  ];
  const onMenu = ({ key }: { key: string }) => {
    if (key.startsWith("speed-")) {
      if (media) media.playbackRate = Number(key.slice(6));
      return;
    }
    if (key === "shuffle") onShuffle();
    else if (key === "repeat") onRepeat();
    else if (key === "equalizer") setEqOpen(true);
    else if (key === "bookmark-add") marks?.add();
    else if (key === "bookmarks") marks?.openList();
    else if (key === "details") onDetails();
    else if (key === "open-folder") openFolder();
  };

  return (
    <>
      <p className="media-phone-line" data-testid="media-phone-line">
        <bdi dir="auto">{line}</bdi>
      </p>
      <div className="media-phone-transport" data-testid="media-phone-transport" dir="ltr">
        <IconButton
          className="media-phone-step"
          data-testid="media-phone-prev"
          label={t("files.media.previous")}
          onClick={() => nav.previous()}
          icon={<SkipBack size={22} aria-hidden="true" focusable="false" />}
        />
        <IconButton
          className="media-phone-play"
          data-testid="media-phone-play"
          label={playing ? t("files.media.pause") : t("files.media.play")}
          onClick={toggle}
          disabled={!media}
          icon={playing ? <Pause size={28} aria-hidden="true" focusable="false" /> : <Play size={28} aria-hidden="true" focusable="false" />}
        />
        <IconButton
          className="media-phone-step"
          data-testid="media-phone-next"
          label={t("files.media.next")}
          onClick={() => nav.next()}
          icon={<SkipForward size={22} aria-hidden="true" focusable="false" />}
        />
      </div>
      <div className="media-phone-actions" data-testid="media-phone-actions">
        <Button type="text" className="media-phone-act" data-testid="media-phone-playlist" onClick={onPlaylist}>
          <ListMusic size={20} aria-hidden="true" focusable="false" />
          <span>{t("files.media.queue")}</span>
        </Button>
        {onDownload && (
          <Button type="text" className="media-phone-act" data-testid="media-phone-download" onClick={onDownload}>
            <Download size={20} aria-hidden="true" focusable="false" />
            <span>{t("files.act.download")}</span>
          </Button>
        )}
        <Dropdown trigger={["click"]} placement="top" destroyOnHidden overlayClassName="media-phone-menu" menu={{ items, onClick: onMenu, triggerSubMenuAction: "click" }}>
          <Button type="text" className="media-phone-act" data-testid="media-phone-more" aria-haspopup="menu">
            <Ellipsis size={20} aria-hidden="true" focusable="false" />
            <span>{t("files.act.more")}</span>
          </Button>
        </Dropdown>
      </div>
      {/* The album page is audio only and never full screen, so the bar's popupHost here is the page itself. */}
      <MediaEqualizer media={media} open={eqOpen} onClose={() => setEqOpen(false)} popupHost={() => document.body} />
    </>
  );
}