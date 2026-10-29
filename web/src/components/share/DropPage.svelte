<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { lang, translate, translatePlural } from '../../lib/i18n';
  import { ApiError } from '../../lib/apiError';
  import { visitorInfo } from '../../lib/sharing/api';
  import { renewDelay } from '../../lib/sharing/anyone';
  import {
    dropMetadata,
    dropTitle,
    uploaderFields,
    uploaderProblem,
    type DropInfo,
    type Uploader,
    type UploaderProblem,
  } from '../../lib/sharing/drop';
  import { UploadManager, type Transport, type UploadItem, type UploadStatus } from '../../lib/files/uploads';

  // DropPage is the page a visitor of a drop link sees: its title and message, the name and email when the link asks
  // for them, and a place to send files. It never lists the folder. It lists only the files this page sent in this
  // visit, from its own upload manager, which keeps none of them in the browser.
  //
  // The credential is the page's, passed in as getters: bearer answers the token of the moment, and expiresAt the
  // time it ends. onrenew renews the credential (the link page does it, and answers whether one is held now);
  // onlost says the link is gone or the credential cannot be renewed.
  let {
    bearer,
    expiresAt,
    onrenew,
    onlost,
  }: {
    bearer: () => string | null;
    expiresAt: () => string;
    onrenew: () => Promise<boolean>;
    onlost: (why: 'expired' | 'gone') => void;
  } = $props();

  let info = $state<DropInfo | null>(null);
  let uploader = $state<Uploader>({ name: '', email: '' });
  let problem = $state<UploaderProblem | 'too_large' | null>(null);
  let failed = $state(false);
  let items = $state<UploadItem[]>([]);
  let fileInput: HTMLInputElement | undefined = $state();
  let dragging = $state(false);

  // The transport gives the manager the credential of each request, read when the request is made, and the drop
  // metadata. fresh renews the credential before a piece when it is near its end, or when forced after a refusal.
  const transport: Transport = {
    bearer: () => bearer(),
    metadata: (it) => dropMetadata(it.name, it.target.uploader ?? { name: '', email: '' }),
    async fresh(force: boolean): Promise<void> {
      if (!force && renewDelay(expiresAt(), Date.now()) > 0) return;
      if (!(await onrenew())) throw new ApiError(401, 'unauthenticated', {}, 0);
    },
    sink(list: UploadItem[]): void {
      items = list;
      // A file the server cannot find any more means the link is gone: the page shows the one dead-link message.
      if (list.some((it) => it.error?.code === 'not_found')) onlost('gone');
    },
  };
  const manager = new UploadManager(() => Date.now(), transport);

  onMount(() => {
    void loadInfo(true);
  });

  onDestroy(() => manager.halt());

  // loadInfo reads what the link says. A refused credential is renewed once and asked again; a dead link is lost.
  async function loadInfo(again: boolean): Promise<void> {
    const token = bearer();
    if (token === null) return;
    try {
      info = await visitorInfo(token);
    } catch (err) {
      if (err instanceof ApiError && err.status === 401 && again) {
        if (await onrenew()) return loadInfo(false);
        return;
      }
      if (err instanceof ApiError && err.status === 404) onlost('gone');
      else if (err instanceof ApiError && err.status === 401) onlost('expired');
      else failed = true;
    }
  }

  // chooseFiles takes the files a person picked or dropped. The uploader's fields must be right first, and each
  // file is checked against the largest file the link takes.
  function chooseFiles(list: FileList | File[] | null): void {
    if (list === null || info === null) return;
    const files = Array.from(list);
    if (files.length === 0) return;
    const p = uploaderProblem(info.ask_name, uploader);
    if (p !== null) {
      problem = p;
      return;
    }
    const limit = info.max_file_size;
    if (files.some((f) => f.size > limit)) {
      problem = 'too_large';
      return;
    }
    problem = null;
    const snapshot = uploaderFields(uploader);
    manager.add(files.map((file) => ({ file, target: { folder: '', uploader: snapshot } })));
    if (fileInput) fileInput.value = '';
  }

  function onDrop(e: DragEvent): void {
    e.preventDefault();
    dragging = false;
    chooseFiles(e.dataTransfer?.files ?? null);
  }

  function onDragOver(e: DragEvent): void {
    e.preventDefault();
    dragging = true;
  }

  function onDragLeave(): void {
    dragging = false;
  }

  // statusKey names the status a row shows. A drop has no name conflicts, so only these states occur.
  function statusKey(s: UploadStatus): string {
    switch (s) {
      case 'queued':
        return 'drop.status.queued';
      case 'uploading':
        return 'drop.status.uploading';
      case 'waiting':
        return 'drop.status.waiting';
      case 'done':
        return 'drop.status.done';
      case 'failed':
        return 'drop.status.failed';
      default:
        return 'drop.status.queued';
    }
  }

  function problemText(p: UploaderProblem | 'too_large'): string {
    switch (p) {
      case 'name_required':
        return translate($lang, 'drop.err.nameRequired');
      case 'email_required':
        return translate($lang, 'drop.err.emailRequired');
      case 'name_long':
        return translate($lang, 'drop.err.nameLong');
      case 'email_long':
        return translate($lang, 'drop.err.emailLong');
      case 'email_invalid':
        return translate($lang, 'drop.err.emailInvalid');
      case 'too_large':
        return translate($lang, 'drop.err.tooLarge');
    }
  }

  function failureText(err: ApiError | null): string {
    if (err === null) return translate($lang, 'error.generic');
    if (err.code === 'too_many_uploads') return translate($lang, 'drop.err.tooMany');
    if (err.code === 'rate_limited') return translatePlural($lang, 'error.rateLimited', 1);
    if (err.code === 'too_large') return translate($lang, 'drop.err.tooLarge');
    if (err.status === 0) return translate($lang, 'error.network');
    return translate($lang, 'error.generic');
  }

  function percent(it: UploadItem): number {
    return it.size === 0 ? 100 : Math.min(100, Math.round((it.offset / it.size) * 100));
  }
</script>

{#if failed}
  <p class="alert error" role="alert">{translate($lang, 'error.generic')}</p>
{:else if info === null}
  <p class="muted" role="status">{translate($lang, 'openlink.working')}</p>
{:else}
  <h1 class="page-title">{dropTitle(info, translate($lang, 'drop.defaultTitle'))}</h1>
  {#if info.message.trim() !== ''}
    <p class="drop-message">{info.message}</p>
  {/if}

  {#if problem !== null}
    <p class="alert error" role="alert">{problemText(problem)}</p>
  {/if}

  <form class="stack" onsubmit={(e) => e.preventDefault()}>
    <div class="pair">
      <div class="field">
        <label for="drop-name">{translate($lang, info.ask_name ? 'drop.name' : 'drop.nameOptional')}</label>
        <input id="drop-name" type="text" autocomplete="name" bind:value={uploader.name} required={info.ask_name} />
      </div>
      <div class="field">
        <label for="drop-email">{translate($lang, info.ask_name ? 'drop.email' : 'drop.emailOptional')}</label>
        <input id="drop-email" type="email" autocomplete="email" bind:value={uploader.email} required={info.ask_name} />
        <p class="hint">{translate($lang, 'drop.emailHint')}</p>
      </div>
    </div>
  </form>

  <div
    class="dropzone"
    class:dragging
    role="region"
    aria-label={translate($lang, 'drop.zone')}
    ondragover={onDragOver}
    ondragleave={onDragLeave}
    ondrop={onDrop}
  >
    <p>{translate($lang, 'drop.zoneHint')}</p>
    <button type="button" class="btn primary" onclick={() => fileInput?.click()}>{translate($lang, 'drop.choose')}</button>
    <input
      bind:this={fileInput}
      class="sr-only"
      type="file"
      multiple
      tabindex="-1"
      aria-hidden="true"
      onchange={(e) => chooseFiles((e.currentTarget as HTMLInputElement).files)}
    />
  </div>

  {#if items.length > 0}
    <ul class="rows" aria-label={translate($lang, 'drop.sent')}>
      {#each items as it (it.id)}
        <li class="row">
          <span class="name">{it.name}</span>
          <span class="muted">{translate($lang, statusKey(it.status))}</span>
          {#if it.status === 'failed'}
            <span class="alert error">{failureText(it.error)}</span>
          {/if}
          {#if it.status === 'uploading' || it.status === 'queued' || it.status === 'waiting'}
            <progress max="100" value={percent(it)}>{percent(it)}%</progress>
            <button type="button" class="btn secondary" onclick={() => void manager.cancel(it.id)}>
              {translate($lang, 'drop.cancel')}
            </button>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
{/if}

<style>
  .pair {
    display: grid;
    gap: var(--space-3);
    grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
  }

  .drop-message {
    white-space: pre-line;
  }

  .dropzone {
    display: grid;
    gap: var(--space-2);
    justify-items: center;
    padding: var(--space-4);
    border: 1px dashed var(--color-muted-foreground);
    border-radius: var(--radius-md, 0.5rem);
    text-align: center;
  }

  .dropzone.dragging {
    border-style: solid;
  }

  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
  }

  .name {
    overflow-wrap: anywhere;
    min-inline-size: 0;
  }

  .muted {
    color: var(--color-muted-foreground);
  }
</style>
