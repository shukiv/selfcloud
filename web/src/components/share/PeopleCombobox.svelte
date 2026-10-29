<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onDestroy } from 'svelte';
  import { tr } from '../../lib/i18n';
  import { searchDirectory } from '../../lib/sharing/api';
  import {
    COMBO_START,
    MIN_QUERY,
    SEARCH_DELAY,
    comboReducer,
    debounce,
    type ComboState,
  } from '../../lib/sharing/combobox';
  import type { DirectoryEntry } from '../../lib/files/types';

  // PeopleCombobox finds a person or a group by name or email. It follows the ARIA combobox pattern: the
  // input is the combobox, the list is its popup, and aria-activedescendant follows the arrow keys. onpick
  // gets the entry when one is chosen, and null when the person types again after a choice.
  let {
    id,
    label,
    hint,
    onpick,
    invalid = false,
    errorId = undefined,
    kind = 'all',
  }: {
    id: string;
    label: string;
    hint: string;
    onpick: (entry: DirectoryEntry | null) => void;
    invalid?: boolean;
    errorId?: string;
    // kind limits the search to people or groups; the default searches both.
    kind?: 'all' | 'user' | 'group';
  } = $props();

  let state = $state<ComboState>(COMBO_START);
  let listId = $derived(`${id}-list`);
  const optionId = (i: number) => `${id}-opt-${i}`;

  const search = debounce((q: string) => {
    void searchDirectory(q.trim(), kind)
      .then((res) => {
        state = comboReducer(state, { type: 'results', query: q, items: res.items });
      })
      .catch(() => {
        state = comboReducer(state, { type: 'fail', query: q });
      });
  }, SEARCH_DELAY);

  onDestroy(() => search.cancel());

  function onInput(e: Event) {
    const value = (e.currentTarget as HTMLInputElement).value;
    const hadChoice = state.chosen !== null;
    state = comboReducer(state, { type: 'type', query: value });
    if (hadChoice) onpick(null);
    if (value.trim().length >= MIN_QUERY) search.call(value);
    else search.cancel();
  }

  function choose(index: number) {
    const entry = state.items[index];
    if (!entry) return;
    state = comboReducer(state, { type: 'pick', entry });
    onpick(entry);
  }

  function onKey(e: KeyboardEvent) {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        state = comboReducer(state, { type: 'open' });
        state = comboReducer(state, { type: 'move', delta: 1 });
        break;
      case 'ArrowUp':
        e.preventDefault();
        state = comboReducer(state, { type: 'open' });
        state = comboReducer(state, { type: 'move', delta: -1 });
        break;
      case 'Enter':
        if (state.open && state.active >= 0) {
          e.preventDefault();
          choose(state.active);
        }
        break;
      case 'Escape':
        // Escape closes the list only. It must not reach the dialog around it.
        if (state.open) {
          e.preventDefault();
          e.stopPropagation();
          state = comboReducer(state, { type: 'close' });
        }
        break;
    }
  }

  let showList = $derived(state.open && state.items.length > 0);
  let statusText = $derived(
    state.status === 'loading'
      ? $tr('share.combo.searching')
      : state.status === 'error'
        ? $tr('share.combo.error')
        : state.status === 'ready' && state.items.length === 0 && state.query.trim().length >= MIN_QUERY
          ? $tr('share.combo.none')
          : '',
  );
</script>

<div class="combo">
  <label class="field-label" for={id}>{label}</label>
  <input
    {id}
    type="text"
    role="combobox"
    autocomplete="off"
    aria-autocomplete="list"
    aria-expanded={showList}
    aria-controls={listId}
    aria-activedescendant={showList && state.active >= 0 ? optionId(state.active) : undefined}
    aria-describedby={errorId ? `${id}-hint ${errorId}` : `${id}-hint`}
    aria-invalid={invalid ? 'true' : undefined}
    value={state.query}
    oninput={onInput}
    onkeydown={onKey}
    onblur={() => (state = comboReducer(state, { type: 'close' }))}
  />
  <p class="hint" id="{id}-hint">{hint}</p>
  <p class="hint" role="status" aria-live="polite">{statusText}</p>
  {#if showList}
    <ul class="options" id={listId} role="listbox" aria-label={label}>
      {#each state.items as item, i (item.kind + item.id)}
        <li
          id={optionId(i)}
          role="option"
          aria-selected={i === state.active}
          class:active={i === state.active}
          onmousedown={(e) => e.preventDefault()}
          onclick={() => choose(i)}
          onkeydown={(e) => {
            // Options are not focused: the input takes the keys. This keeps Enter working on an option anyway.
            if (e.key === 'Enter') choose(i);
          }}
        >
          <span class="opt-name"><bdi>{item.name}</bdi></span>
          <span class="opt-meta">
            {#if item.kind === 'group'}
              {$tr('share.kind.group')}
            {:else}
              <bdi>{item.email ?? ''}</bdi>
            {/if}
          </span>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .combo {
    position: relative;
    display: grid;
    gap: var(--space-2);
  }

  .field-label {
    font-weight: 600;
  }

  input {
    min-height: 44px;
    padding-inline: var(--space-3);
    font: inherit;
    color: var(--color-foreground);
    background: var(--color-surface);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
  }

  input[aria-invalid='true'] {
    border-color: var(--color-danger);
  }

  .hint {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }

  .options {
    position: absolute;
    inset-inline: 0;
    inset-block-start: 100%;
    z-index: 5;
    margin: 0;
    padding: var(--space-1) 0;
    max-height: 240px;
    overflow: auto;
    list-style: none;
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
    box-shadow: var(--shadow-pop);
  }

  .options li {
    display: grid;
    gap: 2px;
    min-height: 44px;
    padding: var(--space-2) var(--space-3);
    cursor: pointer;
    text-align: start;
  }

  .options li.active,
  .options li:hover {
    background: var(--color-primary-soft);
  }

  .opt-meta {
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
    overflow-wrap: anywhere;
  }
</style>
