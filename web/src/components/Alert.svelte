<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { tp, tr } from '../lib/i18n';
  import type { Message } from '../lib/errors';

  interface Props {
    message: Message | null;
    tone?: 'error' | 'success';
  }

  let { message, tone = 'error' }: Props = $props();
</script>

{#if message}
  <div class="alert {tone}" role={tone === 'error' ? 'alert' : 'status'}>
    {#if message.plural !== undefined}
      {$tp(message.key, message.plural)}
    {:else}
      {$tr(message.key)}
    {/if}
  </div>
{/if}
