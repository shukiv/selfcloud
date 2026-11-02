// SPDX-License-Identifier: AGPL-3.0-or-later

import { lazy, Suspense, useEffect, useRef, useState, type ChangeEvent, type CSSProperties, type ReactNode } from "react";
import { keepInView } from "./menuScroll";
import { Alert as AntdAlert, App, Button, Checkbox, Drawer, Dropdown, Grid, Layout, Menu, Skeleton, theme as antdTheme } from "antd";
import type { MenuProps } from "antd";
import { Clock, FolderOpen, Inbox, Share2, Star, Tags, Trash2, HardDrive, Settings, Building2, Users, Menu as MenuIcon, ChevronsLeft, ChevronsRight, UserRound, Ellipsis } from "lucide-react";
import IconButton from "./IconButton";
import { accountLabel, initialOf } from "../lib/avatar";
import { useLocation, useNavigate } from "react-router-dom";
import { useT, useLang, directionOf, translatePlural } from "../lib/i18n";
import { useStore } from "../lib/store";
import { flash, isAdmin, isOwner, session, sessionChanged, sessionKey } from "../lib/session";
import { failureText } from "../lib/manage/failure";
import { folders, hiddenFolders, loadFolders, loadHiddenFolders } from "../lib/files/folders";
import { setHidden } from "../lib/sharing/api";
import { pendingRequests, refreshPendingRequests } from "../lib/sharing/requestCount";
import type { FolderAccess } from "../lib/files/types";
import { signOut } from "../lib/auth";
import { canInstall, iosDialogOpen, isIOS, isStandalone, promptInstall } from "../lib/pwa/install";
import { endImpersonation } from "../lib/impersonation";

import { readPref, writePref } from "../lib/prefs";
import { readTree, writeTree, type TreeState } from "../lib/files/treeState";
import { filesURL } from "../lib/files/route";
import { treePlace } from "../lib/files/treePlace";
import { fromFileList, type DroppedFile, type UploadWhere } from "../lib/files/ops";
import { newPlace, newPlaceFor, queueInto } from "../lib/files/newPlace";
import { uploads } from "../lib/files/uploads";
import { myStorage, refreshMyStorage } from "../lib/storage/api";
import { useSidebar } from "../theme/theme";
import { currentMark } from "../theme/currentMark";
import { useCoarsePointer } from "../lib/pointer";
import Alert from "./Alert";
import LangSwitcher from "./LangSwitcher";
import Sentence from "./Sentence";
import ShellSearch from "./search/ShellSearch";
import ThemeMenu from "./ThemeMenu";
import UploadTrayHost from "./UploadTrayHost";
import MediaHost from "./files/MediaHost";
import BellBoundary from "./notifications/BellBoundary";
import SidebarNew from "./files/SidebarNew";
import SidebarStorage, { storageTitle } from "./storage/SidebarStorage";

// The new folder dialog is a lazy chunk: the sidebar's New opens it, and the first screen does not carry it (decision 66).
const NewFolderDialog = lazy(() => import("./files/NewFolderDialog"));
// The space settings dialog is loaded when a team space's settings are opened.
const SpaceSettings = lazy(() => import("./share/SpaceSettings"));
// The bell is a chunk of its own: the first screen does not carry it (decision 55 measures the first-load budget).
const NotificationsBell = lazy(() => import("./notifications/Bell"));
const IosInstallDialog = lazy(() => import("./pwa/IosInstallDialog"));
// The folder tree is a lazy chunk: the sidebar loads it when a person who has a drive is signed in.
const FolderTree = lazy(() => import("./files/FolderTree"));

// Below this width (the antd `lg` breakpoint, 992px) the sidebar is a drawer.
const SIDE_WIDTH = 264;
const RAIL_WIDTH = 88;
// The choice of the rail: "1" when the person folded the sidebar, "0" when it is open.
const PREF_NAV_RAIL = "selfcloud.navRail";

interface NavItem {
  key: string;
  label: ReactNode;
  icon: ReactNode;
  // className marks an entry whose label is more than one line (the Storage entry's bar), so its row is not held to one line.
  className?: string;
}

const icon = (C: typeof FolderOpen) => <C size={18} strokeWidth={1.75} aria-hidden="true" />;

// The sidebar's entries for the caller. An installation admin has no drive, so it sees the
// administration pages only; an owner also sees the owner's pages.
interface NavSection {
  section?: string;
  items: NavItem[];
}

// My files is the folder tree's first row, so it is an entry here only when the sidebar is folded to its icons (no tree).
// storage is the Storage entry's label for a person (the bar and figures, or the title alone when the sidebar is folded); null for an
// admin, who has no storage of its own.
function navFor(admin: boolean, owner: boolean, t: (k: string) => string, pending: number | null, waiting: (n: number) => string, filesEntry: boolean, storage: ReactNode | null): NavSection[] {
  if (admin) {
    return [
      {
        items: [
          { key: "/admin/tenants", label: t("nav.tenants"), icon: icon(Building2) },
          { key: "/admin/admins", label: t("nav.admins"), icon: icon(UserRound) },
          { key: "/admin/installation", label: t("nav.installation"), icon: icon(Settings) },
        ],
      },
    ];
  }
  const main: NavItem[] = [
    ...(filesEntry ? [{ key: "/files", label: t("nav.files"), icon: icon(FolderOpen) }] : []),
    { key: "/starred", label: t("nav.starred"), icon: icon(Star) },
    { key: "/recent", label: t("nav.recent"), icon: icon(Clock) },
    { key: "/shared", label: t("nav.shared"), icon: icon(Share2) },
    { key: "/trash", label: t("nav.trash"), icon: icon(Trash2) },
    // Storage is the last entry of the person's list, and it is a block with its meter (decision 66), not a plain row.
    ...(storage !== null ? [{ key: "/storage", label: storage, icon: icon(HardDrive), className: "shell-storage-item" }] : []),
  ];
  // The access requests entry shows to an owner, and to anyone who has a request waiting to be decided, with its count.
  if (owner || (pending ?? 0) > 0) {
    const label =
      (pending ?? 0) > 0 ? (
        <>
          {t("nav.requests")}
          <span style={{ marginInlineStart: 8, fontSize: 12, opacity: 0.8 }}>{waiting(pending ?? 0)}</span>
        </>
      ) : (
        t("nav.requests")
      );
    main.push({ key: "/requests", label, icon: icon(Inbox) });
  }
  const sections: NavSection[] = [{ items: main }];
  if (owner) {
    sections.push({
      section: t("nav.manage"),
      items: [
        { key: "/manage", label: t("nav.overview"), icon: icon(Users) },
        { key: "/manage/people", label: t("nav.people"), icon: icon(Users) },
        { key: "/manage/groups", label: t("nav.groups"), icon: icon(Users) },
        { key: "/manage/spaces", label: t("nav.spaces"), icon: icon(FolderOpen) },
        { key: "/manage/storage", label: t("nav.manageStorage"), icon: icon(HardDrive) },
        { key: "/manage/labels", label: t("nav.manageLabels"), icon: icon(Tags) },
        { key: "/manage/links", label: t("nav.links"), icon: icon(Share2) },
        { key: "/manage/audit", label: t("nav.audit"), icon: icon(Settings) },
      ],
    });
  }
  return sections;
}

// menuLeaves turns nav entries into antd menu items; the icon is passed through with the label.
function menuLeaves(items: NavItem[]): NonNullable<MenuProps["items"]> {
  return items.map((it) => ({ key: it.key, label: it.label, icon: it.icon, className: it.className }));
}

// Shell is the signed-in frame: the top bar with the account menu, and the sidebar. The sidebar
// is one tree shared by the desktop sider and the drawer below the lg breakpoint.
export default function Shell({ children }: { children: ReactNode }) {
  const { t } = useT();
  const { token } = antdTheme.useToken();
  const sidebar = useSidebar();
  const screens = Grid.useBreakpoint();
  const isMobile = !screens.lg;
  // On a finger the account button is a 44 pixel target; with a mouse it keeps the button's usual size.
  const coarse = useCoarsePointer();
  const lang = useLang();
  const rtl = directionOf(lang) === "rtl";
  const me = useStore(session);
  const note = useStore(flash);
  const location = useLocation();
  const navigate = useNavigate();
  const [navOpen, setNavOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(() => readPref(PREF_NAV_RAIL) === "1");

  const admin = isAdmin(me?.user);
  const owner = isOwner(me?.user);
  // While an installation admin is signed in as this person, the banner shows across every page, and the sign-out is not
  // offered: the server refuses it, and the banner's button goes back to the admin's session instead.
  const impersonating = !!me?.impersonation;
  const folderList = useStore(folders);
  const hiddenList = useStore(hiddenFolders);
  // The team spaces are listed under My files, from the folders the caller reaches. The hidden ones are listed
  // only when the caller asks to see them. An admin has no drive, so it reads neither list.
  const teams = admin ? [] : folderList.filter((f) => f.kind === "team");
  const hiddenTeams = admin ? [] : hiddenList.filter((f) => f.kind === "team");
  const personal = admin ? null : (folderList.find((f) => f.kind === "personal") ?? null);
  const [showHidden, setShowHidden] = useState(false);
  // openTeam is the team whose options are open (by a click, or the tree's menu key).
  const [openTeam, setOpenTeam] = useState<string | null>(null);
  // The folder tree's spaces are My files, then the teams, and the hidden teams while the person shows them.
  const treeSpaces = [...(personal ? [personal] : []), ...teams, ...(showHidden ? hiddenTeams : [])];
  // The tree's open folders are the person's own, kept under their id: another person on the browser has theirs.
  const userID = me?.user.id ?? "";
  const [treeBox, setTreeBox] = useState(() => ({ uid: userID, state: readTree(userID) }));
  const treeState: TreeState = treeBox.uid === userID ? treeBox.state : readTree(userID);
  const changeTree = (next: TreeState) => {
    setTreeBox({ uid: userID, state: next });
    writeTree(userID, next);
  };
  // The place the browser shows (written by the files page). It is marked only on the files pages.
  const place = useStore(treePlace);
  const onFiles = location.pathname.startsWith("/files");
  // The sidebar's New acts on the folder shown on a files page, and on My files everywhere else (decision 66). The pickers and the
  // new-folder dialog belong here, not to the sidebar, so a click opens the picker in the same gesture.
  const shownPlace = useStore(newPlace);
  const storage = useStore(myStorage);
  const { message } = App.useApp();
  const filesInput = useRef<HTMLInputElement | null>(null);
  const foldersInput = useRef<HTMLInputElement | null>(null);
  const pickWhere = useRef<UploadWhere | null>(null);
  const [folderWhere, setFolderWhere] = useState<UploadWhere | null>(null);
  const newWhere = admin ? null : newPlaceFor(onFiles, shownPlace, personal?.id ?? null);
  const [settingsFor, setSettingsFor] = useState<FolderAccess | null>(null);
  // The iPhone instructions are open for the person who opened them: a change of person closes them (iosDialogOpen).
  const [iosFor, setIosFor] = useState<string | null>(null);
  const who = me?.user.id ?? null;
  const iosOpen = iosDialogOpen(iosFor, who);
  // The install entries (decision 65): the browser's offer where it made one, and the iPhone instructions where Safari has no offer.
  const offerInstall = useStore(canInstall) && !impersonating && !isStandalone();
  const offerIosInstall = !impersonating && isIOS() && !isStandalone();
  // shellError is the text, by code, of a failed read of the spaces or a refused hide. The server's text is never shown.
  const [shellError, setShellError] = useState<string | null>(null);
  // A change of person closes the settings, clears the hidden toggle and the error: none of them belongs to the next person.
  const sessionID = sessionKey(me);
  useEffect(() => {
    setShowHidden(false);
    setSettingsFor(null);
    setShellError(null);
  }, [sessionID]);
  // The sidebar's middle (the folder tree and the menu) scrolls inside the sider. When the address moves to a place below the
  // fold, its row is brought into view inside that scroll area alone, so the page does not move.
  useEffect(() => {
    const follow = () => {
      const region = document.querySelector<HTMLElement>(".shell-scroll");
      const item = region?.querySelector<HTMLElement>('.ant-menu-item-selected, [aria-current="location"]');
      if (region && item) keepInView(region, item);
    };
    follow();
    // The region's height is settled by the sider's flex column after the first paint, so the scroll follows each resize of the box.
    const region = document.querySelector<HTMLElement>(".shell-scroll");
    if (!region || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(follow);
    observer.observe(region);
    return () => observer.disconnect();
  }, [location.pathname, collapsed, isMobile]);
  useEffect(() => {
    if (admin) return;
    const stale = sessionChanged();
    loadFolders().catch((err: unknown) => {
      if (!stale()) setShellError(failureText(err, lang));
    });
  }, [admin]);
  // The count of requests waiting for the caller is read for each person, so the entry shows the right count. An
  // installation admin has no requests of its own, and the server refuses the read: it is not sent (as Svelte). It is read
  // again when the person comes to My files, where Home read it until decision 53 made My files the landing page.
  const atFiles = location.pathname.startsWith("/files");
  useEffect(() => {
    if (me && !admin) void refreshPendingRequests();
  }, [sessionID, atFiles]);
  const pending = useStore(pendingRequests);
  // The storage meter of the sidebar is read at sign-in, and again when an upload batch ends (the queue empties). The other
  // changes that move bytes read it where they read the tree: a delete, a move to the trash and an upload (FilesPage's reload),
  // and a restore or an empty trash (TrashPage). There is no poll.
  useEffect(() => {
    if (me && !admin) void refreshMyStorage().catch(() => undefined);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionID]);
  useEffect(() => {
    if (admin) return;
    let busy = false;
    return uploads.subscribe((list) => {
      const now = list.some((u) => ["queued", "uploading", "paused", "waiting", "conflict", "awaiting_name"].includes(u.status));
      if (busy && !now) void refreshMyStorage().catch(() => undefined);
      busy = now;
    });
  }, [admin, sessionID]);
  // pick opens the picker for a New action. It is called straight from the menu's click, so the browser allows the picker: nothing
  // on this path waits for a render, a read or a navigation. The picked files go to the place the picker was opened for.
  const pick = (kind: "files" | "folders", where: UploadWhere) => {
    pickWhere.current = where;
    (kind === "files" ? filesInput : foldersInput).current?.click();
  };
  const onPicked = (e: ChangeEvent<HTMLInputElement>) => {
    const input = e.currentTarget;
    const files: DroppedFile[] = input.files ? fromFileList(input.files) : [];
    input.value = "";
    const where = pickWhere.current;
    pickWhere.current = null;
    if (where && files.length > 0 && queueInto(where, files) > 0) void message.warning(t("files.upload.noPath"));
  };
  // The menu's entries and its current one. My files is the tree's first row, so it is a menu entry only when the sidebar is
  // folded to icons; then it is current on the files pages where the place is the person's own space.
  const menuFor = (filesEntry: boolean) => {
    // The Storage entry is its bar and figures; folded to icons, it is its title alone, which the tooltip shows.
    // On the Storage page its row is the current place: the block's title takes the mark colour (decision 69, option B).
    const onStorage = location.pathname === "/storage" || location.pathname.startsWith("/storage/");
    const storageLabel = admin ? null : filesEntry ? storageTitle(storage, t) : <SidebarStorage my={storage} sidebar={sidebar} selected={onStorage} />;
    const sections = navFor(admin, owner, t, pending, (n) => translatePlural(lang, "nav.requests.count", n), filesEntry, storageLabel);
    const items: MenuProps["items"] = sections.flatMap((s, i) =>
      s.section
        ? [{ type: "group" as const, key: `group-${i}`, label: s.section, children: menuLeaves(s.items) }]
        : menuLeaves(s.items),
    );
    const current = sections
      .flatMap((s) => s.items)
      .filter((it) => it.key !== "/files" && (location.pathname === it.key || location.pathname.startsWith(it.key + "/")))
      .sort((a, c) => c.key.length - a.key.length)
      .slice(0, 1)
      .map((it) => it.key);
    const ownSpace = !!personal && onFiles && place?.folder === personal.id;
    return { items, selected: filesEntry && ownSpace ? ["/files", ...current] : current };
  };

  const toggleRail = () => {
    setCollapsed((c) => {
      writePref(PREF_NAV_RAIL, c ? "0" : "1");
      return !c;
    });
  };

  // toggleHidden shows or hides the hidden team spaces. Turning them on reads the hidden list.
  const toggleHidden = async (on: boolean) => {
    const stale = sessionChanged();
    setShowHidden(on);
    if (on) {
      await loadHiddenFolders().catch((err: unknown) => {
        if (!stale()) setShellError(failureText(err, lang));
      });
    }
  };

  // hideSpace hides a team space from the caller's own list, or shows it again. Access does not change. A refused
  // change leaves the lists as they are; both are read again either way, so they show the truth. A failed read
  // keeps the list it had.
  // A failure is shown by its code, and a late answer after a change of person is dropped (sessionChanged is taken first).
  const hideSpace = async (team: FolderAccess, hidden: boolean) => {
    const stale = sessionChanged();
    const failed = (err: unknown) => {
      if (!stale()) setShellError(failureText(err, lang));
    };
    setShellError(null);
    await setHidden(team.id, hidden).catch(failed);
    await Promise.all([loadFolders().catch(failed), showHidden ? loadHiddenFolders().catch(failed) : undefined]);
  };

  const go = (key: string) => {
    setNavOpen(false);
    navigate(key);
  };

  async function logout() {
    try {
      await signOut();
    } finally {
      navigate("/login", { replace: true });
    }
  }

  // A team's row in the tree: a tag when it is hidden, and its options menu. The menu offers Hide or Show to anyone, and Space
  // settings to a manager of the space.
  const teamLabel = (team: FolderAccess) =>
    team.hidden ? <span style={{ marginInlineStart: 8, fontSize: 12, opacity: 0.8, flex: "none" }}>{t("nav.hiddenTag")}</span> : null;
  const teamEnd = (team: FolderAccess) => {
    // My files has no options: Hide and the settings are a team's.
    if (team.kind !== "team") return null;
    const items: MenuProps["items"] = [
      { key: "hide", label: team.hidden ? t("nav.show") : t("nav.hide") },
      ...(team.role === "manager" ? [{ key: "settings", label: t("nav.spaceSettings") }] : []),
    ];
    // The options open by a click on the button, and from the tree by its menu key (Shift+F10). The button is not a tab stop:
    // the tree is one, and its menu key reaches the options.
    return (
      <Dropdown
        trigger={["click"]}
        open={openTeam === team.id}
        onOpenChange={(open) => setOpenTeam(open ? team.id : null)}
        menu={{
          items,
          onClick: ({ key }) => {
            setOpenTeam(null);
            if (key === "hide") void hideSpace(team, !team.hidden);
            if (key === "settings") setSettingsFor(team);
          },
        }}
      >
        <IconButton
          tabIndex={-1}
          label={t("nav.spaceOptions", { name: team.name })}
          icon={<Ellipsis size={16} aria-hidden="true" focusable="false" />}
          // The button takes its row's ink: the sidebar's text, or the mark colour of the current row (decision 69).
          style={{ flex: "none", color: "inherit" }}
        />
      </Dropdown>
    );
  };

  // The sidebar in its two forms: the tree of My files and the teams (not when folded to icons, and not for an admin, who has
  // no drive), then the menu. Each part that scrolls is in one region, so the rail toggle at the bottom stays in reach.
  const renderSidebar = (collapsedNow: boolean) => {
    const { items, selected } = menuFor(collapsedNow);
    const newShown = (onFiles || newWhere !== null) && !admin;
    const tree = !admin && !collapsedNow;
    return (
      <div
        className="shell-sider-body"
        style={{
          color: sidebar.text,
          "--sc-side-text": sidebar.text,
          "--sc-side-active": sidebar.active,
          "--sc-side-on-active": sidebar.onActive,
          // The current place's bar and the hover tint of the rows (decision 69): the menu's own hover, and the mark from currentMark.
          "--sc-side-mark": currentMark(sidebar).bar,
          "--sc-side-ink": currentMark(sidebar).ink,
          "--sc-side-hover": sidebar.hover,
          // The icon column of every row (New, the tree, the menu) from the sidebar's content edge: a row's icon sits 28 px in, or
          // 48 px on a finger, where the tree's arrow is 44 px (decision 66).
          "--sc-icon-col": coarse ? "48px" : "28px",
        } as CSSProperties}
      >
        {newShown && <SidebarNew place={newWhere} collapsed={collapsedNow} coarse={coarse} sidebar={sidebar} onPick={pick} onFolder={(where) => setFolderWhere(where)} />}
        {/* With no New drawn, the gap New leaves stays (10 px, or 6 px on a finger, as New's own top edge), so the first item does not
            touch the top bar (decision 69). */}
        {!newShown && <div aria-hidden="true" data-testid="sidebar-top-gap" style={{ flex: "none", blockSize: coarse ? 6 : 10 }} />}
        <div className="shell-scroll">
          {shellError && !collapsedNow && <AntdAlert type="error" showIcon role="alert" message={shellError} style={{ margin: "8px 0" }} />}
          {tree && (
            <div style={{ marginBlockEnd: 8 }}>
              <Suspense fallback={<Skeleton active title={false} paragraph={{ rows: 2 }} />}>
                <FolderTree
                  key={userID}
                  spaces={treeSpaces}
                  folder={place?.folder ?? ""}
                  chain={place?.chain ?? []}
                  top={place?.top}
                  marking={onFiles && place !== null}
                  state={treeState}
                  onChange={changeTree}
                  onOpen={(folder, dir) => {
                    setNavOpen(false);
                    navigate(filesURL(folder, dir));
                  }}
                  coarse={coarse}
                  colours={sidebar}
                  spaceLabel={teamLabel}
                  spaceEnd={teamEnd}
                  onMenu={(s) => {
                    if (s.kind === "team") setOpenTeam(s.id);
                  }}
                  onGone={() => {
                    // A space whose read says it is not there any more: the spaces are read again, and it leaves the tree.
                    const stale = sessionChanged();
                    loadFolders().catch((err: unknown) => {
                      if (!stale()) setShellError(failureText(err, lang));
                    });
                  }}
                />
              </Suspense>
              {(teams.length > 0 || showHidden) && (
                <Checkbox className="side-check" checked={showHidden} onChange={(e) => void toggleHidden(e.target.checked)} style={{ color: sidebar.text, margin: "4px 0 0 8px" }}>
                  {t("nav.showHidden")}
                </Checkbox>
              )}
            </div>
          )}
          <Menu
            theme="dark"
            mode="inline"
            inlineCollapsed={collapsedNow}
            selectedKeys={selected}
            items={items}
            onClick={({ key }) => go(key)}
            className="shell-menu"
            style={{ border: "none", background: "transparent" }}
          />
        </div>
      </div>
    );
  };

  const userItems: MenuProps["items"] = [
    {
      key: "who",
      disabled: true,
      label: <Sentence template={t("user.signedInAs")} names={{ name: me?.user.email ?? "" }} />,
    },
    { type: "divider" },
    { key: "password", label: t("user.changePassword") },
    ...(offerInstall ? [{ key: "install", label: t("pwa.install") }] : []),
    ...(offerIosInstall ? [{ key: "addHome", label: t("pwa.addToHome") }] : []),
    ...(impersonating ? [] : [{ key: "signout", label: t("user.signOut") }]),
  ];

  // The banner is one line, 48px: the sider is 48px shorter below it, so its foot stays on the screen.
  return (
    <>
    {impersonating && (
      <div
        role="status"
        data-testid="impersonation-banner"
        style={{
          display: "flex",
          alignItems: "center",
          flexWrap: "wrap",
          gap: 12,
          // 48px on the desktop, where the sider below is offset by this height; a phone may wrap the name onto two lines.
          minHeight: 48,
          // Counted inside the 48px, so the shell beneath is exactly one banner shorter (review F9).
          boxSizing: "border-box",
          padding: "8px 16px",
          background: token.colorWarningBg,
          color: token.colorWarningText,
          borderBottom: `2px solid ${token.colorWarning}`,
          fontWeight: 600,
          minWidth: 0,
        }}
      >
        <span style={{ flex: "1 1 auto", minWidth: 0, overflowWrap: "anywhere" }}>
          <Sentence
            template={t("impersonation.banner")}
            names={{ name: me?.user.display_name || me?.user.email || "" }}
          />
        </span>
        <Button size="small" onClick={() => void endImpersonation()} style={{ flex: "none" }}>
          {t("impersonation.back")}
        </Button>
      </div>
    )}
      {/* While the banner shows, the shell fills the window below it, and its content column scrolls inside it, so the page
          itself never scrolls (F9). Without the banner the page scrolls as before. */}
      <Layout
        style={
          impersonating
            ? { height: "calc(100vh - 48px)", overflow: "hidden", background: token.colorBgLayout }
            : { minHeight: "100vh", background: token.colorBgLayout }
        }
      >
        <a className="skip-link" href="#main">
          {t("app.skip")}
        </a>
        {!isMobile && (
          <Layout.Sider
            className="shell-sider"
            aria-label={t("nav.sidebar")}
            width={SIDE_WIDTH}
            collapsedWidth={RAIL_WIDTH}
            collapsed={collapsed}
            trigger={null}
            style={{
              background: sidebar.bg,
              color: sidebar.text,
              position: "sticky",
              top: 0,
              height: impersonating ? "calc(100vh - 48px)" : "100vh",
              overflow: "hidden",
              // No top padding: New's top edge lines up with the search box's top edge (decision 69).
              padding: collapsed ? "0 8px 16px" : "0 12px 16px",
            }}
          >
            {renderSidebar(collapsed)}
            <IconButton
              onClick={toggleRail}
              label={collapsed ? t("nav.showSidebar") : t("nav.hideSidebar")}
              data-testid="rail-toggle"
              icon={collapsed ? <ChevronsRight size={18} aria-hidden="true" focusable="false" /> : <ChevronsLeft size={18} aria-hidden="true" focusable="false" />}
              style={{ color: sidebar.text, marginTop: 8 }}
            />
          </Layout.Sider>
        )}
        {/* On a phone the sidebar is this drawer, and it holds the folder tree too. It is mounted only there, so the tree is
            one tree on the page at a time. */}
        {isMobile && (
          <Drawer
            placement={rtl ? "right" : "left"}
            open={navOpen}
            onClose={() => setNavOpen(false)}
            width={280}
            // The drawer's name is the sidebar's (antd names the dialog by its title, so a null title leaves it unnamed).
            title={<span className="sr-only">{t("nav.sidebar")}</span>}
            closable
            styles={{
              body: { padding: "0 12px 16px", background: sidebar.bg, display: "flex", flexDirection: "column", minHeight: 0, overflow: "hidden" },
              header: { background: sidebar.bg, borderBottom: "none" },
            }}
          >
            {renderSidebar(false)}
          </Drawer>
        )}
        <Layout style={{ background: token.colorBgLayout, ...(impersonating ? { height: "100%", overflow: "auto" } : {}) }}>
          <Layout.Header
            style={{
              display: "flex",
              alignItems: "center",
              // A phone's bar has a menu button and four icons: the gap is 4 px there, so 320 px fits.
              gap: isMobile ? 4 : 12,
              padding: isMobile ? "0 12px" : "0 24px",
              background: token.colorBgContainer,
              borderBottom: `1px solid ${token.colorBorderSecondary}`,
              height: 56,
              lineHeight: "56px",
              // The top bar stays on screen as a long page scrolls: a file listing's toolbar sits just below it, at 56 pixels.
              position: "sticky",
              top: 0,
              zIndex: 10,
            }}
          >
            {isMobile && (
              <IconButton data-testid="nav-open" label={t("nav.openMenu")} onClick={() => setNavOpen(true)} icon={<MenuIcon size={18} aria-hidden="true" focusable="false" />} />
            )}
            {admin && <span style={{ color: token.colorTextSecondary, whiteSpace: "nowrap" }}>{t("admin.badge")}</span>}
            {!admin && <ShellSearch wide={!!screens.md} />}
            {/* A plain flex row, not antd's Space: Space wraps each child in an item that does not shrink, so a long account name
                would push the page wider than the window. Here the account button can shrink and its name is cut with an ellipsis. */}
            <div style={{ display: "flex", alignItems: "center", gap: isMobile ? 4 : 12, marginInlineStart: "auto", minWidth: 0 }}>
              {/* The bell is for the person of the drive: an installation admin, or a person signed in as another, has no inbox here. */}
              {/* The bell is for the person of the drive: an installation admin, or a person signed in as another, has no inbox here. */}
              {me && !admin && !impersonating && (
                <BellBoundary>
                  <Suspense fallback={null}>
                    <NotificationsBell />
                  </Suspense>
                </BellBoundary>
              )}
              {/* The top bar's language and theme are icons with tooltips at every width (decision 68). */}
              <LangSwitcher compact />
              {/* Below 360 px the row has no room for the theme icon, so it is not on the bar there (index.css, shell-theme-slot). */}
              <span className="shell-theme-slot">
                <ThemeMenu />
              </span>
              {me && (
                <Dropdown
                  menu={{
                    items: userItems,
                    onClick: ({ key }) => {
                      if (key === "password") navigate("/change-password");
                      if (key === "install") void promptInstall();
                      if (key === "addHome") setIosFor(who);
                      if (key === "signout") void logout();
                    },
                  }}
                  trigger={["click"]}
                >
                  {/* The account is a round avatar with the first letter of the name (of the email, with no name). Its name and tooltip
                      are the name, or the email, or "Account menu" when neither is set; with neither it shows a person (decision 68). */}
                  <IconButton
                    data-testid="account-menu"
                    label={accountLabel(me.user.display_name, me.user.email, t("user.menu"))}
                    icon={
                      initialOf(me.user.display_name, me.user.email) === "" ? (
                        <UserRound size={18} strokeWidth={1.75} aria-hidden="true" focusable="false" />
                      ) : (
                        <span
                          aria-hidden="true"
                          style={{ display: "inline-flex", alignItems: "center", justifyContent: "center", width: 28, height: 28, borderRadius: "50%", background: token.colorFillSecondary, color: token.colorText, fontSize: 14, fontWeight: 600 }}
                        >
                          {initialOf(me.user.display_name, me.user.email)}
                        </span>
                      )
                    }
                  />
                </Dropdown>
              )}
            </div>
          </Layout.Header>
          <Layout.Content id="main" tabIndex={-1} style={{ padding: isMobile ? 14 : 28, outline: "none", "--sc-page-pad": isMobile ? "14px" : "28px" } as CSSProperties}>
            {note && (
              <div style={{ marginBottom: 16 }}>
                <Alert message={{ key: note }} tone="success" />
              </div>
            )}
            {children}
          </Layout.Content>
          {/* The pickers of the sidebar's New. The shell owns them, so the drawer closing does not unmount one mid-pick, and
              the browser opens them inside the click (decision 66). */}
          {me && !admin && (
            <>
              <input ref={filesInput} data-testid="sidebar-file-input" type="file" multiple hidden tabIndex={-1} aria-hidden="true" onChange={onPicked} />
              <input
                data-testid="sidebar-folder-input"
                ref={(el) => {
                  foldersInput.current = el;
                  // The folder picker takes directories; the attribute is not in the typed attribute list.
                  el?.setAttribute("webkitdirectory", "");
                }}
                type="file"
                multiple
                hidden
                tabIndex={-1}
                aria-hidden="true"
                onChange={onPicked}
              />
            </>
          )}
          {folderWhere && (
            <Suspense fallback={null}>
              <NewFolderDialog where={folderWhere} openable={!onFiles} onClose={() => setFolderWhere(null)} />
            </Suspense>
          )}
          <UploadTrayHost />
          {iosOpen && (
            <Suspense fallback={null}>
              <IosInstallDialog onClose={() => setIosFor(null)} />
            </Suspense>
          )}
          {/* The media session (a video or audio player, or the mini player bar) lives across pages; nothing draws without one. */}
          <MediaHost />
          {settingsFor && (
            <Suspense fallback={null}>
              <SpaceSettings space={settingsFor} onclose={() => setSettingsFor(null)} onsaved={() => void loadFolders().catch(() => undefined)} />
            </Suspense>
          )}
        </Layout>
      </Layout>
    </>
  );
}
