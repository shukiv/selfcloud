<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import { brand } from '../lib/brand';
  import { tr, tp } from '../lib/i18n';
  import { go, location } from '../lib/router';
  import { flash, isAdmin, isOwner as ownerOf, session } from '../lib/session';
  import { signOut } from '../lib/auth';
  import { folders, hiddenFolders, loadFolders, loadHiddenFolders } from '../lib/files/folders';
  import type { FolderAccess } from '../lib/files/types';
  import { manager } from '../lib/files/uploads';
  import { setHidden } from '../lib/sharing/api';
  import { pendingRequests, refreshPendingRequests } from '../lib/sharing/requestCount';
  import Dialog from './Dialog.svelte';
  import { onMount } from 'svelte';
  import Alert from './Alert.svelte';
  import UploadTray from './UploadTray.svelte';
  import LangSwitch from './LangSwitch.svelte';
  import ThemeSwitch from './ThemeSwitch.svelte';
  import SpaceSettings from './share/SpaceSettings.svelte';
  import UsageMeter from './UsageMeter.svelte';
  import { heldBytes } from '../lib/storage/logic';
  import { myStorage, refreshMyStorage } from '../lib/storage/api';

  let { children }: { children: Snippet } = $props();

  let menuOpen = $state(false);
  // teamMenu is the team space whose menu is open, by ID.
  let teamMenu = $state<string | null>(null);
  let showHidden = $state(false);
  let settingsFor = $state<FolderAccess | null>(null);
  let admin = $derived(isAdmin($session?.user));
  let owner = $derived(ownerOf($session?.user));

  // An installation admin has no drive: its navigation is the administration pages only.
  const ADMIN_LINKS = [
    { href: '/admin/tenants', label: 'nav.tenants' },
    { href: '/admin/admins', label: 'nav.admins' },
    { href: '/admin/installation', label: 'nav.installation' },
  ];
  const MANAGE_LINKS = [
    { href: '/manage', label: 'nav.overview' },
    { href: '/manage/people', label: 'nav.people' },
    { href: '/manage/groups', label: 'nav.groups' },
    { href: '/manage/spaces', label: 'nav.spaces' },
    { href: '/manage/storage', label: 'nav.manageStorage' },
    { href: '/manage/links', label: 'nav.links' },
    { href: '/manage/audit', label: 'nav.audit' },
  ];

  // The requests entry shows to an owner always, and to anyone else only when a request waits for a decision.
  let links = $derived(
    admin
      ? ADMIN_LINKS
      : [
          { href: '/', label: 'nav.home' },
          { href: '/files', label: 'nav.files' },
          { href: '/starred', label: 'nav.starred' },
          { href: '/recent', label: 'nav.recent' },
          { href: '/shared', label: 'nav.shared' },
          { href: '/trash', label: 'nav.trash' },
          { href: '/storage', label: 'nav.storage' },
          ...(owner || ($pendingRequests ?? 0) > 0 ? [{ href: '/requests', label: 'nav.requests' }] : []),
        ],
  );

  // The team spaces are listed under My files, from the folders the caller reaches. Hidden ones are listed only
  // when the caller asks to see them. An admin has no drive, so it reads neither list.
  onMount(() => {
    if (admin) return;
    void loadFolders().catch(() => undefined);
    void refreshPendingRequests();
    void refreshMyStorage().catch(() => undefined);
  });
  let teams = $derived(admin ? [] : $folders.filter((f) => f.kind === 'team'));
  let hiddenTeams = $derived($hiddenFolders.filter((f) => f.kind === 'team'));

  async function toggleHidden(on: boolean) {
    showHidden = on;
    if (on) await loadHiddenFolders().catch(() => undefined);
  }

  // hide hides a team space from the caller's own list, or shows it again. Access does not change.
  async function hide(team: FolderAccess, hidden: boolean) {
    teamMenu = null;
    // A refused change leaves the lists as they are; they are read again either way, so they show the truth.
    await setHidden(team.id, hidden).catch(() => undefined);
    await Promise.all([
      loadFolders().catch(() => undefined),
      showHidden ? loadHiddenFolders().catch(() => undefined) : undefined,
    ]);
  }

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
    const target = event.target as Element;
    if (menuOpen && !target.closest('.user-menu')) {
      menuOpen = false;
    }
    if (teamMenu !== null && !target.closest('.team-row')) {
      teamMenu = null;
    }
  }
</script>

<svelte:window onclick={closeOnOutside} />

<a class="skip-link" href="#main">{$tr('app.skip')}</a>
<header class="topbar">
  <a class="brand-link" href={admin ? '/admin/tenants' : '/'}>
    {#if $brand.logo_url}
      <img src={$brand.logo_url} alt="" width="28" height="28" />
    {/if}
    <span class="product-name"><bdi>{$brand.name}</bdi></span>
  </a>
  {#if admin}
    <span class="admin-badge">{$tr('admin.badge')}</span>
  {/if}
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
            {#if link.href === '/requests' && ($pendingRequests ?? 0) > 0}
              <span class="count">{$tp('nav.requests.count', $pendingRequests ?? 0)}</span>
            {/if}
          </a>
        </li>
      {/each}
    </ul>
    {#if !admin && $myStorage}
      <div class="nav-meter">
        <UsageMeter used={heldBytes($myStorage)} quota={$myStorage.quota_bytes} />
      </div>
    {/if}
    {#if owner}
      <h2 class="nav-title">{$tr('nav.manage')}</h2>
      <ul>
        {#each MANAGE_LINKS as link (link.href)}
          <li>
            <a href={link.href} aria-current={isCurrent(link.href, $location.path) ? 'page' : undefined}>
              {$tr(link.label)}
            </a>
          </li>
        {/each}
      </ul>
    {/if}
    {#if teams.length > 0 || showHidden}
      <h2 class="nav-title">{$tr('nav.teams')}</h2>
      <ul class="teams">
        {#each teams as team (team.id)}
          {@render teamRow(team)}
        {/each}
        {#if showHidden}
          {#each hiddenTeams as team (team.id)}
            {@render teamRow(team)}
          {/each}
        {/if}
      </ul>
      <label class="check">
        <input type="checkbox" checked={showHidden} onchange={(e) => toggleHidden((e.currentTarget as HTMLInputElement).checked)} />
        <span>{$tr('nav.showHidden')}</span>
      </label>
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

{#snippet teamRow(team: FolderAccess)}
  <li class="team-row">
    <a
      href={`/files/${team.id}`}
      aria-current={isCurrent(`/files/${team.id}`, $location.path) ? 'page' : undefined}
    >
      <bdi>{team.name}</bdi>
      {#if team.hidden}<span class="count">{$tr('nav.hiddenTag')}</span>{/if}
    </a>
    <button
      type="button"
      class="menu-toggle"
      aria-haspopup="menu"
      aria-expanded={teamMenu === team.id}
      aria-label={$tr('nav.spaceOptions', { name: team.name })}
      onclick={() => (teamMenu = teamMenu === team.id ? null : team.id)}
    >
      …
    </button>
    {#if teamMenu === team.id}
      <div class="menu team-menu" role="menu">
        <button type="button" role="menuitem" class="menu-item" onclick={() => hide(team, !team.hidden)}>
          {team.hidden ? $tr('nav.show') : $tr('nav.hide')}
        </button>
        {#if team.role === 'manager'}
          <button
            type="button"
            role="menuitem"
            class="menu-item"
            onclick={() => {
              teamMenu = null;
              settingsFor = team;
            }}
          >
            {$tr('nav.spaceSettings')}
          </button>
        {/if}
      </div>
    {/if}
  </li>
{/snippet}

{#if signOutAsk}
  <Dialog title={$tr('user.signOutTitle')} id="signout-ask" onclose={() => (signOutAsk = false)}>
    <p>{$tr('user.signOutUploads')}</p>
    <div class="foot">
      <button type="button" class="btn secondary" onclick={() => (signOutAsk = false)}>{$tr('common.cancel')}</button>
      <button type="button" class="btn primary danger-fill" data-autofocus onclick={doLogout}>{$tr('user.signOut')}</button>
    </div>
  </Dialog>
{/if}

{#if settingsFor}
  <SpaceSettings
    space={settingsFor}
    onclose={() => (settingsFor = null)}
    onsaved={() => void loadFolders().catch(() => undefined)}
  />
{/if}
<UploadTray />

<style>
  .team-row {
    position: relative;
    display: flex;
    align-items: center;
    gap: var(--space-1);
  }

  .team-row > a {
    flex: 1 1 auto;
    min-width: 0;
  }

  .menu-toggle {
    min-width: 44px;
    min-height: 44px;
    font: inherit;
    color: var(--color-muted-foreground);
    background: transparent;
    border: 0;
    border-radius: var(--radius-sm);
    cursor: pointer;
  }

  .menu-toggle:hover {
    background: var(--color-muted);
  }

  .team-menu {
    inset-block-start: 100%;
    inset-inline-end: 0;
  }

  .admin-badge {
    padding: 0 var(--space-2);
    font-size: var(--text-xs);
    font-weight: 600;
    color: var(--color-primary);
    background: var(--color-primary-soft);
    border-radius: var(--radius-full);
  }

  .count {
    margin-inline-start: var(--space-2);
    padding: 0 var(--space-2);
    font-size: var(--text-xs);
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    color: var(--color-warning-fg);
    background: var(--color-warning-bg);
    border-radius: var(--radius-full);
  }

  .nav-meter {
    margin-block: var(--space-3);
    padding-inline: var(--space-3);
    font-size: var(--text-sm);
  }

  .nav-title {
    margin: var(--space-4) 0 var(--space-2);
    padding-inline: var(--space-3);
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--color-muted-foreground);
  }

  .check {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-height: 44px;
    padding-inline: var(--space-3);
    font-size: var(--text-sm);
  }

  .check input {
    width: 20px;
    height: 20px;
  }
</style>
