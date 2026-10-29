<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  interface Props {
    id: string;
    label: string;
    value?: string;
    type?: 'text' | 'email';
    autocomplete?: HTMLInputElement['autocomplete'];
    inputmode?: 'email' | 'text';
    error?: string | null;
    hint?: string | null;
  }

  let {
    id,
    label,
    value = $bindable(''),
    type = 'text',
    autocomplete,
    inputmode,
    error = null,
    hint = null,
  }: Props = $props();

  let describedBy = $derived(
    [hint ? `${id}-hint` : null, error ? `${id}-error` : null].filter(Boolean).join(' ') || undefined,
  );
</script>

<div class="field">
  <label for={id}>{label}</label>
  {#if hint}
    <p class="hint" id="{id}-hint">{hint}</p>
  {/if}
  <input
    {id}
    {type}
    {autocomplete}
    {inputmode}
    bind:value
    aria-invalid={error ? 'true' : undefined}
    aria-describedby={describedBy}
  />
  {#if error}
    <p class="field-error" id="{id}-error">{error}</p>
  {/if}
</div>
