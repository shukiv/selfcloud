<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { lang, tr } from '../../lib/i18n';
  import { ApiError } from '../../lib/apiError';
  import { deleteVersion, listVersions, restoreVersion, statNode, updateVersion } from '../../lib/files/api';
  import { fileErrorText } from '../../lib/files/fileErrors';
  import { formatBytes, formatDate } from '../../lib/files/format';
  import { startDownloadURL } from '../../lib/files/ops';
  import { can, downloadAllowed } from '../../lib/files/roles';
  import type { Node, Version } from '../../lib/files/types';
  import { contentPath } from '../../lib/files/api';
  import Dialog from '../Dialog.svelte';

  // Versions lists a file's earlier contents, newest first, and lets the person name, keep, restore and
  // delete them, as their role allows.
  let { node, onclose, onchanged }: { node: Node; onclose: () => void; onchanged: () => void } = $props();

  let items = $state<Version[]>([]);
  let loading = $state(true);
  let message = $state<string | null>(null);
  let error = $state<string | null>(null);
  let labels = $state<Record<string, string>>({});

  async function load() {
    loading = true;
    try {
      const page = await listVersions(node.id);
      items = page.items;
      labels = Object.fromEntries(page.items.map((v) => [v.id, v.label ?? '']));
      error = null;
    } catch (err) {
      error = fileErrorText(err, $lang);
    } finally {
      loading = false;
    }
  }

  onMount(() => void load());

  // The newest version ID must come from the server; only the current content has the ID "current".
  async function restore(v: Version) {
    message = null;
    error = null;
    try {
      const fresh = await statNode(node.id);
      await restoreVersion(node.id, v.id, fresh.etag ?? '');
      message = $tr('files.versions.restored');
      onchanged();
      await load();
    } catch (err) {
      error = fileErrorText(err, $lang);
    }
  }

  async function keep(v: Version, on: boolean) {
    error = null;
    try {
      await updateVersion(node.id, v.id, { keep_forever: on });
      await load();
    } catch (err) {
      if (err instanceof ApiError && err.code === 'conflict' && typeof err.details.max_kept === 'number') {
        error = $tr('files.versions.cap', { max: err.details.max_kept });
      } else {
        error = fileErrorText(err, $lang);
      }
    }
  }

  async function saveLabel(v: Version) {
    error = null;
    try {
      const value = (labels[v.id] ?? '').trim();
      await updateVersion(node.id, v.id, { label: value === '' ? null : value });
      message = $tr('files.versions.labelSaved');
      await load();
    } catch (err) {
      error = fileErrorText(err, $lang);
    }
  }

  async function remove(v: Version) {
    error = null;
    try {
      await deleteVersion(node.id, v.id);
      message = $tr('files.versions.deleted');
      await load();
    } catch (err) {
      error = fileErrorText(err, $lang);
    }
  }

  async function download(v: Version) {
    const url = v.current ? contentPath(node.id) : `/api/v1/nodes/${node.id}/versions/${v.id}/content?disposition=attachment`;
    const ok = await startDownloadURL(url);
    if (!ok) error = $tr('files.err.downloadRestricted');
  }
</script>

<Dialog title={$tr('files.versions.title')} id="versions" {onclose} wide>
  <p class="muted">{node.name}</p>
  {#if error}
    <p class="alert error" role="alert">{error}</p>
  {/if}
  {#if message}
    <p class="alert success" role="status">{message}</p>
  {/if}
  {#if loading}
    <p role="status">{$tr('app.loading')}</p>
  {:else if items.length <= 1}
    <p class="muted">{$tr('files.versions.none')}</p>
  {/if}
  <ul class="versions">
    {#each items as v (v.id)}
      <li class="version">
        <div class="meta">
          <strong>{v.current ? $tr('files.versions.current') : formatDate(v.created_at, $lang)}</strong>
          <span class="muted num">{formatBytes(v.size, $lang)}</span>
          {#if !v.current && v.replaced_at}
            <span class="muted">{$tr('files.versions.replaced', { date: formatDate(v.replaced_at, $lang) })}</span>
          {/if}
        </div>
        <div class="actions">
          {#if downloadAllowed(node)}
            <button type="button" class="btn secondary" onclick={() => download(v)}>{$tr('files.versions.download')}</button>
          {/if}
          {#if !v.current && can.restoreVersion(node.role)}
            <button type="button" class="btn secondary" onclick={() => restore(v)}>{$tr('files.versions.restore')}</button>
          {/if}
          {#if can.editVersion(node.role) && !v.current}
            <label class="keep">
              <input type="checkbox" checked={v.keep_forever} onchange={(e) => keep(v, (e.currentTarget as HTMLInputElement).checked)} />
              {$tr('files.versions.keep')}
            </label>
            <button type="button" class="btn secondary danger" onclick={() => remove(v)}>{$tr('files.versions.delete')}</button>
          {/if}
        </div>
        {#if can.editVersion(node.role)}
          <form class="label-form" onsubmit={(e) => { e.preventDefault(); void saveLabel(v); }}>
            <label class="sr-only" for="label-{v.id}">{$tr('files.versions.label')}</label>
            <input id="label-{v.id}" type="text" maxlength="100" bind:value={labels[v.id]} />
            <button type="submit" class="btn secondary">{$tr('files.versions.labelSave')}</button>
          </form>
        {:else if v.label}
          <p class="muted">{v.label}</p>
        {/if}
      </li>
    {/each}
  </ul>
  <div class="foot">
    <button type="button" class="btn secondary" onclick={onclose}>{$tr('common.close')}</button>
  </div>
</Dialog>

<style>
  .versions {
    display: grid;
    gap: var(--space-3);
    margin: 0 0 var(--space-4);
    padding: 0;
    list-style: none;
  }

  .version {
    display: grid;
    gap: var(--space-2);
    padding: var(--space-3);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
  }

  .meta,
  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2) var(--space-3);
  }

  .keep {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    min-height: 44px;
  }

  .label-form {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }

  .label-form input {
    flex: 1 1 200px;
  }

  .danger {
    color: var(--color-danger);
  }

  .foot {
    display: flex;
    justify-content: flex-end;
  }

  .muted {
    color: var(--color-muted-foreground);
  }
</style>
