<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { lang, translate } from '../../lib/i18n';
  import { formatDate } from '../../lib/files/format';
  import { listKeys, revokeKey } from '../../lib/manage/api';
  import { failureText } from '../../lib/manage/failure';
  import type { ApiKey, NewKey } from '../../lib/manage/types';
  import Confirm from '../Confirm.svelte';
  import NewKeyDialog from './NewKey.svelte';
  import ShowOnce from './ShowOnce.svelte';

  // TenantKeys: the API keys of a tenant. A new key is shown once, in a dialog that holds it only while open.
  let { account }: { account: string } = $props();

  let keys = $state<ApiKey[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let creating = $state(false);
  let shown = $state<NewKey | null>(null);
  let revoking = $state<ApiKey | null>(null);
  let revokeError = $state<string | null>(null);

  onMount(() => {
    void load();
  });

  async function load() {
    loading = true;
    try {
      keys = (await listKeys(account)).items;
      error = null;
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      loading = false;
    }
  }

  // A key that is revoked or past its expiry works no more; the list shows which.
  function keyState(k: ApiKey): string {
    if (k.revoked_at) return translate($lang, 'keys.state.revoked');
    if (k.expires_at && new Date(k.expires_at).getTime() < Date.now()) return translate($lang, 'keys.state.expired');
    return translate($lang, 'keys.state.active');
  }

  async function confirmRevoke() {
    if (!revoking) return;
    revokeError = null;
    try {
      await revokeKey(account, revoking.id);
      notice = translate($lang, 'keys.revokedNotice', { name: revoking.name });
      revoking = null;
      await load();
    } catch (err) {
      revokeError = failureText(err, $lang);
    }
  }
</script>

<div class="toolbar">
  <h2 class="section-title">{translate($lang, 'tenant.keys.title')}</h2>
  <div class="toolbar-actions">
    <button type="button" class="btn primary" onclick={() => (creating = true)}>{translate($lang, 'keys.new.open')}</button>
  </div>
</div>

{#if notice}
  <p class="alert success" role="status">{notice}</p>
{/if}
{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if loading}
  <p role="status">{translate($lang, 'app.loading')}</p>
{:else if keys.length === 0}
  <p class="empty" role="status">{translate($lang, 'keys.empty')}</p>
{:else}
  <ul class="rows" aria-label={translate($lang, 'tenant.keys.title')}>
    {#each keys as k (k.id)}
      <li class="row">
        <div class="row-main">
          <p class="row-title"><bdi>{k.name}</bdi></p>
          <p class="mono">{k.prefix}…</p>
        </div>
        <div class="row-cell row-meta">
          {translate($lang, 'keys.created')} <time datetime={k.created_at}>{formatDate(k.created_at, $lang)}</time>
        </div>
        <div class="row-cell row-meta">
          {k.last_used_at ? translate($lang, 'keys.lastUsed') : translate($lang, 'keys.neverUsed')}
          {#if k.last_used_at}<time datetime={k.last_used_at}>{formatDate(k.last_used_at, $lang)}</time>{/if}
        </div>
        <div class="row-cell row-meta">
          {k.expires_at ? translate($lang, 'keys.expiresOn') : translate($lang, 'keys.noExpiry')}
          {#if k.expires_at}<time datetime={k.expires_at}>{formatDate(k.expires_at, $lang)}</time>{/if}
        </div>
        <div class="row-cell"><span class="pill">{keyState(k)}</span></div>
        {#if !k.revoked_at}
          <div class="row-actions">
            <button type="button" class="btn secondary small danger-text" onclick={() => { revokeError = null; revoking = k; }}>
              {translate($lang, 'keys.revoke')}
              <span class="sr-only">: {k.name}</span>
            </button>
          </div>
        {/if}
      </li>
    {/each}
  </ul>
{/if}

{#if creating}
  <NewKeyDialog
    {account}
    onclose={() => (creating = false)}
    oncreated={(k) => {
      creating = false;
      shown = k;
      void load();
    }}
  />
{/if}

{#if shown}
  {@const k = shown}
  <ShowOnce
    id="new-key-once"
    title={translate($lang, 'keys.shown.title', { name: k.name })}
    label={translate($lang, 'keys.shown.label')}
    value={k.key}
    warning={translate($lang, 'keys.shown.warning')}
    onclosed={() => (shown = null)}
  />
{/if}

{#if revoking}
  {@const k = revoking}
  <Confirm
    id="revoke-key"
    title={translate($lang, 'keys.revokeTitle', { name: k.name })}
    message={translate($lang, 'keys.revokeMessage')}
    action={translate($lang, 'keys.revoke')}
    danger
    error={revokeError}
    onconfirm={confirmRevoke}
    onclose={() => (revoking = null)}
  />
{/if}

<style>
  .toolbar {
    margin-block-end: var(--space-3);
  }

  .section-title {
    margin: 0;
    font-size: var(--text-lg);
    font-weight: 600;
  }

  .row-meta {
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }

  .pill {
    font-size: var(--text-xs);
    padding: 0 var(--space-2);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-full);
  }

  .danger-text {
    color: var(--color-danger);
  }
</style>
