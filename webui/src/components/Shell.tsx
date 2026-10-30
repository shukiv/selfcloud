// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type ReactNode } from "react";
import { Button, Drawer, Dropdown, Grid, Layout, Menu, Space, theme as antdTheme } from "antd";
import type { MenuProps } from "antd";
import { Clock, FolderOpen, House, Inbox, Share2, Star, Trash2, HardDrive, Settings, Building2, Users, Menu as MenuIcon, PanelLeftClose, PanelLeftOpen, UserRound } from "lucide-react";
import { useLocation, useNavigate } from "react-router-dom";
import { useT, useLang, directionOf } from "../lib/i18n";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";
import { flash, isAdmin, isOwner, session } from "../lib/session";
import { signOut } from "../lib/auth";
import { readPref, writePref } from "../lib/prefs";
import { useSidebar } from "../theme/theme";
import { RADIUS } from "../theme/tokens";
import Alert from "./Alert";
import LangSwitcher from "./LangSwitcher";
import Sentence from "./Sentence";
import ThemeToggle from "./ThemeToggle";

// Below this width (the antd `lg` breakpoint, 992px) the sidebar is a drawer.
const SIDE_WIDTH = 264;
const RAIL_WIDTH = 88;
// The choice of the rail: "1" when the person folded the sidebar, "0" when it is open.
const PREF_NAV_RAIL = "selfcloud.navRail";

interface NavItem {
  key: string;
  label: string;
  icon: ReactNode;
}

const icon = (C: typeof House) => <C size={18} strokeWidth={1.75} aria-hidden="true" />;

// The sidebar's entries for the caller. An installation admin has no drive, so it sees the
// administration pages only; an owner also sees the owner's pages.
interface NavSection {
  section?: string;
  items: NavItem[];
}

function navFor(admin: boolean, owner: boolean, t: (k: string) => string): NavSection[] {
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
  if (owner) main.push({ key: "/requests", label: t("nav.requests"), icon: icon(Inbox) });
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
  const sections = navFor(admin, owner, t);
  const items: MenuProps["items"] = sections.flatMap((s, i) =>
    s.section
      ? [{ type: "group" as const, key: `group-${i}`, label: s.section, children: menuLeaves(s.items) }]
      : menuLeaves(s.items),
  );
  const selected = sections
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
            aria-label={collapsed ? t("nav.show") : t("nav.hide")}
            icon={collapsed ? <PanelLeftOpen size={18} /> : <PanelLeftClose size={18} />}
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
          <a href={admin ? "/admin/tenants" : "/"} style={{ fontWeight: 700, color: token.colorText }}>
            <bdi>{b.name}</bdi>
          </a>
          {admin && <span style={{ color: token.colorTextSecondary }}>{t("admin.badge")}</span>}
          <Space size={isMobile ? 4 : "middle"} style={{ marginInlineStart: "auto" }}>
            <LangSwitcher />
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
      </Layout>
    </Layout>
  );
}
