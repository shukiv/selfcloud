<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { lang, translate, translatePlural } from '../../lib/i18n';
  import { errorMessage } from '../../lib/errors';
  import { createAccessRequest } from '../../lib/sharing/api';
  import { REQUEST_SENT_KEY, requestOutcome } from '../../lib/sharing/requests';
  import type { Role } from '../../lib/files/types';

  // RequestAccess asks the people who manage an item for a role on it. The server answers 202 whether or not the
  // item exists, so the confirmation is the same whatever happens; only a rate limit or a lost connection is said
  // apart, since those say nothing about the item.
  let { nodeID }: { nodeID: string } = $props();

  const ROLES: Exclude<Role, 'contributor' | 'manager'>[] = ['viewer', 'commenter', 'editor'];
  let role = $state<Exclude<Role, 'contributor' | 'manager'>>('viewer');
  let message = $state('');
  let sending = $state(false);
  let outcome = $state<'idle' | 'sent' | 'limited' | 'failed'>('idle');
  let limitText = $state('');

  async function send(e: SubmitEvent) {
    e.preventDefault();
    if (message.length > 500) return;
    sending = true;
    limitText = '';
    try {
      await createAccessRequest(nodeID, role, message.trim());
      outcome = 'sent';
    } catch (err) {
      const kind = requestOutcome(err);
      outcome = kind === 'sent' ? 'sent' : kind;
      if (kind === 'limited') {
        const msg = errorMessage(err);
        limitText = msg.plural !== undefined ? translatePlural($lang, msg.key, msg.plural) : translate($lang, msg.key);
      }
    } finally {
      sending = false;
    }
  }
</script>

<section class="request" aria-labelledby="request-title">
  <h2 id="request-title" class="section-title">{translate($lang, 'share.request.title')}</h2>
  {#if outcome === 'sent'}
    <p class="sent" role="status">{translate($lang, REQUEST_SENT_KEY)}</p>
  {:else}
    <form class="form" onsubmit={send}>
      <div class="field">
        <label for="request-role">{translate($lang, 'share.request.role')}</label>
        <select id="request-role" bind:value={role}>
          {#each ROLES as r (r)}
            <option value={r}>{translate($lang, `role.${r}`)}</option>
          {/each}
        </select>
      </div>
      <div class="field">
        <label for="request-message">{translate($lang, 'share.request.message')}</label>
        <textarea id="request-message" rows="3" maxlength="500" bind:value={message} aria-describedby="request-message-hint"></textarea>
        <p class="hint" id="request-message-hint">{translate($lang, 'share.messageHint', { count: message.length })}</p>
      </div>
      {#if outcome === 'limited' || outcome === 'failed'}
        <p class="alert error" role="alert">{outcome === 'limited' ? limitText : translate($lang, 'error.network')}</p>
      {/if}
      <div class="foot">
        <button type="submit" class="btn primary" disabled={sending}>{translate($lang, 'share.request.submit')}</button>
      </div>
    </form>
  {/if}
</section>

<style>
  .request {
    display: grid;
    gap: var(--space-3);
    margin-block-start: var(--space-6);
    max-width: 420px;
  }

  .section-title {
    margin: 0;
    font-size: var(--text-lg);
    font-weight: 600;
  }

  .form {
    display: grid;
    gap: var(--space-3);
  }

  .field {
    display: grid;
    gap: var(--space-2);
  }

  .hint,
  .sent {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }

  .sent {
    font-size: var(--text-base);
    color: var(--color-foreground);
  }

  select,
  textarea {
    min-height: 44px;
    padding-inline: var(--space-2);
    font: inherit;
    color: var(--color-foreground);
    background: var(--color-surface);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
  }

  textarea {
    padding-block: var(--space-2);
  }

  .foot {
    display: flex;
    justify-content: flex-end;
  }
</style>
