<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { lang, translate, translatePlural } from '../../lib/i18n';
  import { formatBytes } from '../../lib/files/format';
  import { deleteAccount, getJob, usage } from '../../lib/manage/api';
  import { failureText } from '../../lib/manage/failure';
  import { poll } from '../../lib/jobs/backoff';
  import { typedNameMatches } from '../../lib/manage/confirm';
  import type { Usage } from '../../lib/manage/types';
  import Dialog from '../Dialog.svelte';

  // DeleteTenant deletes a tenant for good. The dialog lists what is lost, asks for the name typed, and then polls
  // the deletion job until it ends. Leaving the page stops the polling. A poll that gives up, after about fifteen
  // minutes, says the deletion goes on in the background: it is not shown as failed.
  let {
    id,
    name,
    onclose,
    ondeleted,
  }: {
    id: string;
    name: string;
    onclose: () => void;
    ondeleted: () => void;
  } = $props();

  let stage = $state<'confirm' | 'deleting' | 'background' | 'failed'>('confirm');
  let typed = $state('');
  let figures = $state<Usage | null>(null);
  let error = $state<string | null>(null);
  let stopPolling: (() => void) | null = null;
  let ok = $derived(typedNameMatches(typed, name));

  onMount(() => {
    usage(id)
      .then((u) => (figures = u))
      .catch(() => (figures = null));
  });

  onDestroy(() => stopPolling?.());

  async function start() {
    if (!ok || stage === 'deleting') return;
    stage = 'deleting';
    error = null;
    try {
      const job = await deleteAccount(id);
      stopPolling = poll(
        () => getJob(job.id),
        (j) => j.status === 'succeeded' || j.status === 'failed',
        (j) => {
          stopPolling = null;
          if (j.status === 'succeeded') {
            ondeleted();
          } else {
            stage = 'failed';
            error = translate($lang, 'tenant.delete.jobFailed');
          }
        },
        (err) => {
          stopPolling = null;
          stage = 'failed';
          error = failureText(err, $lang);
        },
        () => {
          stopPolling = null;
          stage = 'background';
        },
      );
    } catch (err) {
      stage = 'failed';
      error = failureText(err, $lang);
    }
  }

  // The dialog closes by its buttons only while the deletion is not running: it cannot be undone once started.
  function close() {
    if (stage === 'deleting') return;
    onclose();
  }
</script>

<Dialog title={translate($lang, 'tenant.delete.title', { name })} id="delete-tenant" onclose={close}>
  <div class="delete">
    {#if stage === 'deleting'}
      <p role="status">{translate($lang, 'tenant.delete.running')}</p>
    {:else if stage === 'background'}
      <p role="status">{translate($lang, 'tenant.delete.background')}</p>
    {:else}
      <p>{translate($lang, 'tenant.delete.lost')}</p>
      {#if figures}
        <ul class="figures">
          <li>{translatePlural($lang, 'tenant.delete.people', figures.users_active + figures.users_pending)}</li>
          <li>{translatePlural($lang, 'tenant.delete.files', figures.files)}</li>
          <li>{translate($lang, 'tenant.delete.size', { size: formatBytes(figures.used_bytes, $lang) })}</li>
        </ul>
      {/if}
      <label class="field-label" for="delete-tenant-name">{translate($lang, 'confirm.typeName', { name })}</label>
      <input id="delete-tenant-name" type="text" autocomplete="off" bind:value={typed} data-autofocus />
    {/if}
    {#if error}
      <p class="alert error" role="alert">{error}</p>
    {/if}
    <div class="foot">
      {#if stage === 'background'}
        <button type="button" class="btn secondary" onclick={close}>{translate($lang, 'common.close')}</button>
      {:else if stage !== 'deleting'}
        <button type="button" class="btn secondary" onclick={close}>{translate($lang, 'common.cancel')}</button>
        <button type="button" class="btn danger-fill" disabled={!ok} onclick={start}>
          {translate($lang, 'tenant.delete.action', { name })}
        </button>
      {/if}
    </div>
  </div>
</Dialog>

<style>
  .delete {
    display: grid;
    gap: var(--space-3);
  }

  .figures {
    margin: 0;
    padding-inline-start: var(--space-4);
  }

  .foot {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    justify-content: flex-end;
  }
</style>
