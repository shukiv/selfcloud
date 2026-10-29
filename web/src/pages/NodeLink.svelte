<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { go, location } from '../lib/router';
  import { tr } from '../lib/i18n';
  import { statNode, statOrNull } from '../lib/files/api';
  import { fileErrorText } from '../lib/files/fileErrors';
  import { planNodeLink } from '../lib/files/nodeLink';
  import type { Node } from '../lib/files/types';
  import { filesURL, validID } from '../lib/files/route';
  import { lang } from '../lib/i18n';
  import Preview from '../components/files/Preview.svelte';
  import NotFound from './NotFound.svelte';

  // NodeLink resolves /n/{nod}, the address share mails use. The decision is planNodeLink's: a file opens its
  // preview in its folder, or on its own when the folder cannot be reached; a directory opens its parent with
  // the directory selected. Anything the caller cannot reach is not found.
  // linkID is the node the address names; the not-found view offers a request for it.
  let linkID = $derived($location.path.split('/')[2] ?? '');
  let missing = $state(false);
  let failed = $state('');
  let alone = $state<Node | null>(null);

  $effect(() => {
    const id = $location.path.split('/')[2] ?? '';
    missing = false;
    failed = '';
    alone = null;
    void resolve(id);
  });

  async function resolve(id: string): Promise<void> {
    if (!validID('nod', id)) {
      missing = true;
      return;
    }
    try {
      const node = await statOrNull(id);
      if (!node) {
        missing = true;
        return;
      }
      // The parent is read only when there is one; a folder's root has none. null means the caller cannot reach it.
      const parent = node.parent_id === null ? null : await statNode(node.parent_id).catch(() => null);
      const plan = planNodeLink(node, parent);
      if (plan.kind === 'folder') {
        go(filesURL(plan.folder, null), { replace: true });
      } else if (plan.kind === 'alone') {
        alone = plan.node;
      } else {
        const query: Record<string, string> = plan.preview
          ? { preview: plan.preview }
          : { select: plan.select ?? '' };
        go(filesURL(plan.folder, plan.dir, query), { replace: true });
      }
    } catch (err) {
      failed = fileErrorText(err, $lang);
    }
  }
</script>

{#if missing}
  <NotFound nodeID={validID('nod', linkID) ? linkID : undefined} />
{:else if failed}
  <p class="alert error" role="alert">{failed}</p>
{:else if alone}
  <Preview items={[alone]} index={0} onclose={() => go('/files', { replace: true })} onindex={() => {}} />
{:else}
  <p role="status">{$tr('app.loading')}</p>
{/if}
