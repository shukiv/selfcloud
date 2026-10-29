<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { lang, tr } from '../../lib/i18n';
  import { session } from '../../lib/session';
  import { formatBytes, formatDate } from '../../lib/files/format';
  import { kindOf } from '../../lib/files/kind';
  import type { Node } from '../../lib/files/types';
  import Icon from './Icon.svelte';

  // Details is the side panel (a sheet on narrow screens) for one node.
  let {
    node,
    onclose,
    onversions,
    canVersions,
    canShare,
    onshare,
  }: {
    node: Node;
    onclose: () => void;
    onversions: () => void;
    canVersions: boolean;
    canShare: boolean;
    onshare: () => void;
  } = $props();

  let copied = $state<'idle' | 'done' | 'failed'>('idle');

  // who shows "you" for the signed-in user's own ID, and the ID otherwise: the interface has no user lookup.
  function who(id: string | undefined): string {
    if (!id) return '';
    return id === $session?.user.id ? $tr('files.details.you') : id;
  }

  async function copyHash() {
    try {
      await navigator.clipboard.writeText(node.sha256 ?? '');
      copied = 'done';
    } catch {
      copied = 'failed';
    }
  }
</script>

<aside class="details" aria-labelledby="details-title">
  <header class="head">
    <Icon kind={kindOf(node)} size={32} />
    <h2 id="details-title" class="title"><bdi>{node.name}</bdi></h2>
    <button type="button" class="btn secondary" onclick={onclose}>{$tr('files.details.close')}</button>
  </header>
  <dl>
    {#if node.kind === 'file'}
      <dt>{$tr('files.details.size')}</dt>
      <dd class="num">{formatBytes(node.size, $lang)}</dd>
      <dt>{$tr('files.details.type')}</dt>
      <dd><bdi>{node.mime_type ?? ''}</bdi></dd>
    {/if}
    <dt>{$tr('files.details.modified')}</dt>
    <dd>{formatDate(node.modified_at, $lang)}</dd>
    <dt>{$tr('files.details.created')}</dt>
    <dd>{formatDate(node.created_at, $lang)}</dd>
    <dt>{$tr('files.details.createdBy')}</dt>
    <dd><bdi>{who(node.created_by)}</bdi></dd>
    <dt>{$tr('files.details.owner')}</dt>
    <dd><bdi>{who(node.owner_id)}</bdi></dd>
    <dt>{$tr('files.details.role')}</dt>
    <dd>{node.role ? $tr(`role.${node.role}`) : ''}</dd>
    {#if node.sha256}
      <dt>{$tr('files.details.sha256')}</dt>
      <dd>
        <code class="hash">{node.sha256}</code>
        <button type="button" class="btn secondary" onclick={copyHash}>{$tr('files.details.copy')}</button>
        <span class="muted" role="status">
          {copied === 'done' ? $tr('files.details.copied') : copied === 'failed' ? $tr('files.details.copyFailed') : ''}
        </span>
      </dd>
    {/if}
  </dl>
  {#if node.kind === 'file' && canVersions}
    <button type="button" class="btn secondary block" onclick={onversions}>{$tr('files.details.versions')}</button>
  {/if}
  <div class="sharing">
    {#if canShare}
      <button type="button" class="btn primary block" onclick={onshare}>{$tr('share.open')}</button>
    {/if}
    <button type="button" class="btn secondary block" onclick={onshare}>{$tr('share.whoHas')}</button>
  </div>
</aside>

<style>
  .details {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
    padding: var(--space-4);
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
    min-width: 0;
  }

  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
  }

  .title {
    flex: 1 1 160px;
    margin: 0;
    font-size: var(--text-lg);
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  dl {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: var(--space-2) var(--space-4);
    margin: 0;
    font-size: var(--text-sm);
  }

  dt {
    color: var(--color-muted-foreground);
  }

  dd {
    margin: 0;
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .hash {
    display: block;
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    overflow-wrap: anywhere;
    margin-block-end: var(--space-2);
  }

  .block {
    width: 100%;
  }

  .sharing {
    display: grid;
    gap: var(--space-2);
  }
</style>
