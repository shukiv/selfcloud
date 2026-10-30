// SPDX-License-Identifier: AGPL-3.0-or-later

import { lazy, Suspense, useEffect, useRef, useState, type ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { Spin, ConfigProvider } from "antd";
import type { Locale } from "antd/es/locale";
import enUS from "antd/locale/en_US";
import { loadInfo, loadMe } from "./lib/auth";
import { applyBrand, brand, passwordMinLength } from "./lib/brand";
import { maxExpiryDays } from "./lib/sharing/limits";
import { go, signInURL } from "./lib/router";
import { afterSignIn } from "./lib/next";
import { isAdmin, session } from "./lib/session";
import { homeFor, mayOpen, targetOf, type ManageSection, type Target } from "./lib/routes";
import type { User } from "./lib/types";
import { antdLocaleOf, directionOf, matchLang, setLang, useLang, useT, type Lang } from "./lib/i18n";
import { PREF_LANG, readPref } from "./lib/prefs";
import { get, useStore } from "./lib/store";
import Shell from "./components/Shell";
import AuthLayout from "./components/AuthLayout";
import LoginPage from "./pages/LoginPage";
import ForgotPage from "./pages/ForgotPage";
import ResetPage from "./pages/ResetPage";
import VerifyPage from "./pages/VerifyPage";
import SignInPage from "./pages/SignInPage";
import ChangePasswordPage from "./pages/ChangePasswordPage";
import NotFoundPage from "./pages/NotFoundPage";

// Pages beyond the shell and the auth pages load on first use, so the first load of sign-in stays small.
const FilesPage = lazy(() => import("./pages/FilesPage"));
const NodeLinkPage = lazy(() => import("./pages/NodeLinkPage"));
const TrashPage = lazy(() => import("./pages/TrashPage"));
const SharedPage = lazy(() => import("./pages/SharedPage"));
const RequestsPage = lazy(() => import("./pages/RequestsPage"));
const OpenLinkPage = lazy(() => import("./pages/OpenLinkPage"));
const HomePage = lazy(() => import("./pages/HomePage"));
const StarredPage = lazy(() => import("./pages/StarredPage"));
const RecentPage = lazy(() => import("./pages/RecentPage"));
const StoragePage = lazy(() => import("./pages/StoragePage"));
const ManageOverviewPage = lazy(() => import("./pages/manage/OverviewPage"));
const ManagePeoplePage = lazy(() => import("./pages/manage/PeoplePage"));
const ManageGroupsPage = lazy(() => import("./pages/manage/GroupsPage"));
const ManageSpacesPage = lazy(() => import("./pages/manage/SpacesPage"));
const ManageStoragePage = lazy(() => import("./pages/manage/StoragePage"));
const ManageLinksPage = lazy(() => import("./pages/manage/LinksPage"));
const ManageAuditPage = lazy(() => import("./pages/manage/AuditPage"));
const AdminTenantsPage = lazy(() => import("./pages/admin/TenantsPage"));
const AdminTenantPage = lazy(() => import("./pages/admin/TenantPage"));
const AdminAdminsPage = lazy(() => import("./pages/admin/AdminsPage"));
const AdminInstallationPage = lazy(() => import("./pages/admin/InstallationPage"));

// The views are the address's pages, read the way the Svelte build reads them, so both interfaces
// agree on what an address means.
type View =
  | "login" | "verify" | "reset" | "forgot" | "sign-in" | "share-link"
  | "home" | "files" | "node" | "starred" | "recent" | "shared" | "requests" | "trash" | "storage"
  | "manage" | "admin" | "change-password" | "notfound";

const PUBLIC_VIEWS: View[] = ["login", "verify", "reset", "forgot", "sign-in", "share-link"];

// The views of the drive: the files and the pages that list them. An installation admin is refused these.
const DRIVE_VIEWS: View[] = ["home", "files", "node", "starred", "recent", "shared", "requests", "trash", "storage"];

export function viewFor(path: string): View {
  if (path === "/") return "home";
  if (path === "/files" || path.startsWith("/files/")) return "files";
  if (path.startsWith("/n/")) return "node";
  const target = targetOf(path);
  if (target !== null) return target.area;
  switch (path) {
    case "/login": return "login";
    case "/verify": return "verify";
    case "/reset": return "reset";
    case "/forgot": return "forgot";
    case "/sign-in": return "sign-in";
    case "/s": return "share-link";
    case "/shared": return "shared";
    case "/starred": return "starred";
    case "/recent": return "recent";
    case "/requests": return "requests";
    case "/trash": return "trash";
    case "/storage": return "storage";
    case "/change-password": return "change-password";
    default: return "notfound";
  }
}

// chooseLang applies the first of: ?lang=, the signed-in person's language, this browser's saved
// choice, the browser's languages, English.
function chooseLang(account: { user: { locale: string } } | null): void {
  const fromQuery = matchLang(new URLSearchParams(window.location.search).get("lang"));
  const fromAccount = account ? matchLang(account.user.locale) : null;
  const saved = matchLang(readPref(PREF_LANG));
  const fromBrowser = navigator.languages.map((tag) => matchLang(tag)).find((l) => l !== null) ?? null;
  void setLang(fromQuery ?? fromAccount ?? saved ?? fromBrowser ?? "en");
}

// interceptLinks keeps in-app links inside the app: a same-origin link changes the page without a reload.
// Anything that leaves the site, or asks for a new tab, is left to the browser.
function interceptLinks(event: MouseEvent) {
  if (event.defaultPrevented || event.button !== 0) return;
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  const target = event.target as Element | null;
  const anchor = target?.closest?.("a[href]") as HTMLAnchorElement | null | undefined;
  if (!anchor || anchor.target || anchor.hasAttribute("download")) return;
  const url = new URL(anchor.href);
  if (url.origin !== window.location.origin) return;
  if (url.hash && url.pathname === window.location.pathname) return;
  event.preventDefault();
  go(url.pathname + url.search + url.hash);
}

function PageLoading() {
  const { t } = useT();
  return (
    <div role="status" style={{ display: "grid", placeItems: "center", minHeight: 240 }}>
      <Spin tip={t("app.loading")} />
    </div>
  );
}

function Loading() {
  const { t } = useT();
  return (
    <div role="status" style={{ display: "grid", placeItems: "center", minHeight: "100vh" }}>
      <Spin tip={t("app.loading")} size="large" />
    </div>
  );
}

// Boot loads the installation's brand and password rule, the signed-in person if there is one, and
// chooses the language, before anything else is drawn.
export function Boot({ children }: { children: ReactNode }) {
  const [ready, setReady] = useState(false);
  const started = useRef(false);
  // Links are listened to in their own effect: StrictMode runs an effect twice, and the boot below
  // must not run twice, since a failed second run would leave the page unready.
  useEffect(() => {
    document.addEventListener("click", interceptLinks);
    return () => document.removeEventListener("click", interceptLinks);
  }, []);
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    void (async () => {
      let info: Awaited<ReturnType<typeof loadInfo>> | null = null;
      try {
        info = await loadInfo();
      } catch {
        info = null;
      }
      applyBrand(info?.brand ?? get(brand));
      passwordMinLength.set(info?.passwordMinLength ?? 8);
      maxExpiryDays.set(info?.sharingMaxExpiryDays ?? 0);
      const me = await loadMe().catch(() => null);
      chooseLang(me);
      setReady(true);
    })();
  }, []);
  if (!ready) return <Loading />;
  return <>{children}</>;
}

// Directionality follows the language: antd's locale and direction for the current one.
export function Direction({ children }: { children: ReactNode }) {
  const lang = useLang();
  // Never undefined: antd adds its locale provider around the page only when it has a locale, so a locale that
  // arrives later would remount the page and lose what it holds.
  const [locale, setLocale] = useState<Locale>(enUS);
  useEffect(() => {
    let live = true;
    void antdLocaleOf(lang).then((l) => {
      if (live) setLocale(l);
    });
    return () => {
      live = false;
    };
  }, [lang]);
  const dir = directionOf(lang as Lang);
  useEffect(() => {
    document.documentElement.lang = lang;
    document.documentElement.dir = dir;
  }, [lang, dir]);
  return (
    <ConfigProvider locale={locale} direction={dir}>
      {children}
    </ConfigProvider>
  );
}

// The public pages a signed-out visitor may see; the shell and its pages need a session.
function Public({ view }: { view: View }) {
  switch (view) {
    case "login": return <LoginPage />;
    case "verify": return <VerifyPage />;
    case "reset": return <ResetPage />;
    case "forgot": return <ForgotPage />;
    case "sign-in": return <SignInPage />;
    case "share-link":
      return (
        <AuthLayout>
          <Suspense fallback={<PageLoading />}>
            <OpenLinkPage />
          </Suspense>
        </AuthLayout>
      );
    default: return <AuthLayout><NotFoundPage /></AuthLayout>;
  }
}

// Gate decides what the address shows: a public page, the shell with its page, or a redirect.
export function Gate() {
  const loc = useLocation();
  const me = useStore(session);
  const path = loc.pathname;
  const search = loc.search;
  const view = viewFor(path);
  const signedIn = me !== null;

  if (view === "login" && signedIn) {
    return <Navigate to={afterSignIn(new URLSearchParams(search).get("next"), me.user)} replace />;
  }
  if (view === "notfound" ? !signedIn : PUBLIC_VIEWS.includes(view)) {
    return <Public view={view} />;
  }
  if (!signedIn) {
    return <Navigate to={signInURL(path + search)} replace />;
  }
  if (path === "/" && isAdmin(me.user)) {
    return <Navigate to={homeFor(me.user)} replace />;
  }
  return (
    <Shell>
      <Suspense fallback={<PageLoading />}>{signedPage(view, path, me.user)}</Suspense>
    </Shell>
  );
}

// manageSection picks the owner page for a section. Each page checks the owner role itself as well.
function manageSection(section: ManageSection): ReactNode {
  switch (section) {
    case "overview": return <ManageOverviewPage />;
    case "people": return <ManagePeoplePage />;
    case "groups": return <ManageGroupsPage />;
    case "spaces": return <ManageSpacesPage />;
    case "storage": return <ManageStoragePage />;
    case "links": return <ManageLinksPage />;
    case "audit": return <ManageAuditPage />;
  }
}

// adminSection picks the administration page. Each page checks the installation admin role itself as well.
function adminSection(target: Extract<Target, { area: "admin" }>): ReactNode {
  switch (target.section) {
    case "tenants": return <AdminTenantsPage />;
    case "tenant": return <AdminTenantPage account={target.account ?? ""} />;
    case "admins": return <AdminAdminsPage />;
    case "installation": return <AdminInstallationPage />;
  }
}

// signedPage is the page inside the shell. The pages not built yet show their placeholder, and an
// address the person may not open is the not-found page.
function signedPage(view: View, path: string, user: User): ReactNode {
  const target = targetOf(path);
  // An installation admin has no drive: the server refuses it every read of a file (internal/api/access.go), so the
  // drive pages are not its pages. The change-password page is open to every signed-in person.
  if (user.role === "admin" && DRIVE_VIEWS.includes(view)) return <NotFoundPage />;
  switch (view) {
    case "change-password": return <ChangePasswordPage />;
    case "files": return <FilesPage />;
    case "node": return <NodeLinkPage />;
    case "trash": return <TrashPage />;
    case "shared": return <SharedPage />;
    case "requests": return <RequestsPage />;
    case "notfound": return <NotFoundPage />;
    case "home": return <HomePage />;
    case "starred": return <StarredPage />;
    case "recent": return <RecentPage />;
    case "storage": return <StoragePage />;
    case "manage":
      if (target === null || target.area !== "manage" || !mayOpen(target, user)) return <NotFoundPage />;
      return manageSection(target.section);
    case "admin":
      if (target === null || target.area !== "admin" || !mayOpen(target, user)) return <NotFoundPage />;
      return adminSection(target);
    default:
      return <NotFoundPage />;
  }
}
