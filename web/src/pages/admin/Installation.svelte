<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { brand } from '../../lib/brand';
  import { lang, translate } from '../../lib/i18n';
  import { formatBytes } from '../../lib/files/format';
  import { getInfo } from '../../lib/manage/api';
  import { failureText } from '../../lib/manage/failure';
  import type { Info } from '../../lib/manage/types';

  // Installation: the facts of this installation, read-only. The links open the API's description and the
  // readiness check.
  let info = $state<Info | null>(null);
  let error = $state<string | null>(null);

  onMount(() => {
    getInfo()
      .then((i) => (info = i))
      .catch((err: unknown) => (error = failureText(err, $lang)));
  });
</script>

<svelte:head>
  <title>{translate($lang, 'admin.installation.title')} · {$brand.name}</title>
</svelte:head>

<h1 class="page-title">{translate($lang, 'admin.installation.title')}</h1>

{#if error}
  <p class="alert error" role="alert">{error}</p>
{:else if !info}
  <p role="status">{translate($lang, 'app.loading')}</p>
{:else}
  {@const i = info}
  <dl class="facts">
    <div><dt>{translate($lang, 'installation.version')}</dt><dd class="mono">{i.version}</dd></div>
    <div><dt>{translate($lang, 'installation.publicUrl')}</dt><dd><bdi>{i.public_url}</bdi></dd></div>
    <div><dt>{translate($lang, 'installation.webInterface')}</dt><dd>{translate($lang, i.web_interface ? 'installation.on' : 'installation.off')}</dd></div>
    <div><dt>{translate($lang, 'installation.pieceSize')}</dt><dd>{formatBytes(i.limits.max_chunk_size, $lang)}</dd></div>
    <div><dt>{translate($lang, 'installation.largestFile')}</dt><dd>{formatBytes(i.limits.max_file_size, $lang)}</dd></div>
  </dl>
  <p class="links">
    <a href="/api/v1/openapi.yaml">{translate($lang, 'installation.spec')}</a>
    <a href="/readyz">{translate($lang, 'installation.readiness')}</a>
  </p>
{/if}

<style>
  .facts {
    display: grid;
    gap: var(--space-2);
    margin: 0 0 var(--space-4);
    padding: var(--space-4);
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
  }

  .facts > div {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: var(--space-2);
  }

  .facts dt {
    color: var(--color-muted-foreground);
  }

  .facts dd {
    margin: 0;
    font-variant-numeric: tabular-nums;
    overflow-wrap: anywhere;
  }

  .links {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
  }
</style>
