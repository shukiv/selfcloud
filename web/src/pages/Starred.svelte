<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { brand } from '../lib/brand';
  import { lang, translate } from '../lib/i18n';
  import { fileErrorText } from '../lib/files/fileErrors';
  import { formatDate } from '../lib/files/format';
  import { kindOf } from '../lib/files/kind';
  import { sessionChanged, signingOut } from '../lib/session';
  import { listStarred, unstarNode, type StarredItem } from '../lib/stars';
  import Icon from '../components/files/Icon.svelte';

  // Starred: the items the caller starred, newest star first. Removing a star takes the item off this list only;
  // the item itself is not changed. A star on an item the caller can no longer reach is kept but not listed.
  const PAGE = 20;
  let items = $state<StarredItem[]>([]);
  let cursor = $state<string | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  onMount(() => {
    // The session is watched from mount, so no request is sent once a sign-out has begun.
    const changed = sessionChanged();
    void load(true, changed);
  });

  async function load(reset: boolean, changed: () => boolean = sessionChanged()) {
    if (changed()) return;
    loading = true;
    try {
      const page = await listStarred(PAGE, reset ? null : cursor);
      if (changed()) return;
      items = reset ? page.items : [...items, ...page.items.filter((i) => !items.some((s) => s.id === i.id))];
      cursor = page.next_cursor ?? null;
      error = null;
    } catch (err) {
      if (!changed() && !signingOut()) error = fileErrorText(err, $lang);
    } finally {
      loading = false;
    }
  }

  async function unstar(n: StarredItem) {
    const changed = sessionChanged();
    try {
      await unstarNode(n.id);
      if (changed()) return;
      items = items.filter((i) => i.id !== n.id);
      error = null;
    } catch (err) {
      if (!changed() && !signingOut()) error = fileErrorText(err, $lang);
    }
  }
</script>

<svelte:head>
  <title>{translate($lang, 'nav.starred')} · {$brand.name}</title>
</svelte:head>

<h1 class="page-title">{translate($lang, 'nav.starred')}</h1>

{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if !loading && items.length === 0 && !error}
  <p class="empty" role="status">{translate($lang, 'starred.empty')}</p>
{:else if items.length > 0}
  <ul class="items" aria-label={translate($lang, 'nav.starred')}>
    {#each items as n (n.id)}
      <li class="item">
        <Icon kind={kindOf(n)} size={24} />
        <a class="name" href={`/n/${n.id}`}><bdi>{n.name}</bdi></a>
        <span class="meta">{translate($lang, 'starred.on', { date: formatDate(n.starred_at, $lang) })}</span>
        <button type="button" class="btn secondary" onclick={() => unstar(n)}>{translate($lang, 'starred.remove')}</button>
      </li>
    {/each}
  </ul>
{/if}

{#if cursor}
  <div class="more">
    <button type="button" class="btn secondary" onclick={() => load(false)} disabled={loading}>{translate($lang, 'files.loadMore')}</button>
  </div>
{/if}

<style>
  .page-title {
    margin: 0 0 var(--space-4);
    font-size: var(--text-xl);
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

  .more {
    display: flex;
    justify-content: center;
    padding-block: var(--space-4);
  }
</style>
