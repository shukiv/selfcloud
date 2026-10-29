<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { lang, tr, translatePlural } from '../lib/i18n';
  import { fileErrorText } from '../lib/files/fileErrors';
  import { formatBytes, formatRate } from '../lib/files/format';
  import { conflictAsk, manager, uploads, type UploadItem } from '../lib/files/uploads';
  import Conflict from './files/Conflict.svelte';

  // UploadTray lists the uploads: their progress, their state, and the controls each state allows. It is
  // collapsible to a pill that shows the overall progress.
  let open = $state(true);
  let nameDraft = $state<Record<number, string>>({});
  let announce = $state('');
  let resumeInput: HTMLInputElement | undefined = $state();
  let resumeFor = $state<number | null>(null);
  let mismatch = $state<number | null>(null);

  let active = $derived($uploads.filter((u) => ['queued', 'uploading', 'waiting', 'conflict', 'awaiting_name'].includes(u.status)));
  let shown = $derived($uploads.length > 0);
  let total = $derived(active.reduce((n, u) => n + u.size, 0));
  let sent = $derived(active.reduce((n, u) => n + u.offset, 0));
  let percent = $derived(total > 0 ? Math.floor((sent / total) * 100) : 0);

  // Screen readers hear a finish or a failure once, not every piece.
  const seen = new Map<number, string>();
  $effect(() => {
    for (const u of $uploads) {
      const before = seen.get(u.id);
      if (before !== u.status) {
        seen.set(u.id, u.status);
        if (u.status === 'done') announce = $tr('upload.announce.done', { name: u.name });
        if (u.status === 'failed') announce = $tr('upload.announce.failed', { name: u.name });
      }
    }
  });

  function commit(u: UploadItem) {
    const name = (nameDraft[u.id] ?? u.suggested ?? '').trim();
    if (name) void manager.commitName(u.id, name);
  }

  function pickResume(u: UploadItem) {
    resumeFor = u.id;
    resumeInput?.click();
  }

  async function onResumePicked(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = '';
    const id = resumeFor;
    resumeFor = null;
    if (!file || id === null) return;
    const ok = await manager.attach(id, file);
    mismatch = ok ? null : id;
  }

  function errorText(u: UploadItem): string {
    return u.error ? fileErrorText(u.error, $lang) : '';
  }
</script>

<section class="tray" class:closed={!open} aria-labelledby="tray-title">
  <header class="tray-head">
    <h2 id="tray-title" class="tray-title">{$tr('upload.title')}</h2>
    <p class="summary" role="status">
      {#if active.length > 0}
        {translatePlural($lang, 'upload.summary', active.length, { percent: String(percent) })}
      {/if}
    </p>
    <p class="sr-only" aria-live="polite">{announce}</p>
    <button type="button" class="btn secondary" aria-expanded={open} aria-controls="tray-list" onclick={() => (open = !open)}>
      {open ? $tr('upload.collapse') : $tr('upload.expand')}
    </button>
    <button type="button" class="btn secondary" onclick={() => manager.clearFinished()}>{$tr('upload.clear')}</button>
  </header>

  {#if open}
    <ul class="items" id="tray-list">
      {#each $uploads as u (u.id)}
        <li class="item" data-status={u.status}>
          <div class="line">
            <bdi class="name">{u.name}</bdi>
            <span class="status">{$tr(`upload.status.${u.status}`)}</span>
          </div>
          {#if u.status !== 'done' && u.status !== 'skipped' && u.status !== 'cancelled'}
            <progress class="bar" max={Math.max(1, u.size)} value={u.offset}></progress>
            <p class="meta muted num">
              {formatBytes(u.offset, $lang)} / {formatBytes(u.size, $lang)}
              {#if u.status === 'uploading' && u.speed > 0}· {$tr('upload.speed', { rate: formatRate(u.speed, $lang) })}{/if}
            </p>
          {/if}
          {#if u.status === 'failed' || u.status === 'waiting'}
            <p class="error" role={u.status === 'failed' ? 'alert' : 'status'}>{errorText(u)}</p>
          {/if}
          {#if u.status === 'awaiting_name'}
            <form class="name-form" onsubmit={(e) => { e.preventDefault(); commit(u); }}>
              <label for="name-{u.id}">{$tr('upload.name.label', { name: u.name })}</label>
              <input id="name-{u.id}" type="text" value={nameDraft[u.id] ?? u.suggested} oninput={(e) => (nameDraft[u.id] = (e.currentTarget as HTMLInputElement).value)} />
              <button type="submit" class="btn primary">{$tr('upload.name.submit')}</button>
            </form>
            {#if u.error}<p class="error" role="alert">{errorText(u)}</p>{/if}
          {/if}
          {#if mismatch === u.id}
            <p class="error" role="alert">{$tr('upload.resume.mismatch')}</p>
          {/if}
          <div class="actions">
            {#if u.status === 'uploading' || u.status === 'queued' || u.status === 'waiting'}
              <button type="button" class="btn secondary" onclick={() => manager.pause(u.id)}>{$tr('upload.pause')}</button>
            {/if}
            {#if u.status === 'paused'}
              <button type="button" class="btn secondary" onclick={() => manager.resume(u.id)}>{$tr('upload.resume')}</button>
            {/if}
            {#if u.status === 'failed'}
              <button type="button" class="btn secondary" onclick={() => manager.retry(u.id)}>{$tr('upload.retry')}</button>
            {/if}
            {#if u.status === 'interrupted'}
              <button type="button" class="btn secondary" onclick={() => pickResume(u)}>{$tr('upload.resume.pick')}</button>
            {/if}
            {#if ['uploading', 'queued', 'waiting', 'paused', 'interrupted', 'awaiting_name'].includes(u.status)}
              <button type="button" class="btn secondary" onclick={() => manager.cancel(u.id)}>{$tr('upload.cancel')}</button>
            {/if}
            {#if ['done', 'skipped', 'cancelled', 'failed'].includes(u.status)}
              <button type="button" class="btn secondary" onclick={() => manager.dismiss(u.id)}>{$tr('upload.dismiss')}</button>
            {/if}
          </div>
        </li>
      {:else}
        {#if shown}<li class="muted">{$tr('upload.none')}</li>{/if}
      {/each}
    </ul>
  {/if}

  <input bind:this={resumeInput} type="file" class="hidden-input" tabindex="-1" aria-hidden="true" onchange={onResumePicked} />

  {#if $conflictAsk}
    <Conflict
      name={$conflictAsk}
      batch={active.length > 1}
      onanswer={(choice, remember) => manager.answerConflict(choice, remember)}
      oncancel={() => manager.answerConflict('skip', false)}
    />
  {/if}
</section>

<style>
  .tray {
    position: fixed;
    inset-block-end: var(--space-4);
    inset-inline-end: var(--space-4);
    z-index: 30;
    width: min(420px, calc(100vw - 2 * var(--space-4)));
    max-height: 60dvh;
    display: flex;
    flex-direction: column;
    background: var(--color-surface);
    color: var(--color-foreground);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow-pop);
    overflow: hidden;
  }

  .tray.closed {
    max-height: none;
  }

  .tray-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-3) var(--space-4);
    border-block-end: 1px solid var(--color-border);
  }

  .tray-title {
    margin: 0;
    font-size: var(--text-base);
    font-weight: 600;
    flex: 1 1 auto;
  }

  .summary {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }

  .items {
    margin: 0;
    padding: 0;
    list-style: none;
    overflow: auto;
  }

  .item {
    display: grid;
    gap: var(--space-2);
    padding: var(--space-3) var(--space-4);
    border-block-end: 1px solid var(--color-border);
  }

  .line {
    display: flex;
    justify-content: space-between;
    gap: var(--space-2);
    font-size: var(--text-sm);
  }

  .name {
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .status {
    flex: none;
    color: var(--color-muted-foreground);
  }

  .bar {
    width: 100%;
    height: 8px;
  }

  .meta,
  .muted {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--color-muted-foreground);
  }

  .error {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-danger);
  }

  .name-form {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
  }

  .name-form label {
    flex-basis: 100%;
    font-size: var(--text-sm);
  }

  .name-form input {
    flex: 1 1 160px;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }

  .hidden-input {
    position: absolute;
    width: 1px;
    height: 1px;
    opacity: 0;
    overflow: hidden;
  }
</style>
