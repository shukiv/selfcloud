// SPDX-License-Identifier: AGPL-3.0-or-later

import { lazy, Suspense, useEffect, useState, type ReactNode } from "react";
import { keepInView } from "./menuScroll";
import { Alert as AntdAlert, Button, Checkbox, Drawer, Dropdown, Grid, Layout, Menu, Space, theme as antdTheme } from "antd";
import type { MenuProps } from "antd";
import { Clock, FolderOpen, House, Inbox, Share2, Star, Tags, Trash2, HardDrive, Settings, Building2, Users, Menu as MenuIcon, ChevronsLeft, ChevronsRight, UserRound, Ellipsis } from "lucide-react";
import { useLocation, useNavigate } from "react-router-dom";
import { useT, useLang, directionOf, translatePlural } from "../lib/i18n";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";
import { flash, isAdmin, isOwner, session, sessionChanged, sessionKey } from "../lib/session";
import { failureText } from "../lib/manage/failure";
import { folders, hiddenFolders, loadFolders, loadHiddenFolders } from "../lib/files/folders";
import { setHidden } from "../lib/sharing/api";
import { pendingRequests, refreshPendingRequests } from "../lib/sharing/requestCount";
import type { FolderAccess } from "../lib/files/types";
import { signOut } from "../lib/auth";
import { readPref, writePref } from "../lib/prefs";
import { useSidebar } from "../theme/theme";
import { RADIUS } from "../theme/tokens";
import Alert from "./Alert";
import LangSwitcher from "./LangSwitcher";
import Sentence from "./Sentence";
import ThemeToggle from "./ThemeToggle";
import UploadTray from "./UploadTray";

// The space settings dialog is loaded when a team space's settings are opened.
const SpaceSettings = lazy(() => import("./share/SpaceSettings"));

// Below this width (the antd `lg` breakpoint, 992px) the sidebar is a drawer.
const SIDE_WIDTH = 264;
const RAIL_WIDTH = 88;
// The choice of the rail: "1" when the person folded the sidebar, "0" when it is open.
const PREF_NAV_RAIL = "selfcloud.navRail";

interface NavItem {
  key: string;
  label: ReactNode;
  icon: ReactNode;
}

const icon = (C: typeof House) => <C size={18} strokeWidth={1.75} aria-hidden="true" />;

// The sidebar's entries for the caller. An installation admin has no drive, so it sees the
// administration pages only; an owner also sees the owner's pages.
interface NavSection {
  section?: string;
  items: NavItem[];
}

function navFor(admin: boolean, owner: boolean, t: (k: string) => string, pending: number | null, waiting: (n: number) => string): NavSection[] {
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
    { key: "/", label: t("nav.home"), icon: icon(House) },
    { key: "/files", label: t("nav.files"), icon: icon(FolderOpen) },
    { key: "/starred", label: t("nav.starred"), icon: icon(Star) },
    { key: "/recent", label: t("nav.recent"), icon: icon(Clock) },
    { key: "/shared", label: t("nav.shared"), icon: icon(Share2) },
    { key: "/trash", label: t("nav.trash"), icon: icon(Trash2) },
    { key: "/storage", label: t("nav.storage"), icon: icon(HardDrive) },
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
  return items.map((it) => ({ key: it.key, label: it.label, icon: it.icon }));
}

// TeamRow is one team space in the sidebar: a link to its files, its hidden tag, and an options menu. The menu
// offers Hide or Show to anyone, and Space settings to a manager of the space.
function TeamRow({
  team,
  color,
  current,
  onNavigate,
  onHide,
  onSettings,
  hiddenLabel,
  labels,
}: {
  team: FolderAccess;
  color: string;
  current: boolean;
  onNavigate: () => void;
  onHide: () => void;
  onSettings: () => void;
  hiddenLabel: string;
  labels: { hide: string; show: string; settings: string; options: (name: string) => string };
}) {
  const items: MenuProps["items"] = [
    { key: "hide", label: team.hidden ? labels.show : labels.hide },
    ...(team.role === "manager" ? [{ key: "settings", label: labels.settings }] : []),
  ];
  return (
    <li style={{ display: "flex", alignItems: "center", gap: 4, minWidth: 0 }}>
      <a
        href={`/files/${team.id}`}
        aria-current={current ? "page" : undefined}
        onClick={onNavigate}
        style={{ flex: 1, minWidth: 0, padding: "6px 8px", color, fontWeight: current ? 700 : undefined, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
      >
        <bdi>{team.name}</bdi>
        {team.hidden && <span style={{ marginInlineStart: 8, fontSize: 12, opacity: 0.8 }}>{hiddenLabel}</span>}
      </a>
      <Dropdown
        trigger={["click"]}
        menu={{
          items,
          onClick: ({ key }) => {
            if (key === "hide") onHide();
            if (key === "settings") onSettings();
          },
        }}
      >
        <Button type="text" size="small" aria-label={labels.options(team.name)} icon={<Ellipsis size={16} />} style={{ color }} />
      </Dropdown>
    </li>
  );
}

// Shell is the signed-in frame: the top bar with the account menu, and the sidebar. The sidebar
// is one tree shared by the desktop sider and the drawer below the lg breakpoint.
export default function Shell({ children }: { children: ReactNode }) {
  const { t } = useT();
  const { token } = antdTheme.useToken();
  const sidebar = useSidebar();
  const screens = Grid.useBreakpoint();
  const isMobile = !screens.lg;
  const lang = useLang();
  const rtl = directionOf(lang) === "rtl";
  const me = useStore(session);
  const b = useStore(brand);
  const note = useStore(flash);
  const location = useLocation();
  const navigate = useNavigate();
  const [navOpen, setNavOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(() => readPref(PREF_NAV_RAIL) === "1");

  const admin = isAdmin(me?.user);
  const owner = isOwner(me?.user);
  const folderList = useStore(folders);
  const hiddenList = useStore(hiddenFolders);
  // The team spaces are listed under My files, from the folders the caller reaches. The hidden ones are listed
  // only when the caller asks to see them. An admin has no drive, so it reads neither list.
  const teams = admin ? [] : folderList.filter((f) => f.kind === "team");
  const hiddenTeams = admin ? [] : hiddenList.filter((f) => f.kind === "team");
  const [showHidden, setShowHidden] = useState(false);
  const [settingsFor, setSettingsFor] = useState<FolderAccess | null>(null);
  // shellError is the text, by code, of a failed read of the spaces or a refused hide. The server's text is never shown.
  const [shellError, setShellError] = useState<string | null>(null);
  // A change of person closes the settings, clears the hidden toggle and the error: none of them belongs to the next person.
  const sessionID = sessionKey(me);
  useEffect(() => {
    setShowHidden(false);
    setSettingsFor(null);
    setShellError(null);
  }, [sessionID]);
  // The sidebar's menu scrolls inside the sider. When the address moves to a page below the fold, its item is brought into
  // view inside the menu alone, so the page does not move.
  useEffect(() => {
    const follow = () => {
      const menu = document.querySelector<HTMLElement>(".shell-sider .shell-menu");
      const item = menu?.querySelector<HTMLElement>(".ant-menu-item-selected");
      if (menu && item) keepInView(menu, item);
    };
    follow();
    // The menu's height is settled by the sider's flex column after the first paint, so the scroll follows each resize of the box.
    const menu = document.querySelector<HTMLElement>(".shell-sider .shell-menu");
    if (!menu || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(follow);
    observer.observe(menu);
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
  // installation admin has no requests of its own, and the server refuses the read: it is not sent (as Svelte).
  useEffect(() => {
    if (me && !admin) void refreshPendingRequests();
  }, [sessionID]);
  const pending = useStore(pendingRequests);
  const sections = navFor(admin, owner, t, pending, (n) => translatePlural(lang, "nav.requests.count", n));
  const items: MenuProps["items"] = sections.flatMap((s, i) =>
    s.section
      ? [{ type: "group" as const, key: `group-${i}`, label: s.section, children: menuLeaves(s.items) }]
      : menuLeaves(s.items),
  );
  // A team space lives under /files too, and its row below is the current place: My files is not.
  const inTeam = (id: string) => location.pathname === `/files/${id}` || location.pathname.startsWith(`/files/${id}/`);
  const selected = [...teams, ...hiddenTeams].some((team) => inTeam(team.id))
    ? []
    : sections
        .flatMap((s) => s.items)
        .filter((it) => location.pathname === it.key || location.pathname.startsWith(it.key + "/"))
        .sort((a, c) => c.key.length - a.key.length)
        .slice(0, 1)
        .map((it) => it.key);

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

  const renderSidebar = (collapsedNow: boolean) => (
    <div className="shell-sider-body" style={{ color: sidebar.text }}>
      <div style={{ display: "flex", alignItems: "center", gap: 10, margin: "4px 8px 18px", minWidth: 0 }}>
        {b.logo_url && <img src={b.logo_url} alt="" width={32} height={32} />}
        {!collapsedNow && (
          <span style={{ fontWeight: 700, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            <bdi>{b.name}</bdi>
          </span>
        )}
      </div>
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
      {shellError && !collapsedNow && <AntdAlert type="error" showIcon role="alert" message={shellError} style={{ margin: "8px 0" }} />}
      {(teams.length > 0 || showHidden) && (
        <div style={{ margin: "16px 8px 0" }}>
          <div style={{ fontSize: 12, opacity: 0.8, margin: "0 0 6px" }}>{t("nav.teams")}</div>
          <ul className="teams" aria-label={t("nav.teams")} style={{ listStyle: "none", margin: 0, padding: 0 }}>
            {[...teams, ...(showHidden ? hiddenTeams : [])].map((team) => (
              <TeamRow
                key={team.id}
                team={team}
                color={sidebar.text}
                current={inTeam(team.id)}
                onNavigate={() => setNavOpen(false)}
                onHide={() => void hideSpace(team, !team.hidden)}
                onSettings={() => setSettingsFor(team)}
                hiddenLabel={t("nav.hiddenTag")}
                labels={{ hide: t("nav.hide"), show: t("nav.show"), settings: t("nav.spaceSettings"), options: (name: string) => t("nav.spaceOptions", { name }) }}
              />
            ))}
          </ul>
          <Checkbox checked={showHidden} onChange={(e) => void toggleHidden(e.target.checked)} style={{ color: sidebar.text, marginTop: 8 }}>
            {t("nav.showHidden")}
          </Checkbox>
        </div>
      )}
    </div>
  );

  const userItems: MenuProps["items"] = [
    {
      key: "who",
      disabled: true,
      label: <Sentence template={t("user.signedInAs")} names={{ name: me?.user.email ?? "" }} />,
    },
    { type: "divider" },
    { key: "password", label: t("user.changePassword") },
    { key: "signout", label: t("user.signOut") },
  ];

  return (
    <Layout style={{ minHeight: "100vh", background: token.colorBgLayout }}>
      <a className="skip-link" href="#main">
        {t("app.skip")}
      </a>
      {!isMobile && (
        <Layout.Sider
          className="shell-sider"
          width={SIDE_WIDTH}
          collapsedWidth={RAIL_WIDTH}
          collapsed={collapsed}
          trigger={null}
          style={{
            background: sidebar.bg,
            color: sidebar.text,
            position: "sticky",
            top: 0,
            height: "100vh",
            overflow: "hidden",
            padding: collapsed ? "16px 8px" : "16px 12px",
          }}
        >
          {renderSidebar(collapsed)}
          <Button
            type="text"
            onClick={toggleRail}
            aria-label={collapsed ? t("nav.show") : t("nav.hide")} data-testid="rail-toggle"
            icon={collapsed ? <ChevronsRight size={18} /> : <ChevronsLeft size={18} />}
            style={{ color: sidebar.text, marginTop: 8, width: "100%" }}
          />
        </Layout.Sider>
      )}
      <Drawer
        placement={rtl ? "right" : "left"}
        open={isMobile && navOpen}
        onClose={() => setNavOpen(false)}
        width={280}
        title={null}
        closable
        styles={{ body: { padding: "8px 12px 16px", background: sidebar.bg }, header: { background: sidebar.bg, borderBottom: "none" } }}
      >
        {renderSidebar(false)}
      </Drawer>
      <Layout style={{ background: token.colorBgLayout }}>
        <Layout.Header
          style={{
            display: "flex",
            alignItems: "center",
            gap: 12,
            padding: isMobile ? "0 12px" : "0 24px",
            background: token.colorBgContainer,
            borderBottom: `1px solid ${token.colorBorderSecondary}`,
            height: 56,
            lineHeight: "56px",
          }}
        >
          {isMobile && (
            <Button
              type="text"
              data-testid="nav-open"
              aria-label={t("nav.show")}
              onClick={() => setNavOpen(true)}
              icon={<MenuIcon size={18} />}
            />
          )}
          {/* One line in the 56px bar: a long brand name is cut short, never wrapped over the bar's edge. */}
          <a
            href={admin ? "/admin/tenants" : "/"}
            style={{ fontWeight: 700, color: token.colorText, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
          >
            <bdi>{b.name}</bdi>
          </a>
          {admin && <span style={{ color: token.colorTextSecondary, whiteSpace: "nowrap" }}>{t("admin.badge")}</span>}
          <Space size={isMobile ? 4 : "middle"} style={{ marginInlineStart: "auto" }}>
            <LangSwitcher compact={isMobile} />
            {!isMobile && <ThemeToggle />}
            {me && (
              <Dropdown
                menu={{
                  items: userItems,
                  onClick: ({ key }) => {
                    if (key === "password") navigate("/change-password");
                    if (key === "signout") void logout();
                  },
                }}
                trigger={["click"]}
              >
                <Button data-testid="account-menu" aria-label={t("user.menu")} style={{ borderRadius: RADIUS }}>
                  <bdi>{me.user.display_name || me.user.email}</bdi>
                </Button>
              </Dropdown>
            )}
          </Space>
        </Layout.Header>
        <Layout.Content id="main" tabIndex={-1} style={{ padding: isMobile ? 14 : 28, outline: "none" }}>
          {note && (
            <div style={{ marginBottom: 16 }}>
              <Alert message={{ key: note }} tone="success" />
            </div>
          )}
          {children}
        </Layout.Content>
        <UploadTray />
        {settingsFor && (
          <Suspense fallback={null}>
            <SpaceSettings space={settingsFor} onclose={() => setSettingsFor(null)} onsaved={() => void loadFolders().catch(() => undefined)} />
          </Suspense>
        )}
      </Layout>
    </Layout>
  );
}
