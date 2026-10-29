<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { tr } from '../../lib/i18n';
  import type { ConflictChoice } from '../../lib/files/conflict';
  import Dialog from '../Dialog.svelte';

  // Conflict asks what to do with a name that is already taken.
  let {
    name,
    batch = false,
    onanswer,
    oncancel,
  }: {
    name: string;
    batch?: boolean;
    onanswer: (choice: ConflictChoice, remember: boolean) => void;
    oncancel: () => void;
  } = $props();

  let remember = $state(false);
</script>

<Dialog title={$tr('files.conflict.title')} id="conflict" onclose={oncancel}>
  <p>{$tr('files.conflict.body', { name })}</p>
  {#if batch}
    <label class="remember">
      <input type="checkbox" bind:checked={remember} />
      {$tr('files.conflict.remember')}
    </label>
  {/if}
  <div class="choices">
    <button type="button" class="btn primary" data-autofocus onclick={() => onanswer('replace', remember)}>{$tr('files.conflict.replace')}</button>
    <button type="button" class="btn secondary" onclick={() => onanswer('rename', remember)}>{$tr('files.conflict.rename')}</button>
    <button type="button" class="btn secondary" onclick={() => onanswer('skip', remember)}>{$tr('files.conflict.skip')}</button>
  </div>
</Dialog>

<style>
  .remember {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-height: 44px;
    margin-block-end: var(--space-4);
  }

  .choices {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    justify-content: flex-end;
  }
</style>
