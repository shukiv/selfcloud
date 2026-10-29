<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { lang, tr } from '../../lib/i18n';
  import { childrenPage } from '../../lib/files/api';
  import { atLeast } from '../../lib/files/roles';
  import type { FolderAccess, Node, Role } from '../../lib/files/types';
  import { fileErrorText } from '../../lib/files/fileErrors';
  import Dialog from '../Dialog.svelte';

  // A destination: a folder, and a directory in it (null for the folder's root).
  export interface Destination {
    folder: string;
    dir: string | null;
  }

  // PickDestination chooses where to move or copy. It starts at the list of spaces and goes down through
  // folders and directories. Places the caller can only view are shown, and cannot be chosen.
  let {
    mode,
    spaces,
    onpick,
    onclose,
  }: {
    mode: 'move' | 'copy';
    spaces: FolderAccess[];
    onpick: (dest: Destination) => void;
    onclose: () => void;
  } = $props();

  interface Here {
    folder: string;
    name: string;
    dir: Node | null;
    role: Role;
  }

  let at = $state<Here | null>(null);
  let kids = $state<Node[]>([]);
  let cursor = $state<string | undefined>(undefined);
  let busy = $state(false);
  let error = $state<string | null>(null);

  // stack holds the places above the current one, so Back goes up one level.
  let stack: Here[] = [];

  async function openSpace(s: FolderAccess) {
    stack = [];
    at = { folder: s.id, name: s.name, dir: null, role: s.role };
    await loadKids(true);
  }

  async function openDir(n: Node) {
    if (!at) return;
    stack = [...stack, at];
    at = { folder: at.folder, name: n.name, dir: n, role: n.role ?? 'viewer' };
    await loadKids(true);
  }

  async function loadKids(reset: boolean) {
    if (!at) return;
    busy = true;
    try {
      const page = await childrenPage(at.folder, at.dir?.id ?? null, reset ? undefined : cursor);
      kids = reset ? page.items.filter((n) => n.kind === 'dir') : [...kids, ...page.items.filter((n) => n.kind === 'dir')];
      cursor = page.next_cursor;
      error = null;
    } catch (err) {
      error = fileErrorText(err, $lang);
    } finally {
      busy = false;
    }
  }

  // up goes back one level; from a space's root, back to the list of spaces.
  async function up() {
    const prev = stack[stack.length - 1] ?? null;
    stack = stack.slice(0, -1);
    at = prev;
    kids = [];
    cursor = undefined;
    if (at) await loadKids(true);
  }

  let canPick = $derived(at !== null && atLeast(at.role, 'contributor'));
  let title = $derived(mode === 'move' ? $tr('files.pick.move') : $tr('files.pick.copy'));
  let submit = $derived(mode === 'move' ? $tr('files.pick.submitMove') : $tr('files.pick.submitCopy'));
</script>

<Dialog {title} id="pick" {onclose} wide>
  {#if error}
    <p class="alert error" role="alert">{error}</p>
  {/if}
  {#if at === null}
    <p class="muted">{$tr('files.pick.spaces')}</p>
    <ul class="places" aria-label={$tr('files.pick.spaces')}>
      {#each spaces as s (s.id)}
        <li>
          <button type="button" class="place" data-autofocus onclick={() => openSpace(s)}>
            <bdi>{s.name}</bdi>
            <span class="muted">{s.kind === 'personal' ? $tr('files.pick.mine') : $tr(`role.${s.role}`)}</span>
          </button>
        </li>
      {:else}
        <li class="muted">{$tr('files.pick.noSpaces')}</li>
      {/each}
    </ul>
  {:else}
    <div class="here-bar">
      <button type="button" class="btn secondary" onclick={up}>{$tr('files.pick.back')}</button>
      <p class="here"><bdi>{at.dir ? at.dir.name : at.name}</bdi></p>
      <span class="muted">{$tr(`role.${at.role}`)}</span>
    </div>
    {#if !canPick}
      <p class="muted" role="note">{$tr('files.pick.viewOnly')}</p>
    {/if}
    <ul class="places" aria-label={$tr('files.pick.folders')}>
      {#each kids as k (k.id)}
        {@const ok = atLeast(k.role, 'contributor')}
        <li class="kid">
          <button type="button" class="place" onclick={() => openDir(k)}>
            <bdi>{k.name}</bdi>
            <span class="muted">{ok ? $tr(`role.${k.role}`) : $tr('files.pick.viewOnlyShort')}</span>
          </button>
        </li>
      {:else}
        {#if !busy}<li class="muted">{$tr('files.pick.empty')}</li>{/if}
      {/each}
    </ul>
    {#if cursor}
      <button type="button" class="btn secondary block" disabled={busy} onclick={() => loadKids(false)}>
        {$tr('files.loadMore')}
      </button>
    {/if}
  {/if}
  <div class="foot">
    <button type="button" class="btn secondary" onclick={onclose}>{$tr('common.cancel')}</button>
    {#if at !== null}
      <button
        type="button"
        class="btn primary"
        disabled={!canPick}
        onclick={() => at && onpick({ folder: at.folder, dir: at.dir?.id ?? null })}
      >
        {submit}
      </button>
    {/if}
  </div>
</Dialog>

<style>
  .places {
    display: grid;
    gap: var(--space-2);
    margin: 0 0 var(--space-4);
    padding: 0;
    list-style: none;
    max-height: 50dvh;
    overflow: auto;
  }

  .place {
    display: flex;
    width: 100%;
    min-height: 44px;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    padding-inline: var(--space-3);
    font: inherit;
    color: var(--color-foreground);
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
    cursor: pointer;
    text-align: start;
  }

  .here-bar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2) var(--space-3);
    margin-block-end: var(--space-3);
  }

  .here {
    flex: 1 1 120px;
    margin: 0;
    font-weight: 600;
  }

  .foot {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: var(--space-2);
    margin-block-start: var(--space-4);
  }

  .block {
    width: 100%;
  }

  .muted {
    color: var(--color-muted-foreground);
    font-size: var(--text-sm);
  }
</style>
