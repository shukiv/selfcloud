<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { brand } from '../lib/brand';
  import { lang, translate, translatePlural } from '../lib/i18n';
  import { fileErrorText } from '../lib/files/fileErrors';
  import { formatDate } from '../lib/files/format';
  import { kindOf } from '../lib/files/kind';
  import { session, sessionChanged, signingOut } from '../lib/session';
  import { listAccessRequests } from '../lib/sharing/api';
  import { myStorage, refreshMyStorage } from '../lib/storage/api';
  import { HOME_SECTION_SIZE, storageNoticePercent, waitingCount } from '../lib/home';
  import { activityKey, lastActivity, type RecentItem } from '../lib/recent';
  import { listRecent, listSharedRecent, listStarred, type SharedItem, type StarredItem } from '../lib/stars';
  import type { AccessRequest, Node } from '../lib/files/types';
  import Icon from '../components/files/Icon.svelte';
  import UsageMeter from '../components/UsageMeter.svelte';
  import { heldBytes } from '../lib/storage/logic';

  // Home: the caller's recent items, starred items, what was shared with it lately, the requests waiting for its
  // decision, and a notice when its own storage is nearly full. Each section reads its own list; a section that
  // fails shows its own error and the others still show. Every item opens through its address (/n/{nod}).
  let recent = $state<RecentItem[]>([]);
  let starred = $state<StarredItem[]>([]);
  let shared = $state<SharedItem[]>([]);
  let waiting = $state<AccessRequest[]>([]);
  let errors = $state<Record<'recent' | 'starred' | 'shared' | 'waiting', string | null>>({
    recent: null,
    starred: null,
    shared: null,
    waiting: null,
  });
  let loaded = $state(false);

  let percent = $derived(storageNoticePercent($myStorage));

  onMount(() => {
    // An installation admin has no drive, and the root address sends it to the tenants list. Home makes no request for it.
    if ($session?.user.role === 'admin') return;
    // The session is watched from mount, so no section's request is sent once a sign-out has begun, and no answer
    // lands on a page whose session has changed.
    const changed = sessionChanged();
    void (async () => {
      await Promise.all([
        loadRecent(changed),
        loadStarred(changed),
        loadShared(changed),
        loadWaiting(changed),
        refreshMyStorage().catch(() => undefined),
      ]);
      if (!changed()) loaded = true;
    })();
  });

  async function loadRecent(changed: () => boolean) {
    try {
      const page = await listRecent(HOME_SECTION_SIZE);
      if (changed()) return;
      recent = page.items;
      errors.recent = null;
    } catch (err) {
      if (!changed() && !signingOut()) errors.recent = fileErrorText(err, $lang);
    }
  }

  async function loadStarred(changed: () => boolean) {
    try {
      const page = await listStarred(HOME_SECTION_SIZE);
      if (changed()) return;
      starred = page.items;
      errors.starred = null;
    } catch (err) {
      if (!changed() && !signingOut()) errors.starred = fileErrorText(err, $lang);
    }
  }

  async function loadShared(changed: () => boolean) {
    try {
      const page = await listSharedRecent(HOME_SECTION_SIZE);
      if (changed()) return;
      shared = page.items;
      errors.shared = null;
    } catch (err) {
      if (!changed() && !signingOut()) errors.shared = fileErrorText(err, $lang);
    }
  }

  async function loadWaiting(changed: () => boolean) {
    try {
      const { items } = await listAccessRequests();
      if (changed()) return;
      waiting = items;
      errors.waiting = null;
    } catch (err) {
      if (!changed() && !signingOut()) errors.waiting = fileErrorText(err, $lang);
    }
  }

  // nodeHref is the address an item opens at. Items open through /n/{nod}, as links in share mail do.
  function nodeHref(n: Node): string {
    return `/n/${n.id}`;
  }
</script>

<svelte:head>
  <title>{translate($lang, 'nav.home')} · {$brand.name}</title>
</svelte:head>

<h1 class="page-title">{translate($lang, 'nav.home')}</h1>

{#if percent !== null && $myStorage}
  <section class="notice" aria-labelledby="home-storage">
    <h2 id="home-storage" class="sr-only">{translate($lang, 'home.storageLink')}</h2>
    <p>{translate($lang, 'home.storageNotice', { percent: String(percent) })}
      <a href="/storage">{translate($lang, 'home.storageLink')}</a></p>
    <UsageMeter used={heldBytes($myStorage)} quota={$myStorage.quota_bytes} />
  </section>
{/if}

{#if waiting.length > 0}
  <section class="section" aria-labelledby="home-waiting">
    <div class="head">
      <h2 id="home-waiting">{translate($lang, 'home.waiting')}</h2>
      <a href="/requests">{translate($lang, 'home.seeAll')}</a>
    </div>
    <p class="waiting" role="status">{translatePlural($lang, 'home.waiting.count', waitingCount(waiting))}</p>
  </section>
{/if}
{#if errors.waiting}<p class="alert error" role="alert">{errors.waiting}</p>{/if}

<section class="section" aria-labelledby="home-recent">
  <div class="head">
    <h2 id="home-recent">{translate($lang, 'home.recent')}</h2>
    <a href="/recent">{translate($lang, 'home.seeAll')}</a>
  </div>
  {#if errors.recent}<p class="alert error" role="alert">{errors.recent}</p>{/if}
  {#if loaded && recent.length === 0 && !errors.recent}
    <p class="empty" role="status">{translate($lang, 'recent.empty')}</p>
  {:else if recent.length > 0}
    <ul class="items" aria-label={translate($lang, 'home.recent')}>
      {#each recent as n (n.id)}
        {@const act = lastActivity(n)}
        <li class="item">
          <Icon kind={kindOf(n)} size={24} />
          <a class="name" href={nodeHref(n)}><bdi>{n.name}</bdi></a>
          <span class="meta">{translate($lang, activityKey(act), { date: formatDate(act.at, $lang) })}</span>
        </li>
      {/each}
    </ul>
  {/if}
</section>

<section class="section" aria-labelledby="home-starred">
  <div class="head">
    <h2 id="home-starred">{translate($lang, 'home.starred')}</h2>
    <a href="/starred">{translate($lang, 'home.seeAll')}</a>
  </div>
  {#if errors.starred}<p class="alert error" role="alert">{errors.starred}</p>{/if}
  {#if loaded && starred.length === 0 && !errors.starred}
    <p class="empty" role="status">{translate($lang, 'starred.empty')}</p>
  {:else if starred.length > 0}
    <ul class="items" aria-label={translate($lang, 'home.starred')}>
      {#each starred as n (n.id)}
        <li class="item">
          <Icon kind={kindOf(n)} size={24} />
          <a class="name" href={nodeHref(n)}><bdi>{n.name}</bdi></a>
          <span class="meta">{translate($lang, 'starred.on', { date: formatDate(n.starred_at, $lang) })}</span>
        </li>
      {/each}
    </ul>
  {/if}
</section>

<section class="section" aria-labelledby="home-shared">
  <div class="head">
    <h2 id="home-shared">{translate($lang, 'home.shared')}</h2>
    <a href="/shared">{translate($lang, 'home.seeAll')}</a>
  </div>
  {#if errors.shared}<p class="alert error" role="alert">{errors.shared}</p>{/if}
  {#if loaded && shared.length === 0 && !errors.shared}
    <p class="empty" role="status">{translate($lang, 'shared.empty')}</p>
  {:else if shared.length > 0}
    <ul class="items" aria-label={translate($lang, 'home.shared')}>
      {#each shared as n (n.id)}
        <li class="item">
          <Icon kind={kindOf(n)} size={24} />
          <a class="name" href={nodeHref(n)}><bdi>{n.name}</bdi></a>
          <span class="meta">{translate($lang, 'home.sharedOn', { date: formatDate(n.shared_at, $lang) })}</span>
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  .page-title {
    margin: 0 0 var(--space-4);
    font-size: var(--text-xl);
    font-weight: 600;
  }

  .section {
    margin-block-end: var(--space-5);
  }

  .head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-2);
    margin-block-end: var(--space-2);
  }

  .head h2 {
    margin: 0;
    font-size: var(--text-lg);
    font-weight: 600;
  }

  .items {
    display: grid;
    gap: var(--space-2);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .item {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2) var(--space-3);
    min-height: 44px;
    padding: var(--space-2) var(--space-3);
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
  }

  .name {
    flex: 1 1 160px;
    min-width: 0;
    overflow-wrap: anywhere;
    font-weight: 600;
    color: var(--color-primary);
  }

  .meta,
  .empty {
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }

  .notice {
    display: grid;
    gap: var(--space-2);
    margin-block-end: var(--space-5);
    padding: var(--space-3);
    font-size: var(--text-sm);
    background: var(--color-warning-bg);
    border-radius: var(--radius-md);
  }

  .notice p {
    margin: 0;
  }
</style>
