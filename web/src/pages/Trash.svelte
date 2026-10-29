<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { lang, tr, translatePlural } from '../lib/i18n';
  import { folders, loadFolders } from '../lib/files/folders';
  import { emptyTrashStep, getMe, listTrash, purgeTrashed, restoreNode, statOrNull } from '../lib/files/api';
  import { fileErrorText } from '../lib/files/fileErrors';
  import { daysUntil, formatDate } from '../lib/files/format';
  import { atLeast } from '../lib/files/roles';
  import { kindOf } from '../lib/files/kind';
  import type { TrashItem } from '../lib/files/types';
  import { session } from '../lib/session';
  import Icon from '../components/files/Icon.svelte';
  import Dialog from '../components/Dialog.svelte';

  // The trash: items deleted but not yet gone for good. Restore brings one back; deleting for good cannot be
  // undone, so it asks first.
  let items = $state<TrashItem[]>([]);
  let cursor = $state<string | undefined>(undefined);
  let folder = $state<string>('');
  let loading = $state(true);
  let notice = $state<string | null>(null);
  let error = $state<string | null>(null);
  let confirm = $state<null | { kind: 'purge'; item: TrashItem } | { kind: 'empty' }>(null);
  let emptying = $state<{ purged: number } | null>(null);
  let places = $state<Record<string, string>>({});
  let personalID = $state<string | null>(null);

  onMount(() => {
    void (async () => {
      await loadFolders().catch(() => undefined);
      personalID = (await getMe().catch(() => null))?.personal_folder_id ?? null;
      await load(true);
    })();
  });

  async function load(reset: boolean) {
    loading = true;
    try {
      const page = await listTrash(folder || null, reset ? undefined : cursor);
      items = reset ? page.items : [...items, ...page.items];
      cursor = page.next_cursor;
      error = null;
      void locate(page.items);
    } catch (err) {
      error = fileErrorText(err, $lang);
    } finally {
      loading = false;
    }
  }

  // locate names where each item was, for as far as the caller can reach; the rest says so.
  async function locate(list: TrashItem[]) {
    for (const it of list) {
      if (places[it.original_parent_id] !== undefined) continue;
      const parent = await statOrNull(it.original_parent_id);
      places = { ...places, [it.original_parent_id]: parent ? (parent.parent_id === null ? '' : parent.name) : '' };
    }
  }

  function placeOf(it: TrashItem): string {
    const name = places[it.original_parent_id];
    if (name === undefined) return '';
    if (name !== '') return name;
    const f = $folders.find((x) => x.id === it.folder_id);
    return f ? f.name : $tr('trash.place.unknown');
  }

  function who(id: string): string {
    return id === $session?.user.id ? $tr('files.details.you') : id;
  }

  async function restore(it: TrashItem) {
    notice = null;
    error = null;
    try {
      const back = await restoreNode(it.id);
      items = items.filter((x) => x.id !== it.id);
      notice = back.name !== it.name
        ? $tr('trash.restoredAs', { name: back.name })
        : $tr('trash.restored', { name: it.name });
    } catch (err) {
      error = fileErrorText(err, $lang);
    }
  }

  async function purge(it: TrashItem) {
    confirm = null;
    try {
      await purgeTrashed(it.id);
      items = items.filter((x) => x.id !== it.id);
      notice = $tr('trash.purged', { name: it.name });
    } catch (err) {
      error = fileErrorText(err, $lang);
    }
  }

  // emptyTrash purges in steps of up to 1000 items, as long as the server says more remain.
  async function emptyTrash() {
    confirm = null;
    emptying = { purged: 0 };
    error = null;
    try {
      for (;;) {
        const step = await emptyTrashStep(folder || null);
        emptying = { purged: (emptying?.purged ?? 0) + step.purged };
        if (!step.more) break;
      }
      notice = $tr('trash.emptied');
      await load(true);
    } catch (err) {
      error = fileErrorText(err, $lang);
    } finally {
      emptying = null;
    }
  }

  function days(it: TrashItem): string {
    return translatePlural($lang, 'trash.purge', daysUntil(it.purge_at));
  }
</script>

<svelte:head>
  <title>{$tr('trash.title')} · {$tr('nav.trash')}</title>
</svelte:head>

<h1 class="page-title">{$tr('trash.title')}</h1>

<div class="toolbar">
  <label class="field-inline">
    <span>{$tr('trash.space')}</span>
    <select bind:value={folder} onchange={() => load(true)}>
      <option value="">{$tr('trash.allSpaces')}</option>
      {#each $folders as f (f.id)}
        <option value={f.id}>{f.kind === 'personal' || f.id === personalID ? $tr('nav.files') : f.name}</option>
      {/each}
    </select>
  </label>
  {#if items.some((i) => i.role === 'manager')}
    <button type="button" class="btn secondary danger" onclick={() => (confirm = { kind: 'empty' })} disabled={emptying !== null}>
      {$tr('trash.empty')}
    </button>
  {/if}
</div>

{#if emptying}
  <p role="status" class="alert">{$tr('trash.emptying', { count: emptying.purged })}</p>
{/if}
{#if notice}
  <p role="status" class="alert success">{notice}</p>
{/if}
{#if error}
  <p role="alert" class="alert error">{error}</p>
{/if}

{#if loading && items.length === 0}
  <p role="status">{$tr('app.loading')}</p>
{:else if items.length === 0}
  <p class="empty" role="status">{$tr('trash.empty.none')}</p>
{:else}
  <ul class="trash-list" aria-label={$tr('trash.title')}>
    {#each items as it (it.id)}
      <li class="trash-item">
        <Icon kind={kindOf(it)} size={28} />
        <div class="info">
          <p class="name"><bdi>{it.name}</bdi></p>
          <p class="muted">
            {#if placeOf(it)}{$tr('trash.was', { place: placeOf(it) })} · {/if}
            {$tr('trash.trashedOn', { date: formatDate(it.trashed_at, $lang) })}
            · {$tr('trash.by', { who: who(it.trashed_by) })}
          </p>
          <p class="muted">{days(it)}</p>
        </div>
        <div class="actions">
          {#if it.role && atLeast(it.role, 'editor')}
            <button type="button" class="btn secondary" onclick={() => restore(it)}>{$tr('trash.restore')}</button>
          {/if}
          {#if it.role === 'manager'}
            <button type="button" class="btn secondary danger" onclick={() => (confirm = { kind: 'purge', item: it })}>{$tr('trash.purgeNow')}</button>
          {/if}
        </div>
      </li>
    {/each}
  </ul>
  {#if cursor}
    <div class="more">
      <button type="button" class="btn secondary" onclick={() => load(false)} disabled={loading}>{$tr('files.loadMore')}</button>
    </div>
  {/if}
{/if}

{#if confirm}
  <Dialog
    title={confirm.kind === 'empty' ? $tr('trash.empty.title') : $tr('trash.purge.title')}
    id="purge"
    onclose={() => (confirm = null)}
  >
    <p>{confirm.kind === 'empty' ? $tr('trash.empty.body') : $tr('trash.purge.body')}</p>
    <div class="foot">
      <button type="button" class="btn secondary" onclick={() => (confirm = null)}>{$tr('common.cancel')}</button>
      <button
        type="button"
        class="btn primary danger-fill"
        data-autofocus
        onclick={() => (confirm?.kind === 'empty' ? emptyTrash() : confirm?.kind === 'purge' ? purge(confirm.item) : null)}
      >
        {confirm.kind === 'empty' ? $tr('trash.empty.submit') : $tr('trash.purge.submit')}
      </button>
    </div>
  </Dialog>
{/if}

<style>
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    margin-block-end: var(--space-4);
  }

  .field-inline {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .field-inline select {
    min-height: 44px;
    padding-inline: var(--space-2);
    font: inherit;
    color: var(--color-foreground);
    background: var(--color-surface);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
  }

  .trash-list {
    display: grid;
    gap: var(--space-2);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .trash-item {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-3);
    padding: var(--space-3);
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
  }

  .info {
    flex: 1 1 200px;
    min-width: 0;
  }

  .info p {
    margin: 0;
  }

  .name {
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .muted {
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }

  .danger {
    color: var(--color-danger);
  }

  .danger-fill {
    background: var(--color-danger);
    color: var(--color-on-danger);
    border-color: var(--color-danger);
  }

  .empty {
    padding: var(--space-8) var(--space-4);
    text-align: center;
    color: var(--color-muted-foreground);
    border: 1px dashed var(--color-border);
    border-radius: var(--radius-md);
  }

  .more,
  .foot {
    display: flex;
    justify-content: center;
    gap: var(--space-2);
    margin-block-start: var(--space-4);
  }

  .foot {
    justify-content: flex-end;
  }
</style>
