<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { tr } from '../../lib/i18n';
  import { signedLink, contentPath } from '../../lib/files/api';
  import { startDownload } from '../../lib/files/ops';
  import { kindOf, previewable, TEXT_PREVIEW_BYTES, type Kind } from '../../lib/files/kind';
  import { downloadAllowed } from '../../lib/files/roles';
  import { ApiError } from '../../lib/apiError';
  import type { Node } from '../../lib/files/types';

  // Preview shows one file full screen. Images, video and audio, and PDFs load through a short signed link
  // on the content origin. Text is read on this origin, the first 1 MiB only, and shown as text.
  let {
    items,
    index,
    onclose,
    onindex,
  }: { items: Node[]; index: number; onclose: () => void; onindex: (i: number) => void } = $props();

  type State =
    | { status: 'loading' }
    | { status: 'ready'; url?: string; text?: string; truncated?: boolean }
    | { status: 'restricted' }
    | { status: 'error' };

  let node = $derived(items[index]);
  let loaded = $state<State>({ status: 'loading' });
  let panel: HTMLDivElement | undefined = $state();
  let token = 0;

  onMount(() => {
    const opener = document.activeElement as HTMLElement | null;
    void tick().then(() => panel?.querySelector<HTMLElement>('[data-autofocus]')?.focus());
    return () => opener?.focus?.();
  });

  // load fetches what the current file needs. A newer request makes an older answer stale.
  $effect(() => {
    const current = node;
    if (!current) return;
    const my = ++token;
    loaded = { status: 'loading' };
    void load(current, my);
  });

  async function load(n: Node, my: number): Promise<void> {
    const kind: Kind = kindOf(n);
    try {
      if (kind === 'text') {
        const res = await fetch(contentPath(n.id, 'inline'), {
          credentials: 'same-origin',
          cache: 'no-store',
          headers: { Range: `bytes=0-${TEXT_PREVIEW_BYTES - 1}` },
        });
        if (my !== token) return;
        if (res.status === 403) {
          loaded = { status: 'restricted' };
          return;
        }
        if (!res.ok && res.status !== 416) throw new ApiError(res.status, 'internal', {}, 0);
        const text = res.status === 416 ? '' : await res.text();
        loaded = { status: 'ready', text, truncated: n.size > TEXT_PREVIEW_BYTES };
        return;
      }
      const link = await signedLink(n.id, 'inline', 300);
      if (my !== token) return;
      loaded = { status: 'ready', url: link.url };
    } catch (err) {
      if (my !== token) return;
      loaded = err instanceof ApiError && err.code === 'download_restricted' ? { status: 'restricted' } : { status: 'error' };
    }
  }

  function step(delta: number) {
    const next = index + delta;
    if (next >= 0 && next < items.length) onindex(next);
  }

  function keydown(e: KeyboardEvent) {
    const rtl = document.documentElement.dir === 'rtl';
    const forward = rtl ? 'ArrowLeft' : 'ArrowRight';
    const back = rtl ? 'ArrowRight' : 'ArrowLeft';
    if (e.key === 'Escape') {
      e.preventDefault();
      onclose();
    } else if (e.key === forward) {
      e.preventDefault();
      step(1);
    } else if (e.key === back) {
      e.preventDefault();
      step(-1);
    } else if (e.key === 'Tab' && panel) {
      const items = Array.from(panel.querySelectorAll<HTMLElement>('button, a[href], video, audio, iframe, [tabindex]:not([tabindex="-1"])'));
      if (items.length === 0) return;
      const first = items[0];
      const last = items[items.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }
  }

  let canDownload = $derived(node ? downloadAllowed(node) : false);
  let kindNow = $derived(node ? kindOf(node) : 'file');
  let hasPreview = $derived(node ? previewable(node) : false);

  async function download() {
    if (!node) return;
    const ok = await startDownload(node.id);
    if (!ok) loaded = { status: 'restricted' };
  }
</script>

<div class="preview" role="presentation" onkeydown={keydown}>
  <div class="panel" role="dialog" aria-modal="true" aria-label={node?.name ?? ''} bind:this={panel}>
    <header class="bar">
      <button type="button" class="btn secondary" onclick={onclose} data-autofocus>{$tr('files.preview.close')}</button>
      <p class="name"><bdi>{node?.name ?? ''}</bdi></p>
      <span class="count muted" aria-live="polite">{index + 1} / {items.length}</span>
      {#if canDownload}
        <button type="button" class="btn secondary" onclick={download}>{$tr('files.act.download')}</button>
      {/if}
    </header>

    <div class="stage">
      {#if node && !hasPreview}
        <div class="card">
          <p>{$tr('files.preview.none')}</p>
          {#if canDownload}
            <button type="button" class="btn primary" onclick={download}>{$tr('files.act.download')}</button>
          {/if}
        </div>
      {:else if loaded.status === 'loading'}
        <p class="muted" role="status">{$tr('files.preview.loading')}</p>
      {:else if loaded.status === 'restricted'}
        <p class="card" role="status">{$tr('files.err.downloadRestricted')}</p>
      {:else if loaded.status === 'error'}
        <p class="card" role="alert">{$tr('error.generic')}</p>
      {:else if loaded.status === 'ready' && node}
        {#if kindNow === 'image' && loaded.url}
          <img class="media" src={loaded.url} alt={node.name} />
        {:else if kindNow === 'pdf' && loaded.url}
          <iframe class="frame" title={node.name} src={loaded.url}></iframe>
        {:else if kindNow === 'video' && loaded.url}
          <!-- svelte-ignore a11y_media_has_caption -->
          <video class="media" controls src={loaded.url} aria-label={node.name}></video>
        {:else if kindNow === 'audio' && loaded.url}
          <!-- svelte-ignore a11y_media_has_caption -->
          <audio controls src={loaded.url} aria-label={node.name}></audio>
        {:else if kindNow === 'text'}
          {#if loaded.truncated}
            <p class="muted">{$tr('files.preview.truncated')}</p>
          {/if}
          <pre class="text">{loaded.text}</pre>
        {/if}
      {/if}
    </div>

    <nav class="steps" aria-label={$tr('files.preview.steps')}>
      <button type="button" class="btn secondary" onclick={() => step(-1)} disabled={index <= 0}>
        <span class="icon-dir" aria-hidden="true">‹</span> {$tr('files.preview.prev')}
      </button>
      <button type="button" class="btn secondary" onclick={() => step(1)} disabled={index >= items.length - 1}>
        {$tr('files.preview.next')} <span class="icon-dir" aria-hidden="true">›</span>
      </button>
    </nav>
  </div>
</div>

<style>
  .preview {
    position: fixed;
    inset: 0;
    z-index: 60;
    background: rgb(2 6 23 / 0.92);
    color: #f8fafc;
  }

  .panel {
    display: flex;
    flex-direction: column;
    height: 100dvh;
    padding: var(--space-3);
    gap: var(--space-3);
  }

  .bar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-3);
  }

  .name {
    flex: 1 1 160px;
    margin: 0;
    overflow-wrap: anywhere;
    font-weight: 600;
  }

  .count {
    color: #cbd5e1;
    font-variant-numeric: tabular-nums;
  }

  .stage {
    flex: 1 1 auto;
    min-height: 0;
    overflow: auto;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .media {
    max-width: 100%;
    max-height: 100%;
    object-fit: contain;
  }

  .frame {
    width: 100%;
    height: 100%;
    border: 0;
    background: #fff;
  }

  .text {
    align-self: stretch;
    margin: 0;
    padding: var(--space-4);
    overflow: auto;
    background: #0f172a;
    color: #e2e8f0;
    font-family: var(--font-mono);
    font-size: var(--text-sm);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    border-radius: var(--radius-sm);
  }

  .card {
    max-width: 420px;
    padding: var(--space-6);
    text-align: center;
    background: var(--color-surface);
    color: var(--color-foreground);
    border-radius: var(--radius-md);
  }

  .steps {
    display: flex;
    justify-content: space-between;
    gap: var(--space-3);
  }

  .muted {
    color: #cbd5e1;
  }
</style>
