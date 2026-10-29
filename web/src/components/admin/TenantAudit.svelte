<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { lang, translate } from '../../lib/i18n';
  import { auditLog } from '../../lib/manage/api';
  import { AUDIT_ACTIONS } from '../../lib/manage/actions';
  import { failureText } from '../../lib/manage/failure';
  import { loadNames, NO_NAMES } from '../../lib/manage/names';
  import type { AuditEntry, Names } from '../../lib/audit/sentence';
  import AuditItem from '../manage/AuditItem.svelte';

  // TenantAudit: the tenant's audit log, newest first, with a filter by action. The entries about a personal folder
  // are returned or withheld by the server exactly as it answers them.
  let { account }: { account: string } = $props();

  type Entry = AuditEntry & { id: string; time: string };
  let entries = $state<Entry[]>([]);
  let cursor = $state<string | undefined>(undefined);
  let action = $state('');
  let loading = $state(true);
  let error = $state<string | null>(null);
  let names = $state<Names>(NO_NAMES);

  onMount(() => {
    void loadNames(account).then((r) => (names = r.names)).catch(() => undefined);
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

<div class="toolbar">
  <h2 class="section-title">{translate($lang, 'tenant.audit.title')}</h2>
  <div class="toolbar-actions">
    <label for="tenant-audit-action">{translate($lang, 'audit.filter')}</label>
    <select id="tenant-audit-action" value={action} onchange={filterChanged}>
      <option value="">{translate($lang, 'audit.allActions')}</option>
      {#each AUDIT_ACTIONS as a (a)}
        <option value={a}>{a}</option>
      {/each}
    </select>
  </div>
</div>

{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if loading && entries.length === 0}
  <p role="status">{translate($lang, 'app.loading')}</p>
{:else if entries.length === 0}
  <p class="empty" role="status">{translate($lang, 'audit.empty')}</p>
{:else}
  <ul class="rows" aria-label={translate($lang, 'tenant.audit.title')}>
    {#each entries as entry (entry.id)}
      <AuditItem {entry} {names} />
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
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    margin-block-end: var(--space-3);
  }

  .section-title {
    margin: 0;
    font-size: var(--text-lg);
    font-weight: 600;
  }

  .toolbar select {
    min-height: 44px;
    padding-inline: var(--space-2);
    font: inherit;
    color: var(--color-foreground);
    background: var(--color-surface);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
    max-inline-size: 100%;
  }

  .more {
    display: flex;
    justify-content: center;
    margin-block-start: var(--space-4);
  }
</style>
