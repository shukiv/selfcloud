<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { lang, translate } from '../../lib/i18n';
  import { formatDate } from '../../lib/files/format';
  import { renderSentence, sentence, type AuditEntry, type Names } from '../../lib/audit/sentence';

  // AuditItem is one audit entry: the plain sentence, the raw action under it, and when it happened.
  let {
    entry,
    names,
  }: {
    entry: AuditEntry & { id: string; time: string };
    names: Names;
  } = $props();

  let text = $derived(renderSentence(sentence(entry, names), (key, params) => translate($lang, key, params)));
</script>

<li class="row audit-row">
  <div class="row-main">
    <p class="row-title">{text}</p>
    <p class="mono">{entry.action}</p>
  </div>
  <p class="row-cell row-meta"><time datetime={entry.time}>{formatDate(entry.time, $lang)}</time></p>
</li>
