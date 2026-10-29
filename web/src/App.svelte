<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { applyBrand, brand, passwordMinLength } from './lib/brand';
  import { loadInfo, loadMe, type Info } from './lib/auth';
  import { go, location, signInURL } from './lib/router';
  import { safeNext } from './lib/next';
  import { session, canSeeAdmin } from './lib/session';
  import { PREF_LANG, readPref } from './lib/prefs';
  import { lang, matchLang, setLang, tr } from './lib/i18n';
  import { loadTheme } from './lib/theme';
  import PublicLayout from './components/PublicLayout.svelte';
  import Shell from './components/Shell.svelte';
  import Login from './pages/Login.svelte';
  import Verify from './pages/Verify.svelte';
  import Reset from './pages/Reset.svelte';
  import Forgot from './pages/Forgot.svelte';
  import ChangePassword from './pages/ChangePassword.svelte';
  import Placeholder from './pages/Placeholder.svelte';
  import NotFound from './pages/NotFound.svelte';

  type View =
    | 'login'
    | 'verify'
    | 'reset'
    | 'forgot'
    | 'files'
    | 'shared'
    | 'trash'
    | 'admin'
    | 'change-password'
    | 'notfound';

  const PUBLIC_VIEWS: View[] = ['login', 'verify', 'reset', 'forgot'];

  let ready = $state(false);

  function viewFor(path: string): View {
    if (path === '/' || path === '/files' || path.startsWith('/files/')) return 'files';
    switch (path) {
      case '/login':
        return 'login';
      case '/verify':
        return 'verify';
      case '/reset':
        return 'reset';
      case '/forgot':
        return 'forgot';
      case '/shared':
        return 'shared';
      case '/trash':
        return 'trash';
      case '/admin':
        return 'admin';
      case '/change-password':
        return 'change-password';
      default:
        return 'notfound';
    }
  }

  let view = $derived(viewFor($location.path));

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
    if (view === 'admin' && !canSeeAdmin($session?.user)) {
      go('/files', { replace: true });
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
    {:else}
      <NotFound />
    {/if}
  </PublicLayout>
{:else if $session !== null}
  <Shell>
    {#if view === 'files'}
      <Placeholder titleKey="nav.files" />
    {:else if view === 'shared'}
      <Placeholder titleKey="nav.shared" />
    {:else if view === 'trash'}
      <Placeholder titleKey="nav.trash" />
    {:else if view === 'admin'}
      <Placeholder titleKey="nav.admin" />
    {:else if view === 'change-password'}
      <ChangePassword />
    {:else}
      <NotFound />
    {/if}
  </Shell>
{/if}
