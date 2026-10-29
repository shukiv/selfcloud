<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { lang, translate } from '../../lib/i18n';
  import { ApiError } from '../../lib/apiError';
  import type { Node } from '../../lib/files/types';
  import { visitorChildren, visitorLink, visitorNode } from '../../lib/sharing/api';
  import { crumbsTo, previewKind, type NodeRef } from '../../lib/sharing/anyone';

  // PublicBrowse shows what an anyone link opens: a folder, with its children and a path that stops at the
  // link's root, or a file with a preview and a Download button. The bearer credential is held by the page
  // and passed in; this component never stores it. A 401 means the credential ended, and a 404 means the link
  // is dead: both are handed to the page, which decides what to do.
  let {
    token,
    root,
    onlost,
  }: {
    token: string;
    root: string;
    onlost: (why: 'expired' | 'gone') => void;
  } = $props();

  // The nodes the visitor has seen. A path is always made of these, since the visitor only moves down from the
  // root, so nothing above the root is ever asked for.
  const cache = new Map<string, Node>();
  let current = $state<Node | null>(null);
  let trail = $state<NodeRef[]>([]);
  let children = $state<Node[]>([]);
  let previewURL = $state<string | null>(null);
  let downloadURL = $state<string | null>(null);
  let downloadRestricted = $state(false);
  let busy = $state(true);
  let error = $state<string | null>(null);

  onMount(() => {
    void show(root);
  });

  // lost turns a refused call into the page's answer: an ended credential, or a dead link. Anything else is shown
  // as a failure here.
  function lost(err: unknown): void {
    if (err instanceof ApiError && err.status === 401) return onlost('expired');
    if (err instanceof ApiError && err.status === 404) return onlost('gone');
    error = translate($lang, 'error.generic');
  }

  async function show(id: string): Promise<void> {
    busy = true;
    error = null;
    try {
      const node = cache.get(id) ?? (await visitorNode(token, id));
      cache.set(id, node);
      previewURL = null;
      downloadURL = null;
      downloadRestricted = false;
      if (node.kind === 'dir') {
        children = await allChildren(id);
        for (const c of children) cache.set(c.id, c);
      } else {
        children = [];
        await prepareFile(node);
      }
      current = node;
      trail = crumbsTo(cache, id, root) ?? [];
    } catch (err) {
      lost(err);
    } finally {
      busy = false;
    }
  }

  // allChildren reads every page of a folder, up to a fixed number of pages.
  async function allChildren(id: string): Promise<Node[]> {
    const out: Node[] = [];
    let cursor: string | undefined;
    for (let page = 0; page < 50; page++) {
      const res = await visitorChildren(token, id, cursor);
      out.push(...res.items);
      if (!res.next_cursor) break;
      cursor = res.next_cursor;
    }
    return out;
  }

  // prepareFile asks for a preview link, when the type has one, and a download link, which the server refuses
  // when no-download applies. Neither is asked for again until the file is opened again.
  async function prepareFile(node: Node): Promise<void> {
    if (previewKind(node.mime_type) !== null) {
      try {
        previewURL = (await visitorLink(token, node.id, 'inline')).url;
      } catch (err) {
        if (err instanceof ApiError && (err.status === 401 || err.status === 404)) throw err;
        previewURL = null;
      }
    }
    try {
      downloadURL = (await visitorLink(token, node.id, 'attachment')).url;
    } catch (err) {
      if (err instanceof ApiError && err.code === 'download_restricted') {
        downloadRestricted = true;
        return;
      }
      if (err instanceof ApiError && (err.status === 401 || err.status === 404)) throw err;
      downloadURL = null;
    }
  }

  let kind = $derived(current ? previewKind(current.mime_type) : null);
</script>

{#if error}
  <p class="alert error" role="alert">{error}</p>
{/if}

{#if busy && current === null}
  <p class="muted" role="status">{translate($lang, 'openlink.working')}</p>
{:else if current}
  {#if trail.length > 0}
    <nav aria-label={translate($lang, 'share.open.path')} class="crumbs">
      <ol>
        {#each trail as crumb, i (crumb.id)}
          <li>
            {#if i === trail.length - 1}
              <span aria-current="location">{crumb.name}</span>
            {:else}
              <button type="button" class="link" onclick={() => show(crumb.id)}>{crumb.name}</button>
            {/if}
          </li>
        {/each}
      </ol>
    </nav>
  {/if}

  <h1 class="page-title">{current.name}</h1>

  {#if current.kind === 'dir'}
    {#if children.length === 0}
      <p class="empty">{translate($lang, 'share.open.empty')}</p>
    {:else}
      <ul class="rows">
        {#each children as child (child.id)}
          <li class="row">
            <button type="button" class="link" onclick={() => show(child.id)}>{child.name}</button>
            <span class="muted">
              {child.kind === 'dir' ? translate($lang, 'share.open.folder') : translate($lang, 'share.open.file')}
            </span>
          </li>
        {/each}
      </ul>
    {/if}
  {:else}
    {#if previewURL !== null && kind === 'image'}
      <img class="public-preview" src={previewURL} alt={current.name} />
    {:else if previewURL !== null && kind === 'video'}
      <!-- Uploaded video has no caption track to offer; the file's own name is all there is to label it with. -->
      <!-- svelte-ignore a11y_media_has_caption -->
      <video class="public-preview" controls src={previewURL} aria-label={current.name}></video>
    {:else if previewURL !== null && kind === 'audio'}
      <audio controls src={previewURL}></audio>
    {:else if previewURL !== null && kind === 'pdf'}
      <iframe class="public-preview" title={current.name} src={previewURL}></iframe>
    {:else}
      <p class="muted">{translate($lang, 'share.open.noPreview')}</p>
    {/if}

    {#if downloadURL !== null}
      <p class="toolbar-actions">
        <a class="btn primary" href={downloadURL} download>{translate($lang, 'share.open.download')}</a>
      </p>
    {:else if downloadRestricted}
      <p class="hint">{translate($lang, 'share.open.noDownload')}</p>
    {/if}
  {/if}
{/if}

<style>
  .crumbs ol {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .crumbs li + li::before {
    content: '/';
    margin-inline: var(--space-1);
    color: var(--color-muted-foreground);
  }

  .link {
    background: none;
    border: 0;
    padding: 0;
    color: inherit;
    font: inherit;
    text-decoration: underline;
    cursor: pointer;
    text-align: start;
  }

  .row {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: var(--space-2);
  }

  .public-preview {
    display: block;
    max-width: 100%;
    max-height: 70vh;
    inline-size: 100%;
    border: 0;
  }

  .muted {
    color: var(--color-muted-foreground);
  }
</style>
