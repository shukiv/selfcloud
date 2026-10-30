<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { brand } from '../lib/brand';
  import { lang, translate } from '../lib/i18n';
  import { formatBytes, formatDate } from '../lib/files/format';
  import { fileErrorText } from '../lib/files/fileErrors';
  import { sessionChanged } from '../lib/session';
  import { myLargest, myStorage, refreshMyStorage, type LargestFile } from '../lib/storage/api';
  import { availableOf, heldBytes, LARGEST_PAGE, mergePage } from '../lib/storage/logic';
  import UsageMeter from '../components/UsageMeter.svelte';

  // Storage: the caller's own My files, what they hold against their limit, and the largest files in them. Team
  // spaces are not here; they count against the account, and the owner sees them in Manage > Storage.
  let files = $state<LargestFile[]>([]);
  let cursor = $state<string | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  onMount(() => {
    // The session is watched from mount, so the load after the await never runs once a sign-out has begun.
    const changed = sessionChanged();
    void (async () => {
      await refreshMyStorage().catch(() => undefined);
      if (changed()) return;
      await load(true, changed);
    })();
  });

  async function load(reset: boolean, changed: () => boolean = sessionChanged()) {
    if (changed()) return;
    loading = true;
    try {
      const page = await myLargest(LARGEST_PAGE, reset ? null : cursor);
      if (changed()) return;
      files = reset ? page.items : mergePage(files, page.items);
      cursor = page.next_cursor ?? null;
      error = null;
    } catch (err) {
      error = fileErrorText(err, $lang);
    } finally {
      loading = false;
    }
  }

  let held = $derived($myStorage ? heldBytes($myStorage) : 0);
  let available = $derived($myStorage ? availableOf(held, $myStorage.quota_bytes) : null);
</script>

<svelte:head>
  <title>{translate($lang, 'storage.title')} · {$brand.name}</title>
</svelte:head>

<h1 class="page-title">{translate($lang, 'storage.title')}</h1>

{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if $myStorage}
  <section class="panel-plain" aria-labelledby="storage-meter">
    <h2 id="storage-meter">{translate($lang, 'storage.mine')}</h2>
    <UsageMeter used={held} quota={$myStorage.quota_bytes} />
    <dl class="stat-grid">
      <div class="stat">
        <dt>{translate($lang, 'storage.used')}</dt>
        <dd>{formatBytes($myStorage.used_bytes, $lang)}</dd>
      </div>
      <div class="stat">
        <dt>{translate($lang, 'storage.reserved')}</dt>
        <dd>{formatBytes($myStorage.reserved_bytes, $lang)}</dd>
      </div>
      <div class="stat">
        <dt>{translate($lang, 'storage.limit')}</dt>
        <dd>
          {#if $myStorage.quota_bytes === null}
            {translate($lang, 'storage.noLimit')}
          {:else}
            {formatBytes($myStorage.quota_bytes, $lang)}
          {/if}
        </dd>
      </div>
      {#if available !== null}
        <div class="stat">
          <dt>{translate($lang, 'storage.available')}</dt>
          <dd>{formatBytes(available, $lang)}</dd>
        </div>
      {/if}
    </dl>
  </section>
{/if}

<section aria-labelledby="largest-title">
  <div class="toolbar">
    <h2 id="largest-title" class="section-title">{translate($lang, 'storage.largest')}</h2>
    <a href="/trash">{translate($lang, 'storage.trashLink')}</a>
  </div>

  {#if loading && files.length === 0}
    <p role="status">{translate($lang, 'app.loading')}</p>
  {:else if files.length === 0}
    <p class="empty" role="status">{translate($lang, 'storage.largestEmpty')}</p>
  {:else}
    <table class="storage-table">
      <thead>
        <tr>
          <th scope="col">{translate($lang, 'storage.name')}</th>
          <th scope="col" class="num">{translate($lang, 'storage.size')}</th>
          <th scope="col" class="num">{translate($lang, 'storage.oldVersions')}</th>
          <th scope="col">{translate($lang, 'storage.modified')}</th>
        </tr>
      </thead>
      <tbody>
        {#each files as f (f.id)}
          <tr>
            <th scope="row" class="name">
              <a href={`/n/${encodeURIComponent(f.id)}`}><bdi>{f.name}</bdi></a>
              {#if f.in_trash}<span class="badge">{translate($lang, 'storage.inTrash')}</span>{/if}
            </th>
            <td class="num">{formatBytes(f.size, $lang)}</td>
            <td class="num">{formatBytes(f.versions_bytes, $lang)}</td>
            <td><time datetime={f.modified_at}>{formatDate(f.modified_at, $lang)}</time></td>
          </tr>
        {/each}
      </tbody>
    </table>
    {#if cursor}
      <div class="more">
        <button type="button" class="btn secondary" disabled={loading} onclick={() => load(false)}>{translate($lang, 'common.loadMore')}</button>
      </div>
    {/if}
  {/if}
</section>

<style>
  .section-title {
    margin: 0;
    font-size: var(--text-lg);
    font-weight: 600;
  }

  section {
    margin-block-start: var(--space-6);
  }

  .storage-table {
    width: 100%;
    border-collapse: collapse;
    font-variant-numeric: tabular-nums;
  }

  .storage-table th,
  .storage-table td {
    padding: var(--space-2) var(--space-3);
    text-align: start;
    border-block-end: 1px solid var(--color-muted);
    vertical-align: top;
  }

  .storage-table .num {
    text-align: end;
    white-space: nowrap;
  }

  .name a {
    font-weight: 500;
  }

  .badge {
    margin-inline-start: var(--space-2);
    padding: 0 var(--space-2);
    font-size: var(--text-xs);
    font-weight: 600;
    color: var(--color-warning-fg);
    background: var(--color-warning-bg);
    border-radius: var(--radius-full);
  }

  .more {
    display: flex;
    justify-content: center;
    margin-block-start: var(--space-4);
  }
</style>
