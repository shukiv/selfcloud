<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { brand } from '../../lib/brand';
  import { ApiError } from '../../lib/apiError';
  import { lang, translate } from '../../lib/i18n';
  import { go, location } from '../../lib/router';
  import { getAccount, updateAccount } from '../../lib/manage/api';
  import { failureText } from '../../lib/manage/failure';
  import { nextTab, TENANT_TABS, tabFrom, type TenantTab } from '../../lib/manage/admin';
  import type { Account } from '../../lib/manage/types';
  import Badge from '../../components/Badge.svelte';
  import Confirm from '../../components/Confirm.svelte';
  import DeleteTenant from '../../components/admin/DeleteTenant.svelte';
  import TenantOverview from '../../components/admin/TenantOverview.svelte';
  import TenantUsers from '../../components/admin/TenantUsers.svelte';
  import TenantKeys from '../../components/admin/TenantKeys.svelte';
  import TenantAudit from '../../components/admin/TenantAudit.svelte';

  // Tenant: one tenant, read by an installation admin. The tabs are in the address (?tab=), so a tab can be
  // bookmarked. The admin renames, suspends, resumes and deletes the tenant here; members are the owner's to manage.
  let { account: id }: { account: string } = $props();

  let tenant = $state<Account | null>(null);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let tab = $state<TenantTab>(tabFrom($location.search));
  let renaming = $state(false);
  let newName = $state('');
  let renameError = $state<string | null>(null);
  let suspending = $state(false);
  let suspendError = $state<string | null>(null);
  let deleting = $state(false);
  let tabEls: HTMLButtonElement[] = [];

  onMount(() => {
    void load();
  });

  async function load() {
    try {
      tenant = await getAccount(id);
      error = null;
    } catch (err) {
      tenant = null;
      error = err instanceof ApiError && err.status === 404 ? translate($lang, 'tenant.notFound') : failureText(err, $lang);
    }
  }

  function selectTab(t: TenantTab, focus = false) {
    tab = t;
    go(`/admin/tenants/${id}?tab=${t}`, { replace: true });
    if (focus) tabEls[TENANT_TABS.indexOf(t)]?.focus();
  }

  function tabKeys(e: KeyboardEvent) {
    const next = nextTab(tab, e.key);
    if (next === null) return;
    e.preventDefault();
    selectTab(next, true);
  }

  async function saveName() {
    if (!tenant || newName.trim() === '') return;
    renameError = null;
    try {
      tenant = await updateAccount(tenant.id, { name: newName.trim() });
      renaming = false;
      notice = translate($lang, 'tenant.renamed');
    } catch (err) {
      renameError = failureText(err, $lang);
    }
  }

  async function confirmSuspend() {
    if (!tenant) return;
    suspendError = null;
    const next = tenant.status === 'suspended' ? 'active' : 'suspended';
    try {
      tenant = await updateAccount(tenant.id, { status: next });
      suspending = false;
      notice = next === 'suspended' ? translate($lang, 'tenant.suspendedNotice') : translate($lang, 'tenant.resumedNotice');
    } catch (err) {
      suspendError = failureText(err, $lang);
    }
  }
</script>

<svelte:head>
  <title>{tenant?.name ?? translate($lang, 'admin.tenants.title')} · {$brand.name}</title>
</svelte:head>

{#if notice}
  <p class="alert success" role="status">{notice}</p>
{/if}

{#if error}
  <p class="alert error" role="alert">{error}</p>
  <p><a href="/admin/tenants">{translate($lang, 'tenant.back')}</a></p>
{:else if tenant}
  {@const t = tenant}
  <p class="crumb"><a href="/admin/tenants">{translate($lang, 'admin.tenants.title')}</a></p>
  <header class="head">
    {#if renaming}
      <form class="rename" onsubmit={(e) => { e.preventDefault(); void saveName(); }}>
        <label class="sr-only" for="tenant-name">{translate($lang, 'tenant.nameLabel')}</label>
        <input id="tenant-name" type="text" bind:value={newName} maxlength="200" data-autofocus />
        <button type="submit" class="btn primary small" disabled={newName.trim() === ''}>{translate($lang, 'tenant.save')}</button>
        <button type="button" class="btn secondary small" onclick={() => (renaming = false)}>{translate($lang, 'common.cancel')}</button>
        {#if renameError}
          <p class="alert error" role="alert">{renameError}</p>
        {/if}
      </form>
    {:else}
      <h1 class="page-title title"><bdi>{t.name}</bdi></h1>
      <button type="button" class="btn secondary small" onclick={() => { newName = t.name; renameError = null; renaming = true; }}>
        {translate($lang, 'tenant.rename')}
      </button>
    {/if}
    <div class="meta">
      <Badge tone={t.status === 'active' ? 'ok' : t.status === 'suspended' ? 'danger' : 'neutral'}>{translate($lang, `tenant.status.${t.status}`)}</Badge>
      {#if t.external_id}
        <span class="mono">{translate($lang, 'tenant.externalId')}: <bdi>{t.external_id}</bdi></span>
      {/if}
    </div>
    <div class="actions">
      <button type="button" class="btn secondary" onclick={() => { suspendError = null; suspending = true; }}>
        {t.status === 'suspended' ? translate($lang, 'tenant.resume') : translate($lang, 'tenant.suspend')}
      </button>
      <button type="button" class="btn secondary danger-text" onclick={() => (deleting = true)}>{translate($lang, 'tenant.delete.open')}</button>
    </div>
  </header>

  <div class="tabs" role="tablist" aria-label={translate($lang, 'tenant.tabs')}>
    {#each TENANT_TABS as t2, i (t2)}
      <button
        bind:this={tabEls[i]}
        type="button"
        role="tab"
        id="tab-{t2}"
        class="tab"
        aria-selected={tab === t2}
        aria-controls="panel-{t2}"
        tabindex={tab === t2 ? 0 : -1}
        onclick={() => selectTab(t2)}
        onkeydown={tabKeys}
      >
        {translate($lang, `tenant.tab.${t2}`)}
      </button>
    {/each}
  </div>

  <div role="tabpanel" id="panel-{tab}" aria-labelledby="tab-{tab}">
    {#if tab === 'overview'}
      <TenantOverview account={t} onchanged={(a) => { tenant = a; }} />
    {:else if tab === 'users'}
      <TenantUsers account={t.id} />
    {:else if tab === 'keys'}
      <TenantKeys account={t.id} />
    {:else}
      <TenantAudit account={t.id} />
    {/if}
  </div>

  {#if suspending}
    <Confirm
      id="suspend-tenant-page"
      title={t.status === 'suspended' ? translate($lang, 'tenant.resumeTitle', { name: t.name }) : translate($lang, 'tenant.suspendTitle', { name: t.name })}
      message={t.status === 'suspended' ? translate($lang, 'tenant.resumeMessage') : translate($lang, 'tenant.suspendMessage', { name: t.name })}
      action={t.status === 'suspended' ? translate($lang, 'tenant.resume') : translate($lang, 'tenant.suspend')}
      error={suspendError}
      onconfirm={confirmSuspend}
      onclose={() => (suspending = false)}
    />
  {/if}

  {#if deleting}
    <DeleteTenant id={t.id} name={t.name} onclose={() => (deleting = false)} ondeleted={() => go('/admin/tenants')} />
  {/if}
{:else}
  <p role="status">{translate($lang, 'app.loading')}</p>
{/if}

<style>
  .crumb {
    margin: 0 0 var(--space-2);
    font-size: var(--text-sm);
  }

  .head {
    display: grid;
    gap: var(--space-3);
    margin-block-end: var(--space-4);
  }

  .title {
    margin: 0;
    overflow-wrap: anywhere;
  }

  .rename {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    align-items: center;
  }

  .rename input {
    flex: 1 1 240px;
    min-height: 44px;
    padding-inline: var(--space-3);
    font: inherit;
    color: var(--color-foreground);
    background: var(--color-surface);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
  }

  .meta {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    align-items: center;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }

  .danger-text {
    color: var(--color-danger);
  }
</style>
