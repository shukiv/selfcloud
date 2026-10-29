<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { brand } from '../lib/brand';
  import { ApiError } from '../lib/apiError';
  import { lang, translate } from '../lib/i18n';
  import { fileErrorText } from '../lib/files/fileErrors';
  import { statOrNull } from '../lib/files/api';
  import type { AccessRequest, Node, Role } from '../lib/files/types';
  import { approveAccessRequest, denyAccessRequest, listAccessRequests } from '../lib/sharing/api';
  import { grantableRoles, shareCap } from '../lib/sharing/roles';
  import { refreshPendingRequests } from '../lib/sharing/requestCount';

  // Requests: the access requests the caller may decide, oldest first. Approving grants the role asked for, or a
  // role the caller chooses that is not above its own; denying closes the request. An item the caller cannot
  // see is listed without its name.
  interface Row {
    req: AccessRequest;
    node: Node | null;
    grant: Role;
  }

  let rows = $state<Row[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);

  onMount(() => {
    void load();
  });

  async function load() {
    loading = true;
    try {
      const { items } = await listAccessRequests();
      const out: Row[] = [];
      for (const req of items) {
        const node = await statOrNull(req.node_id);
        out.push({ req, node, grant: req.role });
      }
      rows = out;
      error = null;
      void refreshPendingRequests();
    } catch (err) {
      error = fileErrorText(err, $lang);
    } finally {
      loading = false;
    }
  }

  // capFor says the highest role the caller may grant on the item, from its role there.
  function capFor(node: Node | null): Role[] {
    if (node === null) return [];
    return grantableRoles(shareCap({ role: node.role, editorsCanShare: node.editors_can_share, managersOnly: false }));
  }

  // decisionError says what a refused decision means, by its code.
  function decisionError(err: unknown): string {
    if (err instanceof ApiError) {
      if (err.code === 'conflict') return translate($lang, 'requests.err.conflict');
      if (err.code === 'forbidden') return translate($lang, 'requests.err.forbidden');
    }
    return fileErrorText(err, $lang);
  }

  async function approve(row: Row) {
    error = null;
    notice = null;
    try {
      // A role the approver names is sent only when it differs from the one asked for.
      await approveAccessRequest(row.req.id, row.grant === row.req.role ? undefined : row.grant);
      notice = translate($lang, 'requests.approved', { name: row.req.user_name || row.req.user_id });
      rows = rows.filter((r) => r.req.id !== row.req.id);
      void refreshPendingRequests();
    } catch (err) {
      error = decisionError(err);
    }
  }

  async function deny(row: Row) {
    error = null;
    notice = null;
    try {
      await denyAccessRequest(row.req.id);
      notice = translate($lang, 'requests.denied', { name: row.req.user_name || row.req.user_id });
      rows = rows.filter((r) => r.req.id !== row.req.id);
      void refreshPendingRequests();
    } catch (err) {
      error = decisionError(err);
    }
  }
</script>

<svelte:head>
  <title>{translate($lang, 'requests.title')} · {$brand.name}</title>
</svelte:head>

<h1 class="page-title">{translate($lang, 'requests.title')}</h1>

{#if notice}
  <p class="alert success" role="status">{notice}</p>
{/if}
{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if loading}
  <p role="status">{translate($lang, 'app.loading')}</p>
{:else if rows.length === 0}
  <p class="empty" role="status">{translate($lang, 'requests.empty')}</p>
{:else}
  <ul class="requests" aria-label={translate($lang, 'requests.title')}>
    {#each rows as row (row.req.id)}
      {@const roles = capFor(row.node)}
      <li class="request">
        <div class="head">
          <span class="who"><bdi>{row.req.user_name || row.req.user_id}</bdi></span>
          <span class="asks">{translate($lang, 'requests.asks', { role: translate($lang, `role.${row.req.role}`) })}</span>
        </div>
        <p class="item">
          {#if row.node}
            <a href={`/n/${row.node.id}`}><bdi>{row.node.name}</bdi></a>
          {:else}
            <span class="muted">{translate($lang, 'requests.hiddenItem')}</span>
          {/if}
        </p>
        {#if row.req.message}
          <blockquote class="message"><bdi>{row.req.message}</bdi></blockquote>
        {/if}
        {#if roles.length > 0}
          <div class="decide">
            <label for={`grant-${row.req.id}`}>{translate($lang, 'requests.grant')}</label>
            <select id={`grant-${row.req.id}`} bind:value={row.grant}>
              {#each roles as r (r)}
                <option value={r}>{translate($lang, `role.${r}`)}</option>
              {/each}
            </select>
            <button type="button" class="btn primary" onclick={() => approve(row)}>{translate($lang, 'requests.approve')}</button>
            <button type="button" class="btn secondary" onclick={() => deny(row)}>{translate($lang, 'requests.deny')}</button>
          </div>
        {:else}
          <div class="decide">
            <button type="button" class="btn primary" onclick={() => approve(row)}>{translate($lang, 'requests.approve')}</button>
            <button type="button" class="btn secondary" onclick={() => deny(row)}>{translate($lang, 'requests.deny')}</button>
          </div>
        {/if}
      </li>
    {/each}
  </ul>
{/if}

<style>
  .page-title {
    margin: 0 0 var(--space-4);
    font-size: var(--text-xl);
    font-weight: 600;
  }

  .requests {
    display: grid;
    gap: var(--space-3);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .request {
    display: grid;
    gap: var(--space-2);
    padding: var(--space-4);
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
  }

  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-2) var(--space-3);
  }

  .who {
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .asks,
  .muted,
  .empty {
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }

  .item {
    margin: 0;
    overflow-wrap: anywhere;
  }

  .message {
    margin: 0;
    padding-inline-start: var(--space-3);
    border-inline-start: 3px solid var(--color-border);
    color: var(--color-muted-foreground);
  }

  .decide {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
  }

  .decide select {
    min-height: 44px;
    padding-inline: var(--space-2);
    font: inherit;
    color: var(--color-foreground);
    background: var(--color-surface);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
  }
</style>
