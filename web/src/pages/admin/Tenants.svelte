<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { brand } from '../../lib/brand';
  import { lang, translate } from '../../lib/i18n';
  import { formatDate } from '../../lib/files/format';
  import { location, go } from '../../lib/router';
  import { debounce, SEARCH_DELAY } from '../../lib/sharing/combobox';
  import { listAccounts, updateAccount, usage } from '../../lib/manage/api';
  import { failureText } from '../../lib/manage/failure';
  import { tenantQuery, tenantSearch, type TenantStatus } from '../../lib/manage/admin';
  import type { Account, PersonReply, Usage } from '../../lib/manage/types';
  import Badge from '../../components/Badge.svelte';
  import UsageMeter from '../../components/UsageMeter.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import DeleteTenant from '../../components/admin/DeleteTenant.svelte';
  import NewTenant from '../../components/admin/NewTenant.svelte';
  import ShowOnce from '../../components/admin/ShowOnce.svelte';

  // Tenants: every account on this installation. The search text and the status filter are in the address, so a
  // search can be bookmarked. Names are searched on the server; the list is read a page at a time.
  let accounts = $state<Account[]>([]);
  let usages = $state<Record<string, Usage>>({});
  let cursor = $state<string | undefined>(undefined);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let query = $state(tenantQuery($location.search).q);
  let status = $state<TenantStatus>(tenantQuery($location.search).status);
  let creating = $state(false);
  let suspending = $state<Account | null>(null);
  let suspendError = $state<string | null>(null);
  let deleting = $state<Account | null>(null);
  let menuFor = $state<string | null>(null);
  let secret = $state<{ url: string; name: string } | null>(null);
  let loadSeq = 0;

  const search = debounce((text: string) => {
    query = text;
    syncAddress();
    void load(true);
  }, SEARCH_DELAY);

  onDestroy(() => search.cancel());

  onMount(() => {
    void load(true);
  });

  // syncAddress writes the search and the filter into the address, so the list can be bookmarked.
  function syncAddress() {
    const next = `/admin/tenants${tenantSearch(query, status)}`;
    if (`${window.location.pathname}${window.location.search}` !== next) go(next, { replace: true });
  }

  async function load(reset: boolean) {
    const seq = ++loadSeq;
    loading = true;
    try {
      const page = await listAccounts({ q: query || undefined, status: status === 'all' ? undefined : status, cursor: reset ? undefined : cursor });
      if (seq !== loadSeq) return;
      accounts = reset ? page.items : [...accounts, ...page.items];
      cursor = page.next_cursor;
      error = null;
      // Each row's usage is read as the row appears. A row whose usage cannot be read shows no meter.
      for (const a of page.items) {
        usage(a.id)
          .then((u) => (usages = { ...usages, [a.id]: u }))
          .catch(() => undefined);
      }
    } catch (err) {
      if (seq === loadSeq) error = failureText(err, $lang);
    } finally {
      if (seq === loadSeq) loading = false;
    }
  }

  function chooseStatus(s: TenantStatus) {
    status = s;
    syncAddress();
    void load(true);
  }

  async function confirmSuspend() {
    if (!suspending) return;
    suspendError = null;
    const next = suspending.status === 'suspended' ? 'active' : 'suspended';
    try {
      const updated = await updateAccount(suspending.id, { status: next });
      accounts = accounts.map((a) => (a.id === updated.id ? { ...a, ...updated } : a));
      notice = next === 'suspended' ? translate($lang, 'tenants.suspendedNotice', { name: updated.name }) : translate($lang, 'tenants.resumedNotice', { name: updated.name });
      suspending = null;
    } catch (err) {
      suspendError = failureText(err, $lang);
    }
  }

  function created(account: Account, owner: PersonReply | undefined) {
    creating = false;
    if (owner?.verification?.url) {
      secret = { url: owner.verification.url, name: account.name };
      return;
    }
    go(`/admin/tenants/${account.id}`);
  }

  function deleted(name: string) {
    deleting = null;
    notice = translate($lang, 'tenants.deletedNotice', { name });
    void load(true);
  }
</script>

<svelte:head>
  <title>{translate($lang, 'admin.tenants.title')} · {$brand.name}</title>
</svelte:head>

<svelte:window onclick={() => (menuFor = null)} />

<div class="toolbar">
  <h1 class="page-title">{translate($lang, 'admin.tenants.title')}</h1>
  <div class="toolbar-actions">
    <button type="button" class="btn primary" onclick={() => (creating = true)}>{translate($lang, 'tenant.new.open')}</button>
  </div>
</div>

<div class="filters">
  <label class="search-label" for="tenant-search">{translate($lang, 'tenants.search')}</label>
  <input
    id="tenant-search"
    type="search"
    autocomplete="off"
    value={query}
    oninput={(e) => search.call((e.currentTarget as HTMLInputElement).value)}
    placeholder={translate($lang, 'tenants.searchHint')}
  />
  <ul class="chips" aria-label={translate($lang, 'tenants.filter')}>
    <li><button type="button" class="chip" aria-pressed={status === 'all'} onclick={() => chooseStatus('all')}>{translate($lang, 'tenants.all')}</button></li>
    <li><button type="button" class="chip" aria-pressed={status === 'active'} onclick={() => chooseStatus('active')}>{translate($lang, 'tenants.active')}</button></li>
    <li><button type="button" class="chip" aria-pressed={status === 'suspended'} onclick={() => chooseStatus('suspended')}>{translate($lang, 'tenants.suspended')}</button></li>
  </ul>
</div>

{#if notice}
  <p class="alert success" role="status">{notice}</p>
{/if}
{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if loading && accounts.length === 0}
  <p role="status">{translate($lang, 'app.loading')}</p>
{:else if accounts.length === 0}
  <p class="empty" role="status">
    {query || status !== 'all' ? translate($lang, 'tenants.noMatch') : translate($lang, 'tenants.empty')}
  </p>
{:else}
  <ul class="rows" aria-label={translate($lang, 'admin.tenants.title')}>
    {#each accounts as a (a.id)}
      <li class="row tenant-row">
        <div class="row-main">
          <p class="row-title"><a href={`/admin/tenants/${a.id}`}><bdi>{a.name}</bdi></a></p>
          {#if a.external_id}
            <p class="mono"><bdi>{a.external_id}</bdi></p>
          {/if}
        </div>
        <div class="row-cell"><Badge tone={a.status === 'active' ? 'ok' : a.status === 'suspended' ? 'danger' : 'neutral'}>{translate($lang, `tenant.status.${a.status}`)}</Badge></div>
        <div class="row-usage">
          {#if usages[a.id]}
            <UsageMeter used={usages[a.id].used_bytes} quota={usages[a.id].quota_bytes} />
          {:else}
            <span class="muted">{translate($lang, 'usage.loading')}</span>
          {/if}
        </div>
        <div class="row-cell row-meta"><time datetime={a.created_at} title={formatDate(a.created_at, $lang)}>{formatDate(a.created_at, $lang)}</time></div>
        <div class="menu-wrap">
          <button
            type="button"
            class="btn secondary small"
            aria-haspopup="menu"
            aria-expanded={menuFor === a.id}
            aria-label={translate($lang, 'tenants.actionsFor', { name: a.name })}
            onclick={(e) => { e.stopPropagation(); menuFor = menuFor === a.id ? null : a.id; }}
          >
            …
          </button>
          {#if menuFor === a.id}
            <div class="menu tenant-menu" role="menu">
              <a class="menu-item" role="menuitem" href={`/admin/tenants/${a.id}`}>{translate($lang, 'tenants.open')}</a>
              <button type="button" role="menuitem" class="menu-item" onclick={() => { menuFor = null; suspendError = null; suspending = a; }}>
                {a.status === 'suspended' ? translate($lang, 'tenant.resume') : translate($lang, 'tenant.suspend')}
              </button>
              <button type="button" role="menuitem" class="menu-item danger" onclick={() => { menuFor = null; deleting = a; }}>
                {translate($lang, 'tenant.delete.open')}
              </button>
            </div>
          {/if}
        </div>
      </li>
    {/each}
  </ul>
  {#if cursor}
    <div class="more">
      <button type="button" class="btn secondary" disabled={loading} onclick={() => load(false)}>{translate($lang, 'common.loadMore')}</button>
    </div>
  {/if}
{/if}

{#if creating}
  <NewTenant onclose={() => (creating = false)} oncreated={created} />
{/if}

{#if secret}
  {@const s = secret}
  <ShowOnce
    id="tenant-link"
    title={translate($lang, 'tenant.new.linkTitle', { name: s.name })}
    label={translate($lang, 'tenant.new.linkLabel')}
    value={s.url}
    warning={translate($lang, 'tenant.new.linkWarning')}
    onclosed={() => { secret = null; }}
  />
{/if}

{#if suspending}
  {@const s = suspending}
  <Confirm
    id="suspend-tenant"
    title={s.status === 'suspended' ? translate($lang, 'tenant.resumeTitle', { name: s.name }) : translate($lang, 'tenant.suspendTitle', { name: s.name })}
    message={s.status === 'suspended' ? translate($lang, 'tenant.resumeMessage') : translate($lang, 'tenant.suspendMessage', { name: s.name })}
    action={s.status === 'suspended' ? translate($lang, 'tenant.resume') : translate($lang, 'tenant.suspend')}
    error={suspendError}
    onconfirm={confirmSuspend}
    onclose={() => (suspending = null)}
  />
{/if}

{#if deleting}
  <DeleteTenant id={deleting.id} name={deleting.name} onclose={() => (deleting = null)} ondeleted={() => deleted(deleting?.name ?? '')} />
{/if}

<style>
  .toolbar {
    margin-block-end: var(--space-4);
  }

  .filters {
    display: grid;
    gap: var(--space-2);
    margin-block-end: var(--space-4);
  }

  .search-label {
    font-weight: 500;
  }

  .filters input[type='search'] {
    min-height: 44px;
    padding-inline: var(--space-3);
    font: inherit;
    color: var(--color-foreground);
    background: var(--color-surface);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
  }

  .chips {
    margin-block-end: 0;
  }

  .tenant-row {
    align-items: center;
  }

  .row-usage {
    flex: 2 1 220px;
    min-width: 180px;
  }

  .row-meta {
    margin: 0;
  }

  .muted {
    color: var(--color-muted-foreground);
    font-size: var(--text-sm);
  }

  .menu-wrap {
    position: relative;
  }

  .tenant-menu {
    inset-block-start: 100%;
    inset-inline-end: 0;
    min-width: 200px;
  }

  .menu-item.danger {
    color: var(--color-danger);
  }

  .more {
    display: flex;
    justify-content: center;
    margin-block-start: var(--space-4);
  }
</style>
