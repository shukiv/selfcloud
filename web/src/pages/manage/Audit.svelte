<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { brand } from '../../lib/brand';
  import { lang, translate } from '../../lib/i18n';
  import { session } from '../../lib/session';
  import { auditLog } from '../../lib/manage/api';
  import { AUDIT_ACTIONS } from '../../lib/manage/actions';
  import { failureText } from '../../lib/manage/failure';
  import { loadNames, NO_NAMES } from '../../lib/manage/names';
  import type { AuditEntry, Names } from '../../lib/audit/sentence';
  import AuditItem from '../../components/manage/AuditItem.svelte';

  // The audit log: what changed in the account, newest first. The server hides an entry about a personal folder
  // from everyone but its user, and this page shows the list exactly as the server answers it.
  type Entry = AuditEntry & { id: string; time: string };
  let entries = $state<Entry[]>([]);
  let cursor = $state<string | undefined>(undefined);
  let action = $state('');
  let loading = $state(true);
  let error = $state<string | null>(null);
  let names = $state<Names>(NO_NAMES);
  let namesPartial = $state(false);

  let account = $derived($session?.account?.id ?? '');

  onMount(() => {
    void loadNames(account)
      .then((r) => {
        names = r.names;
        namesPartial = !r.complete;
      })
      .catch(() => undefined);
    void load(true);
  });

  async function load(reset: boolean) {
    loading = true;
    try {
      const page = await auditLog(account, { action: action || undefined, cursor: reset ? undefined : cursor });
      entries = reset ? page.items : [...entries, ...page.items];
      cursor = page.next_cursor;
      error = null;
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      loading = false;
    }
  }

  function filterChanged(e: Event) {
    action = (e.currentTarget as HTMLSelectElement).value;
    entries = [];
    cursor = undefined;
    void load(true);
  }
</script>

<svelte:head>
  <title>{translate($lang, 'manage.audit.title')} · {$brand.name}</title>
</svelte:head>

<div class="toolbar">
  <h1 class="page-title">{translate($lang, 'manage.audit.title')}</h1>
  <div class="toolbar-actions">
    <label for="audit-action">{translate($lang, 'audit.filter')}</label>
    <select id="audit-action" value={action} onchange={filterChanged}>
      <option value="">{translate($lang, 'audit.allActions')}</option>
      {#each AUDIT_ACTIONS as a (a)}
        <option value={a}>{a}</option>
      {/each}
    </select>
  </div>
</div>

{#if namesPartial}
  <p class="muted" role="note">{translate($lang, 'audit.namesPartial')}</p>
{/if}
{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if loading && entries.length === 0}
  <p role="status">{translate($lang, 'app.loading')}</p>
{:else if entries.length === 0}
  <p class="empty" role="status">{translate($lang, 'audit.empty')}</p>
{:else}
  <ul class="rows" aria-label={translate($lang, 'manage.audit.title')}>
    {#each entries as entry (entry.id)}
      <AuditItem {entry} names={names} />
    {/each}
  </ul>
{/if}
{#if cursor}
  <div class="more">
    <button type="button" class="btn secondary" disabled={loading} onclick={() => load(false)}>{translate($lang, 'common.loadMore')}</button>
  </div>
{/if}

<style>
  .toolbar {
    margin-block-end: var(--space-4);
  }

  .toolbar-actions select {
    min-height: 44px;
    padding-inline: var(--space-2);
    font: inherit;
    color: var(--color-foreground);
    background: var(--color-surface);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
    max-inline-size: 100%;
  }

  .muted {
    color: var(--color-muted-foreground);
  }

  .more {
    display: flex;
    justify-content: center;
    margin-block-start: var(--space-4);
  }
</style>
