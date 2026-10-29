<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { tr } from '../lib/i18n';

  interface Props {
    id: string;
    label: string;
    value?: string;
    autocomplete: 'current-password' | 'new-password';
    error?: string | null;
  }

  let { id, label, value = $bindable(''), autocomplete, error = null }: Props = $props();

  let shown = $state(false);
  let describedBy = $derived(error ? `${id}-error` : undefined);
</script>

<div class="field">
  <label for={id}>{label}</label>
  <div class="input-wrap">
    <input
      {id}
      type={shown ? 'text' : 'password'}
      {autocomplete}
      bind:value
      aria-invalid={error ? 'true' : undefined}
      aria-describedby={describedBy}
    />
    <button
      type="button"
      class="toggle"
      aria-pressed={shown}
      aria-controls={id}
      onclick={() => (shown = !shown)}
    >
      {shown ? $tr('password.hide') : $tr('password.show')}
    </button>
  </div>
  {#if error}
    <p class="field-error" id="{id}-error">{error}</p>
  {/if}
</div>
