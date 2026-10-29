<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { session } from '../lib/session';
  import { tr } from '../lib/i18n';
  import RequestAccess from '../components/share/RequestAccess.svelte';

  // NotFound is the view for an address the caller cannot reach. For a signed-in person on an item, it offers to ask
  // the people who manage the item for access; the form is keyed on the item so an answer never carries over.
  let { nodeID = undefined }: { nodeID?: string } = $props();
</script>

<h1 class="page-title">{$tr('app.notFound.title')}</h1>
<p class="muted">{$tr('app.notFound.body')}</p>
{#if nodeID && $session}
  {#key nodeID}
    <RequestAccess {nodeID} />
  {/key}
{/if}
