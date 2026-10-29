<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { applyBrand, brand, passwordMinLength } from './lib/brand';
  import { loadInfo, loadMe, type Info } from './lib/auth';
  import { go, location, signInURL } from './lib/router';
  import { safeNext } from './lib/next';
  import { session } from './lib/session';
  import { homeFor, mayOpen, targetOf } from './lib/routes';
  import { PREF_LANG, readPref } from './lib/prefs';
  import { lang, matchLang, setLang, tr } from './lib/i18n';
  import { loadTheme } from './lib/theme';
  import { maxExpiryDays } from './lib/sharing/limits';
  import PublicLayout from './components/PublicLayout.svelte';
  import Shell from './components/Shell.svelte';
  import Login from './pages/Login.svelte';
  import Verify from './pages/Verify.svelte';
  import Reset from './pages/Reset.svelte';
  import SignIn from './pages/SignIn.svelte';
  import Forgot from './pages/Forgot.svelte';
  import ChangePassword from './pages/ChangePassword.svelte';
  import Shared from './pages/Shared.svelte';
  import Requests from './pages/Requests.svelte';
  import NotFound from './pages/NotFound.svelte';
  import Files from './pages/Files.svelte';
  import NodeLink from './pages/NodeLink.svelte';
  import Trash from './pages/Trash.svelte';
  import ManageOverview from './pages/manage/Overview.svelte';
  import ManagePeople from './pages/manage/People.svelte';
  import ManageGroups from './pages/manage/Groups.svelte';
  import ManageSpaces from './pages/manage/Spaces.svelte';
  import ManageAudit from './pages/manage/Audit.svelte';
  import AdminTenants from './pages/admin/Tenants.svelte';
  import AdminTenant from './pages/admin/Tenant.svelte';
  import AdminAdmins from './pages/admin/Admins.svelte';
  import AdminInstallation from './pages/admin/Installation.svelte';

  type View =
    | 'login'
    | 'verify'
    | 'reset'
    | 'forgot'
    | 'sign-in'
    | 'files'
    | 'node'
    | 'shared'
    | 'requests'
    | 'trash'
    | 'manage'
    | 'admin'
    | 'change-password'
    | 'notfound';

  const PUBLIC_VIEWS: View[] = ['login', 'verify', 'reset', 'forgot', 'sign-in'];

  let ready = $state(false);

  function viewFor(path: string): View {
    if (path === '/' || path === '/files' || path.startsWith('/files/')) return 'files';
    if (path.startsWith('/n/')) return 'node';
    // The owner and administration pages are read by their path; whether the person may open one is decided
    // below, so a page for another role is the not-found view.
    const target = targetOf(path);
    if (target !== null) return target.area;
    switch (path) {
      case '/login':
        return 'login';
      case '/verify':
        return 'verify';
      case '/reset':
        return 'reset';
      case '/forgot':
        return 'forgot';
      case '/sign-in':
        return 'sign-in';
      case '/shared':
        return 'shared';
      case '/requests':
        return 'requests';
      case '/trash':
        return 'trash';
      case '/change-password':
        return 'change-password';
      default:
        return 'notfound';
    }
  }

  let view = $derived(viewFor($location.path));
  let target = $derived(targetOf($location.path));

  // The guard: shell pages need a session, the sign-in page is pointless with one, and the
  // administration page is for owners and admins.
  $effect(() => {
    if (!ready) return;
    const signedIn = $session !== null;
    if (view === 'login' && signedIn) {
      go(safeNext(new URLSearchParams($location.search).get('next')), { replace: true });
      return;
    }
    if (view === 'notfound' || PUBLIC_VIEWS.includes(view)) return;
    if (!signedIn) {
      go(signInURL($location.path + $location.search), { replace: true });
      return;
    }
    // The root address opens the home of the person's role: an admin has no drive, so its home is the tenants list.
    if ($location.path === '/' && $session?.user.role === 'admin') {
      go(homeFor($session.user), { replace: true });
    }
  });

  // Links inside the app change the page without a reload. Anything that leaves the site, or asks
  // for a new tab, is left to the browser.
  function interceptLinks(event: MouseEvent) {
    if (event.defaultPrevented || event.button !== 0) return;
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const target = event.target as Element | null;
    const anchor = target?.closest?.('a[href]') as HTMLAnchorElement | null | undefined;
    if (!anchor || anchor.target || anchor.hasAttribute('download')) return;
    const url = new URL(anchor.href);
    if (url.origin !== window.location.origin) return;
    if (url.hash && url.pathname === window.location.pathname) return;
    event.preventDefault();
    go(url.pathname + url.search + url.hash);
  }

  // chooseLang applies the first of: ?lang=, the signed-in person's language, this browser's
  // saved choice, the browser's languages, English.
  function chooseLang(me: { user: { locale: string } } | null): void {
    const fromQuery = matchLang(new URLSearchParams(window.location.search).get('lang'));
    const fromAccount = me ? matchLang(me.user.locale) : null;
    const saved = matchLang(readPref(PREF_LANG));
    const fromBrowser = navigator.languages
      .map((tag) => matchLang(tag))
      .find((l) => l !== null) ?? null;
    setLang(fromQuery ?? fromAccount ?? saved ?? fromBrowser ?? 'en');
  }

  onMount(() => {
    window.addEventListener('click', interceptLinks);
    loadTheme();
    void (async () => {
      let info: Info | null = null;
      try {
        info = await loadInfo();
      } catch {
        info = null;
      }
      applyBrand(info?.brand ?? $brand);
      passwordMinLength.set(info?.passwordMinLength ?? 8);
      maxExpiryDays.set(info?.sharingMaxExpiryDays ?? 0);
      const me = await loadMe().catch(() => null);
      chooseLang(me);
      ready = true;
    })();
    return () => window.removeEventListener('click', interceptLinks);
  });
</script>

{#if !ready}
  <p class="loading" role="status">{$tr('app.loading')}</p>
{:else if PUBLIC_VIEWS.includes(view) || (view === 'notfound' && $session === null)}
  <PublicLayout>
    {#if view === 'login'}
      <Login />
    {:else if view === 'verify'}
      <Verify />
    {:else if view === 'reset'}
      <Reset />
    {:else if view === 'forgot'}
      <Forgot />
    {:else if view === 'sign-in'}
      <SignIn />
    {:else}
      <NotFound />
    {/if}
  </PublicLayout>
{:else if $session !== null}
  <Shell>
    {#if view === 'files'}
      <Files />
    {:else if view === 'node'}
      <NodeLink />
    {:else if view === 'shared'}
      <Shared />
    {:else if view === 'requests'}
      <Requests />
    {:else if view === 'trash'}
      <Trash />
    {:else if view === 'manage' && target?.area === 'manage' && mayOpen(target, $session?.user)}
      {#if target.section === 'overview'}
        <ManageOverview />
      {:else if target.section === 'people'}
        <ManagePeople />
      {:else if target.section === 'groups'}
        <ManageGroups />
      {:else if target.section === 'spaces'}
        <ManageSpaces />
      {:else}
        <ManageAudit />
      {/if}
    {:else if view === 'admin' && target?.area === 'admin' && mayOpen(target, $session?.user)}
      {#if target.section === 'tenants'}
        <AdminTenants />
      {:else if target.section === 'tenant'}
        {#key target.account}
          <AdminTenant account={target.account ?? ''} />
        {/key}
      {:else if target.section === 'admins'}
        <AdminAdmins />
      {:else}
        <AdminInstallation />
      {/if}
    {:else if view === 'manage' || view === 'admin'}
      <NotFound />
    {:else if view === 'change-password'}
      <ChangePassword />
    {:else}
      <NotFound />
    {/if}
  </Shell>
{/if}
