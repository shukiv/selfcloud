<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { brand } from '../lib/brand';
  import { lang, translate } from '../lib/i18n';
  import { fileErrorText } from '../lib/files/fileErrors';
  import { formatDate } from '../lib/files/format';
  import { kindOf } from '../lib/files/kind';
  import { activityKey, lastActivity, mergeRecent, type RecentItem } from '../lib/recent';
  import { clearRecent, listRecent, removeRecent } from '../lib/stars';
  import { sessionChanged, signingOut } from '../lib/session';
  import Confirm from '../components/Confirm.svelte';
  import Icon from '../components/files/Icon.svelte';

  // Recent: the items the caller opened or changed lately, most recent first. It is private to the caller and kept
  // for 30 days, at most 500 items. Removing one takes it off this list only; clearing empties the whole list, and
  // changes no file.
  const PAGE = 20;
  let items = $state<RecentItem[]>([]);
  let cursor = $state<string | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let askClear = $state(false);
  let clearError = $state<string | null>(null);

  onMount(() => {
    // The session is watched from mount, so no request is sent once a sign-out has begun.
    const changed = sessionChanged();
    void load(true, changed);
  });

  async function load(reset: boolean, changed: () => boolean = sessionChanged()) {
    if (changed()) return;
    loading = true;
    try {
      const page = await listRecent(PAGE, reset ? null : cursor);
      if (changed()) return;
      items = reset ? page.items : mergeRecent(items, page.items);
      cursor = page.next_cursor ?? null;
      error = null;
    } catch (err) {
      if (!changed() && !signingOut()) error = fileErrorText(err, $lang);
    } finally {
      loading = false;
    }
  }

  async function remove(n: RecentItem) {
    const changed = sessionChanged();
    try {
      await removeRecent(n.id);
      if (changed()) return;
      items = items.filter((i) => i.id !== n.id);
      error = null;
    } catch (err) {
      if (!changed() && !signingOut()) error = fileErrorText(err, $lang);
    }
  }

  // clear empties the caller's Recent list after the person has confirmed it.
  async function clear() {
    const changed = sessionChanged();
    try {
      await clearRecent();
      if (changed()) return;
      items = [];
      cursor = null;
      askClear = false;
      clearError = null;
      error = null;
    } catch (err) {
      if (!changed() && !signingOut()) clearError = fileErrorText(err, $lang);
    }
  }
</script>

<svelte:head>
  <title>{translate($lang, 'nav.recent')} · {$brand.name}</title>
</svelte:head>

<div class="head">
  <h1 class="page-title">{translate($lang, 'nav.recent')}</h1>
  {#if items.length > 0}
    <button type="button" class="btn secondary" onclick={() => (askClear = true)}>{translate($lang, 'recent.clear')}</button>
  {/if}
</div>

{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if !loading && items.length === 0 && !error}
  <p class="empty" role="status">{translate($lang, 'recent.empty')}</p>
{:else if items.length > 0}
  <ul class="items" aria-label={translate($lang, 'nav.recent')}>
    {#each items as n (n.id)}
      {@const act = lastActivity(n)}
      <li class="item">
        <Icon kind={kindOf(n)} size={24} />
        <a class="name" href={`/n/${n.id}`}><bdi>{n.name}</bdi></a>
        <span class="meta">{translate($lang, activityKey(act), { date: formatDate(act.at, $lang) })}</span>
        <button
          type="button"
          class="btn secondary"
          aria-label={translate($lang, 'recent.removeName', { name: n.name })}
          onclick={() => remove(n)}
        >{translate($lang, 'recent.remove')}</button>
      </li>
    {/each}
  </ul>
{/if}

{#if cursor}
  <div class="more">
    <button type="button" class="btn secondary" onclick={() => load(false)} disabled={loading}>{translate($lang, 'files.loadMore')}</button>
  </div>
{/if}

{#if askClear}
  <Confirm
    id="clear-recent"
    title={translate($lang, 'recent.clearTitle')}
    message={translate($lang, 'recent.clearMessage')}
    action={translate($lang, 'recent.clearAction')}
    danger
    error={clearError}
    onconfirm={clear}
    onclose={() => {
      askClear = false;
      clearError = null;
    }}
  />
{/if}

<style>
  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2) var(--space-3);
    margin-block-end: var(--space-4);
  }

  .page-title {
    margin: 0;
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
