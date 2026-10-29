<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { lang, tr } from '../../lib/i18n';
  import { formatBytes, formatDate } from '../../lib/files/format';
  import { kindOf } from '../../lib/files/kind';
  import type { SortDir, SortKey } from '../../lib/files/sort';
  import type { Node } from '../../lib/files/types';
  import type { ViewMode } from '../../lib/files/viewPrefs';
  import Icon from './Icon.svelte';

  // Listing shows a folder's items as a grid (table) in list view and as a listbox of tiles in grid view.
  // Keyboard handling belongs to the page; this component reports clicks and marks what is selected.
  interface Props {
    items: Node[];
    view: ViewMode;
    selected: Set<string>;
    focusID: string | null;
    sortKey: SortKey;
    sortDir: SortDir;
    renameID: string | null;
    renameValue: string;
    renameError: string | null;
    label: string;
    onclick: (index: number, e: MouseEvent) => void;
    ondblclick: (index: number) => void;
    onsort: (key: SortKey) => void;
    onrename: (commit: boolean) => void;
    onkeydown: (e: KeyboardEvent) => void;
    onfocusitem: (index: number) => void;
    ondragover: (e: DragEvent) => void;
    ondragleave: (e: DragEvent) => void;
    ondrop: (e: DragEvent) => void;
    listEl?: HTMLElement | undefined;
  }

  let {
    items,
    view,
    selected,
    focusID,
    sortKey,
    sortDir,
    renameID,
    renameValue = $bindable(''),
    renameError,
    label,
    onclick,
    ondblclick,
    onsort,
    onrename,
    onkeydown,
    onfocusitem,
    ondragover,
    ondragleave,
    ondrop,
    listEl = $bindable(undefined),
  }: Props = $props();

  function focusAndSelect(node: HTMLInputElement) {
    node.focus();
    node.select();
  }

  function ariaSort(key: SortKey): 'ascending' | 'descending' | 'none' {
    if (sortKey !== key) return 'none';
    return sortDir === 'asc' ? 'ascending' : 'descending';
  }

  // renameKey keeps the keys typed in the name field to the field: Enter must not also open the row.
  function renameKey(e: KeyboardEvent) {
    e.stopPropagation();
    if (e.key === 'Enter') {
      e.preventDefault();
      onrename(true);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      onrename(false);
    }
  }

  const sizeOf = (n: Node) => (n.kind === 'dir' ? '' : formatBytes(n.size, $lang));
</script>

{#snippet nameField(n: Node)}
  {#if renameID === n.id}
    <input
      class="rename"
      type="text"
      aria-label={$tr('files.rename.label')}
      aria-invalid={renameError ? 'true' : undefined}
      aria-describedby={renameError ? `rename-error` : undefined}
      bind:value={renameValue}
      onkeydown={renameKey}
      onblur={() => onrename(true)}
      use:focusAndSelect
    />
  {:else}
    <bdi class="name">{n.name}</bdi>
  {/if}
{/snippet}

{#snippet roleText(n: Node)}
  {#if n.role}{$tr(`role.${n.role}`)}{/if}
{/snippet}

{#if view === 'list'}
  <div
    class="grid"
    role="grid"
    aria-label={label}
    aria-multiselectable="true"
    aria-rowcount={items.length + 1}
    tabindex="-1"
    bind:this={listEl}
    {ondragover}
    {ondragleave}
    {ondrop}
  >
    <div class="row head" role="row" aria-rowindex="1">
      <div class="cell check" role="columnheader"><span class="sr-only">{$tr('files.col.select')}</span></div>
      <div class="cell name-cell" role="columnheader" aria-sort={ariaSort('name')}>
        <button type="button" class="sort" onclick={() => onsort('name')}>{$tr('files.col.name')}</button>
      </div>
      <div class="cell num size-cell" role="columnheader" aria-sort={ariaSort('size')}>
        <button type="button" class="sort" onclick={() => onsort('size')}>{$tr('files.col.size')}</button>
      </div>
      <div class="cell date-cell" role="columnheader" aria-sort={ariaSort('modified')}>
        <button type="button" class="sort" onclick={() => onsort('modified')}>{$tr('files.col.modified')}</button>
      </div>
      <div class="cell role-cell" role="columnheader"><span>{$tr('files.col.role')}</span></div>
    </div>
    {#each items as n, i (n.id)}
      {@const isSel = selected.has(n.id)}
      <div
        class="row"
        class:selected={isSel}
        role="row"
        aria-rowindex={i + 2}
        aria-selected={isSel}
        aria-label={n.name}
        tabindex={focusID === n.id ? 0 : -1}
        data-id={n.id}
        onclick={(e) => onclick(i, e)}
        ondblclick={() => ondblclick(i)}
        onkeydown={onkeydown}
        onfocus={() => onfocusitem(i)}
      >
        <div class="cell check" role="gridcell">
          <span class="box" class:on={isSel} aria-hidden="true">{isSel ? '✓' : ''}</span>
        </div>
        <div class="cell name-cell" role="gridcell">
          <Icon kind={kindOf(n)} size={22} />
          {@render nameField(n)}
          {#if renameID === n.id && renameError}
            <p class="field-error" id="rename-error">{renameError}</p>
          {/if}
        </div>
        <div class="cell num size-cell" role="gridcell">{sizeOf(n)}</div>
        <div class="cell date-cell" role="gridcell">{formatDate(n.modified_at, $lang)}</div>
        <div class="cell role-cell muted" role="gridcell">{@render roleText(n)}</div>
      </div>
    {/each}
  </div>
{:else}
  <div
    class="tiles"
    role="listbox"
    aria-label={label}
    aria-multiselectable="true"
    tabindex="-1"
    bind:this={listEl}
    {ondragover}
    {ondragleave}
    {ondrop}
  >
    {#each items as n, i (n.id)}
      {@const isSel = selected.has(n.id)}
      <div
        class="tile"
        class:selected={isSel}
        role="option"
        aria-selected={isSel}
        aria-label={n.name}
        tabindex={focusID === n.id ? 0 : -1}
        data-id={n.id}
        onclick={(e) => onclick(i, e)}
        ondblclick={() => ondblclick(i)}
        onkeydown={onkeydown}
        onfocus={() => onfocusitem(i)}
      >
        <Icon kind={kindOf(n)} size={40} />
        {#if renameID === n.id}
          <input
            class="rename"
            type="text"
            aria-label={$tr('files.rename.label')}
            bind:value={renameValue}
            onkeydown={renameKey}
            onblur={() => onrename(true)}
            use:focusAndSelect
          />
        {:else}
          <bdi class="name">{n.name}</bdi>
        {/if}
        <span class="muted small">{sizeOf(n)}</span>
      </div>
    {/each}
  </div>
{/if}

<style>
  .grid,
  .tiles {
    position: relative;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
    background: var(--color-surface);
    outline: none;
  }

  .row {
    display: grid;
    grid-template-columns: 44px minmax(0, 1fr) 96px 170px 120px;
    align-items: center;
    min-height: 44px;
    border-block-end: 1px solid var(--color-border);
    cursor: default;
    user-select: none;
  }

  .row:last-child {
    border-block-end: 0;
  }

  .row:not(.head):hover {
    background: var(--color-muted);
  }

  .row.selected {
    background: var(--color-primary-soft);
  }

  .row.head {
    min-height: 40px;
    font-size: var(--text-xs);
    font-weight: 500;
    color: var(--color-muted-foreground);
    background: var(--color-muted);
    border-radius: var(--radius-md) var(--radius-md) 0 0;
  }

  .cell {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-width: 0;
    padding-inline: var(--space-2);
  }

  .name-cell {
    flex-wrap: wrap;
  }

  .name {
    overflow-wrap: anywhere;
    min-width: 0;
  }

  .num,
  .size-cell {
    justify-content: flex-end;
    font-variant-numeric: tabular-nums;
  }

  .size-cell {
    font-size: var(--text-sm);
  }

  .date-cell,
  .role-cell {
    font-size: var(--text-sm);
  }

  .sort {
    min-height: 44px;
    padding: 0;
    font: inherit;
    font-weight: 500;
    color: inherit;
    background: none;
    border: 0;
    cursor: pointer;
  }

  .check {
    justify-content: center;
    padding: 0;
  }

  .box {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 20px;
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
    font-size: 14px;
    color: var(--color-on-primary);
  }

  .box.on {
    background: var(--color-primary);
    border-color: var(--color-primary);
  }

  .rename {
    min-height: 44px;
    flex: 1 1 160px;
    min-width: 0;
  }

  .tiles {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(144px, 1fr));
    gap: var(--space-3);
    padding: var(--space-3);
  }

  .tile {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--space-2);
    min-height: 44px;
    padding: var(--space-3);
    text-align: center;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
    cursor: default;
    user-select: none;
  }

  .tile.selected {
    background: var(--color-primary-soft);
    border-color: var(--color-primary);
  }

  .small {
    font-size: var(--text-xs);
  }

  .field-error {
    flex-basis: 100%;
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-danger);
  }

  @media (max-width: 767px) {
    .row {
      grid-template-columns: 44px minmax(0, 1fr) 84px;
    }

    .date-cell,
    .role-cell {
      display: none;
    }
  }
</style>
