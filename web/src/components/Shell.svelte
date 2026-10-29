<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import { brand } from '../lib/brand';
  import { tr } from '../lib/i18n';
  import { go, location } from '../lib/router';
  import { canSeeAdmin, flash, session } from '../lib/session';
  import { signOut } from '../lib/auth';
  import { folders, loadFolders } from '../lib/files/folders';
  import { manager } from '../lib/files/uploads';
  import Dialog from './Dialog.svelte';
  import { onMount } from 'svelte';
  import Alert from './Alert.svelte';
  import UploadTray from './UploadTray.svelte';
  import LangSwitch from './LangSwitch.svelte';
  import ThemeSwitch from './ThemeSwitch.svelte';

  let { children }: { children: Snippet } = $props();

  let menuOpen = $state(false);

  let links = $derived([
    { href: '/files', label: 'nav.files' },
    { href: '/shared', label: 'nav.shared' },
    { href: '/trash', label: 'nav.trash' },
    ...(canSeeAdmin($session?.user) ? [{ href: '/admin', label: 'nav.admin' }] : []),
  ]);

  // The team spaces are listed under My files, from the folders the caller reaches.
  onMount(() => {
    void loadFolders().catch(() => undefined);
  });
  let teams = $derived($folders.filter((f) => f.kind === 'team'));

  function isCurrent(href: string, path: string): boolean {
    return path === href || path.startsWith(href + '/');
  }

  // isolate wraps user text in directional isolate marks, as <bdi> does, so an address in the other
  // direction keeps its order inside an Arabic or Hebrew sentence.
  function isolate(text: string): string {
    return String.fromCharCode(0x2068) + text + String.fromCharCode(0x2069);
  }

  function openPage(href: string) {
    menuOpen = false;
    go(href);
  }

  // signOutAsk is open while the person is asked whether to sign out with uploads still running.
  let signOutAsk = $state(false);

  // logout asks first when uploads are running: signing out stops them. The uploads leave the list with the
  // session (see the upload manager), so this only confirms.
  function logout() {
    menuOpen = false;
    if (manager.active > 0) {
      signOutAsk = true;
      return;
    }
    void doLogout();
  }

  async function doLogout() {
    signOutAsk = false;
    try {
      await signOut();
    } finally {
      go('/login', { replace: true });
    }
  }

  function closeOnOutside(event: MouseEvent) {
    if (menuOpen && !(event.target as Element).closest('.user-menu')) {
      menuOpen = false;
    }
  }
</script>

<svelte:window onclick={closeOnOutside} />

<a class="skip-link" href="#main">{$tr('app.skip')}</a>
<header class="topbar">
  <a class="brand-link" href="/files">
    {#if $brand.logo_url}
      <img src={$brand.logo_url} alt="" width="28" height="28" />
    {/if}
    <span class="product-name"><bdi>{$brand.name}</bdi></span>
  </a>
  <div class="topbar-end">
    <LangSwitch />
    <ThemeSwitch />
    <div class="user-menu">
      <button
        type="button"
        class="btn secondary"
        aria-haspopup="true"
        aria-expanded={menuOpen}
        aria-controls="account-menu"
        onclick={() => (menuOpen = !menuOpen)}
      >
        <span class="sr-only">{$tr('user.menu')}: </span>
        <bdi>{$session?.user.display_name || $session?.user.email}</bdi>
      </button>
      {#if menuOpen}
        <div class="menu" id="account-menu">
          <p class="menu-who">{$tr('user.signedInAs', { name: isolate($session?.user.email ?? '') })}</p>
          <button type="button" class="menu-item" onclick={() => openPage('/change-password')}>
            {$tr('user.changePassword')}
          </button>
          <button type="button" class="menu-item" onclick={logout}>{$tr('user.signOut')}</button>
        </div>
      {/if}
    </div>
  </div>
</header>

<div class="shell">
  <nav class="sidenav" aria-label={$tr('nav.main')}>
    <ul>
      {#each links as link (link.href)}
        <li>
          <a href={link.href} aria-current={isCurrent(link.href, $location.path) ? 'page' : undefined}>
            {$tr(link.label)}
          </a>
        </li>
      {/each}
    </ul>
    {#if teams.length > 0}
      <h2 class="nav-title">{$tr('nav.teams')}</h2>
      <ul class="teams">
        {#each teams as team (team.id)}
          <li>
            <a
              href={`/files/${team.id}`}
              aria-current={isCurrent(`/files/${team.id}`, $location.path) ? 'page' : undefined}
            >
              <bdi>{team.name}</bdi>
            </a>
          </li>
        {/each}
      </ul>
    {/if}
  </nav>
  <main id="main" class="content" tabindex="-1">
    {#if $flash}
      <div class="notice">
        <Alert message={{ key: $flash }} tone="success" />
        <button type="button" class="btn secondary" onclick={() => flash.set(null)}>{$tr('common.dismiss')}</button>
      </div>
    {/if}
    {@render children()}
  </main>
</div>
<UploadTray />
{#if signOutAsk}
  <Dialog title={$tr('user.signOutTitle')} id="signout-ask" onclose={() => (signOutAsk = false)}>
    <p>{$tr('user.signOutUploads')}</p>
    <div class="foot">
      <button type="button" class="btn secondary" onclick={() => (signOutAsk = false)}>{$tr('common.cancel')}</button>
      <button type="button" class="btn primary danger-fill" data-autofocus onclick={doLogout}>{$tr('user.signOut')}</button>
    </div>
  </Dialog>
{/if}
