<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { tr } from '../../lib/i18n';
  import { go } from '../../lib/router';
  import type { Crumb } from '../../lib/files/breadcrumbs';

  export interface Place {
    key: string;
    label: string;
    href: string;
  }

  // Breadcrumbs shows the places from the top down to here. A long path collapses its middle into one menu.
  let { crumbs }: { crumbs: Crumb<Place>[] } = $props();

  function open(e: MouseEvent, href: string) {
    if (e.metaKey || e.ctrlKey || e.shiftKey) return;
    e.preventDefault();
    go(href);
  }
</script>

<nav class="crumbs" aria-label={$tr('files.crumbs')}>
  <ol>
    {#each crumbs as crumb, i (crumb.kind === 'item' ? crumb.item.key : `gap-${i}`)}
      <li>
        {#if crumb.kind === 'gap'}
          <details class="gap">
            <summary>
              <span aria-label={$tr('files.crumbs.more')}>…</span>
            </summary>
            <ul>
              {#each crumb.hidden as place (place.key)}
                <li><a href={place.href} onclick={(e) => open(e, place.href)}><bdi>{place.label}</bdi></a></li>
              {/each}
            </ul>
          </details>
        {:else if i === crumbs.length - 1}
          <span class="here" aria-current="location"><bdi>{crumb.item.label}</bdi></span>
        {:else}
          <a href={crumb.item.href} onclick={(e) => open(e, crumb.item.href)}><bdi>{crumb.item.label}</bdi></a>
        {/if}
        {#if i < crumbs.length - 1}
          <span class="sep icon-dir" aria-hidden="true">›</span>
        {/if}
      </li>
    {/each}
  </ol>
</nav>

<style>
  ol {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-1);
    margin: 0;
    padding: 0;
    list-style: none;
    font-size: var(--text-sm);
  }

  li {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    min-width: 0;
  }

  a,
  summary,
  .here {
    display: inline-flex;
    align-items: center;
    min-height: 44px;
    padding-inline: var(--space-1);
    color: var(--color-primary);
  }

  .here {
    color: var(--color-foreground);
    font-weight: 600;
  }

  .sep {
    color: var(--color-muted-foreground);
  }

  .gap {
    position: relative;
  }

  .gap ul {
    position: absolute;
    inset-block-start: 100%;
    inset-inline-start: 0;
    z-index: 10;
    min-width: 200px;
    margin: 0;
    padding: var(--space-2);
    list-style: none;
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
    box-shadow: var(--shadow-pop);
  }

  summary {
    cursor: pointer;
    list-style: none;
  }
</style>
