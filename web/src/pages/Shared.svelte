<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { lang, translate } from '../lib/i18n';
  import { brand } from '../lib/brand';
  import { session } from '../lib/session';
  import { fileErrorText } from '../lib/files/fileErrors';
  import { formatDate } from '../lib/files/format';
  import { sharedWithMe } from '../lib/sharing/api';
  import type { Node } from '../lib/files/types';
  import Icon from '../components/files/Icon.svelte';
  import { kindOf } from '../lib/files/kind';

  // Shared with me: the items other people shared with the caller, each at the role it has. An item opens in its
  // folder, through its address (/n/{nod}), like any link a share mail carries.
  let items = $state<Node[]>([]);
  let cursor = $state<string | undefined>(undefined);
  let loading = $state(true);
  let error = $state<string | null>(null);

  onMount(() => {
    void load(true);
  });

  async function load(reset: boolean) {
    loading = true;
    try {
      const page = await sharedWithMe(reset ? undefined : cursor);
      items = reset ? page.items : [...items, ...page.items];
      cursor = page.next_cursor;
      error = null;
    } catch (err) {
      error = fileErrorText(err, $lang);
    } finally {
      loading = false;
    }
  }

  // ownerText names the owner when it is the caller; otherwise the API gives no name, and nothing is shown.
  function ownerText(n: Node): string {
    return n.owner_id !== undefined && n.owner_id === $session?.user.id ? translate($lang, 'files.details.you') : '';
  }
</script>

<svelte:head>
  <title>{translate($lang, 'nav.shared')} · {$brand.name}</title>
</svelte:head>

<h1 class="page-title">{translate($lang, 'nav.shared')}</h1>

{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if !loading && items.length === 0 && !error}
  <p class="empty" role="status">{translate($lang, 'shared.empty')}</p>
{:else if items.length > 0}
  <ul class="shared" aria-label={translate($lang, 'nav.shared')}>
    {#each items as n (n.id)}
      <li class="item">
        <Icon kind={kindOf(n)} size={24} />
        <a class="name" href={`/n/${n.id}`}><bdi>{n.name}</bdi></a>
        <span class="badge">{translate($lang, `role.${n.role ?? 'viewer'}`)}</span>
        <span class="meta">{translate($lang, 'shared.lastChange', { date: formatDate(n.modified_at, $lang) })}</span>
        {#if ownerText(n)}<span class="meta">{ownerText(n)}</span>{/if}
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

  .shared {
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

  .badge {
    padding: 2px var(--space-2);
    font-size: var(--text-xs);
    font-weight: 500;
    background: var(--color-muted);
    border-radius: var(--radius-full);
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
