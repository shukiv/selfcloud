<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { lang, translate, translatePlural } from '../../lib/i18n';
  import { formatBytes, formatDate, formatNumber } from '../../lib/files/format';
  import { listSpaces, updateAccount, usage } from '../../lib/manage/api';
  import { collectAll } from '../../lib/manage/paging';
  import { failureText } from '../../lib/manage/failure';
  import { limitBytes, splitLimit, type LimitUnit } from '../../lib/quota';
  import type { Account, Usage } from '../../lib/manage/types';
  import UsageMeter from '../UsageMeter.svelte';
  import PublicLinksSwitch from '../share/PublicLinksSwitch.svelte';
  import { accountStorage, type AccountStorage } from '../../lib/storage/api';

  // TenantOverview: the tenant's usage and counts, and its storage limit, which an admin changes here.
  let {
    account,
    onchanged,
  }: {
    account: Account;
    onchanged: (a: Account) => void;
  } = $props();

  let figures = $state<Usage | null>(null);
  // storage is the account's totals and its default limit per person; an admin gets the account block only.
  let storage = $state<AccountStorage | null>(null);
  let spaceCount = $state(0);
  let error = $state<string | null>(null);
  let editing = $state(false);
  let noLimit = $state(false);
  let amount = $state('');
  let unit = $state<LimitUnit>('GB');
  let busy = $state(false);

  onMount(() => {
    void load();
  });

  async function load() {
    try {
      const [u, spaces, st] = await Promise.all([
        usage(account.id),
        collectAll((c) => listSpaces(account.id, c)),
        accountStorage(account.id),
      ]);
      figures = u;
      spaceCount = spaces.items.length;
      storage = st;
    } catch (err) {
      error = failureText(err, $lang);
    }
  }

  function startEdit() {
    error = null;
    noLimit = account.quota_bytes === 0;
    if (!noLimit) {
      const s = splitLimit(account.quota_bytes);
      amount = s.amount;
      unit = s.unit;
    }
    editing = true;
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    let quota = 0;
    if (!noLimit) {
      const bytes = limitBytes(amount, unit);
      if (bytes === null) {
        error = translate($lang, 'tenant.new.limitInvalid');
        return;
      }
      quota = bytes;
    }
    busy = true;
    error = null;
    try {
      const updated = await updateAccount(account.id, { quota_bytes: quota });
      editing = false;
      onchanged(updated);
      void load();
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      busy = false;
    }
  }
</script>

{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if figures}
  <section class="panel-plain" aria-labelledby="tenant-usage">
    <h2 id="tenant-usage">{translate($lang, 'tenant.overview.usage')}</h2>
    <UsageMeter used={figures.used_bytes} quota={figures.quota_bytes} />
    {#if storage}
      <p class="hint">{translate($lang, 'storage.reservedLine', { size: formatBytes(storage.account.reserved_bytes, $lang) })}</p>
      <p class="hint">
        {storage.account.user_quota_bytes === 0
          ? translate($lang, 'storage.defaultNone')
          : translate($lang, 'storage.defaultLine', { size: formatBytes(storage.account.user_quota_bytes, $lang) })}
      </p>
    {/if}

    <div class="limit-line">
      <p>
        {#if figures.quota_bytes === null}
          {translate($lang, 'tenant.overview.noLimit')}
        {:else}
          {translate($lang, 'tenant.overview.limit', { size: formatBytes(figures.quota_bytes, $lang) })}
        {/if}
      </p>
      {#if !editing}
        <button type="button" class="btn secondary small" onclick={startEdit}>{translate($lang, 'tenant.overview.change')}</button>
      {/if}
    </div>

    {#if editing}
      <form class="limit-form" onsubmit={save} novalidate>
        <div class="limit-row">
          <input type="text" inputmode="decimal" autocomplete="off" bind:value={amount} disabled={noLimit} aria-label={translate($lang, 'tenant.new.limitAmount')} />
          <select bind:value={unit} disabled={noLimit} aria-label={translate($lang, 'tenant.new.limitUnit')}>
            <option value="GB">GB</option>
            <option value="TB">TB</option>
          </select>
        </div>
        <label class="check"><input type="checkbox" bind:checked={noLimit} /> <span>{translate($lang, 'tenant.new.noLimit')}</span></label>
        <p class="hint">{translate($lang, 'tenant.overview.lowerHint')}</p>
        <div class="foot">
          <button type="button" class="btn secondary" onclick={() => (editing = false)}>{translate($lang, 'common.cancel')}</button>
          <button type="submit" class="btn primary" disabled={busy}>{translate($lang, 'tenant.overview.save')}</button>
        </div>
      </form>
    {/if}
  </section>

  <dl class="stat-grid">
    <div class="stat">
      <dt>{translate($lang, 'tenant.overview.people')}</dt>
      <dd>{formatNumber(figures.users_active, $lang)}</dd>
      <dd class="muted">{translatePlural($lang, 'tenant.overview.pending', figures.users_pending)}</dd>
    </div>
    <div class="stat">
      <dt>{translate($lang, 'tenant.overview.spaces')}</dt>
      <dd>{formatNumber(spaceCount, $lang)}</dd>
    </div>
    <div class="stat">
      <dt>{translate($lang, 'tenant.overview.files')}</dt>
      <dd>{formatNumber(figures.files, $lang)}</dd>
    </div>
  </dl>
{/if}

<PublicLinksSwitch account={account.id} />

<dl class="dates">
  <div><dt>{translate($lang, 'tenant.overview.created')}</dt><dd><time datetime={account.created_at}>{formatDate(account.created_at, $lang)}</time></dd></div>
  <div><dt>{translate($lang, 'tenant.overview.updated')}</dt><dd><time datetime={account.updated_at}>{formatDate(account.updated_at, $lang)}</time></dd></div>
</dl>

<style>
  .limit-line {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
  }

  .limit-line p {
    margin: 0;
  }

  .limit-form {
    display: grid;
    gap: var(--space-3);
  }

  .limit-row {
    display: flex;
    gap: var(--space-2);
  }

  .limit-row input {
    flex: 1 1 auto;
    min-width: 0;
    min-height: 44px;
    padding-inline: var(--space-3);
    font: inherit;
    color: var(--color-foreground);
    background: var(--color-surface);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
  }

  .limit-row select {
    min-height: 44px;
    font: inherit;
    color: var(--color-foreground);
    background: var(--color-surface);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
  }

  .check {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-height: 44px;
  }

  .hint,
  .muted {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }

  .foot {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    justify-content: flex-end;
  }

  .dates {
    display: grid;
    gap: var(--space-2);
    margin: var(--space-4) 0 0;
  }

  .dates > div {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: var(--space-2);
  }

  .dates dt {
    color: var(--color-muted-foreground);
  }

  .dates dd {
    margin: 0;
  }
</style>
